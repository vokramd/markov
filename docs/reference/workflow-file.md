{% raw %}
# Workflow File Reference

This is the canonical reference for the Markov workflow file format. A workflow file is a YAML document that defines one or more workflows, their steps, variables, rules, and custom step types.

Markov accepts either a single workflow YAML file or a workflow directory. Both input forms resolve to the same runtime schema.

## Top-Level Fields

| Field | YAML Key | Type | Required | Default | Description |
|-------|----------|------|----------|---------|-------------|
| Entrypoint | `entrypoint` | string | yes | -- | Name of the workflow to execute first. Must match a workflow defined in `workflows`. |
| Namespace | `namespace` | string | no | `"default"` | Kubernetes namespace used for `k8s_job` steps. Can be overridden by CLI flag or in-cluster ServiceAccount namespace. |
| Forks | `forks` | int | no | `5` | Default maximum concurrency for `for_each` fan-outs. Set to 0 or omit to use the default. |
| Vars | `vars` | map[string]any | no | `{}` | Global variables available to all workflows. Overridden by workflow-level vars, step-level vars, and CLI `--var` flags. |
| Secrets | `secrets` | string[] | no | `[]` | Names of vars whose values are secret: masked as `<secret:NAME>` in events and log lines. Vars named like secrets (`GITHUB_TOKEN`, `db_password`) are masked without being listed. In a directory workflow this goes in `meta.yaml`. See [Secrets](variables-and-context.md#secrets). |
| Rules | `rules` | Rule[] | no | `[]` | Named rule definitions used by `gate` steps. Rules can also be loaded from external files. |
| StepTypes | `step_types` | map[string]StepType | no | `{}` | User-defined step type definitions that wrap a primitive type with default parameters. |
| Workflows | `workflows` | Workflow[] | yes | -- | List of workflow definitions. At least one must be defined, and the entrypoint must reference one by name. |
| OpenShell | `openshell` | map | no | -- | How `openshell_*` steps reach the OpenShell gateway. See [OpenShell](#openshell). |

### OpenShell

Settings for the [`openshell_*` steps](step-types.md#openshell-sandboxes). Every key is optional; an empty block means the `cli` transport with the `openshell` command's own active gateway.

| Key | Description |
|-----|-------------|
| `transport` | `cli` (default) runs the `openshell` command; `sdk` talks gRPC through the OpenShell Go SDK ([differences](step-types.md#openshell-sandboxes)). Overridden by `markov run --openshell-transport` and by a step's `transport` param. |
| `workspace` | OpenShell workspace. Default `default`. |
| `gateway` | A gateway name from `openshell gateway list`; empty means the CLI's active gateway (`OPENSHELL_GATEWAY`, else `openshell gateway select`). `sdk` reads a named gateway's endpoint and mTLS certificates from the CLI's config (`~/.config/openshell/gateways/<name>/`), and also accepts a `host:port` address (`http://host:port` for plaintext). |
| `binary` | `cli`: the command to run. Default `openshell`. |
| `token_env` | `sdk`: environment variable holding a bearer token. |
| `ca_file` | `sdk` with an address: CA certificate for the gateway's TLS. |
| `cert_file`, `key_file` | `sdk` with an address: client certificate and key for an mTLS gateway. |

```yaml
openshell:
  transport: cli
  gateway: openshell
```

---

## Directory Workflow Layout

For larger workflows, pass a directory instead of a single YAML file:

```bash
markov validate examples/dir-based-hello-world
markov run examples/dir-based-hello-world
```

Directory mode uses required conventional filenames:

```text
pipeline/
  meta.yaml
  vars.yaml
  rules.yaml
  step_types.yaml
  workflows/
    main.yaml
    deploy.yaml
```

Step types may also be split across a directory:

```text
pipeline/
  meta.yaml
  vars.yaml
  rules.yaml
  step_types/
    shell.yaml
    http.yaml
  workflows/
    main.yaml
    deploy.yaml
```

The files map directly to the single-file schema:

| File | YAML Shape | Description |
|------|------------|-------------|
| `meta.yaml` | map | `entrypoint`, `namespace`, `forks`, and `openshell` |
| `vars.yaml` | map | Top-level variables, without a wrapping `vars:` key |
| `vars/*.yaml` | map | Optional. More variables, merged in filename order after `vars.yaml`; a variable may be defined only once |
| `rules.yaml` | list | Rule definitions, without a wrapping `rules:` key |
| `step_types.yaml` | map | Step type definitions, without a wrapping `step_types:` key |
| `step_types/*.yaml` | map | Additional step type definition maps, merged by filename |
| `scripts/` | directory | Scripts referenced by `script_exec` `path` values |
| `workflows/*.yaml` | map | One workflow object per file, without a wrapping `workflows:` key |

In directory mode, workflow filenames are only organizational. Markov loads every `workflows/*.yaml` file and identifies workflows by each file's `name:` field. The `meta.yaml` `entrypoint` and the CLI `--workflow` override both refer to that workflow name, not to the YAML filename. For example, `markov run pipeline/ --workflow deploy` runs the workflow object whose `name` is `deploy`; it does not require or specially search for `workflows/deploy.yaml`.

Minimal example:

```yaml
# meta.yaml
entrypoint: main
forks: 2
```

```yaml
# vars.yaml
greeting: hello
```

```yaml
# rules.yaml
[]
```

```yaml
# step_types.yaml
echo_local:
  base: shell_exec
```

```yaml
# workflows/main.yaml
name: main
steps:
  - name: hello
    type: echo_local
    params:
      command: "echo '{{ greeting }}'"
```

Validation runs after the directory is merged into a single in-memory workflow file. Duplicate workflow names, duplicate rule names, and duplicate step type names across step type files are validation errors. Missing required category files are errors; for step types, either `step_types.yaml` or a `step_types/` directory must exist. Empty category files are allowed for `vars.yaml`, `rules.yaml`, and `step_types.yaml`; an empty `step_types/` directory is also allowed.

External rule includes still use the `file` field. In directory mode, relative rule include paths resolve from the workflow directory root:

```yaml
# rules.yaml
- file: rules/common.yaml
```

Runtime artifact paths are left exactly as written because they may refer to container paths or mounted volumes.

---

## Workflow

A workflow is a named sequence of steps. Workflows can invoke other workflows as sub-workflows.

| Field | YAML Key | Type | Required | Default | Description |
|-------|----------|------|----------|---------|-------------|
| Name | `name` | string | yes | -- | Unique workflow name. Referenced by `entrypoint` and by steps that invoke sub-workflows. |
| Description | `description` | string | no | `""` | Human-readable description of what this workflow does. |
| Vars | `vars` | map[string]any | no | `{}` | Workflow-scoped variables. Non-nil values override global vars. Use `null` values to declare required variables that must be passed by the caller. |
| Steps | `steps` | Step[] | yes | -- | Ordered list of normal steps to execute. |
| Rescue | `rescue` | Step[] | no | `[]` | Ordered failure handlers. Runs after a normal step fails, before `always`. |
| Always | `always` | Step[] | no | `[]` | Ordered teardown handlers. Runs after normal success or failure. |

```yaml
workflows:
  - name: deploy
    description: "Deploy the application to staging"
    vars:
      environment: staging
      dry_run: false
    steps:
      - name: build
        type: shell_exec
        params:
          command: make
          args: ["build"]
    rescue:
      - name: report-failure
        type: shell_exec
        params:
          command: "echo 'deployment failed'"
    always:
      - name: cleanup
        type: shell_exec
        params:
          command: "make clean"
```

### Failure handling and teardown

`rescue` and `always` use the same step shape and capabilities as `steps`, including `when`, `register`, sub-workflows, and step types. Step names must be unique across all three sections of a workflow.

Markov executes lifecycle sections as follows:

1. Execute `steps` in order.
2. If a normal step fails, execute every `rescue` step in order.
3. After normal success or failure, execute every `always` step in order.
4. Report the run as failed if the main workflow, a rescue handler, or an always handler failed. Handler errors are included with the original failure.

Markov continues through the remaining handlers when a rescue or always step fails, so cleanup steps later in the list still get a chance to run. A failure in `always` after otherwise successful normal steps fails the run; it does not invoke `rescue`.

A durable `pause` is nonterminal, so neither section runs while the workflow waits for `markov resume`. This preserves resources such as approval locks that must remain in place until the run has actually succeeded or failed.
Lifecycle handlers themselves must finish: a handler that requests a `pause` causes the run to fail rather than suspending teardown.

Lifecycle step records are stored as `rescue/<step-name>` and `always/<step-name>`. Unlike normal completed steps, they run again if a resumed run fails again, ensuring teardown is repeated for each terminal attempt.

---

## Step

A step is the unit of execution within a workflow. Each step either runs a primitive type (or custom step type) or invokes a sub-workflow.

| Field | YAML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Name | `name` | string | yes | Unique name within its workflow. Used for logging, state tracking, and variable scoping. |
| Type | `type` | string | conditional | Step type name. Must be a known primitive or a defined `step_type`. Required unless `workflow` is set. |
| Workflow | `workflow` | string | conditional | Name of a sub-workflow to invoke. Required unless `type` is set. Cannot be combined with `type`. It may be a template, such as `submit-{{ test.arm }}`, resolved when the step runs (per item under `for_each`); validation then can't check the name, and an unknown result fails the step. A resume refuses a sub-run whose name now resolves to a different workflow than it ran, because step state is kept per workflow. `type` can't be templated. |
| WorkflowNames | `workflow_names` | string[] | no | The workflows a templated `workflow` may resolve to. Each must exist (checked when the definition loads), and a run whose template resolves to another name fails the step. Tools such as markovd's diagram draw these as the step's possible targets. Only allowed with a templated `workflow`. |
| When | `when` | string | no | Pongo2 boolean expression. If it evaluates to false, the step is skipped. Has access to all in-scope variables. |
| Register | `register` | string | no | Variable name to store the step's output. The result is available to subsequent steps. |
| Timeout | `timeout` | int | no | Step timeout in seconds. Behavior depends on the step type (e.g., k8s_job watch timeout). |
| Params | `params` | map[string]any | no | Step-type-specific parameters. Values can contain Pongo2 template expressions. |
| Vars | `vars` | map[string]any | no | Variables to pass to a sub-workflow invocation. Only meaningful when `workflow` is set. |
| Artifacts | `artifacts` | map[string]Artifact | no | Artifact definitions to load after the step completes. Each key becomes a variable name. |
| ForEach | `for_each` | string | no | Expression that resolves to a list. The step is executed once per item in the list (fan-out). |
| ForEachKey | `for_each_key` | string | no | Field name on each item to use as a stable iteration key. Ensures deterministic ordering and state tracking across runs. |
| ForEachSort | `for_each_sort` | string | no | Field name to sort items by before iteration begins. Provides predictable execution order. |
| As | `as` | string | conditional | Variable name for the current iteration item. Required when `for_each` is set. |
| Concurrency | `concurrency` | int | no | Override the global `forks` value for this specific `for_each` step. |
| ForEachWhen | `for_each_when` | string | no | Expression evaluated per item, with the item bound to `as`. Items where it is false are skipped. Requires `for_each`. (`when` is evaluated once, before the loop.) |
| That | `that` | string[] | no | List of assertion expressions. Used with `type: assert`. All must evaluate to true or the step fails. |
| Msg | `msg` | string | no | Custom failure message for `assert` steps. Displayed when any assertion in `that` fails. |
| Rules | `rules` | string[] | no | List of rule names to evaluate. Used with `type: gate`. Must reference rules defined in the top-level `rules` block. |
| Facts | `facts` | map[string]any | no | Additional facts to inject into the rule evaluation context for `gate` steps. Use this to pass step results or computed values into rules. |
| Description | `description` | string | no | Free text for readers and tools (long-form notes on what the step is for). It does not change what the step does, and editing it does not count as a step change on resume. |
| IgnoreErrors | `ignore_errors` | bool | no | When the step fails, record the failure and carry on. The registered output keeps whatever the step produced, plus `failed: true` and `error`. On a sub-workflow, the registered context gets the same two keys; on `for_each`, failed items are marked that way and the loop runs every item. Errors in the workflow itself, such as a template that doesn't parse, still fail the step. |
| FailedWhen | `failed_when` | string | no | Expression that alone decides whether the step failed, evaluated after it runs. It sees the step's output as `result` and under the `register` name; an executor error is in `result.error`. True fails the step (and `ignore_errors` still applies); false makes it succeed even if the executor reported an error. Not allowed on sub-workflow steps. |
| Secret | `secret` | bool or string[] | no | Marks the step's output as secret, so its values are masked in events and log lines from the moment the step returns (its own `step_completed` included). `true` marks every string in the output; a list marks dotted paths into it (`[token, body.token]`); on `set_fact` the paths are fact names. Later steps still get the real values. Not allowed on sub-workflow, `gate` and `assert` steps. See [Secrets](variables-and-context.md#secrets). |

```yaml
steps:
  # Simple step with a condition
  - name: notify
    type: shell_exec
    when: "needs_review"
    params:
      command: echo
      args: ["Flagged for review"]

  # Fan-out with concurrency override
  - name: process-all
    for_each: "items"
    for_each_key: "id"
    for_each_sort: "priority"
    as: item
    concurrency: 3
    workflow: process-item
    vars:
      value: "{{ item }}"

  # Sub-workflow invocation
  - name: finalize
    workflow: cleanup
    vars:
      target: "{{ target }}"
```

---

## Rule

Rules are used by `gate` steps to make decisions based on the current variable context. Rules are evaluated by a forward-chaining rule engine (Grule). They fire in salience order; when a rule fires and sets facts, remaining rules are re-evaluated against the updated context.

| Field | YAML Key | Type | Required | Default | Description |
|-------|----------|------|----------|---------|-------------|
| Name | `name` | string | yes | -- | Unique rule name. Referenced by gate steps via the `rules` field. |
| File | `file` | string | no | -- | Path to an external YAML file containing rule definitions. The file must have a top-level `rules` key. When set, this entry is replaced by the rules loaded from the file. |
| Description | `description` | string | no | `""` | Human-readable description of the rule's purpose. |
| Salience | `salience` | int | no | `0` | Priority. Higher values fire first. Use to control evaluation order when multiple rules may match. |
| When | `when` | string | no | -- | Condition expression (Pongo2 syntax). The rule fires when this evaluates to true. |
| Action | `action` | string | no | `"continue"` | What happens when the rule fires. `continue` proceeds; `pause` persists a receipt and stops until an explicit resume override; `skip` is recorded but its control flow is not yet implemented. |
| SetFact | `set_fact` | map[string]any | no | `{}` | Variables to set in the workflow context when the rule fires. These become available to all downstream steps and rules. |

### Rule scoping

Rules automatically see global vars, workflow vars, CLI `--var` overrides, and `set_fact` values. Rules do **not** see step results, register outputs, or artifact data directly. To pass step data into a rule evaluation, use the `facts` field on the gate step:

```yaml
rules:
  - name: auto_approve
    salience: 100
    when: "score >= 8"
    action: continue
    set_fact:
      approved: true

workflows:
  - name: main
    steps:
      - name: analyze
        type: shell_exec
        params:
          command: echo
          args: ["9"]
        register: analysis

      - name: approval_gate
        type: gate
        facts:
          score: "{{ analysis.score }}"
        rules:
          - auto_approve
```

### External rule files

Rules can be loaded from external YAML files. The file must contain a top-level `rules` key with an array of rule definitions:

```yaml
# rules/triage.yaml
rules:
  - name: critical_auto_approve
    salience: 100
    when: "severity == 'critical' and confidence > 0.95"
    action: continue
    set_fact:
      auto_approved: true
```

Reference it in the workflow file:

```yaml
rules:
  - file: rules/triage.yaml
```

---

## StepType

Step types let you create reusable abstractions over primitive types. A step type wraps a primitive with pre-configured parameters, reducing repetition across steps.

| Field | YAML Key | Type | Required | Default | Description |
|-------|----------|------|----------|---------|-------------|
| Base | `base` | string | yes | -- | The primitive type this step type wraps. Must be one of the primitive types listed below. |
| Description | `description` | string | no | `""` | Human-readable description. |
| Defaults | `defaults` | map[string]any | no | `{}` | Default parameter values. Merged after `job` params and before `params`. |
| Job | `job` | map[string]any | no | `{}` | Base parameters merged first (lowest priority). Typically used for image, command, and other job-level settings. |
| Params | `params` | map[string]any | no | `{}` | Additional parameters merged after `defaults`. |

Parameter merge order (later wins): `job` -> `defaults` -> `params` -> step-level `params`.

```yaml
step_types:
  echo_job:
    base: k8s_job
    description: "Run a simple echo in a K8s Job"
    job:
      image: alpine:3.19
      command: ["/bin/sh", "-c"]
      ttl_seconds: 300
      backoff_limit: 0

  python_task:
    base: k8s_job
    description: "Run a Python script in a container"
    job:
      image: python:3.12-slim
      command: ["python3", "-c"]
    defaults:
      ttl_seconds: 600
      backoff_limit: 1
```

---

## Artifact

Artifacts define files to load after a step completes. The loaded content becomes available as a variable for use in subsequent steps.

| Field | YAML Key | Type | Required | Default | Description |
|-------|----------|------|----------|---------|-------------|
| Path | `path` | string | no | -- | File path to load. Supports Pongo2 template expressions (e.g., `/app/artifacts/{{ issue }}/result.yaml`). |
| Format | `format` | string | no | raw text | Parse format. One of: `yaml` (parsed to dict/list), `markdown` (frontmatter extracted), `markdown_table` (table parsed). Defaults to raw text if omitted. |
| Source | `source` | string | no | auto-detect | Where to load the file from. One of: `local` (local filesystem), `k8s` (from a Kubernetes pod/volume). Auto-detected if omitted. |
| Optional | `optional` | bool | no | `false` | If true, a missing file returns nil instead of causing an error. Useful for conditional artifact checks. |

```yaml
artifacts:
  result:
    path: "/app/artifacts/{{ issue }}/analysis/result.yaml"
    format: yaml
  blockers:
    path: "/app/artifacts/{{ issue }}/blockers.yaml"
    format: yaml
    optional: true
```

---

## Primitive Types

These are the built-in step types available in every workflow file.

| Type | Description |
|------|-------------|
| `k8s_job` | Run a container as a Kubernetes Job. Requires a configured K8s client. |
| `k8s_job_wait` | Wait for an existing Kubernetes Job by name. Requires a configured K8s client. |
| `http_request` | Make an HTTP request. |
| `shell_exec` | Execute a shell command on the local machine. |
| `script_exec` | Run an inline script or a script from the workflow `scripts/` directory on the local machine. |
| `write_file` | Write content to a file on the runner, creating parent directories. |
| `claude` | Run the Claude Code CLI with a prompt or skill, streaming its output with enforced turn, token, and duration limits. |
| `ansible_playbook` | Run `ansible-playbook` with an inventory, extra vars, and CLI options. |
| `ansible` | Run an ad-hoc `ansible` module against a host pattern. |
| `prompt` | Read a response from an interactive terminal on the local machine. |
| `gate` | Evaluate named rules against the current context. Controls flow based on rule outcomes. |
| `load_artifact` | Load file artifacts into the variable context. |
| `set_fact` | Set variables in the current workflow context. |
| `assert` | Validate expressions. Fails the step if any assertion in `that` is false. |
| `openshell_provider`, `openshell_build`, `openshell_create`, `openshell_upload`, `openshell_exec`, `openshell_download`, `openshell_delete` | Build a sandbox image and run work in [OpenShell](step-types.md#openshell-sandboxes) sandboxes. |
| `llm_invoke` | Reserved for LLM integration. Not yet implemented. |

---

## Validation Rules

The parser enforces the following rules when loading a workflow file. Validation errors prevent execution.

Structured Markov fields are strict: unsupported keys at the top level or in
workflow, step, rule, step-type, and artifact objects are rejected rather than
silently ignored. This catches misspellings and example-only fields such as
`on_pause`. Free-form maps remain open for executor and workflow data:
`params`, `vars`, `facts`, artifact file contents, and the `job`, `defaults`,
and `params` maps of a custom step type may contain application-specific keys.

1. **Structured fields must be known**; unknown keys are parsing errors.
2. **`entrypoint` is required** and must match the `name` of a defined workflow.
3. **At least one workflow** must be defined in the `workflows` list.
4. **Workflow names must be unique** across all workflows in the file.
5. **Step names must be unique** within their parent workflow.
6. **Steps must have either `type` or `workflow`** set (not neither, not both without `type`).
7. **`for_each` requires `as`** -- you must name the iteration variable.
8. **`type` must be a known primitive or a defined `step_type`** in the file's `step_types` map.
9. **Gate steps must reference at least one rule**, and every referenced rule must exist in the top-level `rules` block.
10. **`forks` defaults to 5** if set to 0 or a negative value.
11. **Sub-workflow references** must point to a workflow name that exists in the file.
12. **`secret`** is `true` or a list of dotted output paths, and is only allowed on steps that produce their own output (not sub-workflow, `gate` or `assert` steps).

---

## Template Expressions

Values in `params`, `vars`, `when`, `that`, `msg`, artifact `path`, and other string fields support Pongo2 template syntax. Pongo2 is a Django/Jinja2-style template engine.

Common patterns:

```yaml
# Variable interpolation
args: ["echo '{{ greeting }}'"]

# Filters
when: "items | length > 0"

# Arithmetic
vars:
  stage: "{{ stage + 1 }}"

# Comparisons in conditions
when: "severity == 'critical' and confidence > 0.95"

# Negation
when: "not deferred and not rejected"

# None/null checks
when: "confidence == None"
```

---

## Complete Annotated Example

This example demonstrates all top-level sections working together: global vars, custom step types, rules with a gate, fan-out with sub-workflows, conditionals, assertions, and artifacts.

```yaml
---
# entrypoint (required): the workflow to run first
entrypoint: main

# namespace: K8s namespace for k8s_job steps
namespace: markov-demo

# forks: default max concurrency for for_each fan-outs
forks: 3

# vars: global variables accessible to all workflows
vars:
  environment: staging
  targets: ["api", "web", "worker"]
  max_retries: 3
  confidence_threshold: 0.8

# rules: named rule definitions for gate steps
rules:
  - name: high_confidence
    description: "Proceed when confidence is high enough"
    salience: 100
    when: "confidence >= confidence_threshold"
    action: continue
    set_fact:
      approved: true

  - name: low_confidence
    description: "Skip deployment when confidence is too low"
    salience: 50
    when: "confidence < confidence_threshold"
    action: skip
    set_fact:
      approved: false
      skip_reason: "Confidence below threshold"

# step_types: reusable step definitions wrapping primitives
step_types:
  run_script:
    base: k8s_job
    description: "Run a shell script in an Alpine container"
    job:
      image: alpine:3.19
      command: ["/bin/sh", "-c"]
      ttl_seconds: 300
      backoff_limit: 0

  python_task:
    base: k8s_job
    description: "Run a Python script"
    job:
      image: python:3.12-slim
      command: ["python3", "-c"]
    defaults:
      ttl_seconds: 600

# workflows: the workflow definitions
workflows:
  - name: main
    description: "Top-level orchestration workflow"
    steps:
      # Step 1: Run analysis and register the result
      - name: analyze
        type: shell_exec
        params:
          command: echo
          args: ["Running analysis for {{ environment }}"]
        register: analysis_result

      # Step 2: Load an artifact produced by analysis
      - name: load-results
        type: load_artifact
        artifacts:
          report:
            path: "/tmp/analysis/{{ environment }}/report.yaml"
            format: yaml
            optional: true

      # Step 3: Set a fact based on analysis
      - name: set-confidence
        type: set_fact
        vars:
          confidence: 0.92

      # Step 4: Gate decision based on rules
      - name: deploy-gate
        type: gate
        rules:
          - high_confidence
          - low_confidence

      # Step 5: Assert preconditions before deployment
      - name: check-approval
        type: assert
        that:
          - "approved"
          - "environment == 'staging' or environment == 'production'"
        msg: "Deployment not approved or invalid environment"

      # Step 6: Fan-out deployment across all targets
      - name: deploy-all
        for_each: "targets"
        for_each_sort: "name"
        as: target
        concurrency: 2
        workflow: deploy-target
        vars:
          service: "{{ target }}"
          env: "{{ environment }}"

      # Step 7: Final notification
      - name: notify
        type: shell_exec
        when: "approved"
        params:
          command: echo
          args: ["Deployment to {{ environment }} complete"]

  - name: deploy-target
    description: "Deploy a single service target"
    vars:
      service: null   # required, passed by parent
      env: null        # required, passed by parent
    steps:
      - name: build
        type: run_script
        params:
          args: ["echo 'building {{ service }} for {{ env }}'"]

      - name: test
        type: run_script
        params:
          args: ["echo 'testing {{ service }}' && sleep 5"]
        timeout: 120

      - name: release
        type: run_script
        when: "env == 'staging'"
        params:
          args: ["echo 'releasing {{ service }} to {{ env }}'"]
```

## Jev support

Top-level `connections` and `decisions` configure Jev endpoints and reusable questions. Directory workflows support optional `connections.yaml` and `decisions.yaml`. See [Jev decisions](jev.md) for the schema.
{% endraw %}
