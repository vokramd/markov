# Task: Retro agent step and run evidence

## Status

Done (2026-10-20). See [notes/codify-phase1-run.md](../../notes/codify-phase1-run.md).

## Summary

Plan 003 phase 1, design item 7. `codify.py metrics` summarizes a task run's jsonl events (claude vs native step executions, turns, tokens, cost, duration, deterministic coverage). The driver copies the project to `candidate/` and runs a `claude` retro step in it with the task's event log, its claude transcripts, the current metrics and the Markov reference docs. It may make one decomposition per pass (edit `workflows/task.yaml`, add `scripts/task/*`) and writes `retro.json` (`changed`, `summary`, `remaining_agent_steps`) to the iteration's evidence dir. Bounded by `limits`; tools limited to read/edit plus `markov validate`.

## Verification

One live iteration from v0 yields a candidate with at least one native step that passes validation, or a clear `retro.json` explaining why not.
