# Output limits, and runaway runs as codify evidence

Created 2026-10-25. Comes out of the plan 006 trial: iteration 6's candidate run grew to 13.6 GB
before the kernel killed it. On the way, the kernel killed about 20 breadboard processes first
(coredns, the reverse proxies, the emulators, cert-manager, the relay), since their
`oom_score_adj` (984-1000) was far above the run's (200). Codify recorded the iteration as
failed with "signal: killed" and the retro learned nothing from it.

Plan 008 removes that cause (registers nested in registers). This plan stops any step, whatever
its cause, from outgrowing its run, keeps a runaway run from taking the machine down with it,
and makes "this step produces too much" evidence codify can act on.

**Status:** phases 1-4 done (2026-10-25); phase 5, the trial, next. See "As built".

## Why plan 008 is not enough

- **No step output has a limit.** A legitimately huge result (a 2 GB stdout, a listing paged 50
  times, an agent transcript) goes into the context whole.
- **Every output is held several times.** In the run context, then serialised into its event,
  its state db row and any template that renders it, and copied whole by the event masker. Peak
  memory is a multiple of the output.
- **Codify reads whole event logs.** `LoadEvents` and `os.ReadFile` of `events.jsonl` in
  `pkg/codify/loop.go`: the 1.85 GB log would have killed codify next.
- **Nothing contains the damage.** The kernel picks victims by `oom_score_adj`, so the lab goes
  first and the runaway last.

## Approach

### `max_output` on every step

A common step field, like `ignore_errors` and `secret`, on every step type:

```yaml
- name: list-issues
  type: http_request
  max_output: 200MB      # this one legitimately returns a lot
```

A size with a unit (`KB`, `MB`, `GB`, or bytes); `0` means no limit, as an explicit opt-out.
Where the limit comes from, most specific first:

1. the step's `max_output:`;
2. its step type's defaults (`step_types.yaml`: "every `claude` step 16MB");
3. a file-level `max_output:` (`meta.yaml` in a directory workflow);
4. the engine default, **64 MB**, changed with `markov run --max-output`.

**Enforced at the source where it can be, and once at the end for everything.**

- **Streaming executors stop reading at the limit** instead of buffering everything and
  checking after: `shell_exec` and `script_exec` stdout and stderr (the process is killed),
  `http_request` bodies (counted across all pages when paginating), `k8s_job` logs,
  `openshell_exec` output, the `claude` step's stream. The memory is spent while reading, so
  this is the check that matters most.
- **One check when a step completes**, on its output's size as JSON: catches `set_fact`,
  `load_artifact`, sub-workflow and `for_each` registers, and anything an executor builds.
  Measured with a byte-counting walk that does not build the JSON, so measuring a huge value
  does not double it. This is what would have stopped `command-inputs` at 64 MB instead of
  1.29 GB.

**Going over fails the step.** `ignore_errors` and `failed_when` treat it like any other
failure. Not truncation: a script or agent given a cut-off issue list misses items without
knowing, the failure plan 006 is about. An explicit `on_max_output: truncate` can come later
for logs, if something wants it.

**The failure says what was big.** The `step_failed` event carries structured fields as well
as the message (`output is 562 MB, over max_output 64 MB`):

```json
{"error_kind": "max_output", "output_bytes": 562603118, "limit_bytes": 67108864,
 "largest": [
   {"path": "command_inputs.pull_reviews", "bytes": 447800000, "items": 3},
   {"path": "command_inputs.pull_comments", "bytes": 112000000, "items": 3},
   {"path": "command_inputs.pull_reviews[0].issue_comments", "bytes": 37000000, "items": 120}]}
```

`largest` is the biggest paths a few levels down (sizes from the same counting walk), with item
counts for lists: the breakdown that found the trial's cause by hand. For a streaming executor
it is the stream that was cut (`stdout`, `body`, page N).

### Codify's runs are contained

The cap handles outputs; it cannot stop a leak in Markov itself or in a script a step runs.

- **A memory-limited scope.** Codify starts each live and candidate run under `systemd-run
  --user --scope -p MemoryMax=<limit> -p MemorySwapMax=0` where that works (checked once at
  start). `--run-memory` sets the limit; the default is a quarter of physical memory. The
  kernel then kills inside the run's cgroup, not across the machine.
- **Otherwise, the run dies first.** Without systemd, the run's process raises its own
  `oom_score_adj` to 1000 (allowed unprivileged, since it only raises it), so a global
  out-of-memory kill picks it before anything else.
- **A killed run is a reason.** A run that ends by SIGKILL with its scope at the limit, or with
  an out-of-memory kill in its cgroup's events, is reported as "ran out of memory
  (MemoryMax N)" with its largest step outputs from its event log, not "signal: killed".

### Codify reads event logs a line at a time

`LoadEvents` and the other event-log reads stream `events.jsonl` with a line limit. A line over
the limit is summarised (its event type, step, size) rather than parsed, so one huge event
cannot take codify down.

### Too much output is evidence

The retro gets told which step is too big and what shape the excess has, and is told how such
steps are usually fixed: further refinement, splitting the step, fanning out, deferring the work
to a script.

- **`output_sizes.md`**, an evidence file built like `agent_calls.md`: every step's output size;
  for a loop, the smallest, median and largest item and the total; steps over a quarter of their
  limit marked; and any `max_output` failure with its `largest` breakdown. Growth shows up before
  anything fails: a step at 30 MB on the 150-issue fixture is a problem on a 1000-issue repo.
- **The retro prompt maps the shapes to fixes:**

  | The evidence shows | Usually |
  |---|---|
  | A register held inside another register's items | pass only the fields needed (`pass:`, or `vars:` with `map(attribute=...)`), not the whole register |
  | A big list rendered into a `set_fact` or template | write it to a file and have a `script_exec` read it: the script filters, and the workflow gets back only its result |
  | A big list given to an agent | fan out with one call per item, or give the agent a file to read |
  | A listing much bigger than what is used | filter on the server (`state=open`, `since=`, `labels=`) or fetch per item |
  | One huge text field (a log, a transcript) | save it as an artifact and register its path or a summary |

- **A candidate over a limit** (`max_output`, or out of memory) is rejected with the step, size
  and limit as its reason, and the evidence goes to the next retro. Iteration 6 would have been
  a cheap, informative rejection.
- **The current version over a limit** does not stop the loop as `live_failed` does today. A
  step that runs out of room is the clearest sign of what to codify next, so the retro gets it as
  its main evidence, and a candidate that completes where the current version did not is judged
  as doing the job better (the judge prompt says so; strict mode, which compares outputs, has no
  outputs to compare and treats a completing candidate as passing if its other checks pass).
  Other live failures still stop the loop.

## Phases

1. **`max_output`:** the parser field and size syntax; precedence (step, step type, file,
   engine, `--max-output`); the completion check with the counting walk; the structured
   `step_failed` fields and `largest`. Tests: each level of precedence; `0` disables; a
   `set_fact` over the limit fails with a breakdown; `ignore_errors` records it. Docs:
   workflow-file, step-types, callbacks (the event fields), cli.
2. **Streaming executors:** `shell_exec`, `script_exec`, `http_request` (across pages),
   `k8s_job` logs, `openshell_exec`, `claude`. Tests: each stops reading at the limit, with a
   process or response that would otherwise use far more memory than the test allows.
3. **Codify containment:** the systemd scope with `--run-memory`, the `oom_score_adj` fallback,
   out-of-memory kills reported as such; event logs read a line at a time with a line limit.
   Tests: a run that allocates past its limit is killed alone and reported as out of memory; a
   log with a 100 MB line is read.
4. **Codify evidence:** `output_sizes.md`; the retro prompt's table; candidates over a limit
   rejected with the reason; the current version over a limit handed to the retro, not
   `live_failed`; the judge prompt. Tests: evidence, rejection reason, the live path; history
   shows the limit failures.
5. **Trial:** the plan 006 trial restarted from v0000 after this and plan 008 phase 2, on the
   same fixture.

## Open questions

- The default limit. 64 MB is far above anything the github trial needs (its largest real
  output is about 1 MB) and far below what hurt; codify could use a lower default for its runs,
  so a codified workflow is held to a stricter standard than a hand-written one.
- Whether the event masker (plan 007) should stop deep-copying every map now that values are
  bounded.
- A limit on a whole run's memory outside codify (`markov run --memory`), using the same scope.

## As built (2026-10-25)

- **Phase 1:** `pkg/outputlimit` (size syntax, a byte-counting walk that stops past the limit,
  `Largest`, the error, the context limit, a shared `Counter`); `max_output` on steps, step
  types, the file and `meta.yaml`; `markov run`/`resume --max-output` sets the default below the
  file's setting. Checked in the engine for executor output, `set_fact`, `load_artifact`, step
  `artifacts:`, sub-workflow and `for_each` registers. `step_failed` carries `error_kind`,
  `output_bytes`, `limit_bytes`, `stream`, `largest`, and no output. `failed_when` is skipped
  for a limit failure (there is no output to see).
- **Phase 2:** `shell_exec`, `script_exec` and `ansible` kill the process at the limit
  (`runCapped`); `http_request` counts every page against one counter; `openshell_exec` cancels
  the command. Not the `claude` stream: its transcript is already bounded (events limit), and
  capping the raw stream would kill long agent sessions whose output is small; its output is
  checked on completion. `k8s_job` logs already had `LimitBytes`.
- **Phase 3:** `pkg/codify/contain.go`: `systemd-run --user --scope -p MemoryMax -p
  MemorySwapMax=0` when a test scope works, `MARKOV_OOM_SCORE_ADJ=1000` always (the child
  raises its own score). An out-of-memory kill is confirmed from the scope's user journal
  ("oom-kill"); without a scope, a SIGKILL is reported as most likely out of memory. `--run-memory`
  defaults to a quarter of MemTotal. Event logs go through `lineSplitter` (16 MB lines; a longer
  one becomes a summary of its header and length): `LoadEvents`, the live `follow` that forwards
  events to the IDE (it used to build each whole line, 1.29 GB in the trial), and the evidence
  copy of `events.jsonl`. Reading the trial's 1.85 GB log now peaks at 122 MB.
- **Phase 4:** `output_sizes.md` (per step: runs, min / median / max, total; over a quarter of
  64 MB marked; limit failures with their largest parts); `candidate_output_sizes.md` per
  iteration, handed to the next retro as `previous_candidate_output_sizes.md`; a candidate over a
  limit is rejected with step, size, limit and largest parts. A current version over a limit (or
  out of memory) goes on to the retro, which is told first; the candidate must complete (and
  write its repeat record), and the judges are told the current version did not complete. In
  strict mode such a candidate can still miss the recording where the current version stopped
  early; live hosts avoid that.
