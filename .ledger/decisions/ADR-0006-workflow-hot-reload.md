# ADR-0006: Workflow hot reload with an explicit reload_workflow step

Status: accepted, 2026-10-20.

## Context

Agentic codification (plan 003) runs a loop in which an agent rewrites part of the workflow and the next iteration should use the new definition. Phase 1 restarted Markov for every iteration. The engine parsed the definition once (`engine.New`) and read it from `e.file` everywhere, including while steps ran (step types, rules, decisions, the scripts directory). Loops are recursive sub-workflow calls, and a running workflow already holds its own step list. Resume replays a run against its source, guarded by source fingerprints (ADR-0002) and step definition hashes.

## Decision

- **Explicit step, opt-in run.** A `reload_workflow` step (params `source`, default the run's source path, and `require`) replaces the definition. A run whose starting workflow can reach one through sub-workflow calls (templated calls: their `workflow_names`, or every workflow) does not start without `--allow-reload` (`Engine.AllowReload`); the step itself also refuses without it. Reaching, not merely containing: the codification validator runs a project's `task` workflow while the same project's driver loop reloads. Reloads are never implicit (no file watching).
- **Pinning.** The definition sits behind an `atomic.Pointer`. Each workflow execution is pinned through its context to the definition it started with (`withFile`/`fileFor`); step types, rules, decisions, scripts directory and definition hashes come from the pinned file. Only sub-workflow resolution reads the current definition, so a reload takes effect at the next sub-workflow call and never changes a workflow mid-execution. The callee is pinned to the file it was resolved from.
- **Atomic, validated swap.** The new source is loaded with the CLI's loader (same parsing, validation and `--forks`/`--namespace` overrides) and fingerprinted. Any failure, or a missing `require`d workflow, fails the step and keeps the current definition. Reloads are serialized with a mutex.
- **Implemented as an engine-provided executor** registered in `engine.New`, so `register`, `when`, `failed_when`, `ignore_errors` and `timeout` behave as for any step. The engine passes the step's identity through the context.
- **Ephemeral runs.** Every reload is recorded in a new `reloads` table against the top-level run; `markov resume` refuses a run that has any. Version-pinned resume is deferred.
- **Observable.** A `workflow_reloaded` callback event (new `Callback.OnWorkflowReloaded`) carries digests, a reload counter and a change summary (workflows and steps added/removed/changed, other top-level sections changed).
- **IDE handshake.** With a control channel attached, the step emits a `reload_pending` debug event (candidate digest and changes, or the load error) and waits like a pause for `reload` (optional `source`, e.g. a fresh IDE snapshot) or `reject_reload`; other resume commands are refused with an error event. After a swap the debugger re-resolves its breakpoints against the new definition and reports them in `breakpoints_set` with `reason: workflow_reloaded`.

## Consequences

- `callback.Callback` and `state.Store` each gain methods; all implementations and test mocks were updated. markovd ignores unknown event types.
- Executors built from the definition at startup are not rebuilt: the Jev executor keeps the connections it was created with, and the k8s executors their namespace. Decisions and connections looked up by the engine do follow the pinned definition.
- `markov validate` cannot know whether a run will pass `--allow-reload`, so the check happens when a run starts.
- `reload` in the step output counts reloads in this process, not across the run's history (a reloaded run is not resumable, so they coincide).
