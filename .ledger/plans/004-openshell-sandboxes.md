# OpenShell sandboxes

Created 2026-10-23. Native Markov steps for [NVIDIA OpenShell](https://github.com/NVIDIA/OpenShell)
sandboxes: build an image, create a sandbox, copy files in, run commands in it, copy files
out, delete it. The target setup is a local gateway using the Podman compute driver.

**Status:** phase 1 done (tasks/done/openshell-cli-transport.md, notes/openshell-setup.md);
phase 2 (SDK transport) next. Design changes from what phase 1 found are folded in below.

## Findings (OpenShell v0.1.5, source checked 2026-10-23)

- OpenShell does not build images. `sandbox create --from` takes an image reference (or a
  rootfs tar, VM driver only); its docs say to `podman build -t localhost/name:tag` and pass
  that tag. With the Podman driver the gateway finds local images through `podman.socket`.
- The `openshell` CLI covers every operation: `sandbox create` (`--from`, `--name`, `--cpu`,
  `--memory`, `--gpu`, `--env`, `--provider`, `--policy`, `--label`, `--detach`, `-o json`),
  `upload NAME LOCAL [DEST]` (respects .gitignore unless `--no-git-ignore`),
  `exec` (`--workdir`, `--env`, `--timeout`, `--no-tty`, `--no-login-shell`),
  `download NAME PATH [DEST]`, `delete NAMES...`. It also owns gateway selection and auth.
- The Go SDK (`github.com/NVIDIA/OpenShell/sdk/go`, needs Go 1.25.13) has typed create,
  `WaitReady`, `Get`, `Delete`, `Exec().Run/Stream/Interactive`. Its `Files().Upload/Download`
  return `ErrTransportNotAvailable` (no SSH transport is shipped). The gateway's exec RPC
  accepts stdin (one message, 1 MiB gRPC limit) and the interactive RPC streams stdin in
  chunks, so the SDK transport can copy files as a tar stream over exec.
- Packaging: Fedora 44 RPMs (`openshell`, `openshell-gateway`); the gateway RPM ships a
  systemd user unit, `openshell-gateway.service`, which wants `podman.socket`.

## Design

### Step types

All share the `openshell_` prefix. `sandbox` names the sandbox to act on.

| Step | Does | Main params | Output |
|---|---|---|---|
| `openshell_build` | `podman build` (both transports; OpenShell has no build) | `tag` (required; bare names become `localhost/<name>`), `context` (default `.`), `containerfile`, `build_args`, `target`, `pull`, `no_cache`, `push` (for remote gateways) | `image`, `id` |
| `openshell_create` | create a sandbox; returns once it is ready | `image`, `name` (default `mk-<step>-<random>`: names are DNS labels of at most 19 characters), `cpu`, `memory`, `gpu`, `env`, `providers`, `policy`, `labels` (Markov adds `markov-run`, `markov-workflow`, `markov-step`) | `name`, `id`, `phase`, `workspace`, `image` |
| `openshell_upload` | copy a local file or directory into `dest` | `sandbox`, `src`, `dest` (default `/sandbox`), `git_ignore` (default false: copy everything) | `sandbox`, `path` (`dest/<base>`) |
| `openshell_exec` | run a command, streaming output as step progress | `sandbox`, `command` (string runs under `sh -c`, list is argv), `workdir`, `env`, `timeout`, `stdin` | `stdout`, `stderr`, `exit_code`, `timed_out`; fails on non-zero exit like `shell_exec` |
| `openshell_download` | copy a file or directory out into `dest` | `sandbox`, `src`, `dest` (default `.`) | `sandbox`, `path` (`dest/<base>`) |
| `openshell_delete` | delete one or more sandboxes (a missing one succeeds) | `sandbox` (string or list) | `deleted` |

Every step also takes `transport` (overrides the default below). Relative local paths
resolve against the run's working directory, as other steps do.

### Transports

`pkg/openshell` defines one interface (`Create`, `Upload`, `Exec`, `Download`, `Delete`) with
two implementations; the executors depend only on the interface.

- **`cli`** (default): runs `openshell`. Gateway selection and auth are the CLI's own
  (`openshell gateway select`, env vars). Create uses `-o json`; exec uses `--no-tty`.
- **`sdk`**: Go SDK over gRPC. Exec streams through `Exec().Stream`. Upload and download
  stream a tar archive through exec (`tar -x -C DEST` / `tar -c`), so the image needs `tar`;
  `git_ignore: true` selects files with `git ls-files`.

Configured per workflow file, at the top level of a single file or in `meta.yaml`:

```yaml
openshell:
  transport: cli          # or sdk
  workspace: default
  gateway: 127.0.0.1:17670 # sdk: address; cli: gateway name, passed as the CLI's gateway flag
  token_env: OPENSHELL_TOKEN   # sdk only, optional
  ca_file: /path/to/ca.crt     # sdk only, optional
  binary: openshell            # cli only
```

`markov run --openshell-transport cli|sdk` overrides the file for a run; a step's `transport`
param overrides both.

### Cleanup

A failed step leaves its sandbox running. Workflows delete sandboxes in `always:` with
`when: "sandbox.name"` (false when create itself failed); the docs and the example show that.
The `markov-run` label finds a run's leftovers: `openshell sandbox list --selector markov-run=<id>`.

### Testing

- Unit tests: a fake `openshell` binary for the CLI transport (records argv, replays
  output); the SDK's `fake` package for the SDK transport.
- One shared conformance suite run against both transports on a live gateway, gated by
  `MARKOV_OPENSHELL_E2E=1`: build, create, upload a tree, exec (rc, stdout, stderr, env,
  workdir, timeout), download, delete, and a failing exec.
- `examples/openshell/`: build an image from a Containerfile, run a script against
  uploaded files, download its output, delete the sandbox in `always:`.

## Phases

1. **Setup and CLI transport.** Install OpenShell (RPMs), enable `podman.socket` and the
   gateway user service, record the setup in a note. `pkg/openshell` interface and CLI
   transport; the six executors; schema, parser and `step-types.md` entries; example;
   conformance suite on the CLI transport.
2. **SDK transport.** Add the SDK dependency (go.mod to Go 1.25.13), implement the
   interface including tar-over-exec copies, run the conformance suite on both transports.
3. **IDE.** markov-ide: icons and palette templates for the six steps (the inspector's
   fields come from the engine schema).

## Open questions

- Whether to cap upload size on the SDK transport or stream without limit.
- Whether `openshell_create` should also accept a `template`.
- Whether Markov should delete sandboxes a failed run left behind on its own (by the
  `markov-run` label) instead of relying on `always:`.
