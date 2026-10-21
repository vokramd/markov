# Project Plan

## Current Milestone

- [M1 Directory Workflow Files](milestones/M1-directory-workflow-files.md)

## Plans

- [Directory Workflow Files Plan](plans/000-directory-workflow-files.md)
- [Step-Through Debugging and Interactive Workflow Building](plans/001-step-through-debugging.md)
- [Durable External Tasks and Orchestration Reliability](plans/002-durable-task-orchestration.md) — proposed; implementation not started.
- [Agentic Codification](plans/003-agentic-codification.md) — phases 1–5 done (notes/codify-phase1-run.md, ADR-0006, markov-ide plan 002); phase 6 (`markov codify`, any workflow) done; retro agent decomposes a single-prompt workflow into native steps, with `reload_workflow` hot-reload.

## Active Tasks

- None

## Pending Tasks

- [Step source positions in events and errors](tasks/pending/step-source-positions.md)

## Completed Tasks

- [markov codify (codify any workflow by default)](tasks/done/markov-codify-command.md) (plan 003 phase 6)
- [Codification loop in one markov process](tasks/done/codify-in-process-loop.md) (plan 003 phase 4)
- [Event-log and current-run variables](tasks/done/event-log-variables.md) (plan 003 phase 3)
- [reload_workflow step and hot reload](tasks/done/reload-workflow-step.md) (plan 003 phase 2)
- [Promotion, version archive and metrics loop](tasks/done/codify-promote-metrics.md) (plan 003 phase 1)
- [Retro agent step and run evidence](tasks/done/codify-retro-step.md) (plan 003 phase 1)
- [Codification fixture recording and candidate validation](tasks/done/codify-fixture-validator.md) (plan 003 phase 1)
- [Codification demo scaffold and v0 task](tasks/done/codify-hn-scaffold.md) (plan 003 phase 1)
- [Templated sub-workflow names, write_file, markov_run_id](tasks/done/templated-workflow-and-write-file.md)
- [failed_when, flatten/pluck, script stdin, chained subscripts](tasks/done/failed-when-flatten-stdin-subscripts.md)
- [Language additions for data-driven benchmark workflows](tasks/done/workflow-language-flow-controls.md)
- [First-class Jev support](tasks/done/jev-support.md)
- [Make all current examples validate](tasks/done/validate-all-examples.md)
- [Add strict YAML validation](tasks/done/strict-yaml-validation.md)
- [Implement source integrity modes](tasks/done/source-integrity-modes.md)
- [Add k8s_job_wait step type](tasks/done/k8s-job-wait-step-type.md)
- [Add Postgres state store support](tasks/done/postgres-state-store.md)
- [Add claude step type](tasks/done/claude-step-type.md)
- [Breakpoints, step mode and control channel](tasks/done/breakpoint-engine.md)
- [Step definition hash and rewind on resume](tasks/done/step-definition-hash-and-rewind.md)
- [markov schema](tasks/done/markov-schema.md)
- [Add ansible and ansible_playbook step types](tasks/done/ansible-step-types.md)
- [Define directory workflow schema](tasks/done/directory-workflow-schema.md)
- [Implement directory loader and merge validation](tasks/done/directory-loader-merge-validation.md)
- [Wire directory input into CLI commands](tasks/done/directory-cli-integration.md)
- [Add directory workflow documentation and examples](tasks/done/directory-docs-and-examples.md)

## Open Bugs

- [Registered sub-workflow and for_each results carry the caller's whole context](bugs/open/register-copies-whole-context.md)

## Fixed Bugs

- [set_fact facts see siblings in random order](bugs/fixed/set-fact-sibling-order.md)
- [JSON integers from http_request render as floats](bugs/fixed/json-integers-render-as-floats.md)
- [Direct for_each with register drops each iteration's output](bugs/fixed/direct-foreach-register-drops-output.md)
- [for_each register collects results in completion order](bugs/fixed/foreach-register-completion-order.md)
- [Direct for_each iterations collide in step state](bugs/fixed/direct-foreach-step-dedup.md)
- [Template map values render as Go strings in HTTP bodies](bugs/fixed/template-map-values-render-as-go-strings.md)
- [CLI `--var false` remains a truthy string](bugs/fixed/cli-var-false-remains-truthy-string.md)

## Decisions

- [ADR-0001: Directory Workflow File Layout](decisions/ADR-0001-directory-workflow-layout.md)
- [ADR-0002: Source Integrity Modes for Resumable Workflow Runs](decisions/ADR-0002-source-integrity-modes.md)

- [ADR-0003: Durable Jev decisions](decisions/ADR-0003-durable-jev-decisions.md)
- [ADR-0004: Native claude step with Markov-enforced limits](decisions/ADR-0004-claude-step-type.md)
- [ADR-0005: In-memory debugger with a stdin control protocol](decisions/ADR-0005-in-memory-debugger-and-control-protocol.md)
- [ADR-0006: Workflow hot reload with an explicit reload_workflow step](decisions/ADR-0006-workflow-hot-reload.md)
