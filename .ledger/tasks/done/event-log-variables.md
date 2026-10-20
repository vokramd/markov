# Task: Event-log and current-run variables

## Status

Done (2026-10-20). Plan 003 phase 3.

## Summary

Built-in variables for a step that reviews how earlier steps ran (the codification retro agent): `markov_event_logs` (absolute paths of the run's `jsonl://` callback files, refreshed on resume; `Engine.EventLogs`, set by the CLI) and `markov_current_run_id` (the executing (sub-)run, set at the start of every workflow execution). A loop in one process appends every iteration to one log, so the driver selects a task's events by its predictable sub-run ID, `<markov_current_run_id>-<call step>`. The jsonl callback already writes and syncs each event synchronously, so the log is complete up to the step that reads it. Documented in `variables-and-context.md`.

## Verification

`TestBuiltinRunIDsAndEventLogs` (top level, sub-workflow, `for_each` iteration, resume refreshes the logs), `TestEventLogsIsAnEmptyListWithoutJSONLCallbacks`; with the real binary a sub-workflow step given a relative `jsonl://` path saw the absolute path, its sub-run ID, and the events already written. `go test -race ./...` passes.
