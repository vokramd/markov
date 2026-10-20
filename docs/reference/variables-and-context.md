{% raw %}
# Variables and Context Reference

Every markov workflow execution operates on a **context** -- a `map[string]any` that holds all variables available during runtime. Templates, conditions, and expressions all resolve against this context.

## Context Build Order

The initial context is constructed by `buildContext` (in `engine.go:1136`) in this priority order (later entries override earlier ones):

| Priority | Source | Description |
|---|---|---|
| 1 (lowest) | Global `vars` | File-level `vars:` block at the top of the workflow file |
| 2 | Workflow `vars` | The `vars:` block on the specific workflow being run (non-nil values only) |
| 3 (highest) | CLI `--var` overrides | Variables passed via `--var key=value` on the command line |

**Important:** Nil values in workflow `vars` are treated as parameter declarations, not overrides. They signal that the workflow accepts a parameter but do not clear any existing value from global vars.

```yaml
# File-level vars (priority 1)
vars:
  environment: "dev"
  retries: 3

workflows:
  - name: deploy
    # Workflow vars (priority 2)
    # "target" is nil -- declares a parameter without setting a default
    # "environment" overrides the global value
    vars:
      target:
      environment: "staging"
    steps: [...]
```

```bash
# CLI override (priority 3) -- wins over both global and workflow vars
markov run workflow.yaml --var environment=prod
```

CLI override values use Markov's normal string coercion before entering the
context. Boolean literals become booleans, numbers become numeric values, JSON
arrays and objects become native structured values, and other values remain
strings:

| CLI value | Context value |
|---|---|
| `--var enabled=true` | `true` (bool) |
| `--var enabled=false` | `false` (bool) |
| `--var retries=3` | `3` (int) |
| `--var threshold=0.75` | `0.75` (float) |
| `--var 'targets=["api","worker"]'` | list |
| `--var environment=prod` | `"prod"` (string) |

This means `--var run_pipeline=false` correctly skips a step guarded by
`when: "run_pipeline"`; the non-empty string `"false"` is not placed in the
context.

A value that is all digits is read as a number, so quote-free commit hashes
that happen to contain no letters arrive as integers. Compare with care, or
pass such values through a workflow var set in YAML.

### Built-in variables

| Variable | Value |
|---|---|
| `markov_run_id` | The run's ID. It is saved with the run, so a resumed run sees the same value, and sub-workflows inherit it. Use it for unique output paths, such as `/results/{{ markov_run_id }}/summary.md`. |
| `markov_current_run_id` | The ID of the (sub-)run executing the step: the run ID at the top level, `<parent>-<call step>` in a sub-workflow, `<parent>-<step>-<key>` in a `for_each` iteration. A sub-workflow called by step `task` from here will run as `{{ markov_current_run_id }}-task`, which is how to pick its events out of the event log. |
| `markov_event_logs` | The absolute paths of the run's `jsonl://` callback files (an empty list without any). Events are written and synced as they happen, so a step can read everything that happened before it started, for example to let an agent review how earlier steps ran. A resumed run sees the logs of the resuming process. |

## Setting Variables at Runtime

There are three mechanisms for setting context variables during execution.

### set_fact

A `set_fact` step computes values and merges them directly into the context.

```yaml
- name: compute_labels
  type: set_fact
  vars:
    full_name: "{{ first_name }} {{ last_name }}"
    is_ready: "{{ count > 0 }}"
    config: "{{ raw_json | from_json }}"
```

Each fact is set into the context **immediately**, so later facts in the same `set_fact` step can reference earlier ones:

```yaml
- name: chained_facts
  type: set_fact
  vars:
    base_url: "https://{{ host }}:{{ port }}"
    health_endpoint: "{{ base_url }}/healthz"  # uses base_url set above
```

### register

The `register` field on a step stores the step's output into the context under the given key.

```yaml
- name: check_api
  type: http_request
  register: api_result
  params:
    url: "https://api.example.com/status"

- name: use_result
  type: shell_exec
  params:
    command: "echo {{ api_result.body }}"
```

### Gate set_fact

Rules in a `gate` step can include `set_fact` maps. When a rule fires, its facts are set via the FactStore and then merged back into the runtime context.

## Variable Scoping

### Sub-workflow isolation

Sub-workflows receive a **copy** of the parent context plus their own vars. Changes made inside a sub-workflow do **not** propagate back to the parent.

```yaml
# Parent workflow
- name: run_child
  workflow: child_workflow
  vars:
    input_data: "{{ some_value }}"
  register: child_output   # only way to get data back

# In child_workflow: modifications to context stay local
# Parent only sees what's in ctx["child_output"]
```

The only way to pass data back from a sub-workflow to the parent is via `register`, which stores the sub-workflow's entire final context map.

### for_each isolation

Each iteration of a `for_each` gets its own copy of the context with the current item set at `ctx[as]`. Iterations run concurrently and do not share state.

If `register` is set on a `for_each` step, it stores a **list of all iteration contexts** (one map per iteration).

## Sub-Workflow Var Merging

The context for a sub-workflow is built by `executeSubWorkflow` (in `engine.go:704`) in this order:

| Step | Operation |
|---|---|
| 1 | Copy all key-value pairs from the parent context |
| 2 | Merge the sub-workflow's own `vars:` block (non-nil values only) |
| 3 | If the step has `vars:`, template-render them against the **parent** context, then merge (string values are coerced) |

```yaml
- name: invoke_analysis
  workflow: analyze
  vars:
    target: "{{ item.name }}"     # rendered against parent context
    threshold: "{{ min_score }}"  # rendered against parent context
```

String values from step-level vars are passed through `coerceString` after rendering, so `"42"` becomes `int(42)`, `"true"` becomes `bool(true)`, etc. Non-string values are stored directly.

## register Behavior

What gets stored by `register` depends on the step type:

| Step Type | Stored Value |
|---|---|
| `shell_exec` | The executor's `Result.Output` map (typically contains `stdout`, `stderr`, `rc`) |
| `k8s_job` | The executor's `Result.Output` map (contains job output) |
| `k8s_job_wait` | The executor's `Result.Output` map (contains `job_name`, `namespace`, `status`, and optional `logs`) |
| `http_request` | The executor's `Result.Output` map (typically contains `body`, `status_code`, `headers`) |
| Sub-workflow (`workflow:`) | The entire sub-workflow context map (all vars at end of execution) |
| `for_each` | A list (`[]map[string]any`) of all iteration contexts |

## set_fact Evaluation

Each variable value in a `set_fact` step is evaluated by `evalFact` (in `facts.go:25`). The evaluation path depends on the value type:

| Value Pattern | Evaluation Path |
|---|---|
| String containing `{{` or `{%` | Template-rendered, then result is coerced via `coerceString` |
| String matching `{{ path \| from_json }}` or `{{ path \| fromjson }}` exactly | Direct context path resolution + JSON parse (preserves structure) |
| Plain string (no template delimiters) | Evaluated as boolean expression via `EvalBool` |
| Map with `"from"` key | Table lookup (see Lookup Syntax below) |
| Anything else (numbers, bools, lists, maps) | Stored directly without modification |

### Evaluation order within a single set_fact

Facts are set into the context one at a time, in the order they appear. Each fact can reference any fact that was set before it in the same step.

## Lookup Syntax

The lookup mechanism (from `lookupFact` in `facts.go:64`) performs a table-style query against a list of maps in the context.

```yaml
- name: find_owner
  type: set_fact
  vars:
    owner_name:
      from: "team_members"           # context path to a list of maps
      match:                          # all criteria must match (AND logic)
        department: "engineering"
        status: "{{ current_status }}"  # template values work in match
      field: "name"                   # extract this field from matched row
      default: "unknown"             # fallback if no match found
```

### Lookup fields

| Field | Required | Description |
|---|---|---|
| `from` | Yes | Dot-separated context path to a list of maps |
| `match` | Yes | Map of field-name to expected-value pairs. All must match (AND). Values are compared as strings via `fmt.Sprintf("%v", ...)`. Template expressions are rendered before comparison. |
| `field` | No | If specified, returns only this field from the matched row. If omitted, returns the entire matched row as a map. |
| `default` | No | Returned if the source list is nil or no rows match. If omitted and no match, returns nil. |

### How from resolves

The `from` value is resolved via dot-path context lookup. For example, `"load_data.artifacts.users.rows"` walks through `ctx["load_data"]["artifacts"]["users"]["rows"]`.

### Match behavior

- The source must be a list (`[]any` or `[]map[string]any`). Non-list sources cause an error.
- Items that are not maps are silently skipped.
- All match criteria must be satisfied for a row to match.
- Comparison is string-based: both the row value and the match value are converted via `fmt.Sprintf("%v", val)`.
- Only the **first** matching row is returned.

## Accessing Step Results

After a step completes and its output is registered, you can access it through the context.

```yaml
# Shell command output
- name: get_version
  type: shell_exec
  register: version_check
  params:
    command: "cat VERSION"

- name: use_version
  type: set_fact
  vars:
    version: "{{ version_check.stdout }}"

# HTTP response
- name: fetch_config
  type: http_request
  register: config_response
  params:
    url: "https://api.example.com/config"

- name: parse_config
  type: set_fact
  vars:
    config: "{{ config_response.body | from_json }}"

# Artifact data (loaded via load_artifact or step artifacts)
- name: load_data
  type: load_artifact
  artifacts:
    inventory:
      path: "data/inventory.yaml"
      format: yaml

- name: use_artifact
  type: set_fact
  vars:
    first_item: "{{ load_data.artifacts.inventory.0 }}"
```

## Context Path Resolution

The `resolveContextPath` function (in `engine.go:1017`) resolves dot-separated paths through nested maps.

```
"a.b.c"  -->  ctx["a"]["b"]["c"]
```

- Each segment is used as a map key lookup.
- If any segment does not exist or the intermediate value is not a map, resolution returns `nil`.
- Array index access is **not** supported in path resolution (use template syntax `{{ list.0 }}` instead, which is handled by pongo2).

This function is used internally for `for_each` list resolution, `from` paths in lookup facts, and the `from_json` / `fromjson` direct resolution shortcut.

## Jev support

Jev step outputs are available through `register`. Decision-backed gate facts become ordinary numeric/string facts and persist in gate receipts. Credentials remain outside workflow context. See [Jev decisions](jev.md).
{% endraw %}
