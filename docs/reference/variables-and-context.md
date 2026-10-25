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

**Important:** a workflow's `vars` are its parameters. A nil value (`~`) declares a required parameter without a default: the run fails at the start unless `--var` (or a file-level var of the same name) sets it. It does not clear a global's value. See [Variable Scoping](#variable-scoping).

```yaml
# File-level vars (priority 1)
vars:
  environment: "dev"
  retries: 3

workflows:
  - name: deploy
    # Workflow vars (priority 2)
    # "target" is nil -- a required parameter, with no default
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
| `markov_source_dir` | The absolute directory of the workflow source: the project directory for a directory workflow, the file's directory otherwise. Use it for files that belong to the project, such as `{{ markov_source_dir }}/fixtures/repo.yaml`: relative paths resolve against the working directory, which differs between runs (codify runs workflows from an empty one). |
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

There are three kinds of variable, and what a workflow sees depends on which kind it is.

| Declared | Kind | Seen by |
|---|---|---|
| File-level `vars:` (`vars.yaml` and `vars/*.yaml` in a directory workflow) | **global** | every workflow in the run |
| A workflow's own `vars:` | that workflow's **parameters**, with defaults (`~`: required) | that workflow; callers fill them |
| `register`, `set_fact`, a loop's `as`, loaded artifacts, gate facts | **locals** | the workflow that made them; a called workflow only if passed |
| `markov_*` | built-in | everywhere |

### Globals

The file-level vars are set when the run starts (then `--var` overrides them) and stay the same for the whole run. No step may change one: a `register`, `set_fact` or `as` that names a global is rejected when the file is parsed, so concurrent `for_each` iterations never share mutable state. A workflow that declares the name in its own `vars:` has a parameter of that name instead, which is its own to change.

A call may **shadow** a global for the workflow it calls, and everything that workflow calls, by passing the name:

```yaml
vars:
  GITHUB_TOKEN: ""          # global; empty until a workflow mints one

workflows:
  - name: cycle
    steps:
      - name: bootstrap
        workflow: bootstrap
        register: bootstrap
      - name: agent
        workflow: agent
        vars:
          GITHUB_TOKEN: "{{ bootstrap.github_token }}"   # agent, and what it calls, see this one
```

A value only some workflows should see belongs in the `vars:` of the workflow that uses it, passed by its callers, not among the globals.

### Called workflows see what they are given

A workflow called with `workflow:` starts with:

1. the globals and built-ins, as the caller sees them (so a shadowed global reaches the grandchildren);
2. its own `vars:` defaults (non-nil values);
3. in a `for_each`, the item, under `as`;
4. what the call passes by name with `pass:`, as it is;
5. the call's `vars:`, rendered against the caller (strings coerced).

Nothing else of the caller's: a register or fact the caller made is visible to the child only when passed.

```yaml
- name: triage-each
  workflow: triage-issue
  for_each: "untriaged.stdout | from_json"
  as: issue
  pass: [repo_labels]                  # by name, the value itself
  vars:
    comments: "{{ comments_by_number[issue.number] }}"   # rendered against the caller

# The called workflow declares what it takes; ~ means required.
- name: triage-issue
  vars:
    issue: ~
    repo_labels: ~
    comments: []
  steps: [...]
```

`pass:` is the way to hand over a register or a list: the value is not rendered to text and parsed back, so it keeps its type and costs nothing however large it is. Use `vars:` to pick out part of a value or rename it.

A parameter declared `~` that the call does not give (not passed, not in `vars:`, not a global, not the loop variable) is an error when the file is parsed, or when the call is made for a templated `workflow:` name.

Changes made inside a called workflow do **not** propagate back to the caller. The only way back is `register`, which stores the called workflow's final context **without** the globals and built-ins: what it was passed and what it made.

```yaml
- name: run_child
  workflow: child_workflow
  vars:
    input_data: "{{ some_value }}"
  register: child_output   # input_data, and whatever child_workflow registered or set
```

### for_each

Each iteration of a `for_each` gets its own copy of the context with the current item set at `ctx[as]`. Iterations run concurrently and do not share state.

A typed step with `for_each` (a `type`, no `workflow`) is not a call: each iteration sees the whole context of its workflow plus the item. If the step has `register`, the register is a list with one element per iteration, each holding the item (under `as`), the step's output (under the register name), and anything else the iteration set (facts; `failed` and `error` under `ignore_errors`). Not the iteration's whole context: a loop that collects over an earlier loop's register stays the size of its data.

```yaml
- name: list-comments
  type: http_request
  for_each: issues.body
  as: issue
  register: comments
  params:
    url: "{{ api }}/issues/{{ issue.number }}/comments"
# comments[0] == {"issue": {...}, "comments": {"status_code": 200, "body": [...], ...}}
```

A `for_each` that calls a workflow registers one element per iteration, as a single call does.

### Checking what a workflow can see

`markov validate` reports names a workflow reads (in templates, `when`, `for_each`, `for_each_when`, `failed_when`, assertions, gate facts) that it cannot see: not a global, a built-in, its own parameter, or something its own steps register, set, load or loop over. The check is best effort (a computed key is not followed):

- **error**: a workflow that is called reads a name it cannot see; a call passes a name the called workflow does not declare in its `vars:`; or a workflow reads a name its callers pass without declaring it. A workflow says what it takes.
- **warning**: a workflow nothing calls (an entry workflow) reads an undeclared name, which must then come from `--var`.

`markov run` logs the same findings as warnings (an entry workflow's names given with `--var` are left out) and goes ahead.

## Sub-Workflow Var Merging

The context for a called workflow is built by `childContext` (in `scope.go`); see [Called workflows see what they are given](#called-workflows-see-what-they-are-given) for the order. String values from step-level vars are passed through `coerceString` after rendering, so `"42"` becomes `int(42)`, `"true"` becomes `bool(true)`, etc. Non-string values are stored directly. `pass:` values are stored as they are.

## register Behavior

What gets stored by `register` depends on the step type:

| Step Type | Stored Value |
|---|---|
| `shell_exec` | The executor's `Result.Output` map (typically contains `stdout`, `stderr`, `rc`) |
| `k8s_job` | The executor's `Result.Output` map (contains job output) |
| `k8s_job_wait` | The executor's `Result.Output` map (contains `job_name`, `namespace`, `status`, and optional `logs`) |
| `http_request` | The executor's `Result.Output` map (typically contains `body`, `status_code`, `headers`) |
| Sub-workflow (`workflow:`) | The called workflow's final context, without the globals and built-ins |
| `for_each` on a typed step | A list (`[]map[string]any`), one element per iteration: the item, the step's output, and what the iteration set |
| `for_each` calling a workflow | A list, one element per iteration, each as for a single call |

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

## Secrets

Markov masks secret values in what it writes out: every callback event (so the JSONL log,
markovd, the IDE and `markov codify`'s evidence) and every log line. Each value is replaced with
`<secret:NAME>` wherever it appears, including inside a rendered URL, shell output or an error
message. Steps still get the real values.

A value is secret when it is:

- a var listed in the file's `secrets:` (`meta.yaml` in a directory workflow);
- a var whose name ends in `token`, `secret`, `password`, `passwd`, `api_key`/`apikey`,
  `private_key` or `credential`/`credentials`, in any case (`GITHUB_TOKEN`, `db_password`),
  listed or not;
- the output of a step marked `secret` (`true`, or a list of output paths), from the moment the
  step returns;
- a secret param of a step type (the `credentials` of `openshell_provider`).

```yaml
secrets: [ADMIN]
vars:
  ADMIN: change-me-admin-token
workflows:
  - name: main
    steps:
      - name: mint
        type: http_request
        register: mint
        secret: [body.token]      # the minted token never shows, even in mint's own event
        params:
          method: POST
          url: "{{ BASE }}/admin/api/tokens"
          headers: {Authorization: "token {{ ADMIN }}"}
```

Vars of a sub-workflow and of each `for_each` item are checked the same way, and a secret value
stays masked under any name it is passed on as.

Limits:

- Strings shorter than 8 characters are not masked. A value is also masked trimmed and
  URL-query-escaped, but not in other forms (base64, part of the value, split across lines).
- The state store keeps real values, because resume rebuilds the run's context from it. On
  resume the masks are rebuilt from the stored vars and the outputs of completed `secret` steps.
- Lines the CLI logs before the workflow is loaded are not masked; `--debug` lists `--var` names
  only.
- Markov cannot mask what a step sends somewhere itself, such as an agent posting a token.

## Jev support

Jev step outputs are available through `register`. Decision-backed gate facts become ordinary numeric/string facts and persist in gate receipts. Credentials remain outside workflow context. See [Jev decisions](jev.md).
{% endraw %}
