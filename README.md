<img width="1173" height="382" alt="markov-logo" src="https://github.com/user-attachments/assets/18feb654-2d5c-47fc-993c-cc95afa2cbf7" />


# Markov

A Go workflow engine for chaining agents with code. Declarative YAML workflows mix agent steps
(Claude Code), scripts, HTTP APIs and Kubernetes Jobs, with explicit state, rules, loops and
checkpoint/resume. [Codify](docs/reference/cli.md#markov-codify) then progressively moves the work
that needs no judgment out of agent steps into native ones, one validated change at a time, and
leaves the judgment with an agent.

## Why "Markov"?

A [Markov chain](https://en.wikipedia.org/wiki/Markov_chain) is a system that transitions between states based on its current state. Markov workflows work the same way: gate steps evaluate rules against the current state, decide whether to continue or pause for explicit external input, and workflows can loop or recursively invoke themselves until the rules say to stop. The result is a declarative state machine where each iteration's next move depends only on the facts right now, not the history of how it got there.

## Features

**Steps**
- **Agent steps** — [`claude`](docs/reference/step-types.md#claude) runs a prompt or skill through the Claude Code CLI, with live transcripts, limits on turns, tokens, time and spend, and optional JSON-schema answers; [`codex`](docs/reference/step-types.md#codex) does the same through the OpenAI Codex CLI
- **Native steps** — `shell_exec`, `script_exec`, `write_file`, `http_request` (pagination, streamed SSE responses), `k8s_job`/`k8s_job_wait` (`batch/v1` Jobs directly, no Argo), `ansible`/`ansible_playbook`, `prompt` (interactive input), and [OpenShell sandboxes](docs/reference/step-types.md#openshell-sandboxes)
- **User-defined step types** — compose reusable types from the primitives
- **Data** — `set_fact`, `assert`, and [`load_artifact`](docs/reference/artifacts.md) for YAML, markdown and markdown tables from local files or K8s pods
- **Jev decisions** — native Jev/Kev API steps, durable inline conditions, and decision-backed gate facts; see the [Jev reference](docs/reference/jev.md)

**Control flow**
- **Conditionals** — `when:` expressions
- **Fan-out / fan-in** — [`for_each`](docs/reference/for-each.md) with sliding-window concurrency (`forks`) and stable keys for resumable fan-outs
- **Loops** — [`repeat`](docs/reference/repeat.md): bounded do-while loops over a sub-workflow (`until`, `max`); workflows can also call themselves
- **Rules and gates** — named rules with salience-based priority ([Grule](https://github.com/hyperjumptech/grule-rule-engine)); gate steps set facts and can pause durably for explicit resume input
- **Failure handling** — `ignore_errors`, `failed_when`, and workflow-level `rescue` and `always` handlers, which also run after a cancelled run

**State and visibility**
- **Checkpoint/resume** — SQLite or Postgres state store; resume failed or paused runs from the last completed step
- **Debugger** — breakpoints (on steps and on loop iterations), stepping and expression evaluation over a JSON-lines [control channel](docs/reference/debugging.md)
- **Callbacks** — [events](docs/reference/callbacks.md) to JSONL files, a live terminal view (`tty://`), HTTP or gRPC

**Evolving workflows**
- **Hot reload** — [`reload_workflow`](docs/reference/step-types.md#reload_workflow) steps, or a request over the control channel, swap the definition of a running workflow; new sub-workflow calls use it (explicit and opt-in with `--allow-reload`)
- **Codify** — record a live run, let a retro agent move one job into native steps in a copy, replay the recording against it, and promote it when its outputs still match; strict and loose modes, agent steps you keep, iteration and spend limits

**Safety and correctness**
- **Variable scoping** — file-level vars are read-only globals, and a called workflow sees only what it declares and is passed
- **`markov validate`** — checks structure, the variables each workflow can see, and that every template and expression parses
- **Secrets** — secret vars and step outputs are masked in events and in stored state
- **Output limits** — steps stop reading output past a size limit (default 64MB)

**Layout**
- **Single-file or [directory workflows](docs/reference/workflow-file.md#directory-workflow-layout)** — one file per workflow, in subfolders named by their path (`workflows/tools/grep.yaml` is `tools.grep`)
- **Templates** — Jinja2-compatible ([pongo2](docs/reference/template-engine.md)) for params and expressions

## Built with Markov

- [markovd](https://github.com/vokramd/markovd) — web dashboard and HTTP API: trigger and schedule runs, and follow them live
- [markov-harness](https://github.com/vokramd/markov-harness) — a minimal Claude Code-style coding agent written entirely as a Markov workflow: streaming replies, tools with approvals, memory and prompt caching

## Documentation

Full documentation is available at [docs/](docs/), including:

- [Getting Started](docs/getting-started.md) — install and run your first workflow
- [Language Reference](docs/reference/workflow-file.md) — complete YAML DSL schema
- [Step Types](docs/reference/step-types.md) — all built-in step types
- [Guides](docs/guides/writing-workflows.md) — practical how-to guides

## How is this different from X?

See [docs/design/project-comparisons.md](docs/design/project-comparisons.md) for detailed comparisons with Ansible, Argo Workflows, Jenkins, Tekton, Kestra, CrewAI, and LangGraph.

## Install

Download a binary for Linux or macOS (amd64, arm64) from the [releases](https://github.com/vokramd/markov/releases),
unpack it and put `markov` on your `PATH`:

```bash
tar -xzf markov_<version>_<os>_<arch>.tar.gz markov && sudo mv markov /usr/local/bin/
```

Or build it with Go 1.25 or later:

```bash
go install github.com/vokramd/markov/cmd/markov@latest   # or, from a clone: make build (bin/markov)
```

## Quick Start

Nothing but `markov` is needed: no cluster, no agent.

```bash
git clone https://github.com/vokramd/markov && cd markov

markov validate examples/quickstart.yaml
markov run examples/quickstart.yaml --callback tty://   # fan-out, a loop, a branch, a report
cat quickstart-report.md
markov status <run-id> --steps                          # what ran, and how long each step took
```

[`examples/quickstart.yaml`](examples/quickstart.yaml) checks three services at once (`for_each`), repeats a
sub-workflow until it reports the cache is warm (`repeat`), runs a step only if it did (`when`), and writes a
report (`write_file`). Next:

- **An agent step:** [`examples/claude.yaml`](examples/claude.yaml) asks Claude a question with enforced limits
  (needs the [`claude` CLI](https://github.com/anthropics/claude-code)); [`examples/codex.yaml`](examples/codex.yaml) does
  the same with the [`codex` CLI](https://github.com/openai/codex).
- **Codify:** [`markov codify`](docs/reference/cli.md#markov-codify) moves the work an agent step does that needs no
  judgment into native steps.
- **Getting started:** [docs/getting-started.md](docs/getting-started.md).

Resume a failed or paused run with `markov resume <run-id>`, and draw a finished one with `markov diagram <run-id>`
(Mermaid).

## On Kubernetes

`k8s_job` steps create `batch/v1` Jobs directly, in the namespace you give:

```bash
markov run examples/k8s-job-test.yaml --namespace markov-test --verbose
```

### Example

```yaml
entrypoint: main
namespace: markov-test
forks: 2

vars:
  greeting: "hello from markov"
  items: ["alpha", "bravo", "charlie"]

step_types:
  echo_job:
    base: k8s_job
    job:
      image: alpine:3.19
      command: ["/bin/sh", "-c"]

workflows:
  - name: main
    steps:
      - name: hello
        type: echo_job
        params:
          args: ["echo '{{ greeting }}'"]

      - name: fan-out
        for_each: "items"
        as: item
        workflow: per-item
        vars:
          value: "{{ item }}"

  - name: per-item
    vars:
      value: null
    steps:
      - name: process
        type: echo_job
        params:
          args: ["echo 'processing: {{ value }}'"]
```

## CLI

| Command | |
|---------|--|
| `markov run <file\|dir>` | Run a workflow |
| `markov resume <run-id>` | Resume a failed or paused run |
| `markov validate <file\|dir>` | Check a workflow without running it |
| `markov codify <dir>` | Progressively move agent work into native steps |
| `markov status <run-id>`, `markov list` | Inspect runs |
| `markov diagram <run-id>` | Mermaid diagram of a run |
| `markov schema` | The workflow format as JSON, for editors and agents |

Common `run` flags: `--var key=value`, `--workflow name`, `--forks N`, `--state-store path-or-dsn`,
`--callback url`, `--control stdin`, `--break workflow.step`, `--allow-reload`, `--verbose`. See the
[CLI reference](docs/reference/cli.md) for all of them.

## Project Structure

```
cmd/markov/          CLI entrypoint
pkg/engine/          Workflow execution, gates, loops, debugger, reload, secrets
pkg/parser/          YAML parsing, validation, lint
pkg/executor/        Step executors (claude, codex, shell_exec, script_exec, http_request, k8s_job, ...)
pkg/codify/          markov codify: recording proxy, retro agent, replay, judge
pkg/state/           Checkpoint store (SQLite, Postgres)
pkg/callback/        Event callbacks (jsonl, tty, http, grpc)
pkg/template/        Pongo2 template rendering and filters
pkg/jev/             Jev/Kev decision API client
pkg/openshell/       OpenShell sandbox steps (CLI and SDK transports)
examples/            Example workflow files
docs/                Documentation (reference, guides, design docs)
```

## License

[Apache License 2.0](LICENSE)
