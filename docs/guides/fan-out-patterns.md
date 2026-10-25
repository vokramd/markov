{% raw %}
# Fan-Out Patterns

This guide covers patterns for using `for_each` to run steps or sub-workflows in parallel.

## Simple List Iteration

Iterate over a list and run a step per item:

```yaml
entrypoint: main

forks: 3

vars:
  items: ["alpha", "bravo", "charlie", "delta", "echo"]

workflows:
  - name: main
    steps:
      - name: process-items
        type: shell_exec
        for_each: "items"
        as: item
        params:
          command: "echo 'processing {{ item }}'"
        register: results

      - name: summary
        type: shell_exec
        params:
          command: "echo 'processed {{ results | length }} items'"
```

The `forks: 3` setting means at most 3 items run simultaneously.

## Sub-Workflow Fan-Out

For multi-step processing per item, use `workflow` instead of `type`:

```yaml
entrypoint: main

vars:
  items: ["alpha", "bravo", "charlie", "skip-me"]

workflows:
  - name: main
    steps:
      - name: fan-out
        for_each: "items"
        as: item
        workflow: per-item
        vars:
          value: "{{ item }}"
        register: results

  - name: per-item
    vars:
      value: null
    steps:
      - name: step-one
        type: shell_exec
        params:
          command: "echo 'processing: {{ value }}'"

      - name: step-two
        type: shell_exec
        when: "value != 'skip-me'"
        params:
          command: "echo 'continued: {{ value }}'"
```

Each sub-workflow gets its own run ID and is tracked independently in the state store.

## Controlling Concurrency

Three levels of concurrency control:

1. **File-level `forks`** — default for all for_each steps
2. **Step-level `concurrency`** — overrides forks for one step
3. **CLI `--forks`** — overrides the file-level default

```yaml
forks: 5  # Default: 5 concurrent

workflows:
  - name: main
    steps:
      - name: fast-items
        for_each: "items"
        as: item
        type: shell_exec
        params:
          command: "echo {{ item }}"
        # Uses forks=5

      - name: slow-items
        for_each: "heavy_jobs"
        as: job
        concurrency: 1           # Override: sequential execution
        workflow: process-heavy
        vars:
          job_id: "{{ job.id }}"
```

## Stable Iteration Keys

By default, for_each uses array index (0, 1, 2...) as the iteration key. If items can be reordered between runs, use `for_each_key` to assign stable keys:

```yaml
vars:
  tickets:
    - key: PROJ-300
      title: "Add caching layer"
      priority: low
    - key: PROJ-100
      title: "Fix auth bug"
      priority: critical
    - key: PROJ-200
      title: "Update docs"
      priority: medium

workflows:
  - name: main
    steps:
      - name: process_tickets
        for_each: tickets
        for_each_key: "key"
        for_each_sort: "key"
        as: ticket
        concurrency: 1
        workflow: handle_ticket
        vars:
          ticket_key: "{{ ticket.key }}"
          ticket_title: "{{ ticket.title }}"
```

- **`for_each_key: "key"`** — uses each item's `key` field as the sub-run identifier instead of array index. Keys must exist on every item and be unique.
- **`for_each_sort: "key"`** — sorts items by the `key` field before dispatch (string comparison, ascending). This makes iteration order deterministic.

Together, these ensure that `markov resume` correctly matches completed items even if the input list order changes.

## Iterating Over Dynamic Data

Use `from_json` to parse JSON output from a previous step. The older `fromjson` spelling is also supported.

```yaml
- name: produce_json
  type: shell_exec
  params:
    command: "echo '[{\"key\":\"PROJ-100\"},{\"key\":\"PROJ-200\"}]'"
  register: produce_json

- name: parse_it
  type: set_fact
  vars:
    items: "{{ produce_json.stdout | from_json }}"

- name: process_items
  for_each: items
  for_each_key: "key"
  as: item
  workflow: handle_item
  vars:
    item_key: "{{ item.key }}"
```

## Collecting Results

Use `register` on a for_each step to collect one result per iteration into a list:

```yaml
- name: fan-out
  for_each: "items"
  as: item
  workflow: per-item
  vars:
    value: "{{ item }}"
  register: results

- name: summary
  type: shell_exec
  params:
    command: "echo 'Completed {{ results | length }} items'"
```

Each entry in `results` is that iteration's sub-workflow context without the globals: the `value` it was passed, the item, and what its steps registered or set. The sub-workflow sees only the globals, its own `vars:`, the item and what the call passes (`pass:` or `vars:`), not the caller's registers; see [Variable Scoping](../reference/variables-and-context.md#variable-scoping).

## Error Handling

When one item fails during for_each:

1. The first error is captured
2. In-flight items are allowed to finish
3. No new items are started
4. The step is marked failed with the first error
5. On `markov resume`, only failed/missing items are retried

## What's Next

- [Looping with Gates](looping-with-gates.md) — recursive workflows controlled by rules
- [for_each Reference](../reference/for-each.md) — complete field reference
- [Variables and Context](../reference/variables-and-context.md) — how data flows through fan-outs
{% endraw %}
