# Session Log

## 2026-09-01

Agent: codex

Completed:

- Reworked the legacy artifact-condition example into the current workflow
  schema, using clearly labelled shell placeholders for external agents.
- Replaced unavailable LLM and rule-less gate placeholders in the batch RFE
  example, and removed the obsolete `on_pause` block from the RFE gate example.
- Added strict parsing for structured workflow YAML. Unsupported top-level,
  workflow, step, rule, step-type, and artifact fields now fail validation;
  free-form executor maps remain supported.
- Added source-tree fingerprints and `warn`, `strict`, and `off` source-integrity
  modes for resume.
- Added SQLite/Postgres persistence for original source digests and resume-check
  history.
- Recorded ADR-0002 and documented CLI, state-store, resume, and callback
  behavior.

Verified:

- `bin/markov validate` passed for all 21 current top-level and directory
  examples.
- Focused source, state, engine, and CLI tests plus `go vet ./...` passed.
- Built `bin/markov` and exercised an unchanged strict resume end to end.

## 2026-07-01

Agent: codex

Completed:

- Created ledger plan structure for directory workflow file support.
- Added `PLAN.md`, milestone, ADR, and pending task files.
- Moved detailed feature planning into `.ledger/plans/000-directory-workflow-files.md`.

Discovered:

- Repository did not yet have the ledger directories described in `.ledger/agentic_work_ledger.md`.

Next:

- Directory workflow support is implemented and verified with focused tests.
- Full `go test ./...` still cannot complete in this sandbox because callback tests require local TCP listeners.

## 2026-07-13

Agent: codex

Fixed:

- CLI `--var` values now use canonical Markov type coercion, preventing the
  string `"false"` from evaluating truthy in `when` conditions.

Verified:

- Repository `make test` passed with callback listener access.
- Platform `make host-rebuild-markov` imported the rebuilt image into k3s.
- Live run `markov-run-622d5874` skipped `main.run_pipeline` for
  `--var run_pipeline=false` and created no pipeline child steps.

## 2026-09-27 — Jev integration

Implemented named connections/decisions, native Jev steps, durable inline conditions/assertions, and decision-backed gate facts. Added optional directory definition files and documentation. Verified against authenticated CPU Kev-0.8B, including a successful resume after stopping the server. All tests/build/vet and 22 example validations pass; full formatting check has four unchanged baseline failures. See [validation](jev-kev-validation.md) and [completed task](../tasks/done/jev-support.md).

## 2026-10-05 — Ansible step types

Added `ansible_playbook` and `ansible` primitives with inline/path inventory, extra vars via temp files, CLI flag params, and parsed `PLAY RECAP`. See [task](../tasks/done/ansible-step-types.md). Tested with a fake binary; not yet run against real Ansible.

## 2026-10-05 — claude step type

Added the `claude` primitive with live stream-json processing, Markov-enforced limits, and a new `step_progress` callback event. Verified against the real CLI with the OAuth login (no API key). See [task](../tasks/done/claude-step-type.md) and [ADR-0004](../decisions/ADR-0004-claude-step-type.md).

## 2026-10-05 — In-memory debugger

Added `--control stdin`, `--breakpoint`, `--breakpoints-file`, `--break` and `--step`, a `debug` callback event, and `pkg/engine/debug.go` (ADR-0005, plan 001). See [task](../tasks/done/breakpoint-engine.md).

## 2026-10-05 — Step hashes and rewind

Step rows store a hash of their definition; resume reports edited completed steps, and `resume --rewind` / `--rewind-changed` re-run from a step (sub-workflow edits rewind to the call). See [task](../tasks/done/step-definition-hash-and-rewind.md).

## 2026-10-05 — markov schema

`markov schema` describes the file, workflow and step fields and every built-in step type with its parameters; a test keeps the table in step with the reference docs. See [task](../tasks/done/markov-schema.md).

## 2026-10-11 — Orchestration reliability proposals

Moved the six architecture/language proposals from Breadboard's
strategy evaluation into [plan 002](../plans/002-durable-task-orchestration.md),
and added it to the Markov plan index. Priorities are durable external
task ownership/reconciliation and engine-side output contracts, followed
by explicit retry policy, durable events, shared budgets and evidence
preservation. Proposals only; no engine changes, commit or deployment.

## 2026-10-23 — OpenShell phase 1

Plan 004 phase 1 done: six `openshell_*` step types over the CLI transport, verified against a
local OpenShell 0.1.5 gateway on the Podman driver (setup in notes/openshell-setup.md). Next:
phase 2, the Go SDK transport, running the same `TestE2E` conformance checks on both
transports. Uncommitted at the end of the session.

## 2026-10-23 — OpenShell phase 2

Plan 004 phase 2 done: the `sdk` transport (Go SDK pinned to the v0.1.5 commit; go.mod now Go
1.25.13). TestE2E runs identical checks on both transports. Found that OpenShell 0.1.5's exec
timeout and a killed one-shot exec both leave the command running; both transports now stop
commands by ending a streaming exec. Phase 3 (IDE) is next.

## 2026-10-26 — Reloads on request

The debugger's `request_reload` command (docs/reference/debugging.md#reloading-on-request) lets a
client reload a running workflow that has no `reload_workflow` step: the candidate is loaded and
checked at once (it must keep the run's workflow) and answered with `reload_queued`,
`reload_unchanged` or `reload_failed`; it is swapped in just before the next sub-workflow call,
through the same swap, record and `workflow_reloaded` event as the step (`swapDefinition`). The
IDE's Run → Reload Running Workflow uses it.

## 2026-11-17 — Concurrent SQLite checkpoint source fix

Implemented and verified in isolated `fix/sqlite-concurrent-state` worktree.
Bounded busy handler on every connection, atomic startup migration, subprocess
regressions, bounded-timeout/reconnection checks and state-store docs. Full
suite, lint, state race tests and build passed; original-source negative control
failed as expected. [Task](../tasks/done/sqlite-concurrent-state.md).
Production keeps PostgreSQL; no deployment, release, push or signing performed.

## 2026-11-17 — v0.2.1 unsigned snapshot preparation

Human returned packaging to Actions plus manual p15 signing/promotion; no new
markovd work. Actions run 44015543510 from merged fcb6ce0 passed candidate and
native Arm jobs, producing 0.2.1-dev. Artifact 14299211967 safely downloaded and
checksummed. 11 serial native upgrade/install/removal rows, apt/RPM/pacman
signature/hash negatives, 6 SBOMs/86 source-license records, both OCI runtime
platforms and exact image/binary identities passed. Eight concurrent candidate
CLI processes shared a startup-locked SQLite file and all checkpoints completed.
66 repository-tool and 5 digest-helper tests passed. All persistent outputs use
home disk; no real-key use, tag, release/registry/site publication or VM changes.
[Review](release-v0.2.1-review.md), [receipt](release-v0.2.1-review.json).
Next explicit gate is v0.2.1 tag at fcb6ce0 and clean-tag CI; snapshot bytes cannot
become a production release. Fresh Omarchy VM validation is not claimed.

## 2026-11-17 — Approved v0.2.1 tag and stable unsigned preparation

Human explicitly approved v0.2.1 at reviewed fcb6ce0. Annotated tag d25141e
pushed and peeled remotely to fcb6ce0; real signing key not used for Git tag.
Clean-tag Actions run 44109044662 passed candidate and native Arm jobs. Artifact
14340017702 safely downloaded; 29-file inventory/checksums, 6 SBOMs, 86 license
records, OCI index/platform hashes verified. Host-only prepare retained all CI
bytes in a private vault and captured RPM header requests without signing.
Debian/Fedora/Arch stable old-to-new fixture checks and both final OCI identity/
persisted-workspace checks passed. [Stable review](release-v0.2.1-stable.md).
No real-key use, Release, GHCR, native public repo or site publication. Next
gate is real-key signing approval with backup-custody revalidation before use.

## 2026-11-17 — Authorized v0.2.1 signing completed

Human approved signing. Mounted m720q encrypted backup hash matched prior
restored/private-sign-tested receipt; independent tag/CI/license/candidate checks
repeated before private approval JSON/key access. Wayland Qt unlock and signing
succeeded without reading stored passphrase, copying keys or changing identity.
Public-only checksums/manifest, both RPM signatures and exact signed Debian/
Fedora/Arch upgrades passed. Local package/site plans ready; site draft commit
982caac. [Signed review](release-v0.2.1-signed.md). No publication authorized.

## 2026-11-17 — Approved v0.2.1 publication complete

Native repos, GHCR version/latest, Release 448891576 and one-file install page
published after human approval. 214 native/history paths verified, 11 real public
old-to-new upgrades passed, both anonymously downloaded image platforms passed,
26 anonymous Release downloads/signature verified. All unrelated site/config/
authorized_keys hashes preserved. No key transfer, credential persistence,
server-config/DNS/daemon changes or fresh VM. [Closeout](release-v0.2.1-live.md).
Source review branches remain local; publication approval listed services/content,
not additional source branch pushes/PR merges. Phase 2/admission remain pending.


## 2026-11-17 — CC-71 source closeout and phase-2 plan

Prepared plan 013 on an isolated documentation branch after the approved v0.2.1/source closeout. Proposed RPM reuse across EL/openSUSE before two new APK artifacts; recorded CPU, lifecycle and native signing gates. Implementation, new keys and new outward publication await review. Site source closeout PR #6 merged as cdfe5365a1ed975189461ef1f60a0f49f821efb8. Engine source closeout PR #6 merged as 1ca19f2d8c9541c439192fc0275569508c45bd72 after test, unsigned candidate and native-arm64 checks all passed. No unrelated work changed.


## 2026-11-17 — RHEL via UBI added to phase-2 plan

Human requested RHEL 9 and 10 using free UBI images. Added four x86_64/aarch64 rows, exact proposed rhel/9 and rhel/10 roots, subscription-free validation and explicit UBI-versus-full-host evidence limits. Phase 2 now proposes 18 additional container rows (29 total), with the same seven native artifacts after Alpine. Planning only; no images pulled or packages published.
