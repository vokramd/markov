# Bug: a secret restored on resume inside a sub-workflow is not masked

## Status

Fixed 2026-11-06.

`executeStepCore`'s completed-step shortcut (`pkg/engine/engine.go`) now calls
`noteStepOutput` on the output it restores, so a `secret` step's stored output is masked again
before anything uses it. `noteStepOutput` does nothing for steps without `secret`.

Test: `TestRestoredSubWorkflowSecretStaysMaskedOnResume`. A sub-workflow mints a token
(`secret: [token]`), a later step fails with the token in its error, and a new process resumes.
Without the fix the resumed run's `step_failed` event held the token in plain text; with it, it
reads `t=<secret:mint.token>`.

## Summary

On resume, `Resume` re-notes the outputs of the top-level run's completed `secret` steps
(`engine.go` around line 413), but not of steps in sub-runs. Re-entering a sub-workflow, the
shortcut put a completed secret step's stored output back into the context without telling the
masker, so in a new process every later use of it was unmasked: events, log lines, errors.

Found by a Codex review of the `repeat` design
([repeat-sub-workflow-calls](../../tasks/pending/repeat-sub-workflow-calls.md)), point 8.
