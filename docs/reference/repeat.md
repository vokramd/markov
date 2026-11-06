{% raw %}
# Repeat (bounded do-while loops)

`repeat` runs a called workflow again and again until a condition holds, up to a limit: a
do-while loop. It replaces looping by having a workflow call itself, which nests every iteration
inside the previous one. A `repeat` loop is flat: each iteration is its own sub-run of the calling
run.

```yaml
steps:
  - name: chat
    workflow: turn
    register: chat
    vars:
      session_file: "{{ session_path }}"
      round: "{{ repeat.index }}"
    repeat:
      until: "quit"          # checked after each iteration, against its results
      max: 10000             # the most iterations it may run
      on_max: fail           # or stop
```

[`examples/repeat.yaml`](../../examples/repeat.yaml) counts to ten this way.

## Fields

| Field | Type | Required | Description |
|---|---|---|---|
| `until` | expression | yes | Evaluated after each iteration. True ends the loop. |
| `max` | int or expression | yes | The most iterations. An expression (`"{{ max_rounds }}"`) is rendered once, against the caller, when the loop starts. It must come out a whole number of at least 1. |
| `on_max` | `fail` or `stop` | no | What happens when `max` iterations ran and `until` never became true. `fail` (the default) fails the step; `stop` ends it successfully. |
| `resume` | bool | no | Must be `false` (the default). `true`, resuming an unfinished loop in place, is not supported yet and is rejected. See [Resume](#resume). |

`repeat` goes on a step that calls a workflow (`workflow:`). It can't be combined with `for_each`
or its options (`as`, `for_each_key`, `for_each_sort`, `for_each_when`, `concurrency`). The
called workflow may itself contain `for_each` and `repeat` steps.

## How a loop runs

1. The step's `when`, if any, is evaluated once, before the first iteration.
2. `max` is rendered. If it isn't a positive whole number, the step fails before anything runs.
3. For each iteration:
   - the step's `vars:` are rendered against the caller's context plus `repeat`;
   - the workflow runs as a new sub-run;
   - `until` is evaluated.
4. The loop ends when `until` is true, when an iteration fails, or after `max` iterations.
   `until` is checked before `max`, so becoming true on the last allowed iteration counts as
   success.

### `repeat`: the index and the previous result

Where the step's `vars:` and `until` are rendered, `repeat` holds:

| Name | Value |
|---|---|
| `repeat.index` | The iteration number, from 0. |
| `repeat.previous` | The previous iteration's results, or `none` in the first iteration. |

`repeat` exists only there. It is not passed into the called workflow, and it isn't in the results.
A workflow that wants the index is passed it, as `round` is above.

Nothing carries over between iterations by itself; pass what the next iteration needs through
`vars:`. An exact expression keeps its type, so a list or map passes through whole:

```yaml
vars:
  cursor: "{% if repeat.previous %}{{ repeat.previous.next_cursor }}{% else %}0{% endif %}"
  prev: "{{ repeat.previous }}"
```

As with every call var, a string that looks like a number or a bool (`"3"`, `"false"`) arrives as
one.

### What `until` sees

`until` sees the iteration's results, plus the globals (file-level vars) and `repeat`. The
results are what the called workflow was passed and what its steps set, the same as a sub-workflow
call's `register` (see [Variable Scoping](variables-and-context.md#variable-scoping)). It does
not see the caller's other variables.

`markov validate` warns when `until` reads a name the called workflow neither takes nor sets.

### The result: `register`

The step's `register` holds one envelope, not a list of every iteration:

| Key | Value |
|---|---|
| `last` | The last iteration's results (as far as they got, if it failed). |
| `count` | Iterations run, a failed one included. |
| `stopped_by` | `until`, `max` or `error`. |
| `error` | Why it failed, when it did. |
| `failed` | `true`, when it failed under `ignore_errors`. |

## Failure

The first iteration that fails ends the loop and fails the step. `until` is not evaluated for it.
The envelope is registered before the step fails, so the caller's `rescue` can read it. A run
that reaches `max` with `on_max: fail` fails the same way, with `stopped_by: max`.

- **`ignore_errors`:** the step succeeds instead, with the envelope's `failed: true` and `error`
  set. This differs from `for_each`, which carries on with the next item after an ignored
  failure.
- **The called workflow's handlers:** an iteration's result is the workflow's context after its
  `rescue` and `always` ran. A workflow whose steps failed counts as failed even if its `rescue`
  succeeded, which is what `rescue` means everywhere.
- **Gates can't pause inside a loop.** A `gate` whose rules choose `pause` fails its own step
  instead, with "pausing inside repeat is not supported yet". This applies inside the iteration
  and inside anything it calls. The iteration's `rescue` and `always` then run as for any
  failure, and no `run_paused` is emitted. (A paused iteration could only be restarted, asking
  for the same approval again.)
- **Timeout:** the step's `timeout` bounds the whole loop. Steps inside keep their own timeouts.
  Cancellation (Ctrl-C, a timeout) is checked between iterations.

## Resume

A loop is not resumed in place (`resume: false`). When a run is resumed:

| The repeat step was | What happens |
|---|---|
| not reached | It runs normally. |
| unfinished (the run failed or stopped inside it, or it failed) | It **starts over** at iteration 0. |
| finished | It is not run again. Its envelope is restored from the state store. |

**Starting over repeats side effects.** Iterations that already ran do their work again, the same
way a failed step runs again on resume. Steps inside them are not skipped.

**A restored envelope is masked.** The envelope is stored with secrets masked, so a secret in it
(say `last.token` from a `secret: [token]` step) comes back as `<secret:NAME>` after a resume.
Later steps, and a `rescue` after a later failure, then see the placeholder, not the value. The
steps inside the iterations keep their own outputs in the store as usual.

Each start of a loop is a **generation**: 0 the first time, then one more each time it starts over,
whether because of a resume, a rewind, or because a `repeat` step in `always` or `rescue` runs
again. Iteration sub-runs of earlier generations stay in the store and are ignored. For example,
`--rewind-changed` looks only at each loop's latest generation.

Rewinding to the repeat step, or to a step before it, starts it over. Rewinding to a step inside
an iteration is rejected, as for any sub-workflow.

Two processes resuming the same run at once are not supported. If they both start the same loop,
the second fails with "another process is running repeat step …".

## Sub-runs and events

Iteration `i` of generation `g` runs as sub-run `<run>-<step>@<g>.<i>~<hash>`, a child of the calling
run. The hash covers who owns it (the calling run, workflow and step, and the generation and
index), so it can't collide with the name-based IDs of ordinary calls and fan-outs. Every
sub-run row records its owner. A call that finds a row owned by another call fails, naming both,
instead of using it.

Each iteration emits `sub_run_started` and then `sub_run_completed` or `sub_run_failed`, with
`generation` and `iteration` fields. The step's `step_started` has `resolved_type: repeat`. When
the loop ends, the step emits a `step_progress` event of kind `repeat_stopped`, whose data is
`stopped_by`, `count` and `generation`.

## Storage

Memory holds the running iteration and `repeat.previous`, however long the loop runs, apart from
whatever the workflow itself carries forward through `vars:`. The state store keeps a sub-run row
and that iteration's step rows for every iteration, so it grows with the number of iterations.
`max_output` applies to each iteration's results and to the envelope.
{% endraw %}
