# Task: claude in sandboxes, and openshell_provider

## Status

Done (2026-10-23). Follow-on to plan 004.

## Summary

- `claude` takes `sandbox` (and `transport`): the same CLI command runs in that OpenShell
  sandbox through the openshell transport; transcript, usage, cost and limits behave as on the
  runner (ending the exec stops the CLI). Only the step's `env` is passed in.
- `openshell_provider` ensures a provider profile (import, or update when the file differs; cli
  transport only) and a provider with credentials (create, or replace credentials; both
  transports). Credential values stay out of outputs, `--verbose` (`executor.SecretParams`) and
  argv (the CLI gets them through its environment).

## Findings (OpenShell 0.1.5, lab relay)

- Sandboxes deny all egress; model access goes through a provider whose profile names the hosts
  and binaries (for the npm Claude Code install, the real binary is
  `.../@anthropic-ai/claude-code/bin/claude.exe`).
- Podman copies the host's /etc/hosts into sandboxes; a name resolved there reaches OpenShell as
  a bare private IP and is denied. Images should look names up DNS-first (OpenShell's resolver
  answers with mapped placeholder addresses); the endpoint also pins `allowed_ips`.
- The proxy terminates TLS to swap the placeholder credential, so it must trust the upstream's
  CA (`additional_ca_bundle`); `tls: skip` reaches the relay but disables the swap.
- `profile export` fills defaults inside list items, so "unchanged" is a recursive subset
  comparison; SDK provider create needs `ProfileWorkspace` for workspace-scoped profiles.
- Known gap: variables from vars.yaml (including keys) are recorded in `run_started` and state.

## Verification

- Unit: claude in a sandbox (argv, env, workdir, stdin, transcript, limit ends the exec);
  provider create/update with the secret only in the CLI's environment; profile
  import/unchanged/update with resource_version; recursive subset compare.
- Live: `TestE2EProvider` on cli and sdk; the github-workflow-example provider step reported
  `profile: unchanged` with credentials redacted in `--verbose` and events.
- `make lint`, `go test ./...`.
