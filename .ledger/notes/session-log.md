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
