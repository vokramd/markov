# Bug: a load_artifact step's data is lost on resume

## Summary

A completed `load_artifact` step is skipped on resume, but what it loaded is not restored, so
later steps see `<step>.artifacts` as missing.

## Status

Open. Found 2026-10-24 while adding secret masking (plan 007), by reading the code; not yet
reproduced in a test.

## Cause

The `load_artifact` branch in `pkg/engine/engine.go` (`if base == "load_artifact"`) puts
`{"artifacts": ...}` into the run context and saves the step as completed with neither
`OutputJSON` nor `ArtifactsJSON`. `ResumeWithVars` rebuilds a completed step's context entry
from `ArtifactsJSON` or `OutputJSON` only, so it gets nothing.

`artifacts:` on any other step type is saved in `ArtifactsJSON` and restored; only the dedicated
step type is affected.

## Fix

Save the step's `{"artifacts": ...}` as `ArtifactsJSON` (as the generic path does), so resume
restores it. Its `secret` marks are already rebuilt from `ArtifactsJSON` on resume.

## Test

A run that loads a file, then fails at a later step that uses `<step>.artifacts`; resume, and
check that the later step gets the loaded content.
