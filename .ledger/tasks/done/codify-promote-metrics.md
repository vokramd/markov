# Task: Promotion, version archive and metrics loop

## Status

Done (2026-10-20). Seven-iteration curve in [notes/codify-phase1-run.md](../../notes/codify-phase1-run.md) (run 2).

## Summary

Plan 003 phase 1, design items 6 and 9. `codify.py promote` archives the current task files to `versions/vNNNN/` (v0000 is the baseline) and copies the candidate's task files into the project; `codify.py record` appends one row per iteration to `metrics.jsonl`. `codify.sh N` loops iterations and stops early when the retro reports nothing left to codify. Record the resulting cost/coverage curve in `.ledger/notes/`.

## Verification

A multi-iteration run shows the task moving from one agent step to native steps, with the versions archived and metrics rows for every iteration; rollback by copying an archived version back works.
