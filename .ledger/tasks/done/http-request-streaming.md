# Task: Streaming http_request (SSE) and the tty:// callback

## Status

Done (2026-11-05)

## Why

markov-harness (`../markov-harness`, a Claude Code-like chat harness written as a markov workflow)
calls the Messages API with `http_request`. Without streaming, a reply appeared only after it was
complete. Executors already report live progress (`ProgressFunc` → `step_progress` events), but only
`claude` and the openshell steps used it, and no callback showed progress in a terminal.

## Summary

| Addition | What |
| --- | --- |
| `http_request` `stream` | `sse`, or `{format: sse, text: <dotted path>}`. A `text/event-stream` response is read event by event; each event is reported as `sse` progress (`event`, `data`, decoded when JSON) as soon as it is complete, and `body` ends as the list of `{event, data}`. `text` names a string in each event's data to report as `text` progress; `thinking` does the same as `thinking` progress. Sends `Accept: text/event-stream`. A response that is not an event stream (an error answered with JSON) is read whole as before. Bytes count against `max_output` as they arrive. Not combinable with `paginate`. |
| `tty://` callback | Writes `text` progress verbatim to `/dev/tty` (or `tty://stdout`, `tty://stderr`), and ends a step's line with a newline when the step completes or fails. Writes `thinking` progress dimmed (on a terminal), topped up to one blank line before the text that follows. Ignores every other event. Also shows `claude` steps' assistant text. |

The executor stays provider-neutral: reassembling an Anthropic message from its events
(`content_block_*`, `message_delta`) is the workflow's job; markov-harness does it in
`scripts/history.py assemble`.

## Files

- `pkg/executor/http_stream.go` (parser, settings), `http_request.go` (wiring, `statusError`)
- `pkg/callback/tty.go`, `parse.go`; `cmd/markov/main.go` flag help
- `pkg/schema/schema.go` (`stream` param)
- Docs: `step-types.md` (Streaming section), `callbacks.md` (tty://, progress kinds), `cli.md`

## Verification

- `make lint`, `go test ./...` pass.
- New tests: `TestHTTPRequestStreamSSEReturnsEventsAndReportsProgress` (comments, CRLF, multi-line
  data, final event without a blank line, non-JSON data, `text` path),
  `TestHTTPRequestStreamSSEReportsEventsAsTheyArrive` (progress before the response ends),
  `TestHTTPRequestStreamFallsBackToWholeBody`, `TestHTTPRequestStreamErrorStatusFails`,
  `TestHTTPRequestStreamCountsAgainstOutputLimit`, `TestHTTPRequestStreamRejectsBadSettings`,
  `TestTTYCallbackWritesStepTextAndEndsItsLine`, `TestTTYCallbackDimsThinkingAndSeparatesItFromText`,
  `TestParseTTYCallbackURL`.
- Live: markov-harness `tests/test_harness.py` (7/7 against model-relay with claude-haiku-5-5),
  including `test_reply_streams`; a 150-word reply reached the terminal in 40 pieces.

## Follow-ups

- Done 2026-11-06: `stream.thinking`. The 5.x models default to `thinking.display: omitted` (empty
  thinking text, so a silent pause); markov-harness requests `summarized` (`thinking_display` var).
  First visible output went from ~4-9 s to ~2 s on claude-haiku-5-5.
- Streamed progress is masked like every event, but a secret split across two deltas would not be.
