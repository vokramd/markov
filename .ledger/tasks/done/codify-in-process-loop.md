# Task: Codification loop in one markov process

## Status

Done (2026-10-20). Plan 003 phase 4.

## Summary

`codify_loop` (resolve paths, call `codify_iteration`) and `codify_iteration` (begin an `iter-NNNN` dir, call `task`, run the `codify` pass on that task's events, `reload_workflow` when the candidate was promoted, recurse while something changed, `i < max_iterations` and the retro spend is below `max_codify_usd`). `codify.sh` is now one `markov run --workflow codify_loop --allow-reload` with a jsonl callback in the codify dir. The task's events are selected from the loop's log by `markov_current_run_id`-based run ID (`codify.py evidence --run-id`); new helper subcommands `resolve-paths` (absolute paths, markov binary from PATH or `MARKOV_BIN`), `begin-iteration` and `spend`. All driver workflows are protected by the validator guard; only `task`, `task_*` and `scripts/task/` may change.

## Findings

- First run: every candidate failed because the validator's child `markov run --workflow task` was refused: the project now contains a reload step (in the driver), and the `--allow-reload` check looked at the whole file. Fixed in the engine: the check considers only workflows reachable from the starting one (templated calls: `workflow_names`, else all); the step still refuses at run time without the flag. ADR-0006 and docs updated; `TestAllowReloadIsOnlyNeededWhenAReloadIsReachable`. That run spent ~$1.6 on three retro passes that could not succeed; validating a known-good candidate before an agent run would have caught it.
- The validator kept the last 2000 characters of a failed command, which was cobra's usage text; it now reports the `Error:`/failure lines.

## Verification

From v0004 (one small claude step left), `./codify.sh 3` in one process: iteration 1 ran the agent ($0.111), the retro codified the final job ($0.279), validation passed, promotion, `reload_workflow` swapped the definition (changes: `task`); iteration 2 ran the reloaded task with 0 agent steps and 0 tokens, the retro reported nothing left ($0.171), `no_change` stopped the loop. `go test -race ./...` passes. Example restored to v0.
