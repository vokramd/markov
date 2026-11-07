# Running markov as a Kubernetes Job

When markovd deploys to Kubernetes, it spawns `markov run` as a K8s Job rather than a local subprocess. This doc covers what markov needs to support that model — running inside a pod, authenticating via ServiceAccount, and creating child Jobs.

## What Already Works

- **k8s_job executor** (`pkg/executor/k8s_job.go`) — creates Jobs with `service_account`, `secrets`, `volumes`, `env`, `envFrom`, init containers, resources, affinity
- **Callback system** — HTTP/gRPC/JSONL callbacks with headers, TLS, async buffering
- **State store** — SQLite checkpoint/resume
- **Namespace override** — `--namespace` flag and `wfFile.Namespace`

## What Needs to Change

### 1. In-cluster Kubernetes auth

**File**: `cmd/markov/main.go` — `getK8sClient()` (lines 349-380)

Currently uses `clientcmd.NewDefaultClientConfigLoadingRules()` which *can* find in-cluster config as a last resort, but doesn't explicitly try `rest.InClusterConfig()`. When running as a Job pod, the ServiceAccount token is mounted at `/var/run/secrets/kubernetes.io/serviceaccount/token` — `InClusterConfig()` is the canonical way to pick it up.

**Change**: try `rest.InClusterConfig()` first, fall back to `clientcmd` for local development:

```go
func getK8sClient() (kubernetes.Interface, *rest.Config, error) {
    // In-cluster (running as a pod)
    if restConfig, err := rest.InClusterConfig(); err == nil {
        client, err := kubernetes.NewForConfig(restConfig)
        if err != nil {
            return nil, nil, err
        }
        return client, restConfig, nil
    }

    // Out-of-cluster (local dev, explicit kubeconfig)
    kubeconfig := flagKubeconfig
    if kubeconfig == "" {
        kubeconfig = os.Getenv("KUBECONFIG")
    }
    // ... existing clientcmd logic ...
}
```

### 2. Capture pod logs from child Jobs

**File**: `pkg/executor/k8s_job.go`

The `k8s_job` executor currently returns only `{job_name, namespace}` after a Job completes. It does not capture stdout/stderr from the Job's pod. When debugging workflows running inside a cluster, having the child job output in the step result is important.

**Change**: after `waitForCompletion()` succeeds or fails, fetch the pod logs and include them in the result:

```go
output := map[string]any{
    "job_name":  jobName,
    "namespace": ns,
    "logs":      logs,   // stdout/stderr from the job's pod
}
```

Implementation:
- List pods with label selector `job-name=<jobName>`
- Get logs from the first matching pod via `CoreV1().Pods(ns).GetLogs()`
- Truncate to a reasonable limit (e.g., 64KB) to avoid blowing up the state store
- Non-fatal — if log retrieval fails, return the result without logs

Requires the `pipeline-agent` ServiceAccount to have `get` on `pods/log` (already granted per `docs/k8s-service-account.md` in markovd).

### 3. Inherit namespace from pod environment

**File**: `cmd/markov/main.go` — `buildExecutors()` (lines 328-347)

When `wfFile.Namespace` is empty and no `--namespace` flag is set, the executor defaults to `"default"`. Inside a pod, markov should default to its own namespace instead.

**Change**: read the namespace from the downward API file if no explicit namespace is set:

```go
namespace := wf.Namespace
if namespace == "" {
    namespace = flagNamespace
}
if namespace == "" {
    if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
        namespace = strings.TrimSpace(string(data))
    }
}
if namespace == "" {
    namespace = "default"
}
```

### 4. State store path for ephemeral pods

**File**: `cmd/markov/main.go`

The default state store is `./markov-state.db`, which writes to the container's ephemeral filesystem. This works for single-run Jobs (the state is only needed for the duration of the run), but the path should be writable.

**Change**: default to `/tmp/markov-state.db` when running in-cluster (detected by the existence of the ServiceAccount token), so it works regardless of the working directory:

```go
defaultStateStore := "./markov-state.db"
if _, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
    defaultStateStore = "/tmp/markov-state.db"
}
```

## Files to Modify

| File | Change |
|------|--------|
| `cmd/markov/main.go` | `getK8sClient()`: try `InClusterConfig()` first |
| `cmd/markov/main.go` | `buildExecutors()`: inherit namespace from pod |
| `cmd/markov/main.go` | Default state store path for in-cluster |
| `pkg/executor/k8s_job.go` | Capture pod logs after job completion |

## How markovd Spawns markov

For context, markovd creates a Job like:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: markov-run-<id>
  labels:
    app: markov
    markov/run-id: "<id>"
spec:
  serviceAccountName: pipeline-agent
  containers:
    - name: markov
      image: ghcr.io/vokramd/markov:latest
      command: ["markov", "run", "/etc/markov/workflow.yaml"]
      args:
        - --callback
        - http://markovd:8080/api/v1/events
        - --callback-header
        - Authorization=Bearer <token>
        - --namespace
        - ai-pipeline
      volumeMounts:
        - name: workflow
          mountPath: /etc/markov
      envFrom:
        - secretRef:
            name: pipeline-credentials
  volumes:
    - name: workflow
      configMap:
        name: markov-workflow-<id>
```

The `pipeline-agent` ServiceAccount gives markov RBAC to create child Jobs, read pods/logs, and manage secrets within the namespace.

## Verification

1. Build markov image and push to registry
2. Deploy markovd with `MARKOVD_RUNNER=kubernetes`
3. Trigger a workflow with a `k8s_job` step
4. Confirm markov pod starts and picks up in-cluster config (no kubeconfig needed)
5. Confirm child Jobs are created in the correct namespace (not `default`)
6. Confirm child Job logs appear in the step result
7. Confirm callbacks flow back to markovd
