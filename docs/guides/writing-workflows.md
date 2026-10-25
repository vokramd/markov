{% raw %}
# Writing Workflows

This guide walks through building a markov workflow step by step, from a minimal example to a multi-workflow pipeline.

## Anatomy of a Workflow File

Every markov workflow file is a YAML document with these top-level sections:

```yaml
entrypoint: main          # Which workflow runs first (required)
namespace: markov-test    # K8s namespace for k8s_job steps
forks: 5                  # Default concurrency for for_each

vars:                     # Global variables
  greeting: "hello"

step_types:               # Reusable step type definitions
  echo_job:
    base: k8s_job
    job:
      image: alpine:3.19

rules:                    # Rule definitions for gate steps
  - name: my_rule
    when: "score > 8"
    action: continue

workflows:                # One or more workflow definitions
  - name: main
    steps:
      - name: first-step
        type: shell_exec
        params:
          command: "echo hello"
```

Only `entrypoint` and `workflows` are required.

## Directory Workflows

Single-file workflows are still the simplest place to start. When a workflow grows, you can split it into a directory and pass the directory path to `markov run` or `markov validate`.

```text
my-pipeline/
  meta.yaml
  vars.yaml
  rules.yaml
  step_types.yaml
  workflows/
    main.yaml
    per-item.yaml
```

For larger custom type libraries, `step_types.yaml` can be replaced by or combined with a `step_types/` directory containing one or more YAML maps.

The category files contain their category directly:

```yaml
# meta.yaml
entrypoint: main
forks: 3
```

```yaml
# vars.yaml
items: ["alpha", "bravo", "charlie"]
```

```yaml
# workflows/main.yaml
name: main
steps:
  - name: process
    for_each: items
    as: item
    type: shell_exec
    params:
      command: "echo '{{ item }}'"
```

Run it with:

```bash
markov validate my-pipeline
markov run my-pipeline
```

See `examples/dir-based-hello-world/` for a minimal runnable directory workflow.

## Starting Simple

The simplest workflow runs a shell command:

```yaml
entrypoint: hello

workflows:
  - name: hello
    steps:
      - name: say-hello
        type: shell_exec
        params:
          command: "echo 'hello from markov'"
```

Run it:

```bash
markov run hello.yaml
```

## Using Variables

Define variables in the `vars` block and reference them with `{{ }}` syntax:

```yaml
entrypoint: hello

vars:
  greeting: "hello from markov"

workflows:
  - name: hello
    steps:
      - name: say-hello
        type: shell_exec
        params:
          command: "echo '{{ greeting }}'"
```

Override variables from the command line:

```bash
markov run hello.yaml --var greeting="hey there"
```

## Capturing Output with register

Use `register` to store a step's output and reference it in later steps:

```yaml
entrypoint: hello

vars:
  greeting: "hello from markov"

workflows:
  - name: hello
    steps:
      - name: say-hello
        type: shell_exec
        params:
          command: "echo '{{ greeting }}'"
        register: hello_result

      - name: show-date
        type: shell_exec
        params:
          command: "date +%Y-%m-%d"
        register: date_result

      - name: combine
        type: shell_exec
        params:
          command: "echo 'Message: {{ hello_result.stdout }} on {{ date_result.stdout }}'"
```

Each step type produces different output fields. `shell_exec` produces `stdout`, `stderr`, and `exit_code`.

## Conditional Steps with when

Skip steps based on conditions:

```yaml
- name: step-two
  type: shell_exec
  when: "value != 'skip-me'"
  params:
    command: "echo 'continued: {{ value }}'"
```

When the expression evaluates to false, the step is marked as "skipped" (not failed) and execution continues.

## Computing Values with set_fact

Use `set_fact` to compute and store variables:

```yaml
- name: compute
  type: set_fact
  vars:
    item_count: "{{ items | length }}"
    is_ready: "status == 'ready'"
    data: "{{ raw_json | from_json }}"
```

Values are merged directly into the workflow context and available to all downstream steps.

## Validating State with assert

Use `assert` to fail the workflow if conditions aren't met:

```yaml
- name: check_preconditions
  type: assert
  that:
    - "status == 'ready'"
    - "score >= 4"
  msg: "Preconditions not met"
```

The workflow fails on the first false expression. If `msg` is omitted, the default message includes the failed expression.

## Multiple Workflows and Sub-Workflows

A file can define multiple workflows. Use the `workflow` field on a step to invoke another workflow:

```yaml
entrypoint: main

vars:
  items: ["alpha", "bravo", "charlie"]

workflows:
  - name: main
    steps:
      - name: fan-out
        for_each: "items"
        as: item
        workflow: per-item
        vars:
          value: "{{ item }}"

      - name: done
        type: shell_exec
        params:
          command: "echo 'all done'"

  - name: per-item
    vars:
      value: null
    steps:
      - name: process
        type: shell_exec
        params:
          command: "echo 'processing: {{ value }}'"
```

Sub-workflow `vars` with `null` values are parameter declarations — they document what the workflow expects to receive.

## Custom Step Types

Define reusable step types to avoid repeating configuration:

```yaml
step_types:
  echo_job:
    base: k8s_job
    job:
      image: alpine:3.19
      command: ["/bin/sh", "-c"]
    defaults:
      backoff_limit: 0
      ttl_seconds: 300

workflows:
  - name: main
    steps:
      - name: hello
        type: echo_job
        params:
          args: ["echo 'hello world'"]
```

The `base` field specifies which primitive type to wrap. Parameters are merged in order: `job` < `defaults` < `params` < step-level `params`.

## Validating Before Running

Use `markov validate` to check syntax without executing:

```bash
markov validate my-pipeline.yaml
```

This checks that the entrypoint exists, workflow/step names are unique, types resolve correctly, `for_each` has the required `as` field, and unsupported structured YAML fields are rejected. Executor-specific keys remain valid under `params`. It also checks what each workflow can see of its variables: a called workflow that reads a name it is not given, or a call that passes a name the called workflow does not declare, is an error ([Variable Scoping](../reference/variables-and-context.md#checking-what-a-workflow-can-see)).

## Common Mistakes

- **Missing `as` with `for_each`**: Every `for_each` step must declare an `as` variable name.
- **Referencing non-existent workflows**: The `workflow` field must match a defined workflow name.
- **Using templates in non-rendered fields**: `name`, `type`, `register`, `for_each_key`, `for_each_sort`, and `as` fields are not template-rendered.
- **Expecting sub-workflow changes to propagate**: Changes inside a sub-workflow don't affect the parent unless captured via `register`.
- **Expecting a sub-workflow to see the caller's registers**: A called workflow sees the globals (file-level vars), its own `vars:`, and what the call passes with `pass:` or `vars:`. Declare what it takes in its `vars:` (`~` for required) and pass it; `markov validate` reports names a workflow reads but cannot see.
- **Changing a global**: A `register`, `set_fact` or `as` may not name a file-level var. Shadow it for a called workflow by passing a value instead.

## What's Next

- [Fan-Out Patterns](fan-out-patterns.md) — for_each concurrency and sub-workflow patterns
- [Looping with Gates](looping-with-gates.md) — recursive workflows and retry loops
- [Conditional Logic](conditional-logic.md) — when vs gates vs assert
- [Step Types Reference](../reference/step-types.md) — all built-in step types
- [Template Engine Reference](../reference/template-engine.md) — filters, expressions, and template syntax
{% endraw %}
