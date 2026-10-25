{% raw %}
# for_each

Iterates over a list, running a step or sub-workflow per item with concurrency control.

## Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `for_each` | string | Yes | Expression resolving to a list |
| `as` | string | Yes (with for_each) | Variable name bound to each item |
| `for_each_key` | string | No | Field name on items used as stable iteration keys |
| `for_each_sort` | string | No | Field to sort items by (string comparison, ascending) |
| `concurrency` | int | No | Override global forks for this step |
| `for_each_when` | string | No | Per-item filter, evaluated with the item bound to `as`; false skips the item |

## List resolution

The `for_each` expression is resolved in the following order:

1. **Dot-path context lookup** -- the expression is split on `.` and walked through the context map (e.g., `"items"` looks up `ctx["items"]`, `"fetch.results"` looks up `ctx["fetch"]["results"]`).
2. **Type coercion** -- `[]any` is used directly; `[]string` is converted to `[]any`.
3. **Expression** -- otherwise the expression is evaluated as a template expression and its native value is used if it is a list, so filters work: `repeats | seq`, `only_tests | csv`, `variants | csv | default:own`.
4. **Text fallback** -- otherwise the expression is rendered as text (`{{ expr }}`) and the text is JSON-parsed into a list, so an expression producing JSON text also works.
5. **Error** -- if none of the above produce a list, the step fails with `for_each expression "..." did not resolve to a list`.

```yaml
# Direct context lookup
for_each: items
as: item

# Dot-path lookup
for_each: fetch_step.results
as: result

# Expression with filters (evaluated natively)
for_each: "repeats | seq"
as: round

# Nested results flattened into one list
for_each: "rounds_done | pluck:'tests_done' | flatten"
as: test_result
```

## for_each with workflow

When `workflow` is set alongside `for_each`, each item spawns a sub-workflow with its own run ID:

```
{parentRunID}-{stepName}-{key}
```

- `key` is the array index by default, or the `for_each_key` field value if set.
- The sub-workflow receives a **copy** of the parent context, merged with the workflow's own vars, then the current item under its `as` name, then the step's rendered vars.
- Each sub-run is tracked in the state store with `parent_run_id`, `parent_step`, and `for_each_key`.

```yaml
steps:
  - name: deploy-all
    for_each: services
    as: svc
    for_each_key: name
    workflow: deploy-single
    concurrency: 3
    vars:
      service_name: "{{ svc.name }}"
      image: "{{ svc.image }}"
```

## for_each with type (no workflow)

When `for_each` is used on a step that has a `type` but no `workflow`, the step is executed directly for each item. The step's `for_each` and `register` fields are cleared on the copy passed to the sub-execution so the inner step runs as a normal single step.

```yaml
steps:
  - name: ping-hosts
    type: http_request
    for_each: endpoints
    as: ep
    params:
      url: "{{ ep.url }}/healthz"
      method: GET
    register: health_results
```

## for_each_key

Provides stable iteration keys for resume and sub-run ID generation. Validation rules:

- **Must exist on every item.** An error is raised if any item at any index is missing the field:
  `for_each_key "name" not found on item at index 3`
- **Must be unique across all items.** Duplicate values produce an error with both index positions:
  `duplicate for_each_key "api-server" at indices 1 and 4`
- The key value is converted to a string via `fmt.Sprintf("%v", val)`.

```yaml
steps:
  - name: process-tickets
    for_each: tickets
    as: ticket
    for_each_key: ticket_id    # Each ticket must have a unique ticket_id
    workflow: process-single
```

## for_each_sort

Sorts items before iteration. Sorting happens before `for_each_key` validation.

- **Must exist on every item.** An error is raised if any item is missing the field.
- Uses `sort.SliceStable` -- items with equal sort values keep their original order.
- Comparison is lexicographic on the string representation: `fmt.Sprintf("%v", field_value)`.

```yaml
steps:
  - name: deploy-in-order
    for_each: services
    as: svc
    for_each_key: name
    for_each_sort: priority     # Sort by priority field (string comparison)
    workflow: deploy-one
```

## Concurrency control

| Scenario | Effective concurrency |
|----------|----------------------|
| `concurrency` set on step | Uses step value |
| `concurrency` not set or <= 0 | Uses global `forks` (default 5) |

Implementation details:

- A buffered channel of size `concurrency` acts as a semaphore.
- Each iteration acquires a slot before launching its goroutine.
- If the first error has already occurred, no new items are started -- the loop breaks immediately.

```yaml
# Process 10 items, but only 2 at a time
steps:
  - name: heavy-work
    for_each: items
    as: item
    concurrency: 2
    type: shell_exec
    params:
      command: "./process.sh {{ item.id }}"
```

## register with for_each

When `register` is set, one element per iteration is collected into a list and stored at `ctx[register_name]`, in the order of the input list whatever the `concurrency`. This includes iterations that ran but failed; items skipped by `for_each_when`, or not started after a failure, are left out.

For a sub-workflow, each element is the called workflow's final variables without the globals and built-ins: what it was passed (the item included) and what its steps registered or set. For a typed step, each element holds the item (under `as`), the step's output (under the register name), and anything else the iteration set, such as `failed` and `error` under `ignore_errors`. Neither holds the parent's context, so a loop that collects over an earlier loop's register stays the size of its data. See [Variable Scoping](variables-and-context.md#variable-scoping).

```yaml
steps:
  - name: check-all
    for_each: hosts
    as: host
    type: http_request
    params:
      url: "{{ host }}/health"
    register: health_checks    # [{host: ..., health_checks: {status_code: ..., ...}}, ...]

  - name: report
    type: shell_exec
    params:
      command: >
        echo '{% for c in health_checks %}{{ c.host }}={{ c.health_checks.status_code }} {% endfor %}'
```

## Error handling

| Behavior | Detail |
|----------|--------|
| First error captured | Via `sync.Once` -- only the first error is recorded |
| In-flight items | Allowed to complete naturally |
| New items | Not started after first error (loop breaks) |
| Step status | Marked failed with the first error |
| Registered results | Still collected from all iterations that ran |

With `ignore_errors: true` on the `for_each` step, a failed item doesn't stop the loop: every
item runs, failed items' registered contexts get `failed: true` and `error`, and the step
succeeds.

## for_each_when

Filters items one at a time. The expression sees the item under its `as` name; items where it is
false are skipped and logged. Use it instead of `when`, which is evaluated once for the whole step.

```yaml
- name: tests
  for_each: tier_def.tests
  as: test
  for_each_key: id
  for_each_when: "not only_tests or test.id in only_tests | csv"
  workflow: run-test
```

## Resume behavior

For `for_each` steps that use `workflow`:

1. Completed sub-runs are matched by `for_each_key` in the state store.
2. On resume, completed sub-runs are skipped.
3. Only failed or missing items are retried.

This is why `for_each_key` matters for production workflows -- without it, keys are array indices, which can shift if the input list changes between runs.

## Examples

### Simple list iteration with shell_exec

```yaml
vars:
  regions:
    - us-east-1
    - us-west-2
    - eu-west-1

workflows:
  - name: main
    steps:
      - name: check-regions
        type: shell_exec
        for_each: regions
        as: region
        params:
          command: "echo Checking {{ region }}"
        register: region_results
```

### Sub-workflow fan-out with vars

```yaml
vars:
  components:
    - name: api
      image: api:latest
      replicas: 3
    - name: worker
      image: worker:latest
      replicas: 5

workflows:
  - name: main
    steps:
      - name: deploy-components
        for_each: components
        as: comp
        for_each_key: name
        for_each_sort: name
        concurrency: 2
        workflow: deploy
        vars:
          component_name: "{{ comp.name }}"
          component_image: "{{ comp.image }}"
          replica_count: "{{ comp.replicas }}"

  - name: deploy
    steps:
      - name: rollout
        type: k8s_job
        params:
          image: "{{ component_image }}"
          command: ["deploy", "--replicas", "{{ replica_count }}"]
```

### Stable keys for resumable pipelines

```yaml
steps:
  - name: process-issues
    for_each: issues
    as: issue
    for_each_key: issue_key     # e.g., "PROJ-1234" -- stable across retries
    for_each_sort: priority     # Process high-priority first (lexicographic)
    workflow: triage-issue
    concurrency: 5
    vars:
      key: "{{ issue.issue_key }}"
      summary: "{{ issue.summary }}"
```
{% endraw %}
