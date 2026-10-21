# Task: markov codify (codify any workflow by default)

## Status

Done (2026-10-20). Plan 003 phase 6, slices 6a–6d (6e in markov-ide).

## Summary

`markov codify <dir> --workflow W [--model] [--max-iterations] [--budget-usd] [--keep] [--prompt-file] [--codify-dir]` (`pkg/codify`, documented in `cli.md`), and the `codify` step type (one iteration, `step-types.md`).

- **Unit:** W plus the workflows it reaches; helpers `workflows/W_*.yaml` and `scripts/W/`; everything else protected (byte comparison). Directory workflows only.
- **Runs:** every version runs as a child `markov run --workflow W` in an empty directory; its outputs are the files it writes there. No driver in the project, no reload needed.
- **Recording proxy** (`proxy.go`): HTTP and HTTPS with its own CA (trusted through `HTTPS_PROXY`, `SSL_CERT_FILE`, `CURL_CA_BUNDLE`, `REQUESTS_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`); record for the live run (a repeated request gets its first response), strict replay for candidates (misses and non-GET/HEAD fail); the claude CLI's own hosts tunnelled untouched.
- **Oracle:** identical outputs pass; otherwise, since an agent produced the current outputs, a judge (the codify model) compares content. Stops when the workflow writes no files (nothing to keep equal), fails, has no agent steps, the retro agent changes nothing, three candidates in a row fail, or the budget/iterations run out.
- **Prompts:** default retro prompt (`retro.md`, replaceable, Go template) and judge prompt (`judge.md`), run through the claude executor. Evidence: events, transcripts, outputs, `requests.md` (every request with the start of its response, so native steps use the agent's URLs), metrics, the previous pass's result. Reference docs and `schema.json` embedded in the binary (`docs/embed.go`).
- **History:** `<dir>.codify/` with `versions/vNNNN/` (the codified files), `metrics.jsonl` (the IDE tab's format), `iter-NNNN/`.

## Verification

- Experiment: the real claude CLI through the proxy recorded curl and WebFetch traffic and tunnelled api.anthropic.com; telemetry host added to the tunnelled list.
- `pkg/codify` tests with a fake claude playing task, retro and judge: promotion by judgment then stop with no agent steps (the candidate replays, the server is hit only by live runs); edits outside the target rejected three times then stop; a candidate fetching an unrecorded URL rejected naming it; a workflow writing no files stops; `Pass`; proxy record/replay/fill/HTTP/passthrough/curl-through-env; a repeated request keeps its first response. `codifyConfig` params test. `go test -race ./...` and `go vet` pass.
- Real run on a copy of `~/Documents/markhov-hn-codify` (see notes/codify-phase1-run.md, "Phase 6").

## Follow-ups (2026-10-20, from the first IDE run)

- **Progress.** Each phase reports a line on stdout as it starts and ends (live run, retro, candidate, judge); the IDE shows an intro line at once instead of "Waiting for the first event…".
- **Stopping.** SIGTERM/Ctrl-C cancels the iteration, records it as `stopped` (nothing half-done installed) and stops child runs with SIGTERM. `markov run` and `resume` now also cancel on SIGTERM/Ctrl-C, so a claude step kills its process group instead of being orphaned (it runs in its own group, which the IDE's Stop did not reach). Checked with the real binary: no processes left behind.
- **Events.** `markov codify` takes the callback flags. It is one run; its phases are steps of the virtual `codify` workflow (`live`, `retro`, `candidate`, `judge`, `promote`), the retro agent's and judge's claude streams are `step_progress`, and the target's runs are forwarded unchanged except that their own start/end become `sub_run_*` (new `callback.Dispatch` replays a logged event onto any callback). The `codify` step type sends the same to its run's callbacks. Tests: `TestDispatchReplaysEveryLoggedEventType`, `TestCodifyEmitsItsPhasesTheAgentsStreamsAndTheTargetsEvents`, `TestStoppingRecordsTheIterationAndChangesNothing`; real-binary smoke check of the event file.
