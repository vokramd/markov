# State Store

Markov stores checkpoint/resume data in a state backend. SQLite is the default for local use and simple deployments. Postgres is available for orchestrators and concurrent Kubernetes runs that need durable shared state.

For practical recovery steps and operational caveats, see [Resuming Workflows](../guides/resuming-workflows.md). For production Postgres setup, see [Using Postgres for Markov State](../guides/postgres-state-store.md).

## Backend selection

The state store value selects the backend:

| Value | Backend |
|-------|---------|
| `./markov-state.db` | SQLite |
| `/path/to/markov-state.db` | SQLite |
| `postgres://user:pass@host:5432/db?sslmode=disable` | Postgres |
| `postgresql://user:pass@host:5432/db?sslmode=disable` | Postgres |

SQLite uses pure Go SQLite via `modernc.org/sqlite` and does not require CGO. Postgres uses a native Go driver and creates Markov-owned checkpoint tables in the selected database.

## Default location and precedence

Markov chooses the state store in this order:

| Priority | Source | Description |
|----------|--------|-------------|
| 1 | `--state-store <path-or-dsn>` | Explicit CLI flag on stateful commands. |
| 2 | `MARKOV_STATE_STORE` | Environment variable containing a SQLite path or Postgres DSN. |
| 3 | In-cluster default | `/tmp/markov-state.db` when a Kubernetes ServiceAccount token is mounted. |
| 4 | Local default | `./markov-state.db`. |

### Default SQLite location

| Environment | Path | Detection |
|-------------|------|-----------|
| Local | `./markov-state.db` | Default |
| In-cluster (K8s) | `/tmp/markov-state.db` | ServiceAccount token exists at `/var/run/secrets/kubernetes.io/serviceaccount/token` |
| Custom | Any path or DSN | `MARKOV_STATE_STORE` or `--state-store <path-or-dsn>` |

## Postgres usage

Use Postgres when multiple workflow pods need a shared durable checkpoint store:

```bash
export MARKOV_STATE_STORE='postgres://markov:...@postgres:5432/markov_state?sslmode=disable'

markov run /etc/markov/workflow \
  --run-id markov-run-abc123

markov resume markov-run-abc123
```

Recommended production practice:

- Pass the DSN through an environment variable sourced from a Kubernetes Secret.
- Use a dedicated database, schema, or database user for Markov checkpoint state.
- Keep Markov checkpoint tables separate from orchestrator application tables.
- Ensure the original workflow source path is mounted at the same path for resume jobs.
- Avoid putting Postgres credentials directly in shell history or logs.

## Schema

### runs table

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `run_id` | TEXT | PRIMARY KEY | Unique run identifier (UUID prefix or `{parentRunID}-{stepName}-{key}`) |
| `workflow_file` | TEXT | NOT NULL | Source YAML file path |
| `source_digest` | TEXT | NULL | SHA-256 digest of the original workflow source tree. NULL for runs created before source-integrity support. |
| `entrypoint` | TEXT | NOT NULL | Workflow name that was executed |
| `status` | TEXT | NOT NULL | `running`, `paused`, `completed`, or `failed` |
| `vars_json` | TEXT | NOT NULL, DEFAULT '{}' | Serialized run context variables |
| `parent_run_id` | TEXT | NULL | Set for sub-workflow and for_each runs |
| `parent_step` | TEXT | NULL | Step name that spawned this sub-run |
| `for_each_key` | TEXT | NULL | Iteration key for for_each sub-runs |
| `started_at` | TIMESTAMP | NOT NULL | When the run began |
| `completed_at` | TIMESTAMP | NULL | When the run finished (NULL while running or paused) |

### source_checks table

Each resume attempt writes one source-integrity check. The original digest in
`runs.source_digest` is never changed.

| Column | Type | Description |
|--------|------|-------------|
| `run_id` | TEXT | Run being resumed |
| `checked_at` | TIMESTAMP | When Markov checked the source tree |
| `mode` | TEXT | `warn`, `strict`, or `off` |
| `expected_digest` | TEXT | The original run digest, when available |
| `observed_digest` | TEXT | Digest found at resume time; empty in `off` mode |
| `source_drifted` | BOOLEAN | Whether the observed digest differs from the original digest |

### reloads table

Each [`reload_workflow`](step-types.md#reload_workflow) that replaced the definition writes one row, keyed by the top-level run. `markov resume` refuses a run that has any.

| Column | Type | Description |
|--------|------|-------------|
| `run_id` | TEXT | Top-level run |
| `step_run_id` | TEXT | The (sub-)run whose step reloaded |
| `workflow`, `step` | TEXT | The reload step |
| `source` | TEXT | Source loaded |
| `old_digest`, `new_digest` | TEXT | Source-tree digests before and after |
| `reloaded_at` | TIMESTAMP | When the definition was replaced |

### steps table

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `run_id` | TEXT | NOT NULL | Parent run identifier |
| `workflow_name` | TEXT | NOT NULL | Workflow containing this step |
| `step_name` | TEXT | NOT NULL | Step name |
| `status` | TEXT | NOT NULL | `pending`, `running`, `paused`, `completed`, `failed`, or `skipped` |
| `output_json` | TEXT | NULL | Step output as serialized JSON |
| `artifacts_json` | TEXT | NULL | Artifact data as serialized JSON |
| `error` | TEXT | NULL | Error message if step failed |
| `started_at` | TIMESTAMP | NULL | When the step began |
| `completed_at` | TIMESTAMP | NULL | When the step finished |
| `definition_hash` | TEXT | NULL | Fingerprint of the step's definition (its parsed fields and the custom step type it resolves to) when the row was saved. Empty for rows saved by older versions, which are never reported as changed. |

Primary key: `(run_id, workflow_name, step_name)`

The database is migrated in place: `definition_hash` is added to an existing `steps` table the first time a newer `markov` opens it.

## Statuses

### Run statuses

```
running --> completed
        \-> failed
        \-> paused --> running
```

### Step statuses

```
pending --> running --> completed
                    \-> failed
                    \-> skipped
                    \-> paused --> running
```

A step is `skipped` when its `when` condition evaluates to false.

## Resume flow

Resume picks up a failed or paused run and re-executes it, skipping already-completed steps. A run that [reloaded its workflow](step-types.md#reload_workflow) cannot be resumed. Paused runs require explicit variable input (`markov resume <run_id> --var key=value`). By default it records and accepts source drift; `--source-integrity strict` rejects an edited workflow source.

```
markov resume <run_id>
```

### Step-by-step resume process

1. **Load run record** from state store by `run_id`.
2. **Fingerprint the current source tree** and save a source-check record according to the selected policy.
3. **Load all steps** for the run.
4. **Replay completed step outputs** into the context:

   | Step type | Replay behavior |
   |-----------|----------------|
   | `set_fact` | Merge facts directly into context (`ctx[key] = value`) |
   | `gate` | Extract `facts` from output and merge into context |
   | Steps with artifacts | Restore full step data to `ctx[stepname]` |
   | Steps with output | Restore output map to `ctx[stepname]` |

5. **Apply resume overrides** to the context (required for paused runs), then mark the run as `running` again.
6. **Fire `run_resumed` event** with completed/remaining step counts, source-integrity information, the completed steps whose definition changed (`changed_steps`), and whether the run was rewound (`rewound`).
7. **Continue execution** -- `executeWorkflow` runs all steps in order; `executeStep` skips already-completed steps.

### Context replay example

If a workflow ran steps A (set_fact), B (shell_exec), and C (failed):

```
Resume:
  1. Replay A's facts into ctx      --> ctx["region"] = "us-east-1"
  2. Replay B's output into ctx     --> ctx["B"] = {"stdout": "...", "rc": 0}
  3. Execute step A -> skip (completed)
  4. Execute step B -> skip (completed)
  5. Execute step C -> run (was failed)
```

## Step deduplication

`executeStep` checks if a step is already completed before running it:

1. Query state store for `(run_id, workflow_name, step_name)`.
2. If status is `completed`:
   - Replay output into context (same rules as resume replay).
   - Log skip and return nil.
3. Otherwise, execute the step normally.

This makes `executeWorkflow` safe to call on a partially-completed workflow -- it simply fast-forwards through completed steps.

`rescue` and `always` lifecycle handlers use state names prefixed with their section, such as `rescue/report-failure` and `always/cleanup`. They deliberately run again when a resumed run fails again, even if the handler completed during an earlier failed attempt.

### Edited steps and rewinding

A completed step is recognised by name: its row says `completed`, so it is skipped and its saved output is reused, **even if you have edited it since**. Each row therefore stores a hash of the definition that produced it, and resume compares it with the current definition (in the run and in its sub-runs):

- By default a changed completed step is **reported** (a log line and `changed_steps` on the `run_resumed` event) and still reused.
- `markov resume <run_id> --rewind-changed` re-runs from the earliest changed step.
- `markov resume <run_id> --rewind '{"workflow":"main","step":"build"}'` re-runs from a chosen step of the entrypoint workflow (repeatable; the earliest wins). A completed run can be rewound this way too.

Rewinding deletes the step records of that step and of every later step of the entrypoint workflow (including `for_each` iterations), and every record of the sub-runs those steps started, then resumes normally, so the cleared steps run again with the current workflow. Earlier steps keep their results. A step inside a sub-workflow cannot be named directly; rewind to the step that calls it (the error names the callers), and `--rewind-changed` does this automatically. `rescue` and `always` steps already run every time. The hash covers the step and its custom step type, not the workflow's `vars` or other files it reads.

## for_each resume

Sub-runs created by `for_each` are matched by `for_each_key`:

1. Each sub-run is stored with `parent_run_id`, `parent_step`, and `for_each_key`.
2. Sub-run IDs follow the pattern: `{parentRunID}-{stepName}-{key}`.
3. On resume, completed sub-runs are skipped via the normal step deduplication logic.
4. Failed or missing sub-runs are retried.

This is why `for_each_key` is important for production workflows -- without it, keys default to array indices, which can change if the input list is modified between runs.

## Step save behavior

Steps are saved using `INSERT ... ON CONFLICT DO UPDATE` (upsert):

```sql
INSERT INTO steps (run_id, workflow_name, step_name, status, ...)
VALUES (?, ?, ?, ?, ...)
ON CONFLICT (run_id, workflow_name, step_name) DO UPDATE SET
    status = excluded.status,
    output_json = excluded.output_json,
    artifacts_json = excluded.artifacts_json,
    error = excluded.error,
    started_at = excluded.started_at,
    completed_at = excluded.completed_at
```

This means:

- A step can be re-saved safely on resume without manual cleanup.
- A failed step that is retried will have its record updated in place.

## Inspecting state

### Show run status

```bash
markov status <run_id>
```

### Show run status with step details

```bash
markov status <run_id> --steps
```

Output:

```
Run:        a1b2c3d4
Workflow:   deploy-pipeline
Status:     failed
Started:    2025-01-15 10:30:00

STEP                           STATUS       DURATION
----                           ------       --------
fetch-config                   completed    1.2s
validate-input                 completed    0.1s
deploy-service                 failed       45.3s
  error: container image pull failed
```

### List all runs

```bash
markov list
```

Output:

```
RUN ID     WORKFLOW                  STATUS       STARTED              DURATION
------     --------                  ------       -------              --------
a1b2c3d4   deploy-pipeline           failed       2025-01-15 10:30:00  46.6s
e5f6g7h8   health-check              completed    2025-01-15 10:25:00  3.2s
```

## Parent-child relationships

The state store tracks run hierarchy through `parent_run_id` and `parent_step`:

```
Top-level run (a1b2c3d4)
  |
  +-- Sub-run (a1b2c3d4-deploy-api)        [for_each_key: "api"]
  +-- Sub-run (a1b2c3d4-deploy-worker)     [for_each_key: "worker"]
  +-- Sub-run (a1b2c3d4-deploy-frontend)   [for_each_key: "frontend"]
```

Child runs can be queried with `GetChildRuns(parentRunID)`.

## Diagram generation

Generate a Mermaid diagram of a completed run and its sub-runs:

```bash
markov diagram <run_id>
```

This walks the run tree (parent and children) and produces a Mermaid-formatted output showing the execution flow.

## Jev support

Inline and gate decisions persist internal `__jev__/...` step receipts, including skipped-condition decisions. The prefix is reserved and these records are not restored as ordinary template variables. See [Jev decisions](jev.md).
