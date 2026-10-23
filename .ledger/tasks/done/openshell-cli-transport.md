# Task: OpenShell steps on the CLI transport

## Status

Done (2026-10-23). Plan 004 phase 1.

## Summary

Six step types (`openshell_build`, `openshell_create`, `openshell_upload`, `openshell_exec`,
`openshell_download`, `openshell_delete`) in `pkg/executor/openshell.go`, over the
`pkg/openshell` Transport interface with the CLI transport (`cli.go`) and podman builds
(`build.go`). The `openshell:` block in a single file or `meta.yaml`, `--openshell-transport`
on run and resume, and a per-step `transport` param select the transport (`sdk` reports not
implemented). Executors now get the run, workflow and step through `executor.StepFrom(ctx)`.
Docs: step-types (OpenShell sandboxes section), workflow-file, cli. Example:
`examples/openshell/`.

## Findings from the live gateway (OpenShell 0.1.5, Podman driver)

- Sandbox names are DNS labels of at most 19 characters. Default names are
  `mk-<step, up to 9>-<6 hex>`; the run is recorded in `markov-run`, `markov-workflow` and
  `markov-step` labels instead.
- `sandbox create --detach -o json` waits for Ready (JSON on stdout, progress on stderr).
  Under a second with cached images; the first create after the gateway starts took ~45s.
- `sandbox upload` puts a file or directory into the destination under its own name (with a
  trailing slash); `sandbox download` writes a file to the given path and a directory's
  contents into it. Markov's rule for both directions is "copy into `dest`, keeping the name",
  so download passes `dest/<base>`.
- `sandbox exec --no-tty` passes stdin, separates stdout and stderr, returns the command's
  exit code, and 124 on `--timeout`. A missing sandbox also exits 1.
- `sandbox delete` of a missing sandbox succeeds. A failed create leaves nothing behind.
- The default policy lets sandboxes read only `/bin`, `/usr`, `/lib`, `/etc`, `/proc`,
  `/var/log`; a script in `/opt` fails with EACCES. The example installs under `/usr/local/lib`.
- Error reports are boxed and wrapped even with `OPENSHELL_COLOR=never`; `plainError`
  flattens them to one line.

## Verification

- `pkg/openshell/cli_test.go` (fake openshell binary): argv for create, exec, upload,
  download; exit code and stream separation; stderr in errors; `ImageRef`; unknown transports.
- `pkg/executor/openshell_test.go` (fake transport): default names fit 19 characters and
  differ per call; labels, with the step's own winning; string and argv commands; stdin as
  JSON; progress lines; non-zero exit and timeout fail but keep output; copy result paths;
  delete of a list; step `transport` overrides the file's.
- `MARKOV_OPENSHELL_E2E=1 go test ./pkg/openshell -run E2E` against the live gateway: podman
  build, create with env and labels, exec (exit code, stderr, stdin, env, workdir, timeout,
  argv with spaces), upload of a tree with a dotfile and of a file, download of both, missing
  path and missing sandbox errors, delete. Passes in ~9s.
- `markov run examples/openshell` (from that directory): built, created, uploaded, ran the
  report, downloaded it, deleted the sandbox in `always:`; `step_progress` events of kind
  `output` streamed build and exec lines. With a bad image, create failed and the cleanup step
  skipped (`when: "sandbox.name"` is false); no sandboxes left.
- `make lint` and `go test ./...` pass.
