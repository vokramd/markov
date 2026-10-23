# Task: OpenShell SDK transport

## Status

Done (2026-10-23). Plan 004 phase 2.

## Summary

`pkg/openshell/sdk.go`: the `sdk` transport over the OpenShell Go SDK, pinned to the v0.1.5
release commit (`github.com/NVIDIA/OpenShell/sdk/go` `bdb956f`; the SDK has no tags of its
own). go.mod moves to Go 1.25.13 with grpc 1.83 and newer x/ modules. Create, wait-ready and
delete use the SDK's typed client; exec uses the interactive exec RPC directly (TTY off, stdin
streamed in 256 KiB chunks), because the SDK's own interactive exec forces a TTY and its other
calls cannot send stdin. Upload and download stream a tar archive through that exec
(`tarcopy.go`), since the SDK's file client has no transport. `gateway.go` resolves the gateway
like the CLI: a name (or the CLI's active gateway) from `~/.config/openshell/gateways/<name>/`
with its mTLS certificates, or a `host:port` address with `ca_file`, `cert_file`, `key_file`,
`token_env`. Executors close transports that implement io.Closer.

## Findings (OpenShell 0.1.5)

- The SDK's gateway helper rejects mTLS gateways, but its TLSConfig takes a client cert; the
  CLI keeps them in `gateways/<name>/mtls/{ca.crt,tls.crt,tls.key}`.
- The exec `execution_timeout` returns 124 but leaves the command running, on both exec RPCs.
- Ending an interactive exec stream stops the command; a one-shot exec (what the CLI uses when
  stdin is complete within 200ms, including no stdin) keeps running when its client goes away.
- One exec session cannot signal another's processes ("Operation not permitted"), so killing a
  stray command with a second exec does not work.
- The CLI caps exec stdin at 4 MiB; the supervisor's stdin queue is unbounded.

So both transports enforce `timeout` by cancelling (shared `execDeadline`), and the CLI
transport holds the CLI's stdin open for 300ms so it always uses the streaming RPC. The CLI
transport no longer passes `--timeout`.

## Verification

- `TestE2E` runs the same subtests on `cli` and `sdk` against the live gateway, all passing:
  exec (exit code, stderr, stdin, env, workdir, timeout exit code, argv), copy (tree with a
  dotfile, single file, both directions, missing path), a 3 MiB file (several stdin chunks)
  with an executable bit and a symlink round trip, git_ignore on and off, cancel stops the
  command, timeout stops the command (`sleep N | cat` gone from /proc), errors (missing
  sandbox, deleting a missing one). ~25s for both.
- Before the fixes, "cancel" failed on cli and "timeout" failed on both; the cancel check's
  first version matched its own command line and was fixed with a bracket pattern.
- `tarcopy_test.go`: hostile archives (`..`, absolute, write through a symlink, write onto a
  symlink, nested `../..` symlink) cannot leave the destination; disabling the guards fails
  the three symlink cases. Round trip keeps modes and links. `ResolveEndpoint` with a temporary
  XDG_CONFIG_HOME: active gateway with mTLS, address, unknown name, OIDC refusal, unset token.
- `cli_test.go`: timeout kills the CLI and reports 124; cancel reports an error.
- `markov run examples/openshell --openshell-transport sdk` and `cli`: both succeed; no
  sandboxes left.
- `make lint` and `go test ./...` pass.
