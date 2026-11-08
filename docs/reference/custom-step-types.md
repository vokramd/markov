{% raw %}
# Custom Step Types

Custom step types let you define reusable abstractions on top of [built-in primitives](step-types.md). They are defined in the top-level `step_types` map of a workflow file and can be used anywhere a primitive type would be used.

## What They Are

A custom step type wraps a primitive (such as `k8s_job`, `k8s_job_wait`, `http_request`, `shell_exec`, `script_exec`, or `prompt`) with fixed defaults and pre-configured parameters. This eliminates repetition when the same image, secrets, volumes, or other parameters are shared across many steps.

When a step references a custom type, the engine resolves it to its base primitive and merges all parameter layers before execution.

## StepType Schema

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `base` | string | yes | Must be a primitive type: `k8s_job`, `k8s_job_wait`, `http_request`, `shell_exec`, `script_exec`, `ansible_playbook`, `ansible`, `claude`, `codex`, `prompt`, `gate`, `load_artifact`, `set_fact`, `assert`, or `llm_invoke` |
| `description` | string | no | Human-readable description of the step type |
| `job` | map[string]any | no | Base parameters, typically the core spec (image, command for `k8s_job`; base_url for `http_request`) |
| `defaults` | map[string]any | no | Default parameter values that override `job` fields |
| `params` | map[string]any | no | Additional parameters that override `defaults` fields |

## Merge Order

When a step uses a custom type, the engine resolves parameters by merging four layers in order. Each layer overrides the previous one:

```
1. st.Job          (base spec from the step type definition)
2. st.Defaults     (overrides Job)
3. st.Params       (overrides Defaults)
4. step.Params     (from the step using this type -- overrides everything)
```

This means:

- `job` provides the foundation (image, command, volumes, etc.).
- `defaults` sets sensible defaults that individual steps typically should not change.
- `params` at the step-type level provides values that are common but may occasionally be overridden.
- `params` on the step instance always wins, giving each step full control.

All parameter values support Pongo2 template expressions (e.g., `"{{ model }}"`) which are rendered against the workflow context at execution time.

## Validation

During parsing, the engine verifies that every step type referenced by a step exists either as a primitive or as a key in the `step_types` map. Unknown types produce a parse error:

```
unknown type "my_type" (not a primitive or step_type)
```

The `base` field must itself be a primitive. Chaining custom types (a custom type whose base is another custom type) is not supported.

## Examples

### Minimal: echo_job

A simple wrapper that runs shell commands inside an Alpine container:

```yaml
step_types:
  echo_job:
    base: k8s_job
    description: "Run a shell command in Alpine"
    job:
      image: alpine:3.19
      command: ["/bin/sh", "-c"]
    defaults:
      backoff_limit: 0
      ttl_seconds: 300
```

Usage:

```yaml
steps:
  - name: hello
    type: echo_job
    params:
      args: ["echo 'hello world'"]

  - name: check_disk
    type: echo_job
    params:
      args: ["df -h && echo 'disk check complete'"]
    register: disk_info

  - name: conditional_step
    type: echo_job
    when: "run_diagnostics"
    params:
      args: ["uname -a"]
      ttl_seconds: 60  # override the default for this step
```

### API Client: jira_api

A wrapper for `http_request` that pre-configures the base URL and Basic Auth:

```yaml
step_types:
  jira_api:
    base: http_request
    description: "Call the Jira REST API"
    params:
      base_url: "{{ jira_server }}/rest/api/2"
      basic_auth:
        username: "{{ jira_user }}"
        password: "{{ jira_password }}"
      headers:
        Accept: "application/json"
```

Usage:

```yaml
vars:
  jira_server: "https://issues.redhat.com"
  jira_user: admin
  jira_password: admin
  request_id: demo-001

steps:
  - name: fetch_issues
    type: jira_api
    params:
      path: "/search?jql=project=MYPROJECT&maxResults=100"
      method: GET
      headers:
        X-Request-ID: "{{ request_id }}"
    register: issue_list

  - name: get_issue
    type: jira_api
    params:
      path: "/issue/{{ issue_key }}"
    register: issue_detail
```

When a step type and a step both define `headers`, the maps are merged. Step-level values override headers with the same name while preserving the other headers from the step type.

### Production: agent_skill

A full-featured step type for running AI agent skills in Kubernetes. This demonstrates all `k8s_job` features working together:

```yaml
step_types:
  agent_skill:
    base: k8s_job
    description: "Run a Claude skill in a pipeline-agent container"
    defaults:
      model: "{{ model }}"
      runner: "{{ runner }}"
    job:
      image: pipeline-agent:latest
      command: ["/bin/bash", "/app/scripts/run_skill.sh"]
      args:
        - "--skill"
        - "{{ skill }}"
        - "--issue"
        - "{{ issue }}"
        - "--model"
        - "{{ model }}"
        - "--runner"
        - "{{ runner }}"
      namespace: markov-pipelines
      service_account: pipeline-runner
      ttl_seconds: 86400
      backoff_limit: 0
      resources:
        requests:
          memory: 2Gi
          cpu: 500m
        limits:
          memory: 8Gi
          cpu: "2000m"
      secrets:
        - pipeline-secrets
        - gcp-credentials
      volumes:
        - name: pipeline-issues
          mount: /app/issues
        - name: pipeline-workspace
          mount: /app/workspace
        - name: pipeline-logs
          mount: /app/logs
        - name: pipeline-artifacts
          mount: /app/artifacts
        - name: pipeline-context
          mount: /app/.context
      affinity:
        pod_affinity:
          required:
            topology_key: kubernetes.io/hostname
            match_labels:
              app: pipeline-dashboard
```

Usage:

```yaml
vars:
  model: opus
  runner: cli

steps:
  - name: rfe_analysis
    type: agent_skill
    params:
      skill: rfe-creator
      issue: "{{ issue }}"
    artifacts:
      result:
        path: "/app/artifacts/{{ issue }}/rfe-analysis/result.yaml"
        format: yaml

  - name: create_strategy
    type: agent_skill
    params:
      skill: strategy-create
      issue: "{{ issue }}"
    artifacts:
      strategy:
        path: "/app/artifacts/{{ issue }}/strategy/strategy.yaml"
        format: yaml
      document:
        path: "/app/artifacts/{{ issue }}/strategy/strategy.md"
        format: markdown

  - name: security_review
    type: agent_skill
    params:
      skill: strat-security-review
      issue: "{{ issue }}"
      model: opus  # override the default model for this step
```

In this example, each step only needs to specify the `skill` and `issue` params. The image, command structure, volumes, secrets, resources, and affinity are all inherited from the step type definition. The `model` param can be overridden per-step (as shown in `security_review`) because step-level params take the highest precedence in the merge order.

## How Merge Order Works in Practice

Given the `echo_job` definition above and this step:

```yaml
- name: custom_echo
  type: echo_job
  params:
    args: ["echo 'custom'"]
    ttl_seconds: 60
    image: alpine:3.20  # override the image from job
```

The resolved parameters passed to the `k8s_job` executor are:

| Param | Value | Source |
|-------|-------|--------|
| `image` | `alpine:3.20` | step params (overrides `job`) |
| `command` | `["/bin/sh", "-c"]` | job |
| `backoff_limit` | `0` | defaults |
| `ttl_seconds` | `60` | step params (overrides `defaults`) |
| `args` | `["echo 'custom'"]` | step params |

The merge is a flat key-level override, not a deep merge. If a step overrides `resources`, it replaces the entire `resources` map from the step type, not individual keys within it.
{% endraw %}
