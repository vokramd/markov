# Bug: a cancelled run's rescue and always steps fail at once

## Status

Fixed 2026-10-25.

- `executeWorkflow` runs `rescue` and `always` steps on `terminalContext`: the run's values
  without its cancellation, cancelled `terminalStepGrace` (two minutes) after the run is.
- The final run and sub-run status saves use `context.WithoutCancel`, so a stopped run is
  recorded as failed instead of staying `running`.
- `markov run` and `markov resume` restore default signal handling after the first SIGINT or
  SIGTERM, so a second one ends the process without waiting for cleanup.
- The CLI transport's `openshell_create` deletes a sandbox that failed to provision, as the SDK
  transport already did.
- markov-ide's Stop waits up to 150 s (`CLEANUP_GRACE`) before SIGKILL instead of 3 s; while the
  run cleans up the button reads Kill, and pressing it kills the run.

Tests: `TestCancelledRunStillRunsRescueAndAlways` (fails without the fix: both handlers saw
`context canceled`), `TestTerminalContextOutlivesCancellationByTheGrace`,
`TestCLICreateDeletesASandboxThatFailedToProvision`, and markov-ide's
`a_stopped_run_may_clean_up_and_a_second_stop_kills_it`. Checked with the real binary: SIGTERM
during a `sleep 30` step still ran an `always` step that sleeps 3 s and writes a file; a second
SIGTERM during that step ended the process (exit 143). Documented in
`docs/reference/workflow-file.md` (lifecycle sections).

## Summary

`executeWorkflow` passed the run's context to `executeTerminalSteps`. After Ctrl-C or the IDE's
Stop that context is already cancelled, so every cleanup step failed immediately with "context
canceled". On top of that the IDE sent SIGKILL 3 s after SIGTERM, shorter than an
`openshell sandbox delete` (about 6 s).

## Reproduction

github-event-workflow, run from the IDE and stopped while `event-loop` long-polled the webhook
sink (2026-10-25 16:46:34): `always: delete-sandbox` failed with "context canceled" and sandbox
`mk-create-sa-a2fef0` stayed Ready. Two earlier stopped runs had left `mk-create-sa-a292a5`
(Ready) and `mk-create-sa-c93ed4` (Error).

Separately, a sandbox create that fails its health check (an invalid provider policy) leaves the
sandbox in the Error phase; the step registers nothing, so `when: "sandbox.name"` skipped the
delete (`mk-create-sa-08f62f`, `mk-create-sa-82f00e`).

## Impact

Every stopped run leaked whatever its `always` steps were meant to release: sandboxes, locks,
temporary resources. The run also stayed `running` in the state store.
