# Agentic codification

Created 2026-10-20. Synthesizes the workspace-root discussion docs
(`markov_agentic_distillation_summary.md`,
`agentic_to_deterministic_conversation.md`, `sam-manifesto.md`) into a
Markov plan with a concrete first scenario. Must work both headless (CLI,
markovd) and in the Markov IDE; the IDE side is
`../../../markov-ide/.ledger/plans/002-agentic-codification.md`.

**Status:** phase 1 done (notes/codify-phase1-run.md), phase 2 done
(ADR-0006, tasks/done/reload-workflow-step.md), phase 3 done
(tasks/done/event-log-variables.md). Engine and IDE findings
below were checked against the code on 2026-10-20.

## Idea

**Agentic codification:** an agent first does a task from a single broad
prompt. A retrospective agent then reads how that run actually went,
decomposes the prompt into its bare parts, and progressively swaps parts
that need no judgment for native steps (`http_request`, `write_file`,
`set_fact`, `for_each`) or `script_exec` calls. What cannot be codified stays
as a smaller **residual agent** step. Markov stays a stable engine; only the
workflow content changes, at explicit points, after validation.

"Codification" replaces "distillation" from the earlier docs to avoid
confusion with model distillation.

Supporting vocabulary:

- **Inference debt:** model calls spent on work that could be code.
- **Residual agent:** the narrower prompt left after codification.
- **Deterministic coverage:** share of the task's steps (and of its cost)
  handled without a model. The metric the loop should raise.
- Principle: *deterministic by default, agentic by exception*.

The objective is lower total cost per verified successful outcome, not
fewest tokens at any cost; codifying an ambiguous judgment into a brittle
rule is a regression.

## Findings that shape the design

- **No reload today.** `engine.New` takes one parsed `*parser.WorkflowFile`
  stored in `e.file`; the CLI parses once at startup.
- **A running workflow keeps its own step list.** Sub-workflows are looked up
  through `e.file.GetWorkflow` at call time (`resolveSubWorkflow`). Swapping
  `e.file` therefore leaves the current workflow's remaining steps on the old
  definition and makes the *next sub-workflow call* use the new one. That is
  the natural activation point.
- **Loops are recursion.** `examples/loop-test.yaml`: `iterate` gates, runs
  work, then calls itself. Each recursive call is a sub-workflow call, so it
  picks up reloaded content. Sub-run IDs nest as `<parent>-<call step>`.
- **Agent logs mostly exist already.** The `claude` step registers
  `session_id`, turns, tokens, usage, cost and a capped list of compact
  events; Claude Code itself keeps the full transcript at
  `~/.claude/projects/<encoded cwd>/<session_id>.jsonl`. The `jsonl://`
  callback records step started/completed/failed/skipped and live
  `step_progress`. What is missing is a way for a workflow to learn its own
  callback/event-log path, and for native steps' results the retro agent
  currently only sees what was `register`ed.
- **Resume conflicts with reload.** Source integrity (ADR-0002) fingerprints
  the source tree and step definition hashes flag edited steps; a reloaded run
  is source drift by definition. Plan 001 put "hot-patching inside a running
  process" out of scope; this plan adds workflow-level reload only, still not
  patching a step mid-execution.
- **The IDE never runs the user's files.** It materializes a snapshot of the
  editor text in a temp dir (IDE ADR-0003/0004: documents written, everything
  else symlinked) and runs that, with the *project directory as working
  directory*. Reloading "the run's source" would re-read the stale snapshot,
  so in the IDE the IDE must supply the new source. Relative writes from a
  step land in the real project, and IDE disk sync already reloads changed
  files into the editor as undoable edits.
- The IDE already drives runs over the ADR-0005 control channel
  (`--control stdin`) and folds `debug` and `step_progress` events into its
  run state, including per-step claude transcripts.
- `markov validate <file|dir>` exists and can gate candidates.

## Design

1. **`reload_workflow` step type.** Explicit, author-placed. Params:
   `source` (path to load; default the run's source), optional `require`
   (workflow names that must exist after reload). Opt-in: the run must be
   started with `--allow-reload`; otherwise validation rejects the step.
   Loading parses and validates with the same loader as `run`; any error
   fails the step and keeps the old file. Success atomically swaps the
   engine's file (`atomic.Pointer` or lock, since `for_each` goroutines read
   `e.file`) and takes effect from the next sub-workflow call.
2. **Reload handshake when a control channel is attached.** With
   `--control`, the step does not load on its own. It emits a debug event
   `reload_pending` (source, current digest, candidate digest, a summary of
   added/removed/changed workflows and steps) and blocks like a breakpoint.
   Commands: `reload {source?}` (load this path instead, e.g. a fresh IDE
   snapshot) and `reject_reload` (step completes with `reloaded: false`; the
   driver decides what to do). Without a control channel it loads directly,
   so CLI and markovd runs behave the same minus the approval.
3. **Reload is observable.** A `workflow_reloaded` callback event (all
   callback transports; markovd ignores unknown types) carries old/new
   digests, version label and the change summary. The run record keeps an
   append-only list of activated digests. Breakpoints are re-resolved against
   the new file and reported with `breakpoints_set` (unresolved ones listed,
   not fatal).
4. **Ephemeral runs first.** A run that has reloaded is marked
   non-resumable across versions (resume refuses with a clear error).
   Version-pinned resume is deferred.
5. **Live task in the project; history outside it.** The demo is a
   directory workflow (ADR-0001 layout): `workflows/main.yaml`,
   `workflows/iterate.yaml`, `workflows/retro.yaml`,
   `workflows/validate.yaml`, `workflows/promote.yaml` (the protected
   driver) and `workflows/task.yaml`, helper `workflows/task_*.yaml` and
   `scripts/task/` (the codifiable part; helpers are needed because a
   directory workflow holds one workflow per file). Phase 1 uses a single
   `workflows/codify.yaml` driver plus `codify.sh`. Candidates, archived versions and evidence live in a work dir
   *outside* the source tree (`codify_dir` var), so they do not perturb
   source fingerprints or IDE snapshots.
6. **Candidate → validate → promote.** The retro agent writes only
   `{{ codify_dir }}/candidate/` (a copy of the source tree it edits). A
   deterministic validation workflow runs `markov validate`, a fixture
   replay (see 8), and a guard that every driver file is byte-identical to
   the running version. Promotion archives the current task files to
   `{{ codify_dir }}/versions/vNNNN/`, then writes the candidate's
   `workflows/task.yaml` and `scripts/task/` into the project. In the IDE the
   editor and graph pick this up through disk sync before the reload
   handshake; rollback is restoring an archived version and reloading.
7. **Run evidence for the retro agent.** First slice uses what exists: the
   task's registered `claude` output (session id, usage, compact events) is
   written to `{{ codify_dir }}/evidence/` with `write_file`, and the retro
   prompt points at it plus the Claude transcript path. Engine addition:
   expose the active jsonl callback path(s) as a context variable (e.g.
   `markov_event_logs`) so the retro agent can read step events for native
   steps too. Pass paths, not log text; the agent Reads/Greps them.
8. **Validation that tolerates live data.** HN content changes between runs,
   so a fixture-record step snapshots the API responses once per iteration;
   the candidate task runs against the fixture (`base_url` var) and must
   produce the same markdown as the current version on that fixture, and
   pass format assertions (exactly 10 bullets, each with title and URL).
9. **Metrics per iteration.** Append one JSONL row per iteration to
   `{{ codify_dir }}/metrics.jsonl`: version, digest, claude step count,
   turns, tokens, cost, wall time, validation result, deterministic coverage
   (by step count, cost and wall time). The IDE charts this file.
10. **Budgets.** Retro and task `claude` steps use `limits`; the driver
    stops after N iterations, when the retro agent reports nothing left to
    codify, or when codification spend exceeds a set budget. The agent
    cannot edit driver files, so it cannot raise its own limits or tools.

## Scenario: HN top 10

`workflows/task.yaml` v0 is one step:

```yaml
name: task
steps:
  - name: do_task
    type: claude
    params:
      prompt: >
        Get the current top 10 Hacker News stories and write
        {{ out_dir }}/hn-top10.md as a bulleted markdown list of
        "[title](url)" headlines.
      allowed_tools: [WebFetch, Bash, Write]
```

Expected decomposition the retro agent should discover (not pre-written into
the demo): fetch `topstories.json` → take 10 IDs → fetch each item
(`for_each`) → format bullets → `write_file`. Likely end state is fully
codified with no residual agent; an optional variant adds a judgment step
("tag each story by topic") to show a residual agent surviving.

Driver loop: `main` → `iterate(i)`: record fixture → call `task` → save
evidence and metrics → `retro` (claude, writes candidate) → `validate` →
`promote` when passed → `reload_workflow` → stop gate → `iterate(i+1)`.

## Phases

1. **Manual loop, no engine changes.** Build `examples/codification/hn-top10`
   with the driver/task split; run task, run retro, promote by hand,
   restart Markov. Proves the retro prompt and validation and gives a
   baseline. Opens in the IDE as an ordinary directory project.
2. **`reload_workflow`, `--allow-reload`, ephemeral marking, control
   handshake, `workflow_reloaded` event, breakpoint re-resolution.** Engine,
   parser, schema, callbacks, docs (`step-types.md`, `workflow-file.md`,
   `callbacks.md`), ADR. Tests: reload success; invalid candidate keeps old
   file; current workflow finishes on old steps; next sub-workflow call uses
   new ones; concurrent `for_each` during swap; handshake reload with an
   alternate source and reject; breakpoints re-resolved; resume refusal.
3. **Event-log variable** for the retro agent; `variables-and-context.md`.
   Done as `markov_event_logs` plus `markov_current_run_id` (needed to
   find one iteration's events in a log shared by the whole loop).
4. **Automated driver loop** with metrics and budget stop; record a run and
   the coverage/cost curve in `.ledger/notes/`.
5. **IDE support** per the IDE plan (needs 2; useful from 1 for viewing).

## Relationship to plan 002

Plan 002's output contracts are the general form of the validation gate in
(8); its evidence-as-completion-contract is the general form of (7); its
retry/budget policies are themselves codification targets. This plan does
not depend on 002 for the HN demo (no external side effects), but any
codified workflow that mutates external systems needs 002's durable task
ownership before promotion is safe, since rollback cannot undo a POST.

## Open questions

- Does the retro agent rewrite the whole task each pass, or propose one
  decomposition at a time (smaller diffs, easier validation and review)?
  Default: one change per iteration.
- What does markovd show for a run whose definition changed mid-run?
- In the IDE, should the retro agent eventually edit through the IDE's
  agent interface (undoable edits in the open project) instead of a
  candidate dir? Candidate dir first; revisit when the agent interface lands.
