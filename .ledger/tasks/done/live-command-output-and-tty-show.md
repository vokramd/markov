# Task: Live command output, stream progress kinds, and tty:// show styles

## Status

Done (2026-11-06)

## Why

markov-harness grew Claude Code-style tools (edit_file, glob, grep, parallel read-only tools).
Two things it needed from markov: a `bash` tool whose output appears while the command runs, and a
way to show a tool call's input while the model is still writing it, without hard-coding either
into the tty renderer. Follows [http-request-streaming](http-request-streaming.md).

## Summary

| Addition | What |
| --- | --- |
| `script_exec` / `shell_exec` `live_output: true` | Each stdout/stderr line is reported as `output` progress (`stream`, `text`) as it is written, the same kind the openshell steps emit (reuses `progressLines`). Output variables unchanged; off by default. |
| `http_request` `stream.progress` | A map of progress kind to dotted path, for any kind besides `text` and `thinking` (which become shorthands). A kind can't be `sse` or be set twice. |
| `tty://?show=kind:style,...` | Styles `text` (verbatim), `dim`, `lines` (one dimmed line per event), `count` (a status line counting characters, redrawn in place, cleared on other output or step end; terminal only). Default `text,thinking:dim,output:lines`. |

## Files

- `pkg/executor/limits.go` (`runCappedLive`, `liveOutputParam`), `script_exec.go`, `shell_exec.go`
- `pkg/executor/http_stream.go` (`streaming.reports`)
- `pkg/callback/tty.go` (rewritten around styles)
- `pkg/schema/schema.go`; docs: `step-types.md`, `callbacks.md`, `cli.md`

## Verification

- `make lint`, `go test ./...` pass. New tests: `TestScriptExecLiveOutputReportsLinesAsTheyAreWritten`
  (the script blocks until the progress handler has seen its first line),
  `TestScriptExecReportsNoOutputProgressByDefault`, `TestTTYCallbackLinesAndCountStyles`,
  `TestNewTTYCallbackShowSetting`; stream tests extended for `progress`.
- Live: markov-harness `tests/test_harness.py` 12/12 on claude-haiku-5-5, including
  `test_bash_output_streams_live` and `test_tool_input_streams_as_a_count`.

## Notes

- The schema test finds a step's params by scanning executor source for `params["..."]`; a helper
  that takes the params map and a step name hides them, so pass `params["live_output"]` in.
- `for_each` `concurrency` must be a literal integer; a template is a YAML unmarshal error.
