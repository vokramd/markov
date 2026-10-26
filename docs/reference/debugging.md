{% raw %}
# Step-Through Debugging

`markov run` can pause at breakpoints, report the variables the next step will see, and continue one step at a time. The Markov IDE uses this, but the protocol is plain JSON and works from any program or a terminal.

A paused run is held **in memory**: the engine blocks the step and waits for a command, so `for_each` and loop state are intact. This is separate from a gate's `pause` action, which stops the process and is resumed later with `markov resume`.

## Starting a debug run

Breakpoints and `--step` need `--control stdin`, so a paused run can always be continued:

```bash
markov run pipeline.yaml --control stdin \
  --callback jsonl:///tmp/events.jsonl \
  --breakpoint '{"workflow":"main","step":"deploy"}'
```

| Flag | Description |
|------|-------------|
| `--control stdin` | Read debugger commands from standard input, one JSON object per line. |
| `--breakpoint '<json>'` | A breakpoint object (repeatable). |
| `--breakpoints-file file` | A JSON file holding an array of breakpoint objects. |
| `--break workflow.step` | Shorthand for typing in a terminal (see below). |
| `--step` | Pause before every step. |

Every breakpoint is resolved against the workflow before the run starts. An unknown workflow, step, section or phase fails immediately and lists the real names, so a typo can never silently fail to fire. Unknown JSON fields are rejected as well.

Pauses, resumes and other debugger output are sent to the run's callbacks as [`debug` events](callbacks.md#debug-events); without a `--callback` they are only logged.

`markov resume` accepts the same flags, so a resumed (or rewound) run can be debugged too.

## Breakpoints

A breakpoint is a JSON object. Names are never parsed out of a string, so workflow and step names can contain any characters (spaces, dots, colons, brackets).

| Field | Required | Description |
|-------|----------|-------------|
| `workflow` | yes | Workflow name. |
| `step` | yes | Step name. |
| `section` | no | `steps` (default), `rescue`, or `always`. |
| `iteration` | no | Only this `for_each` key. Without it the breakpoint applies to the step as a whole: a `for_each` step pauses once, before the loop starts. |
| `phase` | no | `before` (default: the step is about to run) or `after` (it just completed; the pause carries its output). |
| `when` | no | A condition in the same syntax as a step's `when`, evaluated against the variables the step sees. If the condition itself fails to evaluate, the run pauses and reports the error rather than silently never firing. Conditions must be side-effect free; Jev functions are not available in them. |
| `skip` | no | Ignore the first N hits. |
| `log` | no | A template. The breakpoint becomes a logpoint: the rendered message is reported in a `logpoint` event and the run continues. |

A breakpoint only fires for a step that will actually run: a step skipped by its own `when:`, or replayed as already completed by `markov resume`, does not pause. A step in a sub-workflow is matched by the sub-workflow's name; the `run_id` of the event identifies which call it belongs to (a sub-workflow run is named `<parent run id>-<call step name>`).

### The `--break` shorthand

`--break main.build` is for typing in a terminal. It is not split on the dot. Every real workflow name is tried as a prefix and the remainder must be one of that workflow's steps, so `my flow.v1:x.step one.a:b [c]` works. If nothing matches, the error lists every known `workflow.step`. If two readings are both valid (a workflow `a` with a step `b.c`, and a workflow `a.b` with a step `c`), the command refuses and shows the `--breakpoint` form to use.

## Commands

One JSON object per line on standard input.

| Command | Fields | Effect |
|---------|--------|--------|
| `set_breakpoints` | `breakpoints` | Replace the whole breakpoint list. Allowed at any time. Answered with a `breakpoints_set` event listing `resolved` and `unresolved` entries. |
| `continue` | | Run until the next breakpoint. |
| `step` | | Pause before the next step that starts, in any workflow or iteration. While running, it requests a pause at the next step. |
| `until` | `workflow`, `step`, optional `section`, `iteration` | Continue and pause before that step (one shot). |
| `evaluate` | `id`, `expression` | While paused, render the expression against the paused variables (`n + 1` and `{{ n + 1 }}` are equivalent). Answered with an `evaluated` event carrying the same `id`. |
| `terminate` | | Stop the run. The step is recorded as failed and the run can be continued with `markov resume`. |
| `reload` | optional `source` | Answer a pending reload: load the step's source, or `source` instead (the IDE sends a fresh snapshot of the project). |
| `reject_reload` | | Answer a pending reload: keep the current definition. The step completes with `reloaded: false, rejected: true`. |
| `request_reload` | optional `source` | Replace the definition at the next sub-workflow call, without a `reload_workflow` step: load `source` (the run's own when omitted) now and queue it. Allowed at any time; needs `--allow-reload`. Answered with `reload_queued`, `reload_unchanged` or `reload_failed` (see below). |

Only one pause is active at a time. With concurrent `for_each` iterations, the others wait their turn; a `continue` drops step-mode pauses queued behind it. Use `concurrency: 1` on the step for a deterministic order.

If standard input closes while the run is paused, the run is aborted (failed, resumable) so nothing is left blocked; if it closes while running, debugging is simply switched off.

## Reloading the workflow

When a [`reload_workflow`](step-types.md#reload_workflow) step runs with a control channel attached, it holds like a pause and emits `reload_pending` with the candidate's `source`, `current_digest`, `candidate_digest` and `changes` (workflows and steps added, removed and changed), or an `error` if the candidate does not load. Only `reload`, `reject_reload` and `terminate` are accepted until it is answered; other resume commands get an `error` event. `evaluate` is not available (there is no paused context).

### Reloading on request

`request_reload` lets a client (the IDE's Reload Running Workflow) change a running workflow that has no `reload_workflow` step. The candidate is loaded and checked straight away; it must contain the workflow the run started with. The answer is a debug event:

- `reload_queued`: `source`, `candidate_digest`, `entry` (the run's workflow) and `changes`, as in `reload_pending`. The definition is swapped just before the next sub-workflow call looks its workflow up (a `workflow:` step, or a `for_each` iteration of one), with the same rules as a [`reload_workflow`](step-types.md#reload_workflow) step: steps already running keep their definition, sub-workflows called from then on use the new one, so the workflow the run started with keeps its own steps unless it calls itself. The swap is reported as `workflow_reloaded` with the calling step as `step_name`, recorded, and makes the run unresumable.
- `reload_unchanged`: the candidate is what is running. Any earlier queued request is dropped (an edit undone).
- `reload_failed`: `error`: the candidate does not load, lacks the run's workflow, or the run was started without `--allow-reload`. Nothing changes.

A later request replaces a queued one, and a `reload_workflow` step's own reload drops it.

After a reload the breakpoints are resolved again against the new definition. Those whose step still exists keep their hit counts; the others are dropped. Both lists are reported in a `breakpoints_set` event with `reason: workflow_reloaded`.

## What a pause contains

See [debug events](callbacks.md#debug-events). The `paused` event has `workflow_name`, `step_name`, and `data` with `reason` (`breakpoint`, `step`, `until`, `condition_error`), `phase`, `section`, `iteration`, `step_type`, `variables` and, for `after`, `output`. Variables are the JSON form of the context the step sees; very large contexts are replaced by their key list.

## Example session

```bash
markov run examples/debugging.yaml --control stdin \
  --callback jsonl:///tmp/events.jsonl --break main.greet
```

```
{"cmd":"evaluate","id":"w1","expression":"level * 6"}     # -> evaluated: 42
{"cmd":"step"}                                             # pause before the next step
{"cmd":"until","workflow":"main","step":"done"}            # run to the final step
{"cmd":"continue"}
```

## Limits

- Editing variables while paused is not supported; edit the workflow and resume instead.
- There is no pause inside a running step (for example in the middle of a `claude` step).
- Breakpoint conditions cannot call Jev decisions.
{% endraw %}
