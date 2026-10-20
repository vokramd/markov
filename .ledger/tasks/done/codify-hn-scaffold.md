# Task: Codification demo scaffold and v0 task

## Status

Done (2026-10-20). See [notes/codify-phase1-run.md](../../notes/codify-phase1-run.md).

## Summary

Plan 003 phase 1. Create `examples/codification/hn-top10` as a directory workflow: `meta.yaml` (entrypoint `task`, so the IDE runs the task), `vars.yaml`, `workflows/task.yaml` v0 (one `claude` prompt that writes the HN top 10 as `- [title](url)` bullets to `{{ out_dir }}/hn-top10.md`, reading the API at `{{ hn_base_url }}`), the protected driver `workflows/codify.yaml`, and `codify.sh`, which runs one iteration as two processes: the task (`--workflow task`, live API, jsonl callback) then the codify driver (`--workflow codify`). Work data (fixtures, evidence, candidate, versions, metrics) lives in a codify dir outside the source tree.

The output contract (shared by the v0 prompt and the validator): exactly 10 lines in `topstories.json` order, `- [<title>](<url>)`, title verbatim, url = item `url` or `https://news.ycombinator.com/item?id=<id>` when absent.

## Verification

`markov validate` passes; a live v0 run produces a file that satisfies the contract.

## Notes

The directory loader requires `rules.yaml` and `step_types.yaml` even when empty. v0 is one `claude` step plus a `mkdir` (the `claude` step's `chdir` must exist). Editable set widened to `workflows/task_*.yaml` because a directory workflow holds one workflow per file, so a codified task needs somewhere to put helper sub-workflows.
