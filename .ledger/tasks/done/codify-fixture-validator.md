# Task: Codification fixture recording and candidate validation

## Status

Done (2026-10-20). See [notes/codify-phase1-run.md](../../notes/codify-phase1-run.md).

## Summary

Plan 003 phase 1, design items 6 and 8. `scripts/codify/codify.py` (stdlib only) subcommands: `record-fixture` (top story IDs plus the first 10 items in API path layout) and `validate` (driver files byte-identical to the project, only `workflows/task.yaml` and `scripts/task/**` may differ; `markov validate` on the candidate; serve the fixture on 127.0.0.1; run the candidate's `task` in a child markov against it; compare output with the contract applied to the fixture). Emits one JSON result: `status` (`passed`, `failed`, `no_change`), reasons, changed files, and the candidate run's metrics.

## Verification

A hand-written fully native candidate passes; a candidate that edits a driver file, breaks the format, or fails validation is rejected with a precise reason. No model calls needed.

## Notes

Verified with no model calls: a hand-written fully native candidate (http_request → sub-workflow `for_each` with `concurrency: 1` → `write_file`) passed; no change, an edited driver file, `concurrency: 5` (wrong order), invalid YAML and 9 lines were each rejected with a precise reason. Writing the native candidate exposed three engine bugs (see `bugs/open/`).
