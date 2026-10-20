# Task: reload_workflow step and hot reload

## Status

Done (2026-10-20). Plan 003 phase 2; [ADR-0006](../../decisions/ADR-0006-workflow-hot-reload.md).

## Summary

`reload_workflow` step, `--allow-reload` on run and resume, per-execution pinning of the definition, `reloads` table and resume refusal, `workflow_reloaded` callback event, `reload_pending` control handshake with `reload`/`reject_reload`, breakpoint re-resolution. Docs: step-types, cli, callbacks, debugging, state-store. Schema lists the new type.

## Verification

- `pkg/engine/engine_reload_test.go`: next sub-workflow call uses the new definition while the reloading workflow keeps its own steps; step types stay pinned for the rest of the workflow; invalid source and missing `require` keep the current definition; refusal without `--allow-reload`; resume refused after a reload; eight concurrent reloads from `for_each` (passes under `-race`); handshake loads the client's source, rejects, refuses `continue` while pending; breakpoints re-resolved.
- Mutation checks: disabling pinning fails the step-type test; resolving sub-workflows against the initial definition fails three tests.
- Store contract test covers `SaveReload`/`GetReloads`; jsonl test covers the new event.
- Real binary: a recursive loop that rewrites its own `work` workflow ran versions 0, 1, 2 in one process with three `workflow_reloaded` events; the handshake over `--control stdin` loaded an alternate snapshot, then rejected twice; `markov resume` refused a run that reloaded and then failed.
- `go test -race ./...` and `go vet` pass.
