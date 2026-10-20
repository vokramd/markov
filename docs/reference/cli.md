# CLI Reference

Markov is a YAML workflow engine for Kubernetes. All commands are invoked through the `markov` binary.

```
markov <command> [flags]
```

---

## Commands

### markov run

Run a workflow from a YAML file or workflow directory.

```
markov run <file.yaml|directory> [flags]
```

**Arguments:**

| Argument | Description |
|----------|-------------|
| `file.yaml|directory` | Path to the workflow file or directory to execute. Required. |

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--var key=value` | string (repeatable) | -- | Override workflow variables. Can be specified multiple times. Values are parsed as strings. |
| `--workflow name` | string | -- | Run a specific workflow by its `name:` field instead of the file's `entrypoint`. |
| `--forks N` | int | `0` (use file default) | Override the global `forks` concurrency limit. A value of 0 defers to the workflow file setting (which defaults to 5). |
| `--namespace ns` | string | -- | Override the Kubernetes namespace for `k8s_job` steps. |
| `--kubeconfig path` | string | -- | Path to a kubeconfig file for Kubernetes client configuration. |
| `--state-store path-or-dsn` | string | `MARKOV_STATE_STORE`, `/tmp/markov-state.db`, or `./markov-state.db` | SQLite path or Postgres DSN. Defaults to `MARKOV_STATE_STORE` when set, then `/tmp/markov-state.db` in-cluster, otherwise `./markov-state.db`. |
| `--verbose` | bool | `false` | Show detailed execution output including parameter values and template rendering results. |
| `--debug` | bool | `false` | Show debug logging for flag parsing, callback setup, and K8s client initialization. Implies `--verbose`. |
| `--run-id id` | string | -- | Use a specific run ID instead of generating a UUID. Useful for deterministic run tracking. |
| `--callback url` | string (repeatable) | -- | Callback destination URL for execution events. Can be specified multiple times. See [Callbacks](#callbacks) for supported schemes. |
| `--callback-header key=value` | string (repeatable) | -- | Extra HTTP headers to include with HTTP/HTTPS callback requests. Can be specified multiple times. |
| `--callback-tls-insecure` | bool | `false` | Skip TLS certificate verification for callback connections. |
| `--callback-tls-cert path` | string | -- | Path to a client TLS certificate for callback connections. |
| `--callback-buffer-size N` | int | `1000` | Async send buffer size for callback dispatching. |
| `--control stdin` | string | -- | Read [debugger commands](debugging.md) as JSON lines from standard input. Required for breakpoints and `--step`. |
| `--breakpoint json` | string (repeatable) | -- | Breakpoint object, e.g. `{"workflow":"main","step":"build"}`. Resolved against the workflow before the run starts. |
| `--breakpoints-file path` | string | -- | JSON file with an array of breakpoint objects. |
| `--break workflow.step` | string (repeatable) | -- | Breakpoint shorthand, resolved against the real workflow and step names. |
| `--step` | bool | `false` | Pause before every step. |
| `--allow-reload` | bool | `false` | Let [`reload_workflow`](step-types.md#reload_workflow) steps replace the workflow definition while it runs. A run that can reach such a step (through sub-workflow calls) does not start without it. A run that reloads cannot be resumed. |

**Examples:**

```bash
# Run a workflow file
markov run pipeline.yaml

# Run a directory workflow
markov run examples/dir-based-hello-world

# Override variables
markov run pipeline.yaml --var environment=production --var dry_run=true

# Run a specific workflow instead of the entrypoint
markov run pipeline.yaml --workflow deploy-target

# Run a specific workflow from a directory workflow
markov run examples/dir-based-hello-world --workflow process-item

# Override concurrency and namespace
markov run pipeline.yaml --forks 10 --namespace staging

# Run with callbacks and verbose output
markov run pipeline.yaml \
  --verbose \
  --callback jsonl:///var/log/markov/events.jsonl \
  --callback https://hooks.example.com/markov \
  --callback-header "Authorization=Bearer tok123"

# Use a specific run ID
markov run pipeline.yaml --run-id my-pipeline-run-001
```

---

### markov resume

Resume a previously failed or paused workflow run from the point of failure. The workflow file path is stored in the state database, so it does not need to be provided again. A paused run requires at least one explicit `--var` override, which is applied before its paused gate is evaluated again.

For operational guidance, prerequisites, and caveats, see [Resuming Workflows](../guides/resuming-workflows.md).

```
markov resume <run_id> [flags]
```

**Arguments:**

| Argument | Description |
|----------|-------------|
| `run_id` | The run ID to resume. Required. |

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--state-store path-or-dsn` | string | `MARKOV_STATE_STORE`, `/tmp/markov-state.db`, or `./markov-state.db` | SQLite path or Postgres DSN. |
| `--var key=value` | string (repeatable) | -- | Override a variable before resuming. Required for paused runs. Values are parsed with the same coercion as `markov run --var`. |
| `--source-integrity mode` | `warn`, `strict`, or `off` | `warn` | How to handle a workflow source-tree digest mismatch. `warn` resumes and records drift; `strict` rejects the resume; `off` skips the comparison. |
| `--rewind json` | string (repeatable) | -- | Re-run from a step of the entrypoint workflow, e.g. `{"workflow":"main","step":"build"}`. That step and every later one run again; the earliest target wins. A completed run can be rewound. See [Edited steps and rewinding](state-store.md#edited-steps-and-rewinding). |
| `--rewind-changed` | bool | `false` | Re-run from the earliest completed step whose definition changed since it ran. |
| `--namespace`, `--kubeconfig`, `--forks`, `--verbose`, `--debug` | | | The same execution settings as `markov run`. They aren't stored with the run, so pass the original run's `--namespace` or `--forks` again if it used them. |
| `--callback`, `--callback-header`, `--callback-tls-insecure`, `--callback-tls-cert`, `--callback-buffer-size` | | | The same event callbacks as `markov run`; the resumed run emits `run_resumed` first. |
| `--control`, `--breakpoint`, `--breakpoints-file`, `--break`, `--step` | | | The same [debugger](debugging.md) flags as `markov run`, so a resumed run can be stepped through. |
| `--allow-reload` | bool | `false` | As for `markov run`. A run that has already reloaded is never resumable. |

**Examples:**

```bash
# Resume a failed run
markov resume abc123

# Supply an explicit approval input to resume a paused run
markov resume abc123 --var approved=true

# Resume using a specific state store
markov resume abc123 --state-store /data/markov.db

# Require the original workflow directory and scripts to be unchanged
markov resume abc123 --source-integrity strict

# Re-run from the first step you edited since the run
markov resume abc123 --rewind-changed

# Re-run from a specific step
markov resume abc123 --rewind '{"workflow":"main","step":"deploy"}'
```

---

### markov schema

Print the workflow format as JSON: the fields of the file, workflow and step, and every built-in step type with its parameters (and, for types such as `set_fact` and `gate` whose inputs are step-level fields, which fields). Editors and agents use it instead of keeping their own lists.

```
markov schema
```

```json
{ "version": 1, "file": [{"name": "entrypoint", "type": "string"}, ...],
  "step_types": [{"name": "shell_exec", "inputs": "params", "params": [{"name": "command", "required": true}], ...},
                 {"name": "set_fact", "inputs": "step", "step_fields": ["vars"], ...}] }
```

The parameter table is checked against [Step Types](step-types.md) by a test; custom `step_types` defined in a workflow are not included.

---

### markov status

Display the status of a workflow run.

```
markov status <run_id> [flags]
```

**Arguments:**

| Argument | Description |
|----------|-------------|
| `run_id` | The run ID to inspect. Required. |

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--state-store path-or-dsn` | string | `MARKOV_STATE_STORE`, `/tmp/markov-state.db`, or `./markov-state.db` | SQLite path or Postgres DSN. |
| `--steps` | bool | `false` | Show a per-step status table with individual step names, statuses, durations, and errors. |

**Output:**

```
Run:        abc123
Workflow:   main
Status:     completed
Source:     3be8b1...
Started:    2026-05-05 10:30:00
Completed:  2026-05-05 10:32:15
```

When a `warn` resume accepted source drift, status also shows the latest source
check and its observed digest.

With `--steps`:

```
Run:        abc123
Workflow:   main
Status:     completed
Started:    2026-05-05 10:30:00
Completed:  2026-05-05 10:32:15

STEP                           STATUS       DURATION
----                           ------       --------
analyze                        completed    1.2s
deploy-gate                    completed    0.1s
deploy-all                     completed    45.3s
notify                         completed    0.5s
```

**Examples:**

```bash
# Show run summary
markov status abc123

# Show run summary with per-step breakdown
markov status abc123 --steps
```

---

### markov list

List all workflow runs recorded in the state store.

```
markov list [flags]
```

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--state-store path-or-dsn` | string | `MARKOV_STATE_STORE`, `/tmp/markov-state.db`, or `./markov-state.db` | SQLite path or Postgres DSN. |

**Output:**

```
RUN ID     WORKFLOW                  STATUS       STARTED              DURATION
------     --------                  ------       -------              --------
abc123     main                      completed    2026-05-05 10:30:00  2m15s
def456     main                      failed       2026-05-05 09:15:00  1m3s
ghi789     deploy-target             completed    2026-05-04 16:00:00  45.3s
```

**Examples:**

```bash
# List all runs
markov list

# List runs from a specific state store
markov list --state-store /data/markov.db
```

---

### markov validate

Validate a workflow file or workflow directory without executing it. Checks all structural and referential integrity rules.

```
markov validate <file.yaml|directory>
```

**Arguments:**

| Argument | Description |
|----------|-------------|
| `file.yaml|directory` | Path to the workflow file or directory to validate. Required. |

**No additional flags.**

On success, prints `valid` and exits with code 0. On failure, prints the validation error and exits with code 1.

**Examples:**

```bash
# Validate a workflow file
markov validate pipeline.yaml

# Validate a directory workflow
markov validate examples/dir-based-hello-world

# Use in CI scripts
if markov validate pipeline.yaml; then
  echo "Workflow file is valid"
else
  echo "Validation failed"
  exit 1
fi
```

---

### markov diagram

Generate a Mermaid flowchart diagram from a completed (or failed) workflow run. The diagram is written to stdout.

```
markov diagram <run_id> [flags]
```

**Arguments:**

| Argument | Description |
|----------|-------------|
| `run_id` | The run ID to generate a diagram for. Required. |

**Flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--state-store path-or-dsn` | string | `MARKOV_STATE_STORE`, `/tmp/markov-state.db`, or `./markov-state.db` | SQLite path or Postgres DSN. |

**Examples:**

```bash
# Print Mermaid diagram to stdout
markov diagram abc123

# Save to a file
markov diagram abc123 > run-abc123.mmd

# Render to an image (requires mmdc / mermaid-cli)
markov diagram abc123 | mmdc -i - -o diagram.png
```

---

## Callbacks

The `--callback` flag accepts URLs with the following schemes:

| Scheme | Description |
|--------|-------------|
| `jsonl://` | Append events as JSON lines to a local file. Example: `jsonl:///var/log/markov/events.jsonl` |
| `http://` | POST events to an HTTP endpoint. |
| `https://` | POST events to an HTTPS endpoint. |
| `grpc://` | Stream events to a gRPC endpoint. |
| `grpcs://` | Stream events to a gRPC endpoint with TLS. |

Multiple callbacks can be specified by repeating the `--callback` flag. All callbacks receive the same events.

Use `--callback-header` to add HTTP headers (e.g., authentication tokens) to HTTP/HTTPS callbacks. Use `--callback-tls-insecure` and `--callback-tls-cert` to control TLS behavior for HTTPS and gRPCS callbacks.

---

## Kubernetes Client Resolution

Markov resolves the Kubernetes client configuration in the following order. The first successful method is used.

| Priority | Method | Description |
|----------|--------|-------------|
| 1 | In-cluster config | Uses the ServiceAccount token at `/var/run/secrets/kubernetes.io/serviceaccount/token`. Automatically available when running inside a Kubernetes pod. |
| 2 | `--kubeconfig` flag | Explicit path to a kubeconfig file passed via the CLI flag. |
| 3 | `KUBECONFIG` env var | Path to a kubeconfig file set via the environment variable. |
| 4 | Default loading rules | Standard kubeconfig resolution (`~/.kube/config` and other default locations). |

If no Kubernetes client can be configured, a warning is logged and `k8s_job` or `k8s_job_wait` steps will fail at runtime.

---

## Namespace Resolution

The Kubernetes namespace for `k8s_job` steps is resolved in the following order. The first non-empty value is used.

| Priority | Source | Description |
|----------|--------|-------------|
| 1 | Workflow file `namespace` field | The `namespace` key in the workflow YAML. |
| 2 | `--namespace` flag | CLI flag override. |
| 3 | ServiceAccount namespace | Read from `/var/run/secrets/kubernetes.io/serviceaccount/namespace` when running in-cluster. |
| 4 | `"default"` | Falls back to the `default` namespace. |

---

## State Store

Markov persists run state in SQLite by default. The default path depends on the runtime environment:

| Priority | Source | Description |
|----------|--------|-------------|
| 1 | `--state-store path-or-dsn` | Explicit CLI flag on stateful commands. |
| 2 | `MARKOV_STATE_STORE` | Environment variable containing a SQLite path or Postgres DSN. |
| 3 | In-cluster default | `/tmp/markov-state.db` when a ServiceAccount token exists at `/var/run/secrets/kubernetes.io/serviceaccount/token`. |
| 4 | Local default | `./markov-state.db`. |

Override with `--state-store` on any command that uses state (`run`, `resume`, `status`, `list`, `diagram`). A filesystem path selects SQLite. A `postgres://` or `postgresql://` DSN selects Postgres.

---

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success. Workflow completed, file is valid, or command ran without error. |
| `1` | Error. Workflow failed, validation error, invalid arguments, or any other failure. |
