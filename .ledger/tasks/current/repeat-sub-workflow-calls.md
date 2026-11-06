# Task: `repeat` — bounded do-while loops for sub-workflow calls

## Status

Current. Phase 1 (engine) and phase 3 (markov-harness) done 2026-11-06; phase 2 (debugger,
markov-ide) pending. Design revision 4.

Phase 3: markov-harness d56e147. `main` repeats `turn` until `/exit`, `turn` repeats `agent_step`
up to `max_tool_rounds` (50), and the extraction pass repeats `extract_step` up to
`extract_max_turns`. Its 16 end-to-end tests pass, including the new `test_tool_rounds_are_capped`.
The longest run ID in a fresh state db is now 68 characters
(`<run>-chat@0.0~<h>-agent@0.0~<h>-run_mutating-0`), flat however long the session runs.

### Phase 1 as built

- **Code:**
  - `pkg/engine/repeat.go`: the loop, generations, iteration sub-runs, the envelope;
  - `pkg/engine/owner.go`: sub-run owners, `claimSubRun`, checked `startSubRun`. Used by
    ordinary calls and `for_each` too, which replaces `checkSubRunWorkflow`;
  - `pkg/parser` (`Repeat`, `validateRepeat`, lint of `until`);
  - `pkg/state` (`runs.owner`, `ErrRunNotFound`);
  - the gate's pause converted under `insideRepeat`, `currentGenerations` in `changedSteps`, and
    `generation`/`iteration` on sub-run events.
- **Docs:** `docs/reference/repeat.md`, plus `workflow-file.md`, `concepts.md` (Loops),
  `variables-and-context.md`, `for-each.md` and `docs/README.md`. Example: `examples/repeat.yaml`
  (counts 0..9, total 45).
- **Tests:** `pkg/engine/repeat_test.go` (15 tests) and `TestRepeatValidation`/`TestLintRepeat` in
  `pkg/parser`.
- **Where it differs from the design below:**
  - `until` also sees the globals, as every other expression does (found when a test's `until`
    read a file-level var and saw nothing);
  - `max` is rendered against the caller without `repeat`;
  - the end-of-loop event is a `step_progress` of kind `repeat_stopped`, not a new event type, so
    no callback interface changes;
  - the iteration ID hash is 12 hex digits of SHA-256 over the owner JSON;
  - `max_output` is checked per iteration and on the envelope.

- Revision 1 resumed loops by replaying finished iterations.
- Revision 2 replaced that with durable iteration checkpoints.
- Two Codex reviews (2026-11-06) showed both are unsafe or incomplete with the current engine.
  Nearly every blocker comes from resuming a loop in place after a crash or pause.

Revision 3 builds the loop without in-place resume, behind an explicit `resume` key, and keeps
resumable loops as a later task (see "Later: `resume: true`"). Revision 4 settles review 3's
remaining v1 blockers: two-way ownership checks, exclusive generation claims, gate pauses, and
old generations in rewind.

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

None of the harness's loops need markov-level resume: its sessions resume from their JSONL files.

## Syntax

```yaml
- name: chat
  workflow: turn
  vars:
    session_file: "{{ session_path }}"
    round: "{{ repeat.index }}"
  repeat:
    until: "quit"        # required; after each iteration, against its results
    max: 10000           # required; int, or an expression rendered once per loop start
    on_max: fail         # fail (default) | stop
    resume: false        # default; true is rejected (not supported yet)
  register: chat         # {last, count, stopped_by: until|max|error, failed?, error?}
```

## Semantics

1. **Do-while.** Each iteration runs the workflow, then `until` is evaluated against it. The
   step's `when` is checked once, before the first iteration.
2. **What `until` sees.** The iteration's results, the same projection a `for_each` sub-workflow
   item registers (`childResult`, pkg/engine/scope.go): what the workflow was passed plus what
   its steps set. Plus `repeat`, and (as built) the globals.
3. **`repeat` is caller-side only.** `repeat.index` (0-based) and `repeat.previous` (the
   previous iteration's results, or none) exist only where the call step's `vars:`, `until` and
   `max` are rendered. They are never put into the child or its results, so there is no
   `previous.repeat.previous…` chain. A child that wants the index is passed it.
4. **Explicit carry.** `vars:` are re-rendered before each iteration. Exact expressions evaluate
   natively (`RenderMap`, pkg/template/template.go ~244), so lists, maps and bools pass through
   unchanged. As with every call var, string results go through `coerceString`
   (pkg/engine/scope.go ~61), so `"123"` or `"false"` become a number or a bool. Nothing is
   carried implicitly.
5. **Stopping.**
   - **`until`:** a true `until` ends the loop with `stopped_by: until`. It is evaluated before
     `max` is checked, so success on the last allowed iteration counts as success.
   - **`max`:** reaching `max` fails the step (`on_max: fail`), or ends it with
     `stopped_by: max` (`on_max: stop`). `max` must render to a positive integer that fits in an
     int. A bool, a fraction, empty or an error fails the step before the first iteration.
   - **Errors:** an error evaluating `until` fails the step with `stopped_by: error`.
6. **Failure.** The first failed iteration ends the loop and fails the step; `until` is not
   evaluated for it.
   - **The result:** the envelope has `last` set to the failed iteration's results (as far as
     they exist), `count` including that attempt, `stopped_by: error` and `error`. It is
     registered before the error returns, so the caller's `rescue` can read it. (Today a failed
     ordinary call returns before registering, engine.go ~1282; repeat does not.)
   - **`ignore_errors`:** the step succeeds with `failed: true` and the same envelope. This
     differs from `for_each`, which carries on after an ignored failure; that will be documented.
   - **The child's handlers:** an iteration's result is the child's context after its lifecycle
     sections ran. A child whose normal steps failed counts as failed even if its `rescue`
     succeeded, which is the current meaning of `rescue`.
7. **Gate pauses are not supported inside a loop (v1).** The pause is converted where it
   happens, not at the loop boundary. Converting it there would be too late: on a pause,
   `executeWorkflow` returns before the child's `rescue`/`always` run (engine.go ~556).
   - **How:** the iteration's context carries an "inside repeat" mark, which nested calls and
     fan-outs inherit. A gate whose rules choose `pause` under that mark fails its own step:
     "gate X chose pause inside repeat step Y; pausing inside repeat is not supported yet".
   - **What it emits:** `gate_evaluated` as usual, then `step_failed`. No `run_paused` and no
     receipt for a durable pause. The error does not match `PauseError`.
   - **What follows:** the child's `rescue`/`always` run as for any failure, the sub-run is
     recorded as failed, and the loop stops with `stopped_by: error`.
   - **Why:** nothing waits for an approval that a restarted loop would ask for again.
8. **Timeout and cancellation.** The step's `timeout` bounds the whole loop. Children's steps
   keep their own timeouts. Cancellation is checked between iterations.
9. **Not allowed with `repeat`:**
   - `for_each` and its options (`as`, `concurrency`, `for_each_key`, `for_each_when`);
   - `failed_when` and `secret`, which are already rejected on workflow calls;
   - `resume: true`.

   A child may contain its own `for_each` and `repeat` steps.

## Resume (`resume: false`)

| State of the repeat step when the run is resumed | What happens |
|---|---|
| not started | runs normally |
| started, not completed (the run failed or crashed in it, or it failed) | **starts over** at iteration 0 in a new generation; earlier iterations' side effects happen again |
| completed | not re-entered; its `register` envelope is restored from the step row |

- **Re-running from the start** is the same contract as a failed step re-running on resume
  today, applied to the whole loop. Docs will say plainly that side effects repeat.
  Earlier generations' sub-run rows stay in the store, and are ignored as described under
  "Old generations" below.
- **The completed envelope is a new contract.** Ordinary call steps save no completed row of
  their own and are re-entered on resume (engine.go ~1291). A repeat step instead saves its
  envelope as its step output when the loop ends, and that write is checked. On resume the
  existing completed-step shortcut restores it under `register` (engine.go ~663).
- **The envelope is stored masked, so restoring it is lossy.** It is saved through the masker,
  as sub-run vars are (`subRunVarsJSON`), so a secret in it reads `<secret:NAME>` after resume.
  Later steps, and a caller's `rescue` after a later failure, get the placeholder and cannot use
  the credential. That is the stated v1 contract, documented and covered by a test.
  - **What is stored:** only the envelope is masked. The iterations' child step rows still hold
    real outputs, as all step rows do (engine.go ~835).
  - **What it avoids:** none of those values is ever restored by the loop, which sidesteps the
    recoverable-secrets problem (revision 2 review, point 5).
- **Lifecycle sections.** A repeat step inside `rescue` or `always` runs every time its section
  is forced to run (engine.go ~614). The completed-step shortcut doesn't apply there, so each
  forced run starts a new generation.
- **Crashed runs.** Their status is left `running`, and `Resume` rejects them today
  (engine.go ~327). That is unchanged; recovering crashed runs is outside this task.
- **Rewind.** Rewinding to the repeat step, or to an earlier step, re-runs it the same way:
  a new generation, starting at iteration 0. Rewinding to a step inside an iteration is rejected,
  as root rewind into sub-workflows already is (rewind.go ~227).
- **Reload.** Each iteration resolves its workflow at its call boundary, as ordinary calls do
  (engine.go ~1612), so a requested reload takes effect from the next iteration. The loop's own
  `until`/`max`/`vars` are the caller's definition at loop start. Resuming a run that recorded a
  reload stays rejected (engine.go ~333).
- **Generations.**
  - **Numbering:** a loop start takes generation = 1 + the highest generation among existing
    iteration sub-run rows owned by this step in this caller run (0 if none). Every surviving row
    counts, whatever its status, including rows a crash left `running`. A start that fails before
    iteration 0's row exists (a bad `max`, a failed `when`) leaves nothing behind, so the next
    start reuses that number harmlessly.
  - **Claiming it:** creating iteration 0's sub-run row is the claim. `CreateRun` is a plain
    insert keyed by run ID (sqlite.go ~124), so a second resumer that picked the same generation
    gets a unique-key error. That error fails the step with "another process is running this
    loop", never a reuse: a matching ownership tuple alone never authorizes taking over a row in a
    new loop start. Later iterations are created by the process holding iteration 0.
  - **Concurrency:** concurrent resumes of one run are otherwise unsupported, as today.
- **Old generations.** Rows of earlier generations are kept, so the numbering survives, but v1
  filters them out of everything that reads children:
  - `changedSteps` (rewind.go ~140), and so `--rewind-changed`, visits only the latest generation
    of each repeat step. Edits found only in abandoned generations trigger nothing.
  - Rewind's recursive clearing (rewind.go ~279, ~297) may delete old generations' step rows and
    keeps their run rows. That is fine, since numbering reads run rows only.
  - Diagnostics and listing that walk child runs show old generations as such, marked by their
    ownership tuple.

## Iteration sub-run IDs and ownership

Sub-run IDs today are plain concatenations (`<run>-<step>`, `<run>-<step>-<key>`), and
`checkSubRunWorkflow` compares only workflow names. Any readable separator can be produced by
another call path: a `repeat` step `a-b`, versus a call `a` whose child has a `repeat` step `b`,
versus a fan-out key.

- **ID:** `<parent run>-<step>@<generation>.<index>~<h>`, where `<h>` is the first 12 hex digits
  of SHA-256 over the ownership tuple. The readable part is for people. A collision needs
  another producer to generate the exact hash suffix, and ownership checks turn even that into
  an error, never silent reuse. Step names are not restricted; `@` stays allowed.
- **Ownership tuple:** top-level run ID, parent run ID, caller workflow, lifecycle section, step
  state name, call kind (`repeat`), generation, index. It is stored on the sub-run row (new
  columns or one JSON column; decided during implementation), and checked whenever a row with
  that ID already exists.
- **Both directions.** Every producer records its kind and owner on the rows it creates:
  ordinary calls (`call`), `for_each` items (`for_each`, with the key) and `repeat` iterations.
  Every producer checks an existing row before reusing it. So an ordinary call whose
  concatenated ID happens to equal a repeat-owned row's ID fails ("sub-run ID X is owned by
  repeat step Y"), instead of entering it (today `checkSubRunWorkflow` compares only workflow
  names, engine.go ~1647). The same check stops a repeat from entering a call- or fan-out-owned
  row. Rows written before this change carry no owner and are treated as ordinary calls, which
  keeps existing resumes working.
- **Writes:** `CreateRun` errors are checked on these paths (today they are ignored,
  engine.go ~1236). An existing row is validated against the ownership tuple, not blindly
  re-inserted. A store lookup error is an error, not "absent".
- **Encoding:** the canonical encoding of the hashed tuple is an implementation detail.

## Storage and memory

- **In memory:** the active iteration plus `repeat.previous` — bounded regardless of iteration
  count, apart from data the workflow itself carries forward in `vars:`.
- **In the store:** one sub-run row (with masked vars) and that iteration's step rows per
  iteration. Rows grow with the iteration count, as recursion's do today, but flatly. Compacting
  old generations or iterations is not part of v1.
- **`max_output`** is enforced on each iteration's result and on the envelope.
- **The masker** keeps every distinct secret it sees, so a credential rotated each iteration
  grows it. Documented, not solved here.

## Events and debugging

- Iteration sub-runs emit `sub_run_*` with new `iteration` and `generation` fields. The loop
  emits a `repeat_stopped` event carrying `stopped_by` and `count`.
- Phase 2: debugger breakpoints by iteration, with loop ancestry for nested repeat/fan-out, and
  markov-ide drawing the step as a loop.

## Validation

- `repeat` needs `workflow:`. `until` and `max` are required. `on_max` is `fail` or `stop`.
  `resume` is a bool, and `true` is rejected with "not supported yet".
- The options in Semantics 9 are rejected with `repeat`.
- `repeat` is a reserved name in the call step's expression scope.
- Best effort: `until` reads only names the child is passed or sets, or `repeat`. Conditional
  outputs make this a hint, so runtime errors stay authoritative.

## Decisions on the open questions (from review 1)

| Question | Decision |
|---|---|
| `until` scope | the iteration's results plus caller-side `repeat` |
| Carry | explicit, through `vars:`; exact expressions evaluate natively |
| `repeat` on typed steps (polling) | separate task |
| `while` | deferred; `when` is the entry check |
| Reaching `max` | `fail` by default |
| Replay or checkpoints | neither in v1: `resume: false` restarts the loop; checkpoints are the later `resume: true` |
| `delay` | deferred to the polling task |

## Later: `resume: true`

A separate task, which must first solve review 2's blockers:

1. **A complete durable state machine.**
   - A loop-level record: generation, entry decision, resolved `max`, pinned caller definition,
     current iteration, outcome.
   - Write ordering or transactions for every gap. The store has no multi-write transactions
     today.
   - Recovery of a crashed run.
   - Guarantees for an active child (its skipped steps stay skipped, its handlers aren't
     repeated).
2. **Gate approvals versus pinned inputs**, and a fresh invocation identity for loops inside
   `rescue`/`always`.
3. **Recoverable secret metadata** that covers list elements, substrings and errors, re-noted
   before any exposure (root resume, the completed-step shortcut, debugger snapshots).
4. **Durable definition pinning** (a source snapshot, or rejection when it can't be recovered),
   rewind invalidation across generations, and safe compaction.

## Phases

1. **Engine:**
   - loop semantics, ownership-keyed IDs and checked writes;
   - restart-on-resume, and the masked envelope;
   - validation, including `resume: true` rejected;
   - the `repeat_stopped` event;
   - docs: `workflow-file.md`, a new `repeat.md`, `variables-and-context.md` and the loops
     section of `concepts.md`.
2. Debugger iteration breakpoints; markov-ide loop rendering.
3. markov-harness: `turn` loses `next_turn`, `agent_step` loses `continue` (with a real
   `max_tool_rounds`), `extract_step` loses `round`.

## Verification

- **Stopping:** `until` stops after the right iteration; `max` with `fail` and with `stop`;
  `until` succeeding on the last allowed iteration; a bad `max` fails before the first
  iteration.
- **Carry:** `vars:` re-rendered with `repeat.index` and a structured `repeat.previous`;
  `repeat` absent from the child and from the results.
- **Failure:** the envelope is registered on failure and readable by the caller's `rescue`;
  the `ignore_errors` envelope; a gate pause inside an iteration fails the loop.
- **IDs:** the `a-b` collision case and a fan-out-key case produce distinct IDs; a planted row
  with a mismatched owner is an error in both directions (repeat meeting a call-owned row, and an
  ordinary call meeting a repeat-owned one); two engines resuming the same loop: one wins
  generation N, the other fails.
- **Resume:**
  - an unfinished loop restarts at iteration 0 in generation 1;
  - a crash-left `running` row consumes its generation;
  - a completed loop is not re-entered, and its envelope comes back masked: a later step that
    reads a secret from it gets `<secret:NAME>`;
  - rewind restarts the loop;
  - `--rewind-changed` ignores edits seen only in old generations.
- **Gates:** a gate choosing `pause` inside an iteration, or in a nested call inside one, fails
  its step. The child's `always` runs, no `run_paused` is emitted, and the loop ends with
  `stopped_by: error`.
- **Lifecycle:** a repeat inside `always` starts a new generation on each forced run.
- **Validation:** `resume: true` rejected by parse and by validate; the incompatible options
  rejected.
- **Limits:** cancellation between iterations; the step `timeout` bounds the whole loop.
- markov-harness converted, with its end-to-end tests passing.

## Review record

Both reviews were by Codex CLI (codex-cli 0.166.0), read-only, on 2026-11-06.

- **Review 1, of revision 1.** Replay is unsafe: only some outputs are restored, skipped steps
  run again and `always` handlers repeat. It called for checkpoints, recoverable secrets,
  lifecycle and pause rules, reload and rewind, collision-safe IDs, and keeping metadata out of
  results.
- **Review 2, of revision 2.** Needs another revision. Blockers:
  - an incomplete state machine, with crashed runs not resumable at all;
  - gate overrides contradicting pinned inputs;
  - unimplementable secret paths, with exposure before re-noting;
  - colliding IDs, with `@` already allowed in step names;
  - pinning, rewind and compaction only asserted.

  It confirmed that keeping metadata out of results and native structured carry were addressed.
  Revision 3 moves every in-place-resume blocker to "Later" and fixes IDs, the failure envelope
  and the `coerceString` qualification in v1.
- **Review 3, of revision 3.** Needs another revision. It agreed the deferred items are
  legitimately deferred and the crashed-run claim is correct. Blockers:
  - ownership checks in only one direction;
  - generation claims that are not exclusive;
  - a gate pause converted too late, so the child's cleanup is skipped;
  - old generations still visited by rewind.

  It also asked to settle the checked envelope write, forced lifecycle runs and a test of lossy
  restoration. Revision 4 addresses each in "Resume", "Iteration sub-run IDs and ownership" and
  Semantics 7.
