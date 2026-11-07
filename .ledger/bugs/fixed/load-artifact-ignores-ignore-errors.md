# Bug: artifact loading ignores `ignore_errors`

## Status

Fixed 2026-11-06.

- `executeStep` (`pkg/engine/engine.go`): when `loadArtifacts` fails on a `load_artifact` step, or on
  `artifacts` attached to another step, and the step has `ignore_errors`, the step completes with
  `artifacts: {}` plus `failed: true` and `error` (`ignoredFailure`), like any other ignored failure.
  An empty `artifacts` map on `load_artifact` is a workflow error and still fails.
- Docs: `step-types.md` (load_artifact, Failure Conditions).

Test: `TestIgnoreErrorsOnAMissingArtifact` (pkg/engine), both cases. Before the fix each failed with
`loading artifacts: artifact "text": reading …: no such file or directory`.

## Summary

Both artifact paths returned `failStep` on a load error without checking `step.IgnoreErrors`.

## Reproduction

markov-harness's `read_file` tool loaded the requested path with `load_artifact` and
`ignore_errors: true`, so a missing file could become a tool error result. Instead the whole session
crashed on the first missing path. The harness switched to `scripts/search.py read`.
