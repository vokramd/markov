{% raw %}
# Getting Started

Get from zero to a running workflow in under 5 minutes.

## Prerequisites

- **Nothing**, with a release binary. **Go 1.25+** to build from source.
- **The [`claude` CLI](https://github.com/anthropics/claude-code)** (optional) — only needed for `claude` steps
- **A Kubernetes cluster** (optional) — only needed for `k8s_job` steps

## Installation

Download a binary for Linux or macOS (amd64, arm64) from the
[releases](https://github.com/vokramd/markov/releases), unpack it and put `markov` on your `PATH`. Or build it:

```bash
git clone https://github.com/vokramd/markov.git
cd markov
make build            # bin/markov; or: go install github.com/vokramd/markov/cmd/markov@latest
```

`markov --version` prints the version.

## Your First Workflow

Create a file called `hello.yaml`:

```yaml
entrypoint: hello

vars:
  greeting: "hello from markov"

workflows:
  - name: hello
    steps:
      - name: say-hello
        type: shell_exec
        params:
          command: "echo '{{ greeting }}'"
        register: hello_result

      - name: show-date
        type: shell_exec
        params:
          command: "date +%Y-%m-%d"
        register: date_result

      - name: combine
        type: shell_exec
        params:
          command: "echo 'Message: {{ hello_result.stdout }} on {{ date_result.stdout }}'"
```

## Validate It

```bash
markov validate hello.yaml
# Output: valid
```

## Run It

```bash
markov run hello.yaml --verbose
```

You'll see each step execute in order, with the `--verbose` flag showing parameter values and registered outputs.

## Check the Result

```bash
markov status <run-id> --steps
```

This shows the status and duration of each step.

If a run fails, fix the cause and resume it from the same state store:

```bash
markov resume <run-id>
```

See [Resuming Workflows](guides/resuming-workflows.md) for the full recovery workflow and caveats.

## Override Variables

```bash
markov run hello.yaml --var greeting="hey there"
```

CLI `--var` flags override the values defined in the workflow file.

## Try Fan-Out

Create `fanout.yaml`:

```yaml
entrypoint: main

forks: 3

vars:
  items: ["alpha", "bravo", "charlie", "delta", "echo"]

workflows:
  - name: main
    steps:
      - name: process-items
        type: shell_exec
        for_each: "items"
        as: item
        params:
          command: "echo 'processing {{ item }}'"
        register: results

      - name: summary
        type: shell_exec
        params:
          command: "echo 'processed {{ results | length }} items'"
```

```bash
markov run fanout.yaml --verbose
```

Items process 3 at a time (controlled by `forks: 3`).

## Try a Directory Workflow

Markov can also run a workflow directory. The directory splits the same workflow schema across required files:

```text
examples/dir-based-hello-world/
  meta.yaml
  vars.yaml
  rules.yaml
  step_types.yaml
  workflows/
    main.yaml
```

Custom step types can also live in `step_types/*.yaml` files when you want to split them by category.

Validate and run it:

```bash
markov validate examples/dir-based-hello-world
markov run examples/dir-based-hello-world --verbose
```

## What's Next

- [Core Concepts](concepts.md) — understand the execution model
- [Writing Workflows](guides/writing-workflows.md) — step-by-step workflow building guide
- [Language Reference](reference/workflow-file.md) — complete YAML schema reference
- [Step Types](reference/step-types.md) — all built-in step types
{% endraw %}
