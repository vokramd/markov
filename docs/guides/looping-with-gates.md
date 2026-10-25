{% raw %}
# Looping with Gates

Markov has no built-in loop construct. Instead, workflows loop by recursively invoking themselves, with gate steps controlling when to stop. This follows the Markov chain philosophy: each iteration evaluates the current state and decides the next transition.

## The Pattern

A loop requires three workflows:

```
main → iterate → do_work
         ↑           │
         └───────────┘  (recurse if gate says continue)
```

1. **main** — initializes state and calls `iterate`
2. **iterate** — evaluates a gate, runs `do_work` if continuing, increments a counter, then calls itself again
3. **do_work** — performs the per-iteration logic

## Complete Example

```yaml
entrypoint: main

vars:
  iteration: 0
  max_iterations: 10

rules:
  - name: loop_continue
    description: "Keep looping while under the limit"
    salience: 10
    when: "iteration < max_iterations"
    action: continue
    set_fact:
      loop_done: false

  - name: loop_exit
    description: "Stop when iteration limit is reached"
    salience: 100
    when: "iteration >= max_iterations"
    action: skip
    set_fact:
      loop_done: true

workflows:
  - name: main
    steps:
      - name: start
        type: shell_exec
        params:
          command: "echo 'starting loop (max={{ max_iterations }})'"

      - name: run_loop
        workflow: iterate
        vars:
          iteration: "{{ iteration }}"

  - name: iterate
    vars:
      iteration: null
    steps:
      - name: loop_gate
        type: gate
        rules:
          - loop_continue
          - loop_exit

      - name: run_work
        workflow: do_work
        when: "not loop_done"
        vars:
          iteration: "{{ iteration }}"

      - name: increment
        type: set_fact
        when: "not loop_done"
        vars:
          iteration: "{{ iteration + 1 }}"

      - name: next_iteration
        workflow: iterate
        when: "not loop_done"
        vars:
          iteration: "{{ iteration }}"

  - name: do_work
    vars:
      iteration: null
    steps:
      - name: show_iteration
        type: shell_exec
        params:
          command: "echo '  working on iteration {{ iteration }}'"
```

## How It Works

1. `main` calls `iterate` with `iteration: 0`
2. `iterate` evaluates the gate — `loop_continue` fires (iteration 0 < 10), sets `loop_done: false`
3. `do_work` runs for this iteration
4. `set_fact` increments `iteration` to 1
5. `iterate` calls itself with `iteration: 1`
6. Steps 2-5 repeat until iteration reaches 10
7. At iteration 10, `loop_exit` fires (salience 100 > 10, so it wins), sets `loop_done: true`
8. `when: "not loop_done"` skips the remaining steps, and recursion stops

## Key Design Points

### Exit Rule Must Have Higher Salience

The exit rule needs a higher salience than the continue rule. When `iteration >= max_iterations`, both rules' conditions could potentially interact — the higher salience ensures the exit action wins:

```yaml
- name: loop_continue
  salience: 10              # Lower priority
  when: "iteration < max_iterations"
  action: continue

- name: loop_exit
  salience: 100             # Higher priority — wins when it fires
  when: "iteration >= max_iterations"
  action: skip
```

### Gate Facts Are Set in Context

When a gate rule fires and has `set_fact`, those values are merged into the workflow context. Downstream `when` conditions can reference them:

```yaml
# Gate sets: loop_done: true
# Downstream step checks it:
- name: run_work
  when: "not loop_done"    # Skipped when loop_done is true
```

### Variables Must Be Passed Explicitly

A called workflow sees only the globals, its own `vars:` and what the call passes, so the incremented `iteration` value must be explicitly passed into the recursive call. `iterate` declares `iteration` in its own `vars:`, which makes it a parameter it may change even though a global has the same name:

```yaml
- name: increment
  type: set_fact
  vars:
    iteration: "{{ iteration + 1 }}"

- name: next_iteration
  workflow: iterate
  vars:
    iteration: "{{ iteration }}"   # Pass the new value
```

## Retry Pattern

Use a gate-controlled loop to retry an operation until it succeeds:

```yaml
vars:
  attempt: 0
  max_retries: 5

rules:
  - name: retry_continue
    salience: 10
    when: "attempt < max_retries and not succeeded"
    action: continue
    set_fact:
      should_retry: true

  - name: retry_exhausted
    salience: 100
    when: "attempt >= max_retries"
    action: skip
    set_fact:
      should_retry: false

  - name: retry_success
    salience: 200
    when: "succeeded == true"
    action: skip
    set_fact:
      should_retry: false

workflows:
  - name: retry_loop
    vars:
      attempt: null
    steps:
      - name: retry_gate
        type: gate
        rules:
          - retry_continue
          - retry_exhausted
          - retry_success

      - name: try_operation
        type: shell_exec
        when: "should_retry"
        params:
          command: "curl -sf https://api.example.com/health"
        register: health_check

      - name: check_result
        type: set_fact
        when: "should_retry"
        vars:
          succeeded: "{{ health_check.exit_code == 0 }}"
          attempt: "{{ attempt + 1 }}"

      - name: wait
        type: shell_exec
        when: "should_retry and not succeeded"
        params:
          command: "sleep 10"

      - name: next_attempt
        workflow: retry_loop
        when: "should_retry and not succeeded"
        vars:
          attempt: "{{ attempt }}"
```

## What's Next

- [Conditional Logic](conditional-logic.md) — when vs gates vs assert
- [Rule Engine Reference](../reference/rule-engine.md) — complete rule syntax and evaluation
- [Variables and Context](../reference/variables-and-context.md) — how variables flow through recursive workflows
{% endraw %}
