# Callbacks

Fire-and-forget event system for external observability. Errors in callbacks are logged but never fail the workflow. Inspired by Ansible callback plugins.

## CLI flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--callback <url>` | string (repeatable) | none | Destination URL for events |
| `--callback-header <key=value>` | string (repeatable) | none | HTTP headers sent with each request |
| `--callback-tls-insecure` | bool | false | Skip TLS certificate verification |
| `--callback-tls-cert <path>` | string | none | Client TLS certificate path |
| `--callback-buffer-size <N>` | int | 1000 | Async send buffer size for HTTP callbacks |

Multiple `--callback` flags can be specified to send events to multiple destinations simultaneously.

## Supported schemes

### jsonl:// -- File-based logging

Appends one JSON line per event to a local file.

| Behavior | Detail |
|----------|--------|
| Write mode | Append (`O_APPEND \| O_CREATE \| O_WRONLY`) |
| Concurrency | Mutex-locked per write |
| Durability | `fsync` after every event |
| Format | One JSON object per line |

```bash
--callback jsonl:///var/log/markov-events.jsonl
```

### http:// / https:// -- Async HTTP POST

Events are queued and sent asynchronously via a background goroutine.

| Behavior | Detail |
|----------|--------|
| Method | POST |
| Content-Type | application/json |
| Buffer | Configurable via `--callback-buffer-size` (default 1000) |
| Retries | 3 attempts with backoff: 0ms, 200ms, 400ms |
| Timeout | 10s per request, 30s client timeout |
| Retry logic | Retries on network errors and 5xx responses; does not retry 4xx |
| Flush | `run_completed`, `run_failed`, and `run_paused` events block until sent |
| Close | Drains the buffer with a 10s timeout |

```bash
--callback https://hooks.example.com/markov \
--callback-header "Authorization=Bearer tok123"
```

### grpc:// / grpcs:// -- gRPC streaming

Sends events via unary gRPC calls using a JSON codec (no protobuf compilation needed).

| Behavior | Detail |
|----------|--------|
| RPC method | `/markov.MarkovCallback/SendEvent` |
| Codec | JSON (custom codec, no protobuf) |
| Timeout | 5s per send |
| TLS | `grpcs://` enables TLS; uses `--callback-tls-cert` or falls back to insecure |
| Payload | Wraps event in `{event_type, run_id, timestamp, payload}` envelope |

```bash
--callback grpcs://callback-server:9090 \
--callback-tls-cert /etc/certs/client.pem
```

For plain gRPC without TLS:

```bash
--callback grpc://callback-server:9090
```

## Event types

All events share a common header:

```json
{
  "timestamp": "2025-01-15T10:30:00.123Z",
  "run_id": "a1b2c3d4",
  "event_type": "step_completed"
}
```

### Run lifecycle events

| Event Type | When Fired | Key Fields |
|------------|------------|------------|
| `run_started` | Run begins | `workflow_name`, `workflow_file`, `source_digest`, `vars`, `forks`, `namespace` |
| `run_completed` | Run succeeds | `workflow_name`, `duration_seconds` |
| `run_failed` | Run fails | `workflow_name`, `error`, `duration_seconds` |
| `run_paused` | A gate intentionally pauses the run | `workflow_name`, `step_name`, `fired_rules`, `facts`, `duration_seconds` |
| `workflow_reloaded` | A [`reload_workflow`](step-types.md#reload_workflow) step, or a [requested reload](debugging.md#reloading-on-request) at a sub-workflow call, replaced the definition | `workflow_name`, `step_name`, `source`, `old_digest`, `new_digest`, `reload` (count in this process), `changes` |
| `run_resumed` | Run is resumed | `workflow_name`, `completed_steps`, `remaining_steps`, `source_integrity_mode`, `expected_source_digest`, `observed_source_digest`, `source_drifted`, `changed_steps` (completed steps whose definition changed: `run_id`, `workflow`, `step`), `rewound` |

### Step lifecycle events

| Event Type | When Fired | Key Fields |
|------------|------------|------------|
| `step_started` | Step begins | `workflow_name`, `step_name`, `step_type`, `resolved_type`, `params` |
| `step_completed` | Step succeeds | `workflow_name`, `step_name`, `step_type`, `output`, `duration_seconds` |
| `step_failed` | Step fails | `workflow_name`, `step_name`, `step_type`, `error`, `duration_seconds`, and `output` when the step's executor returned any before failing (a shell step's `stdout`/`stderr`, a `claude` step's events and `sandbox_network`). A step over its `max_output` has `error_kind: "max_output"`, `output_bytes`, `limit_bytes`, `stream` (what a streaming executor cut off) and `largest` (`[{path, bytes, items}]`), and no `output` ([Output limits](workflow-file.md#output-limits)) |
| `step_skipped` | Step skipped (when: false) | `workflow_name`, `step_name`, `reason` |
| `step_progress` | Live progress from inside a running step (emitted by `claude`, `openshell_exec` and `openshell_build`) | `workflow_name`, `step_name`, `step_type`, `kind`, `data` |

`step_progress` events are informational: they are delivered to callbacks only and are not stored in the state store. For `claude` steps, `kind` is one of `init`, `text`, `tool_use`, `tool_result`, `usage` (running `turns` and `tokens` totals), `api_retry` (the CLI could not reach the API and will retry: `attempt`, `max_retries`, `retry_delay_ms`, `error`), `limit_exceeded`, or `result`. See [claude](step-types.md#claude). `openshell_exec` and `openshell_build` emit `output` with `stream` (`stdout` or `stderr`) and `text` (one line).

### Debug events

| Event | Description | Key fields |
|-------|-------------|------------|
| `debug` | Debugger activity during a [step-through](debugging.md) run | `workflow_name`, `step_name` (when about a step), `kind`, `data` |

`kind` is one of:

| Kind | Meaning | `data` |
|------|---------|--------|
| `breakpoints_set` | A `set_breakpoints` command was applied, or the workflow was reloaded (`reason: workflow_reloaded`) | `resolved` (normalized breakpoints), `unresolved` (each with `error`), optional `reason` |
| `reload_pending` | A `reload_workflow` step waits for `reload` or `reject_reload` | `source`, `current_digest`, `candidate_digest` and `changes` (or `error` when the candidate does not load) |
| `reload_queued` | A `request_reload` was loaded and waits for the next sub-workflow call | `source`, `candidate_digest`, `entry`, `changes` |
| `reload_unchanged` | A `request_reload` matched the running definition; nothing is queued | `source`, `candidate_digest`, `entry` |
| `reload_failed` | A `request_reload` was refused | `source`, `error` |
| `paused` | A step is held at a breakpoint or in step mode | `reason`, `phase`, `section`, `iteration`, `step_type`, `variables`, optional `output` (after phase), and `hits` or `condition`/`error` |
| `resumed` | The pause was released | `command` |
| `logpoint` | A logpoint rendered its message | `message`, `phase`, `section`, `iteration` |
| `evaluated` | Answer to `evaluate` | `id`, `expression`, `result` or `error` |
| `error` | A control command was invalid | `error` |

Debug events are informational and are not stored in the state store.

### Kubernetes events

| Event Type | When Fired | Key Fields |
|------------|------------|------------|
| `job_created` | K8s Job resource created | `workflow_name`, `step_name`, `step_type`, `job_name`, `namespace`, `pod_selector` Also emitted by `k8s_job_wait` when it starts watching an existing Job. |

### Gate events

| Event Type | When Fired | Key Fields |
|------------|------------|------------|
| `gate_evaluated` | Gate step completes | `workflow_name`, `step_name`, `action`, `fired_rules`, `facts` |

### Sub-run events (for_each / workflow)

| Event Type | When Fired | Key Fields |
|------------|------------|------------|
| `sub_run_started` | Sub-workflow begins | `parent_run_id`, `parent_step`, `workflow_name`, `for_each_key` |
| `sub_run_completed` | Sub-workflow succeeds | `parent_run_id`, `parent_step`, `workflow_name`, `for_each_key`, `duration_seconds` |
| `sub_run_failed` | Sub-workflow fails | `parent_run_id`, `parent_step`, `workflow_name`, `for_each_key`, `error`, `duration_seconds` |

## Event lifecycle

A typical workflow produces events in this order:

```
run_started
  step_started (fetch-data)
  step_completed (fetch-data)
  step_started (deploy, resolved_type=for_each)
    sub_run_started (deploy-api)
      step_started (rollout)
      job_created (rollout)
      step_completed (rollout)
    sub_run_completed (deploy-api)
    sub_run_started (deploy-worker)
      step_started (rollout)
      job_created (rollout)
      step_completed (rollout)
    sub_run_completed (deploy-worker)
  step_completed (deploy)
  step_started (validate-gate)
  gate_evaluated (validate-gate)
  step_completed (validate-gate)
run_completed
```

## Secret masking

Every callback gets a copy of each event with secret values replaced by `<secret:NAME>`: vars
listed in `secrets:` or named like secrets, outputs of steps marked `secret`, and secret step
params. The run's own context is not changed. See
[Secrets](variables-and-context.md#secrets).

## Error isolation

Callback errors never affect workflow execution:

```go
func (e *Engine) fireEvent(fn func(callback.Callback) error) {
    for _, cb := range e.callbacks {
        if err := fn(cb); err != nil {
            log.Printf("callback error: %v", err)  // logged, not returned
        }
    }
}
```

If a callback destination is unreachable, events are dropped after retries and the workflow continues normally.

## Example: Full configuration

```bash
markov run pipeline.yaml \
  --callback jsonl:///var/log/markov-events.jsonl \
  --callback https://hooks.example.com/markov \
  --callback grpcs://metrics.internal:9090 \
  --callback-header "Authorization=Bearer tok123" \
  --callback-header "X-Team=platform" \
  --callback-tls-cert /etc/certs/client.pem \
  --callback-buffer-size 2000 \
  --verbose
```

This sends every event to three destinations simultaneously:
1. A local JSONL file for debugging and audit.
2. An HTTPS webhook for integration (Slack, PagerDuty, custom dashboards).
3. A gRPC server for metrics collection.

## Example: JSONL event output

```json
{"timestamp":"2025-01-15T10:30:00.123Z","run_id":"a1b2c3d4","event_type":"run_started","workflow_name":"deploy","workflow_file":"deploy.yaml","source_digest":"3be8b1...","vars":{"env":"prod"},"forks":5,"namespace":"default"}
{"timestamp":"2025-01-15T10:30:00.456Z","run_id":"a1b2c3d4","event_type":"step_started","workflow_name":"deploy","step_name":"fetch-config","step_type":"shell_exec","resolved_type":"shell_exec","params":{"command":"cat config.yaml"}}
{"timestamp":"2025-01-15T10:30:01.789Z","run_id":"a1b2c3d4","event_type":"step_completed","workflow_name":"deploy","step_name":"fetch-config","step_type":"shell_exec","output":{"stdout":"...","rc":0},"duration_seconds":1.333}
{"timestamp":"2025-01-15T10:30:45.000Z","run_id":"a1b2c3d4","event_type":"run_completed","workflow_name":"deploy","duration_seconds":44.877}
```

## Consuming events

### Tail JSONL in real time

```bash
tail -f /var/log/markov-events.jsonl | jq -r '[.event_type, .step_name // .workflow_name, .duration_seconds // "" | tostring] | join("\t")'
```

### Filter failures

```bash
cat /var/log/markov-events.jsonl | jq -c 'select(.event_type | endswith("_failed"))'
```

### Build a timeline

```bash
cat /var/log/markov-events.jsonl | jq -c 'select(.event_type == "step_completed") | {step: .step_name, duration: .duration_seconds}' | sort -t: -k2 -rn
```
