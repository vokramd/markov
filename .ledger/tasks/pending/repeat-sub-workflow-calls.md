# Task: `repeat` — bounded do-while loops for sub-workflow calls

## Status

Pending (design, revision 2). Revision 1 resumed loops by replaying finished iterations; a Codex
review (2026-11-06, below) showed that is unsafe with the current engine. This revision replaces
replay with durable iteration checkpoints. Not started.

## Why

Markov's only open-ended loop is a workflow calling itself (docs/concepts.md). markov-harness
loops twice that way: `turn` calls itself after each user turn, and `agent_step` calls itself
while the model asks for tools. That costs three things:

1. **Nesting without bound.** Each iteration is a sub-run of the last; run IDs grow, every level
   holds its caller's context until the chain unwinds, and `executeSubWorkflow` has no depth
   limit.
2. **Nothing bounds it.** `agent_step` recurses as long as the model asks; the extraction loop
   threads its own `round` counter.
3. **The intent is hidden** from readers, `markov validate` and markov-ide's graph.

## Syntax

```yaml
- name: chat
  workflow: turn
  vars:
    session_file: "{{ session_path }}"
    round: "{{ repeat.index }}"
  repeat:
    until: "quit"        # required; after each iteration, against its results
    max: 10000           # required; int, or an expression rendered once
    on_max: fail         # fail (default) | stop
  register: chat         # {last, count, stopped_by: until|max|error, failed?, error?}
```

## Semantics

1. **Do-while.** Each iteration runs the workflow, then `until` is evaluated against it. The
   step's `when` is checked once per loop execution, before the first iteration. That decision is
   saved, so resume doesn't check it again.
2. **What `until` sees.** The iteration's results, the same projection a `for_each` sub-workflow
   item registers (`childResult`, pkg/engine/scope.go): what the workflow was passed plus what
   its steps set, without globals or `markov_*`. Plus `repeat`.
3. **`repeat` is caller-side only.** `repeat.index` (0-based) and `repeat.previous` (the
   previous iteration's results, or none) exist only where the call step's `vars:`, `until` and
   `max` are rendered. They are never put into the child or its results, which avoids a
   `previous.repeat.previous…` chain that keeps every iteration alive. A child that wants the
   index is passed it (`round: "{{ repeat.index }}"`).
4. **Explicit carry.** `vars:` are re-rendered before each iteration. An exact expression keeps
   its type: a list, a map or a bool passes through unchanged, as call vars already do (checked
   2026-11-06: `b: "{{ blocks }}"` arrived as a list, `"{{ k < 5 }}"` as a bool). Nothing is
   carried implicitly.
5. **Stopping.**
   - **`until`:** a true `until` ends the loop with `stopped_by: until`. It is evaluated before
     `max` is checked, so success on the last allowed iteration counts as success.
   - **`max`:** reaching `max` fails the step (`on_max: fail`), or ends it with
     `stopped_by: max` (`on_max: stop`).
   - **Errors:** an error evaluating `until` or `max` fails the step.
   - **Validating `max`:** it must render to a positive integer that fits in an int. A bool, a
     fraction or an empty value is an error.
6. **Failure.** The first failed iteration ends the loop and fails the step.
   - **The result:** `register` (and any `rescue` of the caller) still gets the envelope, with
     `last` set to the failed iteration's results, `count` including that attempt,
     `stopped_by: error` and `error` set.
   - **`ignore_errors`:** the step succeeds with `failed: true` and the same envelope. This
     differs from `for_each`, which carries on after an ignored failure; that will be documented.
   - **The child's handlers:** an iteration's result is the child's context after its lifecycle
     sections (`rescue`, `always`) have run. A child whose normal steps failed still counts as
     failed even if its `rescue` succeeded, which is the current meaning of `rescue`.
7. **Gate pauses** propagate unchanged. The pause doesn't evaluate `until`, doesn't advance the
   loop, and doesn't run the caller's handlers. Resume overrides reach the child only through
   what the call passes it, as now; that will be documented.
8. **Timeout and cancellation.** The step's `timeout` bounds the whole loop. Children's own
   steps keep their timeouts. Cancellation is checked between iterations.
9. **Not allowed with `repeat`:** `for_each` and its options (`as`, `concurrency`,
   `for_each_key`, `for_each_when`), `failed_when` and `secret`. The last two are already
   rejected on workflow calls. A child may contain its own `for_each` and `repeat` steps.

## Persistence: iteration checkpoints, not replay

### Why replay doesn't work

Revision 1 resumed by re-running finished iterations and relying on completed-step skipping.
The review found, and I confirmed in the code:

- **Only some outputs come back.** The skip shortcut (`executeStepCore`, engine.go ~661)
  restores only `set_fact` facts, gate facts and registered outputs; artifacts don't come back.
- **Skipped steps aren't skipped again.** A step skipped by its `when` is evaluated again, so a
  changed input can now run a `prompt` or a mutating request that was skipped the first time.
- **Teardown runs again.** `always` handlers bypass the check on purpose
  (`executeTerminalSteps`, engine.go ~611), so replaying N iterations runs N teardowns.

### Records

A new store record per loop iteration, keyed by
`(run_id, workflow, step state name, generation, index)`, holds:

- `status`: `started` → `finished` → `committed`;
- `inputs`: the iteration's rendered vars and the resolved workflow name and definition digest,
  written when it starts;
- `result`: the iteration's results (or its failure), written when the child returns;
- `decision`: `continue`, `until`, `max` or `error`, written once `until` and `max` are
  evaluated;
- `secret_paths`: which paths in `inputs`/`result` held secrets, with their mask names.

### Crash boundaries and recovery

| State found on resume | Action |
|---|---|
| no record | start the loop (save the `when` decision first) |
| `started` | re-enter the child with the **stored** inputs and pinned definition; inside it, the usual step skipping applies |
| `finished` | don't re-enter the child; evaluate `until` and `max` from the stored result with the **pinned** loop definition, then commit |
| `committed`, `continue` | start iteration n+1 from the stored result (`repeat.previous`) |
| `committed`, terminal | complete the step from the stored envelope |

Exactly-once external effects remain impossible: a process can die after an HTTP mutation
and before the step's output is saved. That will be documented. The stable iteration identity is
there for callers that want idempotency keys.

### Storage stays bounded

- The **active** iteration's record keeps `inputs`.
- The **last committed** record keeps `result`, which `repeat.previous` needs.
- Older records shrink to `status`/`decision`/timestamps once a later one commits.

So a long loop stores about two full copies, not one per iteration, and the loop's own memory is
the active iteration plus `repeat.previous`. Data the workflow carries forward itself can still
grow, and `max_output` will be enforced on every iteration result and on the final envelope.
State-store rows and events still grow with the iteration count.

### Secrets

Checkpoints store real values, the same policy as completed step outputs (resume needs them; a
masked copy can't be turned back into the value). `secret_paths` is recorded by walking the
values for strings the masker already holds. On restore, each path is re-noted (`AddPath`)
before anything reads or emits the value. That is the same re-noting fixed for restored step
outputs in [restored-sub-run-secret-unmasked](../../bugs/fixed/restored-sub-run-secret-unmasked.md).

Known cost: the masker keeps every distinct secret it has seen, so a credential rotated each
iteration grows it. That will be documented, not solved here.

### Iteration sub-run IDs

`<run>-<step>-<n>` collides with an ordinary call named `<step>-<n>`. Today sub-run IDs are
plain concatenations, and `checkSubRunWorkflow` compares only workflow names.

Fix:
- Iteration IDs use a separator that step names cannot contain: `<run>-<step>@<generation>.<n>`,
  with `@` rejected in step names.
- The sub-run row records its loop ownership (parent, step, kind, generation, index), and
  ownership is checked whenever a row is reused.
- `CreateRun`/`SaveStep` errors on these paths are checked, not ignored.

### Reload, source drift and rewind

- **Reload:** each new iteration resolves the workflow afresh, so the existing call-boundary
  reload applies. The active iteration keeps its pinned definition and inputs. The loop's own
  `until`/`max`/`vars` come from the caller's definition, pinned for the loop's execution.
  Resuming a run that recorded a reload stays rejected, as now (engine.go ~333).
- **Source drift:** `until`/`max` decisions already committed are never re-evaluated against
  edited source.
- **Rewind:** only the whole loop can be rewound at first. Rewinding the call step bumps
  `generation`, so every earlier record and iteration sub-run is ignored. Rewinding to a step
  inside an iteration is rejected, as root rewind into sub-workflows is today.

## Events and debugging

- Iteration sub-runs emit `sub_run_*` with new `iteration` and `generation` fields, and the loop
  emits a `repeat_stopped` event carrying `stopped_by` and `count`.
- Phase 2: debugger breakpoints by iteration, with loop ancestry so a nested repeat or fan-out
  can be targeted, and no breakpoints while restoring from a checkpoint. markov-ide draws the
  step as a loop.

## Validation

- `repeat` needs `workflow:`; `until` and `max` are required; `on_max` is `fail` or `stop`.
- The options listed in Semantics 9 are rejected with `repeat`.
- `repeat` is a reserved name in the call step's expression scope.
- Best effort: `until` reads only names the child is passed or sets, or `repeat`. Conditional
  outputs make this a hint, so runtime errors stay authoritative.

## Decisions on the open questions (from the review)

| Question | Decision |
|---|---|
| `until` scope | the iteration's results plus caller-side `repeat` |
| Carry | explicit, through `vars:`; exact expressions keep their type |
| `repeat` on typed steps (polling) | separate task: different attempt identity, delay/backoff and result semantics |
| `while` | deferred; `when` is the entry check |
| Reaching `max` | `fail` by default |
| Replay or checkpoints | checkpoints (above) |
| `delay` | deferred to the polling task |

## Phases

1. **Engine:** records and recovery, flat collision-safe IDs, semantics, validation, the
   `repeat_stopped` event, and docs: `workflow-file.md`, a new `repeat.md`,
   `variables-and-context.md` and the loops section of `concepts.md`.
2. Debugger iteration breakpoints; markov-ide loop rendering.
3. markov-harness: `turn` loses `next_turn`, `agent_step` loses `continue` (with a real
   `max_tool_rounds`), `extract_step` loses `round`.

## Verification

Recovery tests before the harness is converted:
- a crash at each boundary (`started`, `finished`, `committed`);
- a previously skipped side-effecting step is not run on resume;
- `always` isn't repeated for committed iterations;
- artifacts, and secrets restored and masked;
- a reload between iterations;
- an ID collision with a `<step>-<n>` call;
- whole-loop rewind ignoring earlier generations;
- the `max`/`until` edges (success on the last allowed iteration);
- `ignore_errors` envelope, gate pause mid-loop, cancellation between iterations.

## Review record

Codex CLI (codex-cli 0.166.0), read-only, 2026-11-06, of revision 1. Its prioritized changes were:
- P0: checkpoints instead of replay;
- P0: recoverable secrets;
- P0: lifecycle, pause and register semantics;
- P1: reload and rewind;
- P1: collision-safe IDs;
- P1: metadata kept out of results;
- P2: validation, debugger and cancellation.

All are folded in above. One claim was not taken: that call `vars:` render to strings (they keep
the type for exact expressions; see Semantics 4).
