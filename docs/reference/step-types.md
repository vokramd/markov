{% raw %}
# Built-in Step Types

Markov ships with twenty-five primitive step types. Every step in a workflow must resolve to one of these primitives, either directly or through a [custom step type](custom-step-types.md).

All step types support these common fields:

| Field | Type | Description |
|-------|------|-------------|
| `name` | string, required | Unique step name within the workflow |
| `type` | string | The step type (primitive or custom) |
| `when` | string | Pongo2 boolean expression; step is skipped if false |
| `register` | string | Store the step output in the workflow context under this key |
| `timeout` | int | Maximum execution time in seconds |
| `for_each` | string | Context path to a list; runs the step once per item |
| `for_each_key` | string | Field on each item to use as iteration key (must be unique) |
| `for_each_sort` | string | Field on each item to sort by before iterating |
| `as` | string | Variable name for the current item (required when `for_each` is set) |
| `concurrency` | int | Max parallel iterations for `for_each` (defaults to global `forks`) |
| `workflow` | string | Name of a sub-workflow to invoke instead of running a type. May be a template, resolved when the step runs (per item under `for_each`): `submit-{{ test.arm }}` |
| `workflow_names` | string[] | For a templated `workflow`: the workflows it may resolve to. Checked when the definition loads, enforced at run time, and used by diagrams. |

---

## shell_exec

Runs a shell command via `bash -c`. The command executes on the Markov runner host, not inside Kubernetes.

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `command` | string | yes | Shell command to execute |

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `stdout` | string | Standard output from the command |
| `stderr` | string | Standard error from the command |
| `exit_code` | int | Process exit code |

### Failure Conditions

- The `command` param is empty or missing.
- The command exits with a non-zero exit code. The error message includes stderr.

### Examples

Simple echo:

```yaml
- name: greet
  type: shell_exec
  params:
    command: "echo 'hello from markov'"
```

Piped command with registered output:

```yaml
- name: count_pods
  type: shell_exec
  params:
    command: "kubectl get pods -n default --no-headers | wc -l"
  register: pod_count

- name: report
  type: shell_exec
  params:
    command: "echo 'Found {{ pod_count.stdout | trim }} pods'"
```

---

## script_exec

Runs a deterministic script on the Markov runner host through an explicit interpreter. Unlike `shell_exec`, Markov does not build a `bash -c` command: the interpreter, script arguments, and environment variables are passed directly to the process.

Provide exactly one script source:

- `content` writes an inline script body to a temporary file for this execution.
- `path` reads a script from the workflow's `scripts/` directory. The value must be relative to that directory; paths that escape it are rejected. For a directory workflow, use `<workflow-root>/scripts/`; for a single-file workflow, use a sibling `scripts/` directory.

`script_exec` runs on the runner host. To run a script in a container, package it in the image (or mount it) and use `k8s_job`.

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `interpreter` | string | yes | Interpreter executable, such as `python3`, `bash`, or `sh`. |
| `content` | string | one of `content`/`path` | Inline script body. |
| `path` | string | one of `content`/`path` | Script path relative to the workflow `scripts/` directory. |
| `args` | list | no | Arguments passed to the script after its path. Strings, numbers and booleans; numbers and booleans are passed as their text (`3`, `true`). |
| `env` | map | no | Environment variables added to or overriding the runner environment. Values as for `args`. |
| `stdin` | string, map or list | no | Standard input for the script. A map or list (from an exact expression) is sent as JSON. Use it for large values: a single argument is limited to about 128 KB on Linux, stdin is not. |

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `stdout` | string | Standard output from the script. |
| `stderr` | string | Standard error from the script. |
| `exit_code` | int | Process exit code. |

### Examples

An inline Python script:

```yaml
- name: print-issue
  type: script_exec
  params:
    interpreter: python3
    content: |
      import os
      import sys
      print(f"{os.environ['ISSUE']}: {sys.argv[1]}")
    args: ["review"]
    env:
      ISSUE: "RFE-123"
```

A script stored alongside a directory workflow:

```text
my-workflow/
  scripts/
    reconcile.py
  workflows/
    main.yaml
```

```yaml
- name: reconcile
  type: script_exec
  params:
    interpreter: python3
    path: reconcile.py
    args: ["--dry-run"]
```

---

## write_file

Writes content to a file on the Markov runner, creating missing parent directories. Use it to
save results to a mounted volume without an inline script: the content is a rendered template or a
value, with no size limit and no shell quoting.

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `path` | string | yes | File to write. |
| `content` | string, map or list | yes | Text to write. A map or list (from an exact expression) is written as indented JSON. |
| `mode` | string | no | Octal file mode, default `"0644"`. Quote it, so YAML doesn't read it as a decimal number. It is applied exactly, regardless of the umask. |
| `dir_mode` | string | no | Octal mode for directories it creates, default `"0755"`. Existing directories are left alone. A new directory keeps its parent's setgid bit, so it stays in a shared volume's group. |
| `append` | bool | no | Append instead of replacing the file. |

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `path` | string | The file written. |
| `bytes` | int | Bytes written. |

### Example

The runner usually runs as root, while other services read and clean the volume as another user.
Group-writable modes on a setgid volume let them remove what the runner wrote:

```yaml
- name: save_result
  type: write_file
  params:
    path: "/app/artifacts/benchmarks/{{ markov_run_id }}/{{ run.id }}.json"
    content: "{{ eval.stdout | from_json }}"
    mode: "0664"
    dir_mode: "2775"
```

---

## claude

Runs the Claude Code CLI non-interactively (`claude -p`) with `--output-format stream-json --verbose` and processes its event stream live. Use it to run a prompt or a skill (a slash command) in an execution directory, with limits that Markov enforces while the run is in progress.

The step runs on the Markov runner host and needs the `claude` CLI installed there, unless `sandbox` names an [OpenShell sandbox](#openshell-sandboxes) to run it in.

**In a sandbox.** With `sandbox`, the same command runs inside that sandbox through the `openshell_*` steps' transport, and everything else works as on the runner: the live transcript, usage and cost, and limits (a breach ends the exec, which stops `claude` in the sandbox). The image needs the `claude` CLI; `chdir`, `add_dirs`, `mcp_config` and `settings` are sandbox paths; and only the step's `env` is passed in, not the runner's environment. The sandbox's network policy must allow the API endpoint: attach an OpenShell provider for it on `openshell_create` (`providers`), which also keeps the key itself out of the sandbox.

**Authentication.** The step never requires or sets `ANTHROPIC_API_KEY`. It starts `claude` with the runner's environment, so an existing OAuth login under `HOME` (`~/.claude`) is used. To use a key or token instead, pass it through `env`. In Kubernetes there is no interactive login in the pod; provide credentials through `env` or by mounting them.

The prompt is written to the process's stdin, so it does not appear in the process list.

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `prompt` | string | one of `prompt`/`skill` | The prompt text. May itself be a slash command such as `/review-pr 123`. |
| `skill` | string | one of `prompt`/`skill` | Skill or command name; the prompt becomes `/<skill> <args>`. |
| `args` | string | no | Arguments appended after `skill`. |
| `chdir` | string | no | Directory to run in. Must exist. Defaults to the Markov process working directory. |
| `model` | string | no | `--model`. |
| `effort` | string | no | `--effort`. |
| `fallback_model` | string | no | `--fallback-model`. |
| `permission_mode` | string | no | `acceptEdits`, `auto`, `bypassPermissions` (alias `bypass`), `manual`, `dontAsk`, or `plan`. Unset uses the CLI default, which in non-interactive mode denies anything that would prompt. **`bypassPermissions` lets the run use every tool without asking; only enable it where that is acceptable.** |
| `allowed_tools` / `disallowed_tools` | string[] | no | `--allowedTools` / `--disallowedTools`. Prefer these over bypass. |
| `append_system_prompt` | string | no | `--append-system-prompt`. |
| `add_dirs` | string[] | no | Extra directories the run may access (`--add-dir`). |
| `mcp_config` | string or string[] | no | `--mcp-config` file(s) or JSON. |
| `settings` | string | no | `--settings` file or JSON. |
| `resume` / `session_id` | string | no | `--resume` / `--session-id`. |
| `bare` | bool | no | `--bare`: skip hooks, plugins, and CLAUDE.md discovery. Bare mode requires API-key authentication, so it will not use an OAuth login. |
| `limits` | map | no | Limits enforced by Markov; see below. |
| `events_limit` | int | no | Maximum events kept in the `events` output. Default 200; `0` keeps none. Live callbacks are not capped. |
| `env` | map[string]string | no | Extra environment variables. |
| `extra_args` | string[] | no | Extra arguments appended verbatim. |
| `binary` | string | no | Executable to run instead of `claude`. |
| `sandbox` | string | no | Run in this OpenShell sandbox instead of on the runner. |
| `transport` | string | no | With `sandbox`: `cli` or `sdk` (default from the workflow file's `openshell:` block). |

### Limits

| Limit | Meaning |
|-------|---------|
| `max_turns` | Maximum assistant turns (top-level assistant messages). |
| `max_tokens` | Maximum new tokens: input + output + cache-creation. Cache reads are excluded because every turn re-reads the cached context. |
| `max_duration` | Maximum wall-clock seconds for the run. |
| `max_budget_usd` | Passed to `--max-budget-usd`. On a subscription this is a notional figure rather than a charge. |

Markov counts turns and tokens from the stream as events arrive. When a limit is exceeded it stops the process (and any tools it started), emits a `limit_exceeded` progress event, and fails the step with `claude: limit exceeded: <limit>`. The step output is still returned, with `limit_exceeded` set. Use the step-level `timeout` as an additional bound.

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `result` | string | Final result text. |
| `is_error` | bool | True if the run reported an error or a limit was exceeded. |
| `session_id` | string | Claude session ID. |
| `model` | string | Model reported by the session. |
| `num_turns` | int | Turns taken. |
| `tokens` | int | New tokens counted against `max_tokens`. |
| `usage` | map | `input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`. |
| `total_cost_usd` | float | Cost reported by the CLI. |
| `limit_exceeded` | string | Name of the exceeded limit, or empty. |
| `stop_reason`, `terminal_reason`, `duration_ms`, `permission_denials` | | Passed through from the CLI result when present. |
| `events` | list | Compact events (`kind` plus fields), capped by `events_limit`. Long strings are truncated. |
| `events_truncated` | bool | True if events were dropped by the cap. |
| `exit_code`, `stderr` | | Process exit code and standard error. |

### Live progress

Each stream event is also sent to configured callbacks as a [`step_progress`](callbacks.md#step-lifecycle-events) event while the step runs.

### Failure Conditions

The step fails if a limit is exceeded, the CLI reports `is_error`, no result event is produced, or the process exits non-zero. Permission denials in bypass-free modes do not fail the step by themselves; inspect `permission_denials`.

On resume, a completed `claude` step is skipped like any other step; an interrupted one starts over rather than resuming its session. Concurrent `for_each` iterations share one login and its rate limits, so use a low `concurrency`.

### Example

```yaml
- name: review
  type: claude
  register: review
  params:
    skill: review-pr
    args: "{{ pr }}"
    chdir: ./repo
    allowed_tools: [Read, Grep, Bash]
    limits:
      max_turns: 30
      max_tokens: 500000
      max_duration: 900

- name: report
  type: shell_exec
  when: "not review.is_error"
  params:
    command: echo "{{ review.result }}"
```

---

## ansible_playbook

Runs `ansible-playbook` on the Markov runner host. The binary is executed directly (no shell), so parameter values cannot inject shell syntax. `ansible-playbook` must be installed on the runner (or in the job image when run through a custom environment).

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `playbook` | string or string[] | yes | Playbook path(s), relative to `chdir` if set. |
| `chdir` | string | no | Directory to run in. Must exist. Defaults to the Markov process working directory. |
| `inventory` | string, string[], or map | no | A path or inline host list (`"web01,web02,"`), a list of those (one `-i` each), or an inline inventory in Ansible YAML form (`all: {hosts: ..., children: ..., vars: ...}`), which is written to a temporary file removed after the run. |
| `limit` | string or string[] | no | `--limit`; lists are comma-joined. |
| `tags` / `skip_tags` | string or string[] | no | `--tags` / `--skip-tags`. |
| `start_at_task` | string | no | `--start-at-task`. |
| `extra_vars` | map or string | no | A map is written to a temporary 0600 JSON file and passed as `-e @file`, keeping values out of the process list. A string is passed as-is (`k=v`, JSON, or `@file`). |
| `extra_vars_files` | string[] | no | Each passed as `-e @file`. |
| `check` / `diff` / `become` | bool | no | `--check` / `--diff` / `--become`. |
| `become_user`, `become_method`, `remote_user`, `connection`, `private_key`, `vault_password_file`, `vault_id` | string | no | Corresponding Ansible flags. |
| `forks`, `timeout` | int | no | `--forks`, `--timeout` (SSH timeout; use the step-level `timeout` to bound the whole run). |
| `verbosity` | int 0-6 | no | Becomes `-v` repeated. |
| `env` | map[string]string | no | Extra environment variables (e.g. `ANSIBLE_HOST_KEY_CHECKING`). `ANSIBLE_NOCOLOR=1` is set by default. |
| `extra_args` | string[] | no | Extra arguments appended verbatim, for flags not listed above. |
| `binary` | string | no | Executable to run instead of `ansible-playbook`. |

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `stdout` | string | Standard output. |
| `stderr` | string | Standard error. |
| `exit_code` | int | Process exit code. |
| `recap` | map | Parsed `PLAY RECAP`: `{host: {ok, changed, unreachable, failed, skipped, rescued, ignored}}`. |
| `changed` | bool | True if any host reported `changed > 0`. |

### Failure Conditions

A non-zero exit code (including failed or unreachable hosts) fails the step. Output is still available to `rescue`/`always` handling.

### Example

```yaml
- name: deploy
  type: ansible_playbook
  register: deploy
  params:
    chdir: ./infra
    playbook: site.yml
    inventory:
      all:
        hosts:
          web01: {ansible_host: "{{ web_ip }}"}
    tags: [deploy]
    extra_vars:
      version: "{{ version }}"
    become: true
  timeout: 900

- name: notify
  type: shell_exec
  when: deploy.changed
  params:
    command: echo "changed hosts"
```

---

## ansible

Runs an ad-hoc `ansible` module against a host pattern. Shares the connection and privilege parameters of `ansible_playbook`.

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `pattern` | string | yes | Host pattern, e.g. `all`, `web`, `web01:web02`. |
| `module` | string | no | Module name (`-m`). Default `command`. |
| `module_args` | string or map | no | `-a`. A map is JSON-encoded. |
| `poll`, `background` | int | no | `-P`, `-B` for async execution. |

Also accepted, with the same meaning as for [`ansible_playbook`](#ansible_playbook): `chdir`, `inventory`, `limit`, `extra_vars`, `extra_vars_files`, `check`, `diff`, `become`, `become_user`, `become_method`, `remote_user`, `connection`, `private_key`, `vault_password_file`, `vault_id`, `forks`, `timeout`, `verbosity`, `env`, `extra_args`, `binary`.

### Output Variables

`stdout`, `stderr`, `exit_code`, and `changed` (true if any host line reports `CHANGED`).

### Example

```yaml
- name: ping
  type: ansible
  params:
    pattern: all
    inventory: "web01,web02,"
    module: ping
    connection: ssh
```

---

## prompt

Displays a prompt and reads one line of input from the Markov runner's interactive terminal. Use it for local, synchronous decisions; it is not suitable for Kubernetes Jobs, CI, or other non-interactive runners. For a durable approval that survives process exit, use a gate with `action: pause` and resume the run later.

The response is available through `register` as `value`.

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `message` | string | yes | Text displayed before the input prompt. |
| `choices` | string[] | no | Allowed responses. Input must match one of these strings exactly unless `case_insensitive` is true. |
| `default` | string | no | Value selected when the user presses Enter. When choices are set, it must be one of them. |
| `case_insensitive` | bool | no | `false` | When `true`, match `choices` without regard to case and return the configured choice spelling. |

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `value` | string | The entered response, or `default` when Enter was pressed. |

### Failure Conditions

- The runner does not have an interactive terminal.
- `message` is missing.
- The input stream closes before a valid response is received.

Empty or invalid responses are explained and re-prompted in the terminal; they do not fail the workflow.

### Example

```yaml
- name: release_decision
  type: prompt
  params:
    message: "Approve release?"
    choices: ["yes", "no"]
    default: "no"
    case_insensitive: true
  register: decision

- name: release
  type: shell_exec
  when: "decision.value == 'yes'"
  params:
    command: "./release.sh"
```

See [the complete interactive example](../../examples/prompt.yaml).

---

## k8s_job

Creates a Kubernetes `batch/v1` Job and polls for completion every 5 seconds. The pod's `RestartPolicy` is always `Never`.

### Parameters

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `image` | string, required | -- | Container image |
| `command` | string[] or string | -- | Container command (entrypoint) |
| `args` | string[] or string | -- | Container arguments |
| `env` | map[string]any | -- | Environment variables as key-value pairs |
| `secrets` | string[] | -- | Kubernetes Secret names to inject via `envFrom` / `secretRef` |
| `volumes` | list | -- | Volume specifications (see below) |
| `init_containers` | list | -- | Init container specifications (see below) |
| `resources` | map | -- | Resource requests/limits using standard Kubernetes quantities |
| `affinity` | map | -- | Pod affinity specification (see below) |
| `service_account` | string | -- | Kubernetes ServiceAccount name for the pod |
| `backoff_limit` | int | `0` | Kubernetes Job `.spec.backoffLimit` |
| `ttl_seconds` | int | `86400` | TTL in seconds after Job completion (`.spec.ttlSecondsAfterFinished`) |
| `image_pull_policy` | string | `IfNotPresent` | Image pull policy (`Always`, `IfNotPresent`, `Never`) |
| `namespace` | string | -- | Override the global namespace for this step |
| `name_prefix` | string | `markov` | Prefix for the auto-generated Job name |

#### Volume Specification

Each entry in the `volumes` list is a map with:

| Field | Type | Description |
|-------|------|-------------|
| `name` | string, required | Volume name |
| `pvc` | string | PersistentVolumeClaim name (mutually exclusive with `config_map` and `secret`) |
| `config_map` | string | ConfigMap name |
| `secret` | string | Secret name |
| `mount` | string | Mount path inside the container |
| `read_only` | bool | Mount as read-only |

If none of `pvc`, `config_map`, or `secret` is specified, the volume is created as an `emptyDir`.

#### Init Container Specification

Each entry in the `init_containers` list is a map with:

| Field | Type | Description |
|-------|------|-------------|
| `name` | string, required | Container name |
| `image` | string, required | Container image |
| `command` | string[] or string | Container command |
| `args` | string[] or string | Container arguments |
| `volume_mounts` | list | Each entry has `name` (string) and `mount_path` (string) |
| `image_pull_policy` | string | Defaults to `IfNotPresent` |

#### Affinity Specification

```yaml
affinity:
  pod_affinity:
    required:
      topology_key: kubernetes.io/hostname
      match_labels:
        app: my-app
```

This maps to a `requiredDuringSchedulingIgnoredDuringExecution` pod affinity term.

#### Resources Specification

```yaml
resources:
  requests:
    memory: 2Gi
    cpu: 500m
  limits:
    memory: 8Gi
    cpu: "2000m"
```

Values are parsed using Kubernetes standard quantity parsing (e.g., `500m`, `2Gi`, `"2000m"`).

### Auto-Injected Fields

These fields are set automatically by the engine and should not be specified in workflow YAML:

| Field | Description |
|-------|-------------|
| `_job_name` | Sanitized Kubernetes name, max 63 characters. Format: `{name_prefix}-{run_id}-{step_name}`. If the raw name exceeds 63 characters, it is truncated and a SHA-256 hash suffix is appended. |
| `_labels` | Labels applied to the Job and Pod: `app=markov`, `markov/run-id`, `markov/workflow`, `markov/step` |

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `job_name` | string | Name of the created Kubernetes Job |
| `namespace` | string | Namespace the Job was created in |
| `logs` | string | Pod logs, up to 64 KB |

### Failure Conditions

- The `image` param is empty or missing.
- The Kubernetes API rejects the Job creation request.
- The Job's status condition becomes `Failed`.
- The step `timeout` expires or the parent context is cancelled.

### Example

Complete k8s_job with volumes and secrets:

```yaml
- name: run_analysis
  type: k8s_job
  timeout: 600
  params:
    image: pipeline-agent:latest
    command: ["/bin/bash", "/app/scripts/run_skill.sh"]
    args: ["--issue", "{{ issue }}", "--model", "opus"]
    namespace: markov-pipelines
    service_account: pipeline-runner
    backoff_limit: 0
    ttl_seconds: 3600
    image_pull_policy: Always
    env:
      ISSUE_KEY: "{{ issue }}"
      LOG_LEVEL: debug
    secrets:
      - pipeline-secrets
      - gcp-credentials
    volumes:
      - name: workspace
        pvc: pipeline-workspace-pvc
        mount: /app/workspace
      - name: config
        config_map: pipeline-config
        mount: /app/config
        read_only: true
      - name: creds
        secret: api-credentials
        mount: /app/secrets
        read_only: true
      - name: scratch
        mount: /tmp/scratch
    init_containers:
      - name: fetch-source
        image: alpine/git:latest
        command: ["/bin/sh", "-c"]
        args: ["git clone https://github.com/org/repo.git /workspace/src"]
        volume_mounts:
          - name: workspace
            mount_path: /workspace
    resources:
      requests:
        memory: 2Gi
        cpu: 500m
      limits:
        memory: 8Gi
        cpu: "2000m"
    affinity:
      pod_affinity:
        required:
          topology_key: kubernetes.io/hostname
          match_labels:
            app: pipeline-dashboard
  register: job_result
```

---

## k8s_job_wait

Waits for an existing Kubernetes `batch/v1` Job to complete or fail. Use this when another service creates the Job and Markov only needs to watch it, collect pod logs, and continue the workflow.

This complements `k8s_job`: `k8s_job` creates a new Job, while `k8s_job_wait` watches a named Job that already exists or may appear shortly.

### Parameters

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `job_name` | string, required | -- | Name of the Kubernetes Job to watch |
| `namespace` | string | workflow namespace | Override the global namespace for this step |
| `timeout` | int | `3600` | Seconds to wait for the Job to appear and then finish. Set to `0` to rely only on the step or parent context timeout. |
| `tail_logs` | bool | `true` | Capture pod logs after completion or failure |
| `log_bytes` | int | `65536` | Maximum number of log bytes to capture |

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `job_name` | string | Name of the watched Kubernetes Job |
| `namespace` | string | Namespace the Job was watched in |
| `status` | string | `completed`, `failed`, `running`, or `pending` when the wait is cancelled before the Job appears |
| `logs` | string | Pod logs, up to `log_bytes`, when `tail_logs` is true and logs are available |

### Failure Conditions

- `job_name` is empty or missing.
- The Kubernetes API returns an error other than "not found" while checking the Job.
- The Job's status condition becomes `Failed`.
- The `timeout` expires or the parent context is cancelled.

### Example

Submit a Job through an external dashboard API, then wait on the Kubernetes Job directly:

```yaml
- name: submit
  type: http_request
  params:
    base_url: "http://pipeline-dashboard:5000"
    path: /api/jobs/submit
    method: POST
    body:
      fqn: "github.local/opendatahub-io/rfe-creator@main:rfe.speedrun"
      args:
        issue: RHAIRFE-1
        model: opus
  register: submitted_job

- name: wait_for_completion
  type: k8s_job_wait
  params:
    job_name: "{{ submitted_job.body.job_name }}"
    timeout: 3600
    tail_logs: true
  register: watched_job
```

When the watch starts, the step emits the same `job_created` callback event as `k8s_job`, with
the watched Job's name and namespace, so consumers such as markovd can follow its log while it
runs.

A failed Job fails the step. To record it and carry on (for example, when a failed Job is a
result to report rather than a reason to stop), set `ignore_errors`; the registered output then
has `status: failed`, the logs, `failed: true` and `error`:

```yaml
- name: wait_for_run
  type: k8s_job_wait
  ignore_errors: true
  params:
    job_name: "{{ submitted_job.body.job_name }}"
  register: watched_job
```

Reusable custom type:

```yaml
step_types:
  agent_job_wait:
    base: k8s_job_wait
    params:
      namespace: ai-pipeline
      timeout: 3600
      tail_logs: true
      log_bytes: 131072
```

---

## http_request

Makes HTTP requests with automatic JSON body encoding and response parsing.

### Parameters

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `method` | string | `GET` | HTTP method (GET, POST, PUT, DELETE, etc.) |
| `url` | string | -- | Full request URL. Required if `base_url` is not set. |
| `base_url` | string | -- | Base URL, concatenated with `path` to form the full URL |
| `path` | string | -- | Path appended to `base_url` |
| `body` | any | -- | Request body, JSON-encoded automatically |
| `headers` | map[string]string | `{}` | Custom HTTP headers to add to the request |
| `basic_auth` | map | -- | HTTP Basic Auth credentials with `username` and `password` fields |
| `ignore_status` | bool or list[int] | -- | Treat matching HTTP error status codes as success. `true` ignores all `>= 400` responses. |
| `tls_insecure` | bool | `false` | Skip TLS certificate verification for HTTPS requests. Intended for local development or trusted test environments. |
| `tls_ca_cert` | string | -- | Path to a PEM-encoded CA certificate bundle to trust for this request. Useful for self-signed or private CA certificates. |

When a `body` is provided, the `Content-Type` header is set to `application/json`. A custom `Content-Type` value in `headers` overrides that default. If both `basic_auth` and a custom `Authorization` header are provided, `basic_auth` takes precedence.

For HTTPS endpoints with self-signed certificates, prefer `tls_ca_cert` when you have the CA certificate. Use `tls_insecure: true` only when certificate verification must be disabled.

Either `url` or `base_url` must be specified. When both `base_url` and `path` are given, they are concatenated directly (no slash is inserted).

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `status_code` | int | HTTP response status code |
| `body` | any | Response body. Auto-parsed as JSON if the response is valid JSON (whole numbers become integers); otherwise returned as a string. |

### Failure Conditions

- Neither `url` nor `base_url` is provided.
- The HTTP request fails at the transport level (DNS, connection refused, etc.).
- The response status code is >= 400 and is not allowed by `ignore_status`. The step fails, but output variables (including the response body) are still populated.

### Examples

GET request with registered output:

```yaml
- name: fetch_issues
  type: http_request
  params:
    base_url: "https://issues.redhat.com/rest/api/2"
    path: "/search?jql=project=MYPROJECT&maxResults=50"
    method: GET
  register: issue_list

- name: report_count
  type: shell_exec
  params:
    command: "echo 'Found {{ issue_list.body.total }} issues'"
```

POST request with JSON body:

```yaml
- name: create_webhook
  type: http_request
  params:
    url: "https://api.example.com/webhooks"
    method: POST
    body:
      name: "pipeline-notify"
      url: "https://hooks.example.com/markov"
      events: ["job.completed", "job.failed"]
  register: webhook
```

Authenticated request with custom headers:

```yaml
- name: create_issue
  type: http_request
  params:
    base_url: "https://issues.example.com/rest/api/2"
    path: "/issue"
    method: POST
    basic_auth:
      username: "{{ jira_user }}"
      password: "{{ jira_password }}"
    headers:
      Accept: "application/json"
    body:
      fields:
        project:
          key: PIPE
        summary: "Pipeline-created issue"
        issuetype:
          name: Task
  register: issue
```

Token auth and tolerated status codes:

```yaml
- name: ensure_repo
  type: http_request
  params:
    base_url: "https://github.example.com/api/v3"
    path: "/orgs/{{ org }}/repos"
    method: POST
    headers:
      Authorization: "token {{ github_token }}"
      Accept: "application/vnd.github+json"
    ignore_status: [422]
    body:
      name: "{{ repo_name }}"
  register: repo_create
```

HTTPS request to an endpoint signed by a private CA:

```yaml
- name: fetch_internal_status
  type: http_request
  params:
    url: "https://internal.example.test/status"
    headers:
      Accept: "application/json"
    tls_ca_cert: "/etc/markov/certs/internal-ca.pem"
  register: internal_status
```

For disposable local environments, certificate verification can be disabled:

```yaml
- name: fetch_local_status
  type: http_request
  params:
    url: "https://github.local/api/v3"
    tls_insecure: true
  register: github_status
```

---

## gate

Evaluates named rules against the workflow context using the Grule rule engine. Gates control workflow progression by producing an action (`continue`, `skip`, or `pause`) and optionally setting facts in the context.

### Step Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `rules` | string[] | yes | Names of rules to evaluate (defined in the top-level `rules:` block) |
| `facts` | map[string]any | no | Additional context values to make available to rule conditions. Template expressions are rendered against the current context. |

### Rule Definition (top-level `rules:` block)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string, required | -- | Rule identifier, referenced by gate steps |
| `description` | string | -- | Human-readable description |
| `salience` | int | `0` | Priority (higher salience fires first) |
| `when` | string, required | -- | Condition expression (Pongo2 syntax) |
| `action` | string | `continue` | Action when the rule fires: `continue`, `skip`, or `pause` |
| `set_fact` | map[string]any | -- | Variables to set in the workflow context when the rule fires |

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `action` | string | The winning action: `continue`, `skip`, or `pause` |
| `fired_rules` | string[] | Names of all rules that fired |
| `facts` | map[string]any | Values set by `set_fact` across all fired rules |

### Evaluation Rules

1. All rules named in the `rules` list are compiled from Pongo2 conditions into GRL (Grule Rule Language) and evaluated together.
2. Rules fire in salience order. When a rule fires and sets facts, remaining rules are re-evaluated against the updated context.
3. The highest-salience fired rule determines the gate action.
4. `set_fact` values from all fired rules are merged back into the workflow context, making them available to downstream steps.
5. `pause` persists a gate receipt, marks the step and run as `paused`, and stops execution. Resume the run with at least one `--var key=value` override; the paused gate is evaluated again with that input.
6. `skip` is recorded as the gate action, but does not yet change engine control flow.

### Scoping

Rules see global vars, workflow vars, CLI `--var` overrides, and `set_fact` values. They do **not** see step results, register outputs, or artifact data directly. To pass step data to a rule, map it through the gate's `facts` block:

```yaml
- name: quality_gate
  type: gate
  facts:
    score: "{{ analysis.artifacts.result.confidence }}"
  rules:
    - auto_approve
    - needs_review
```

This keeps rules portable across workflows.

### Failure Conditions

- A referenced rule name is not found in the top-level `rules:` block.
- A rule condition fails to compile to GRL.
- The Grule engine returns an execution error.

### Example

```yaml
rules:
  - name: tests_pass
    description: "Allow deployment when coverage meets threshold"
    salience: 100
    when: "test_coverage >= min_coverage"
    action: continue
    set_fact:
      tests_approved: true

  - name: tests_fail
    description: "Block deployment on insufficient coverage"
    salience: 200
    when: "test_coverage < min_coverage"
    action: skip
    set_fact:
      tests_approved: false
      block_reason: "Coverage below threshold"

workflows:
  - name: main
    steps:
      - name: test_gate
        type: gate
        rules:
          - tests_pass
          - tests_fail

      - name: deploy
        type: shell_exec
        when: "tests_approved"
        params:
          command: "echo 'Deploying...'"
```

---

## set_fact

Computes and stores variables in the workflow context. Unlike other step types, `set_fact` does not use `params` -- it uses the `vars` field directly on the step.

### Step Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vars` | map[string]any | yes | Key-value pairs to evaluate and store in the context |

### Evaluation Rules

Every value is evaluated against the context as it was before the step, so facts in one step cannot refer to each other (`vars` is unordered). To build one fact from another, use two `set_fact` steps.

Each value in the `vars` map is evaluated according to its type:

| Value Type | Behavior |
|-----------|----------|
| String that is exactly one `{{ expr }}` | Evaluated to the expression's native value: `"{{ tiers[tier] }}"` stores the map, `"{{ names \| csv }}"` the list. A value that comes out as text is coerced as in the next row |
| String containing `{{` or `{%` | Rendered as a Pongo2 template, then coerced: `"true"` becomes `true` (bool), integer strings are parsed to `int`, JSON arrays/objects are parsed into native types |
| String with `{{ path \| from_json }}` or `{{ path \| fromjson }}` | The context path is resolved and its string value is parsed as JSON, preserving structure (maps, arrays, nested types) |
| Plain string (no template syntax) | Evaluated as a boolean expression via `{% if expr %}true{% endif %}` |
| Map with a `from` key | Table lookup (see below) |
| Any other map, or a list | Rendered like step params: template strings are rendered (exact expressions keep native types) and other strings are kept as they are, so a whole record can be built in one fact |
| Any other type | Stored directly (int, bool) |

#### Table Lookup

When a value is a map containing a `from` key, it performs a table lookup:

| Field | Type | Description |
|-------|------|-------------|
| `from` | string, required | Context path to a list of maps |
| `match` | map[string]any, required | Filter criteria -- each key-value pair must match |
| `field` | string | Extract a single field from the matched row. If omitted, the entire row is returned. |
| `default` | any | Fallback value if no match is found or the source list is nil |

Match values can contain template expressions (e.g., `"{{ issue }}"`).

### Output

`set_fact` does not produce output in the `register` sense. The computed values are merged directly into the workflow context and are available to all subsequent steps.

### Failure Conditions

- The `vars` map is empty or not defined.
- A template expression fails to render.
- A boolean expression fails to evaluate.
- A table lookup `from` path resolves to a non-list type.
- A table lookup is missing the `match` field.

### Examples

Template rendering and arithmetic:

```yaml
- name: compute_vars
  type: set_fact
  vars:
    stage: "{{ stage + 1 }}"
    label: "{{ environment }}-{{ build_version }}"
    is_production: "environment == 'production'"
    health_status: "healthy"
```

The `stage` value rendered from `"{{ stage + 1 }}"` produces an integer string like `"3"`, which is coerced to the integer `3`.

The `is_production` value is a plain string without template delimiters, so it is evaluated as a boolean expression.

Table lookup:

```yaml
- name: find_owner
  type: set_fact
  vars:
    owner:
      from: team_roster.artifacts.members.rows
      match:
        component: "{{ component }}"
        role: lead
      field: email
      default: "unassigned@example.com"
```

JSON parsing with `from_json`:

```yaml
- name: parse_result
  type: set_fact
  vars:
    parsed_data: "{{ job_output.stdout | from_json }}"
```

---

## load_artifact

Loads files from the filesystem or from Kubernetes pods into the workflow context as structured data.

### Step Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `artifacts` | map[string]Artifact | yes | Named artifacts to load |

#### Artifact Specification

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `path` | string, required | -- | File path (template expressions are rendered against the context) |
| `format` | string | -- | How to parse the file: `yaml`, `markdown`, `markdown_table`, or omit for raw string |
| `source` | string | auto | Where to read: `local` (filesystem), `k8s` (exec `cat` in a running pod), or omit for auto-detection |
| `optional` | bool | `false` | If `true`, missing files produce `nil` instead of failing |

### Format Details

| Format | Parsed Result |
|--------|---------------|
| `yaml` | `map[string]any` -- parsed YAML document |
| `markdown` | `map[string]any` with two keys: `frontmatter` (parsed YAML between `---` delimiters) and `content` (body text after the second `---`) |
| `markdown_table` | `map[string]any` with `rows` (list of maps with snake_case headers) plus one key per column header containing a comma-joined string of all values in that column |
| (omitted) | `string` -- raw file contents |

### Context Placement

Loaded artifacts are placed in the context at `{step_name}.artifacts.{artifact_name}`. For example, a step named `load_config` with an artifact named `settings` is accessible as `load_config.artifacts.settings`.

### Source Auto-Detection

When `source` is omitted:
- If a Kubernetes client is available and the workflow has a `namespace` set, the engine attempts to read from running pods in that namespace using `kubectl exec cat`.
- Otherwise, it reads from the local filesystem.

For `k8s` source, the engine lists running pods in the workflow namespace (up to 20), iterates through containers with a `Ready` status, and tries `cat {path}` in each until one succeeds.

### Failure Conditions

- The `artifacts` map is empty.
- A file is missing and `optional` is `false`.
- A YAML file fails to parse.

### Example

```yaml
- name: load_config
  type: load_artifact
  artifacts:
    settings:
      path: "/app/config/pipeline.yaml"
      format: yaml
    readme:
      path: "/app/docs/README.md"
      format: markdown
      optional: true
    test_results:
      path: "/app/reports/coverage.md"
      format: markdown_table

- name: check_config
  type: assert
  that:
    - "load_config.artifacts.settings.log_level == 'debug'"
    - "load_config.artifacts.readme != None"
  msg: "Configuration validation failed"
```

Artifacts can also be loaded alongside executor steps. When a step with a type like `k8s_job` includes an `artifacts` field, the engine loads those artifacts after the job completes and merges them into the step's context entry alongside the executor output:

```yaml
- name: run_analysis
  type: k8s_job
  params:
    image: pipeline-agent:latest
    command: ["/bin/sh", "-c"]
    args: ["python /app/analyze.py --issue {{ issue }}"]
  artifacts:
    result:
      path: "/app/artifacts/{{ issue }}/result.yaml"
      format: yaml

# Access both executor output and artifacts:
# run_analysis.job_name, run_analysis.logs
# run_analysis.artifacts.result.confidence
```

---

## assert

Validates conditions and fails the workflow immediately if any condition evaluates to false. Use for precondition checks and invariant validation.

### Step Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `that` | string[] | yes | List of Pongo2 boolean expressions to evaluate |
| `msg` | string | no | Custom failure message. If omitted, the default message is `"assertion failed: {expression}"`. |

### Evaluation

Each expression in the `that` list is evaluated using `EvalBool`, which wraps the expression in `{% if expr %}true{% endif %}` and checks whether the result is `"true"`. Expressions are evaluated in order; the step fails on the first false expression.

### Failure Conditions

- The `that` list is empty.
- Any expression in `that` fails to parse or evaluate.
- Any expression in `that` evaluates to false.

### Example

```yaml
- name: preflight_checks
  type: assert
  that:
    - "build_version is defined"
    - "environments | length > 0"
    - "test_coverage >= min_coverage"
  msg: "Preflight checks failed -- verify build_version, environments, and test_coverage"

- name: verify_triage
  type: assert
  that:
    - "needs_review"
    - "not auto_approved"
    - "not rejected"
    - "not deferred"
  msg: "Triage gate did not produce expected results for severity={{ severity }}"
```

---

## reload_workflow

Replaces the running workflow definition with a fresh load of its source, so a long-running (looping) workflow can pick up changes, including changes it made itself, without restarting Markov. Requires `markov run --allow-reload`: a run whose workflow can reach this step (through its sub-workflow calls) does not start without it. Running another workflow of the same project, one that cannot reach a reload, needs no flag.

What changes when:

- **Steps already running keep their definition.** The rest of the workflow that ran the reload continues with the steps, step types, rules and scripts directory it started with.
- **Sub-workflows called after the reload use the new definition.** A loop written as recursion (an `iterate` workflow that calls itself) therefore runs each iteration on the definition current when that iteration was called.
- **A failed load changes nothing.** An unreadable or invalid source, or one missing a `require`d workflow, fails the step and the current definition stays. Use `ignore_errors` to keep looping.

Concurrent reloads (from `for_each` iterations) are applied one at a time.

A run that reloaded **cannot be resumed** (`markov resume` refuses it), because its completed steps came from more than one definition. Every reload is recorded in the [state store](state-store.md#reloads-table) and reported with a [`workflow_reloaded`](callbacks.md#run-lifecycle-events) event.

With a debugger control channel attached (`--control stdin`, as the IDE uses), the step does not load on its own: it emits a [`reload_pending`](debugging.md#reloading-the-workflow) debug event describing the candidate and waits for `reload` (optionally naming another source) or `reject_reload`.

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `source` | string | no | Workflow file or directory to load. Defaults to the run's own source (the path given to `markov run`). |
| `require` | string[] | no | Workflow names the new definition must contain. |

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `reloaded` | bool | Whether the definition was replaced. |
| `source` | string | The source loaded (or attempted). |
| `old_digest`, `new_digest` | string | Source-tree digests before and after. |
| `reload` | int | How many reloads this process has applied, including this one. |
| `changes` | map | `added_workflows`, `removed_workflows`, `changed_workflows` (each with `workflow`, `added_steps`, `removed_steps`, `changed_steps`) and `other_changed` (top-level sections such as `vars` or `rules`). |
| `rejected` | bool | Set when a control-channel client answered `reject_reload`. |
| `error` | string | Why the load failed. |

### Example

```yaml
- name: iterate
  vars:
    i:
  steps:
    - name: work
      workflow: work            # the definition current when this iteration was called
    - name: improve
      type: claude              # may edit workflows/work.yaml
      params: {prompt: "...", allowed_tools: [Read, Edit]}
    - name: reload
      type: reload_workflow
      ignore_errors: true       # a broken edit keeps the current definition
      params:
        require: [iterate, work]
    - name: next
      workflow: iterate         # picks up the reloaded iterate and work
      when: "i < 10"
      vars:
        i: "{{ i + 1 }}"
```

---

## codify

One iteration of [`markov codify`](cli.md#markov-codify) on a workflow of the project: runs it live through a recording proxy, lets a retro agent move one job out of its agent steps in a copy of the project, runs the copy against the recording, and installs it in the project when its outputs still match. Use it to drive codification from a workflow of your own; follow it with [`reload_workflow`](#reload_workflow) when it promoted a version, and call your loop again.

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `workflow` | string | yes | The workflow to codify (it and the workflows it calls may change). |
| `model` | string | no | Model for the retro agent and the judge. Default `claude-haiku-5-5`. |
| `keep` | string[] | no | Steps (`step` or `workflow.step`) that must stay agent steps. |
| `project` | string | no | The project directory to codify and install into. Default the run's source; set it when the run's source is a copy (an IDE run). |
| `codify_dir` | string | no | History directory. Default `<project>.codify`. |
| `prompt_file` | string | no | Replacement for the retro agent's prompt. |

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `status` | string | `passed`, `failed`, `no_change` (nothing left to codify) or `live_failed` (the workflow itself failed or wrote no files). |
| `promoted` | bool | A new version was installed. |
| `version` | string | The version installed, when promoted. |
| `reasons` | list | Why a candidate was rejected. |
| `judged` | bool | The outputs were compared by content because an agent produced them. |
| `iteration`, `extracted`, `agent_steps`, `spent_usd` | | The iteration directory, the job moved out of the agent, the agent steps the run had, and the model spend. |

The markov binary for the child runs is `MARKOV_BIN` when set, else the running one.

### Example

```yaml
- name: loop
  steps:
    - name: codify
      type: codify
      register: c
      params:
        workflow: codify-me
    - name: reload
      type: reload_workflow
      when: "c.promoted"
    - name: again
      workflow: loop
      when: "c.status == 'passed'"
```

---

## OpenShell sandboxes

The `openshell_*` steps run work in [NVIDIA OpenShell](https://github.com/NVIDIA/OpenShell) sandboxes: register the credentials sandboxes may use, build an image, create a sandbox from it, copy files in, run commands, copy results out, and delete it. They need an OpenShell gateway; on a Linux workstation that is the `openshell-gateway` user service with the Podman driver (`systemctl --user enable --now podman.socket openshell-gateway`, then `openshell gateway add https://127.0.0.1:17670 --local --name openshell`).

**Transport.** Every step except `openshell_build` reaches the gateway through a transport. Choose it, in order of precedence, with the step's `transport` param, `markov run --openshell-transport`, or the workflow file's `openshell:` block ([workflow file](workflow-file.md#openshell)). Both behave the same except where noted:

| | `cli` (default) | `sdk` |
|---|---|---|
| How | Runs the `openshell` command, which uses its own active gateway and credentials. | Talks gRPC to the gateway through the OpenShell Go SDK, finding the gateway the way the CLI does (see [workflow file](workflow-file.md#openshell)). Needs no `openshell` command. |
| Copies | `openshell sandbox upload` / `download`. | A tar stream through exec, so the image needs `sh` and `tar`. |
| `openshell_create` `policy` | Supported. | Not supported (the SDK cannot parse policy files); the step fails. |
| `openshell_exec` `stdin` | At most 4 MiB (the CLI's limit). | Streamed in chunks. |
| Gateways using OIDC or Cloudflare auth | Supported. | Not supported; use `cli`. |
| Latency | Each exec waits about 0.3 s before starting (see below). | None added. |

**Stopping commands.** When a step is cancelled (Ctrl-C, the step's `timeout:` field) or `openshell_exec`'s `timeout` passes, the command in the sandbox is stopped. OpenShell 0.1.5 does not do this on its own: its exec timeout reports 124 but leaves the command running, and a command started by the CLI's one-shot exec keeps running when the CLI is killed. So Markov enforces `timeout` itself by ending the exec stream, and the `cli` transport holds the CLI's stdin open for 0.3 s so the CLI uses its streaming exec, which stops the command when the CLI goes away.

**Copies.** `openshell_upload` and `openshell_download` copy `src` *into* the directory `dest` under its own name, whether `src` is a file or a directory: uploading `./site` to `/sandbox` gives `/sandbox/site`. Rename with `openshell_exec` if you need another name.

**What a sandbox can read.** OpenShell's default policy lets sandbox processes read `/bin`, `/usr`, `/lib`, `/etc`, `/proc` and `/var/log`, and write `/sandbox` (the working directory) and `/tmp`. Install code baked into an image under `/usr` (not `/opt`), or pass a `policy` that allows its path. Sandbox names are DNS labels of at most 19 characters.

**Cleanup.** A sandbox stays up until it is deleted, including when a later step fails. Delete it in the workflow's `always:` section (see the example under [openshell_delete](#openshell_delete)).

Live output of `openshell_build` and `openshell_exec` is reported as `step_progress` events of kind `output`, with `stream` (`stdout` or `stderr`) and `text` (one line).

## openshell_build

Builds an image with `podman build`. OpenShell does not build images: a local gateway with the Podman driver runs images from local Podman storage, so a bare name is tagged `localhost/<name>` for it to find. For a remote gateway, tag the image for a registry the gateway can pull from and set `push`.

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `tag` | string | yes | Image tag. A name without a registry or path (`site:1`) becomes `localhost/site:1`. |
| `context` | string | no | Build context directory. Default `.`. |
| `containerfile` | string | no | Containerfile or Dockerfile path (podman's default lookup when unset). |
| `build_args` | map | no | Build arguments. |
| `target` | string | no | Build stage to stop at. |
| `pull` | bool | no | Always pull base images. |
| `no_cache` | bool | no | Build without the layer cache. |
| `push` | bool | no | Push the image after building. |
| `binary` | string | no | Default `podman`. |

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `image` | string | The image reference to pass to `openshell_create`. |
| `id` | string | The image ID. |

## openshell_create

Creates a sandbox and returns once it is ready.

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `image` | string | no | Image reference. Default: the gateway's default image. |
| `name` | string | no | Sandbox name: lowercase letters, digits and `-`, at most 19 characters. Default `mk-<step>-<random>`, unique per call, so a `for_each` gets one sandbox per item. |
| `cpu` | string | no | CPU limit. |
| `memory` | string | no | Memory limit, for example `4Gi`. |
| `gpu` | bool or int | no | `true` for a GPU, or a count. |
| `env` | map | no | Environment variables for the sandbox. |
| `providers` | string[] | no | OpenShell providers to attach. |
| `policy` | string | no | Policy file (`cli` transport only). |
| `labels` | map | no | Labels. Markov adds `markov-run`, `markov-workflow` and `markov-step` (yours win), so `openshell sandbox list --selector markov-run=<run id>` finds a run's sandboxes. |
| `transport` | string | no | `cli` or `sdk`. |

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `name` | string | The sandbox name; pass it to the other steps. |
| `id` | string | The sandbox ID. |
| `phase` | string | `Ready`. |
| `workspace` | string | The OpenShell workspace. |
| `image` | string | The image requested. |

### Failure Conditions

- The image cannot be pulled or found, a sandbox with the name exists, or the gateway is unreachable.

## openshell_upload

Copies a local file or directory into a sandbox directory.

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `sandbox` | string | yes | Sandbox name. |
| `src` | string | yes | Local file or directory. Relative paths resolve against the run's working directory. |
| `dest` | string | no | Sandbox directory to copy into (created if missing). Default `/sandbox`. |
| `git_ignore` | bool | no | Skip files `.gitignore` excludes when `src` is in a Git work tree. Default `false`: copy everything. |
| `transport` | string | no | `cli` or `sdk`. |

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `sandbox` | string | The sandbox. |
| `path` | string | Where `src` landed: `dest/<base name of src>`. |

## openshell_exec

Runs a command in a sandbox and streams its output.

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `sandbox` | string | yes | Sandbox name. |
| `command` | string or string[] | yes | A string runs under `sh -c`; a list is the argv. |
| `workdir` | string | no | Working directory in the sandbox. |
| `env` | map | no | Environment variables for the command. |
| `timeout` | int | no | Seconds before the command is stopped (by Markov; see [Stopping commands](#openshell-sandboxes)). |
| `stdin` | any | no | Standard input: a string as it is, any other value as JSON. At most 4 MiB on the `cli` transport. |
| `transport` | string | no | `cli` or `sdk`. |

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `stdout` | string | Standard output. |
| `stderr` | string | Standard error. |
| `exit_code` | int | The command's exit code (124 when `timeout` stopped it). |
| `timed_out` | bool | `timeout` stopped the command. |

### Failure Conditions

- The command exits non-zero (the error includes stderr) or is stopped by `timeout`. The output is still registered, so `failed_when` or a later step can inspect it.
- The sandbox does not exist or the gateway is unreachable.

## openshell_download

Copies a file or directory out of a sandbox into a local directory.

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `sandbox` | string | yes | Sandbox name. |
| `src` | string | yes | Path in the sandbox. |
| `dest` | string | no | Local directory to copy into (created if missing). Default `.`, the run's working directory. |
| `transport` | string | no | `cli` or `sdk`. |

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `sandbox` | string | The sandbox. |
| `path` | string | Where `src` landed: `dest/<base name of src>`. |

## openshell_provider

Makes sure an OpenShell [provider](https://github.com/NVIDIA/OpenShell) exists with the given credentials, registering its provider profile first when `profile` names a file. A provider holds a secret in the gateway; a sandbox created with `providers: [<name>]` gets only a placeholder, which the gateway's proxy replaces with the real value on requests the profile allows (its hosts, from its binaries). Run it before `openshell_create`, so the workflow carries its own setup.

It is idempotent: the profile is imported when missing and updated only when the file differs from the registered one; the provider is created when missing, and otherwise has its credentials replaced, so a rotated key takes effect on the next run.

**Secrets.** `credentials` values are never in the step's output, never printed by `--verbose` (shown as `<redacted>`), and never on a command line (the `cli` transport hands them to `openshell` through its environment). Pass them from variables, `"{{ ANTHROPIC_API_KEY }}"`, rather than writing them in the workflow: the `step_started` event carries the step's params as written, before templates are rendered.

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Provider name; `openshell_create` attaches it by this name. |
| `type` | string | no | The provider profile's `id`. Default: `name`. |
| `profile` | string | no | Provider profile YAML to import, or to update when it differs (`cli` transport only: only the openshell CLI parses profile files). |
| `credentials` | map | no | Credential environment variable (as the profile names it) to secret value. |
| `transport` | string | no | `cli` or `sdk`. |

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `name` | string | The provider. |
| `type` | string | Its profile id. |
| `created` | bool | The provider was created (otherwise its credentials were replaced). |
| `profile` | string | `imported`, `updated`, `unchanged`, or empty without `profile`. |
| `credential_keys` | string[] | The credential names set, never their values. |

### Failure Conditions

- The profile file is invalid, or the provider exists with a different type (delete it to change its type).

### Example

```yaml
- name: relay-provider
  type: openshell_provider
  params:
    name: relay-claude
    profile: openshell/relay-claude.yaml
    credentials:
      ANTHROPIC_API_KEY: "{{ ANTHROPIC_API_KEY }}"
- name: sandbox
  type: openshell_create
  register: sandbox
  params:
    providers: [relay-claude]
```

## openshell_delete

Deletes one or more sandboxes. Deleting a sandbox that does not exist succeeds, so it is safe in `always:`.

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `sandbox` | string or string[] | yes | Sandbox name or names. |
| `transport` | string | no | `cli` or `sdk`. |

### Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `deleted` | string[] | The names deleted. |

### Example

The whole lifecycle; `always:` deletes the sandbox even when a step fails:

```yaml
workflows:
  - name: main
    steps:
      - name: image
        type: openshell_build
        register: image
        params:
          tag: report-runner:1
          context: ./image
      - name: sandbox
        type: openshell_create
        register: sandbox
        params:
          image: "{{ image.image }}"
      - name: inputs
        type: openshell_upload
        params:
          sandbox: "{{ sandbox.name }}"
          src: ./data
      - name: report
        type: openshell_exec
        params:
          sandbox: "{{ sandbox.name }}"
          workdir: /sandbox
          command: python3 /usr/local/lib/report.py data > report.md
      - name: fetch
        type: openshell_download
        params:
          sandbox: "{{ sandbox.name }}"
          src: /sandbox/report.md
    always:
      - name: cleanup
        type: openshell_delete
        when: "sandbox.name"
        params:
          sandbox: "{{ sandbox.name }}"
```

---

## llm_invoke

Reserved primitive for future LLM integration. Steps using this type will fail with `"no executor for type"`.

This type is recognized by the parser as a valid primitive, so workflow YAML referencing it will pass validation. However, no executor is registered for it at runtime.

```yaml
# This will parse successfully but fail at execution time.
- name: classify
  type: llm_invoke
  params:
    model: sonnet
    prompt: "Classify this issue..."
  register: classification
```

## Jev support

`type: jev` calls a configured Jev-compatible API and registers structured answers. See [Jev decisions](jev.md) for parameters, response metadata, and errors.
{% endraw %}
