package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jctanner/markov/pkg/callback"
	"github.com/jctanner/markov/pkg/executor"
	"github.com/jctanner/markov/pkg/parser"
	"github.com/jctanner/markov/pkg/state"
	"k8s.io/client-go/kubernetes/fake"
)

type mockCallback struct {
	mu     sync.Mutex
	events []string
	all    []any
}

func (m *mockCallback) record(eventType string, event any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, eventType)
	m.all = append(m.all, event)
	return nil
}

func (m *mockCallback) getEvents() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.events))
	copy(result, m.events)
	return result
}

func (m *mockCallback) OnRunStarted(e callback.RunStartedEvent) error {
	return m.record("run_started", e)
}
func (m *mockCallback) OnRunCompleted(e callback.RunCompletedEvent) error {
	return m.record("run_completed", e)
}
func (m *mockCallback) OnRunFailed(e callback.RunFailedEvent) error { return m.record("run_failed", e) }
func (m *mockCallback) OnRunPaused(e callback.RunPausedEvent) error { return m.record("run_paused", e) }
func (m *mockCallback) OnRunResumed(e callback.RunResumedEvent) error {
	return m.record("run_resumed", e)
}
func (m *mockCallback) OnStepStarted(e callback.StepStartedEvent) error {
	return m.record("step_started", e)
}
func (m *mockCallback) OnStepCompleted(e callback.StepCompletedEvent) error {
	return m.record("step_completed", e)
}
func (m *mockCallback) OnStepFailed(e callback.StepFailedEvent) error {
	return m.record("step_failed", e)
}
func (m *mockCallback) OnStepSkipped(e callback.StepSkippedEvent) error {
	return m.record("step_skipped", e)
}
func (m *mockCallback) OnStepProgress(e callback.StepProgressEvent) error {
	return m.record("step_progress", e)
}
func (m *mockCallback) OnDebug(e callback.DebugEvent) error {
	return m.record("debug", e)
}
func (m *mockCallback) OnJobCreated(e callback.JobCreatedEvent) error {
	return m.record("job_created", e)
}
func (m *mockCallback) OnGateEvaluated(e callback.GateEvaluatedEvent) error {
	return m.record("gate_evaluated", e)
}
func (m *mockCallback) OnSubRunStarted(e callback.SubRunStartedEvent) error {
	return m.record("sub_run_started", e)
}
func (m *mockCallback) OnSubRunCompleted(e callback.SubRunCompletedEvent) error {
	return m.record("sub_run_completed", e)
}
func (m *mockCallback) OnSubRunFailed(e callback.SubRunFailedEvent) error {
	return m.record("sub_run_failed", e)
}
func (m *mockCallback) OnWorkflowReloaded(e callback.WorkflowReloadedEvent) error {
	return m.record("workflow_reloaded", e)
}
func (m *mockCallback) Close() error { return nil }

type mockExec struct {
	output map[string]any
	err    error
}

func (m *mockExec) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &executor.Result{Output: m.output}, nil
}

func newTestEngine(t *testing.T, wfFile *parser.WorkflowFile, execs map[string]executor.Executor) (*Engine, *mockCallback) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := state.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	eng := New(wfFile, store, execs)
	cb := &mockCallback{}
	eng.SetCallbacks([]callback.Callback{cb})
	return eng, cb
}

func TestCallbackRunLifecycle(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{Name: "step1", Type: "shell_exec", Params: map[string]any{"command": "echo hello"}},
				},
			},
		},
	}

	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{
		"shell_exec": &mockExec{output: map[string]any{"stdout": "hello"}},
	})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := cb.getEvents()
	expected := []string{"run_started", "step_started", "step_completed", "run_completed"}
	if len(events) != len(expected) {
		t.Fatalf("events = %v, want %v", events, expected)
	}
	for i, want := range expected {
		if events[i] != want {
			t.Errorf("events[%d] = %q, want %q", i, events[i], want)
		}
	}
}

func TestCallbackStepFailed(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{Name: "bad_step", Type: "shell_exec", Params: map[string]any{"command": "fail"}},
				},
			},
		},
	}

	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{
		"shell_exec": &mockExec{err: fmt.Errorf("command failed")},
	})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", nil)
	if err == nil {
		t.Fatal("expected error from failed step")
	}

	events := cb.getEvents()
	expected := []string{"run_started", "step_started", "step_failed", "run_failed"}
	if len(events) != len(expected) {
		t.Fatalf("events = %v, want %v", events, expected)
	}
	for i, want := range expected {
		if events[i] != want {
			t.Errorf("events[%d] = %q, want %q", i, events[i], want)
		}
	}
}

func TestCallbackStepSkipped(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{Name: "skipped_step", Type: "shell_exec", When: "False", Params: map[string]any{"command": "echo"}},
				},
			},
		},
	}

	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{
		"shell_exec": &mockExec{output: map[string]any{}},
	})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := cb.getEvents()
	expected := []string{"run_started", "step_skipped", "run_completed"}
	if len(events) != len(expected) {
		t.Fatalf("events = %v, want %v", events, expected)
	}
	for i, want := range expected {
		if events[i] != want {
			t.Errorf("events[%d] = %q, want %q", i, events[i], want)
		}
	}
}

func TestCallbackSetFact(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{
						Name: "set_vars",
						Type: "set_fact",
						Vars: map[string]any{"greeting": "hello"},
					},
				},
			},
		},
	}

	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := cb.getEvents()
	expected := []string{"run_started", "step_started", "step_completed", "run_completed"}
	if len(events) != len(expected) {
		t.Fatalf("events = %v, want %v", events, expected)
	}
}

func TestCallbackSubWorkflow(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{Name: "run_child", Workflow: "child"},
				},
			},
			{
				Name: "child",
				Steps: []parser.Step{
					{
						Name: "child_fact",
						Type: "set_fact",
						Vars: map[string]any{"result": "done"},
					},
				},
			},
		},
	}

	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := cb.getEvents()

	// The orchestrator step itself must emit step_started/step_completed
	// so markovd can render it as a graph node.
	expected := []string{
		"run_started",
		"step_started",      // run_child orchestrator
		"sub_run_started",   // child sub-run
		"step_started",      // child_fact inside child
		"step_completed",    // child_fact inside child
		"sub_run_completed", // child sub-run
		"step_completed",    // run_child orchestrator
		"run_completed",
	}
	if len(events) != len(expected) {
		t.Fatalf("events = %v\n want %v", events, expected)
	}
	for i, want := range expected {
		if events[i] != want {
			t.Errorf("events[%d] = %q, want %q", i, events[i], want)
		}
	}
}

func TestCallbackForEach(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"items": []any{"a", "b"}},
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{
						Name:     "process_all",
						ForEach:  "items",
						As:       "item",
						Workflow: "process_item",
					},
				},
			},
			{
				Name: "process_item",
				Steps: []parser.Step{
					{
						Name: "do_thing",
						Type: "set_fact",
						Vars: map[string]any{"done": "yes"},
					},
				},
			},
		},
	}

	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := cb.getEvents()

	counts := map[string]int{}
	for _, e := range events {
		counts[e]++
	}

	// The for_each orchestrator step must appear as a graph node
	if counts["step_started"] < 3 {
		t.Errorf("step_started count = %d, want >= 3 (1 orchestrator + 2 sub-workflow steps): %v", counts["step_started"], events)
	}
	if counts["step_completed"] < 3 {
		t.Errorf("step_completed count = %d, want >= 3: %v", counts["step_completed"], events)
	}

	// Each fan-out iteration gets sub_run events
	if counts["sub_run_started"] != 2 {
		t.Errorf("sub_run_started count = %d, want 2: %v", counts["sub_run_started"], events)
	}
	if counts["sub_run_completed"] != 2 {
		t.Errorf("sub_run_completed count = %d, want 2: %v", counts["sub_run_completed"], events)
	}

	// Orchestrator step_started must come before sub_run events
	if events[0] != "run_started" {
		t.Errorf("events[0] = %q, want run_started", events[0])
	}
	if events[1] != "step_started" {
		t.Errorf("events[1] = %q, want step_started (for_each orchestrator)", events[1])
	}

	// Last two events must be step_completed (orchestrator) then run_completed
	n := len(events)
	if events[n-2] != "step_completed" {
		t.Errorf("events[%d] = %q, want step_completed (for_each orchestrator)", n-2, events[n-2])
	}
	if events[n-1] != "run_completed" {
		t.Errorf("events[%d] = %q, want run_completed", n-1, events[n-1])
	}
}

func TestCallbackMultiStep(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{Name: "step1", Type: "shell_exec", Params: map[string]any{"command": "echo 1"}},
					{Name: "step2", Type: "shell_exec", Params: map[string]any{"command": "echo 2"}},
					{Name: "step3", Type: "shell_exec", Params: map[string]any{"command": "echo 3"}},
				},
			},
		},
	}

	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{
		"shell_exec": &mockExec{output: map[string]any{"rc": 0}},
	})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := cb.getEvents()
	// run_started, (step_started + step_completed) * 3, run_completed = 8
	if len(events) != 8 {
		t.Fatalf("got %d events, want 8: %v", len(events), events)
	}
}

func TestCallbackErrorsDontFailWorkflow(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{Name: "step1", Type: "shell_exec", Params: map[string]any{"command": "echo"}},
				},
			},
		},
	}

	errCB := &errorCallback{}
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, _ := state.NewSQLiteStore(dbPath)
	defer store.Close()

	eng := New(wfFile, store, map[string]executor.Executor{
		"shell_exec": &mockExec{output: map[string]any{}},
	})
	eng.SetCallbacks([]callback.Callback{errCB})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", nil)
	if err != nil {
		t.Fatalf("workflow should succeed even when callbacks error: %v", err)
	}
}

type errorCallback struct{}

func (e *errorCallback) OnRunStarted(callback.RunStartedEvent) error { return fmt.Errorf("cb error") }
func (e *errorCallback) OnRunCompleted(callback.RunCompletedEvent) error {
	return fmt.Errorf("cb error")
}
func (e *errorCallback) OnRunFailed(callback.RunFailedEvent) error     { return fmt.Errorf("cb error") }
func (e *errorCallback) OnRunPaused(callback.RunPausedEvent) error     { return fmt.Errorf("cb error") }
func (e *errorCallback) OnRunResumed(callback.RunResumedEvent) error   { return fmt.Errorf("cb error") }
func (e *errorCallback) OnStepStarted(callback.StepStartedEvent) error { return fmt.Errorf("cb error") }
func (e *errorCallback) OnStepCompleted(callback.StepCompletedEvent) error {
	return fmt.Errorf("cb error")
}
func (e *errorCallback) OnStepFailed(callback.StepFailedEvent) error   { return fmt.Errorf("cb error") }
func (e *errorCallback) OnStepSkipped(callback.StepSkippedEvent) error { return fmt.Errorf("cb error") }
func (e *errorCallback) OnStepProgress(callback.StepProgressEvent) error {
	return fmt.Errorf("cb error")
}
func (e *errorCallback) OnDebug(callback.DebugEvent) error           { return fmt.Errorf("cb error") }
func (e *errorCallback) OnJobCreated(callback.JobCreatedEvent) error { return fmt.Errorf("cb error") }
func (e *errorCallback) OnGateEvaluated(callback.GateEvaluatedEvent) error {
	return fmt.Errorf("cb error")
}
func (e *errorCallback) OnSubRunStarted(callback.SubRunStartedEvent) error {
	return fmt.Errorf("cb error")
}
func (e *errorCallback) OnSubRunCompleted(callback.SubRunCompletedEvent) error {
	return fmt.Errorf("cb error")
}
func (e *errorCallback) OnSubRunFailed(callback.SubRunFailedEvent) error {
	return fmt.Errorf("cb error")
}
func (e *errorCallback) OnWorkflowReloaded(callback.WorkflowReloadedEvent) error {
	return fmt.Errorf("cb error")
}
func (e *errorCallback) Close() error { return nil }

func TestCallbackZeroCallbacksWorks(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{Name: "step1", Type: "shell_exec", Params: map[string]any{"command": "echo"}},
				},
			},
		},
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, _ := state.NewSQLiteStore(dbPath)
	defer store.Close()

	eng := New(wfFile, store, map[string]executor.Executor{
		"shell_exec": &mockExec{output: map[string]any{}},
	})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", nil)
	if err != nil {
		t.Fatalf("workflow should work with zero callbacks: %v", err)
	}
}

func TestCallbackRunStartedEventFields(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Namespace:  "test-ns",
		Forks:      3,
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{Name: "step1", Type: "set_fact", Vars: map[string]any{"x": "1"}},
				},
			},
		},
	}

	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", map[string]any{"env": "prod"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if len(cb.all) == 0 {
		t.Fatal("no events recorded")
	}

	started, ok := cb.all[0].(callback.RunStartedEvent)
	if !ok {
		t.Fatalf("first event type = %T, want RunStartedEvent", cb.all[0])
	}

	if started.WorkflowName != "main" {
		t.Errorf("WorkflowName = %q, want main", started.WorkflowName)
	}
	if started.Namespace != "test-ns" {
		t.Errorf("Namespace = %q, want test-ns", started.Namespace)
	}
	if started.Forks != 3 {
		t.Errorf("Forks = %d, want 3", started.Forks)
	}
	if started.RunID == "" {
		t.Error("RunID is empty")
	}
	if started.Timestamp.IsZero() {
		t.Error("Timestamp is zero")
	}
}

func TestCallbackStepCompletedEventFields(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{Name: "echo_step", Type: "shell_exec", Params: map[string]any{"command": "echo hi"}},
				},
			},
		},
	}

	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{
		"shell_exec": &mockExec{output: map[string]any{"stdout": "hi", "rc": 0}},
	})

	ctx := context.Background()
	eng.Run(ctx, "main", nil)

	cb.mu.Lock()
	defer cb.mu.Unlock()

	var completed *callback.StepCompletedEvent
	for _, e := range cb.all {
		if sc, ok := e.(callback.StepCompletedEvent); ok {
			completed = &sc
			break
		}
	}

	if completed == nil {
		t.Fatal("no StepCompletedEvent found")
	}

	if completed.StepName != "echo_step" {
		t.Errorf("StepName = %q, want echo_step", completed.StepName)
	}
	if completed.StepType != "shell_exec" {
		t.Errorf("StepType = %q, want shell_exec", completed.StepType)
	}
	if completed.Duration <= 0 {
		t.Errorf("Duration = %f, want > 0", completed.Duration)
	}
	if completed.Output["stdout"] != "hi" {
		t.Errorf("Output[stdout] = %v, want hi", completed.Output["stdout"])
	}
}

func TestCallbackMultipleCallbacks(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{Name: "step1", Type: "set_fact", Vars: map[string]any{"x": "1"}},
				},
			},
		},
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, _ := state.NewSQLiteStore(dbPath)
	defer store.Close()

	cb1 := &mockCallback{}
	cb2 := &mockCallback{}

	eng := New(wfFile, store, map[string]executor.Executor{})
	eng.SetCallbacks([]callback.Callback{cb1, cb2})

	ctx := context.Background()
	eng.Run(ctx, "main", nil)

	events1 := cb1.getEvents()
	events2 := cb2.getEvents()

	if len(events1) != len(events2) {
		t.Errorf("cb1 got %d events, cb2 got %d, should be equal", len(events1), len(events2))
	}

	for i := range events1 {
		if events1[i] != events2[i] {
			t.Errorf("event %d: cb1=%q cb2=%q", i, events1[i], events2[i])
		}
	}
}

func TestCallbackDeployPipelineStructure(t *testing.T) {
	// Mirrors the deploy-pipeline.yaml structure:
	// main → for_each(3 envs) → deploy_env → nested sub-workflow + gate + conditional steps
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"environments": []any{"staging", "canary", "production"}},
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{Name: "preflight", Type: "shell_exec", Params: map[string]any{"command": "echo preflight"}},
					{Name: "build", Type: "shell_exec", Params: map[string]any{"command": "echo build"}},
					{
						Name:     "deploy_all",
						ForEach:  "environments",
						As:       "env",
						Workflow: "deploy_env",
						Vars:     map[string]any{"environment": "{{ env }}"},
					},
					{Name: "summary", Type: "shell_exec", Params: map[string]any{"command": "echo done"}},
				},
			},
			{
				Name: "deploy_env",
				Steps: []parser.Step{
					{Name: "announce", Type: "shell_exec", Params: map[string]any{"command": "echo deploying"}},
					{Name: "apply_manifests", Type: "shell_exec", Params: map[string]any{"command": "echo manifests"}},
					{
						Name:     "health_check",
						Workflow: "health_sub",
						Vars:     map[string]any{"environment": "{{ environment }}"},
					},
					{Name: "mark_complete", Type: "shell_exec", Params: map[string]any{"command": "echo complete"}},
				},
			},
			{
				Name: "health_sub",
				Steps: []parser.Step{
					{Name: "do_check", Type: "set_fact", Vars: map[string]any{"health_status": "healthy"}},
				},
			},
		},
	}

	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{
		"shell_exec": &mockExec{output: map[string]any{"rc": 0}},
	})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := cb.getEvents()

	counts := map[string]int{}
	for _, e := range events {
		counts[e]++
	}

	t.Logf("Total events: %d", len(events))
	t.Logf("Events: %v", events)
	t.Logf("Counts: %v", counts)

	// Top-level steps: preflight, build, deploy_all(orchestrator), summary = 4
	// Sub-workflow steps per env: announce, apply_manifests, health_check(orchestrator), mark_complete = 4
	// Health sub steps per env: do_check = 1
	// Total step_started: 4 + 3*(4+1) = 19
	if counts["step_started"] < 19 {
		t.Errorf("step_started count = %d, want >= 19 (4 top-level + 3 envs * 5 steps): %v", counts["step_started"], events)
	}
	if counts["step_completed"] < 19 {
		t.Errorf("step_completed count = %d, want >= 19: %v", counts["step_completed"], events)
	}

	// 3 for_each iterations + 3 nested health_sub calls = 6 sub_run events
	if counts["sub_run_started"] != 6 {
		t.Errorf("sub_run_started count = %d, want 6 (3 deploy_env + 3 health_sub): %v", counts["sub_run_started"], events)
	}
	if counts["sub_run_completed"] != 6 {
		t.Errorf("sub_run_completed count = %d, want 6: %v", counts["sub_run_completed"], events)
	}

	// Total events: run_started + 19*step_started + 19*step_completed + 6*sub_run_started + 6*sub_run_completed + run_completed = 52
	expectedTotal := 1 + 19 + 19 + 6 + 6 + 1
	if len(events) != expectedTotal {
		t.Errorf("total events = %d, want %d: %v", len(events), expectedTotal, events)
	}
}

func TestCallbackJobCreated(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{
						Name:    "run_job",
						Type:    "k8s_job",
						Timeout: 2,
						Params: map[string]any{
							"_job_name": "test-job",
							"image":     "busybox:latest",
							"command":   []any{"/bin/sh", "-c", "echo hello"},
						},
					},
				},
			},
		},
	}

	k8sClient := fake.NewSimpleClientset()
	k8sExec := executor.NewK8sJob(k8sClient, "test-ns")

	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{
		"k8s_job": k8sExec,
	})

	ctx := context.Background()
	// The step will fail because fake client doesn't update Job status,
	// but job_created should fire before waitForCompletion.
	eng.Run(ctx, "main", nil)

	events := cb.getEvents()

	hasJobCreated := false
	for _, ev := range events {
		if ev == "job_created" {
			hasJobCreated = true
			break
		}
	}
	if !hasJobCreated {
		t.Errorf("job_created not found in events: %v", events)
	}

	// job_created must appear after step_started
	stepStartedIdx := -1
	jobCreatedIdx := -1
	for i, ev := range events {
		if ev == "step_started" && stepStartedIdx == -1 {
			stepStartedIdx = i
		}
		if ev == "job_created" && jobCreatedIdx == -1 {
			jobCreatedIdx = i
		}
	}
	if stepStartedIdx >= 0 && jobCreatedIdx >= 0 && jobCreatedIdx <= stepStartedIdx {
		t.Errorf("job_created (idx %d) should come after step_started (idx %d)", jobCreatedIdx, stepStartedIdx)
	}

	// Verify the event fields
	cb.mu.Lock()
	defer cb.mu.Unlock()
	for _, ev := range cb.all {
		if jc, ok := ev.(callback.JobCreatedEvent); ok {
			if jc.StepName != "run_job" {
				t.Errorf("StepName = %q, want run_job", jc.StepName)
			}
			if jc.StepType != "k8s_job" {
				t.Errorf("StepType = %q, want k8s_job", jc.StepType)
			}
			if jc.Namespace != "test-ns" {
				t.Errorf("Namespace = %q, want test-ns", jc.Namespace)
			}
			if jc.JobName == "" {
				t.Error("JobName is empty")
			}
			if jc.PodSelector == "" {
				t.Error("PodSelector is empty")
			}
			if jc.EventType != "job_created" {
				t.Errorf("EventType = %q, want job_created", jc.EventType)
			}
			break
		}
	}
}

func TestCallbackStepProgress(t *testing.T) {
	// A real executor cannot be reached through the context key above, so this
	// test drives the production path with the claude executor and a fake binary.
	script := filepath.Join(t.TempDir(), "fake-claude")
	body := "#!/bin/sh\ncat >/dev/null\n" +
		`echo '{"type":"system","subtype":"init","session_id":"s","model":"m"}'` + "\n" +
		`echo '{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1,"output_tokens":1}}}'` + "\n" +
		`echo '{"type":"result","is_error":false,"result":"hi","num_turns":1,"session_id":"s"}'` + "\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{{
			Name: "main",
			Steps: []parser.Step{{
				Name: "ask", Type: "claude", Register: "ask",
				Params: map[string]any{"binary": script, "prompt": "say hi"},
			}},
		}},
	}
	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{"claude": executor.NewClaude()})
	if _, err := eng.Run(context.Background(), "main", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var kinds []string
	cb.mu.Lock()
	for _, e := range cb.all {
		if p, ok := e.(callback.StepProgressEvent); ok {
			if p.StepName != "ask" || p.StepType != "claude" || p.EventType != "step_progress" {
				t.Errorf("unexpected progress header: %+v", p)
			}
			kinds = append(kinds, p.Kind)
		}
	}
	cb.mu.Unlock()
	if got := fmt.Sprint(kinds); got != "[init text usage result]" {
		t.Errorf("progress kinds = %s", got)
	}
}

func TestCallbackJobCreatedForK8sJobWait(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Namespace:  "ai-pipeline",
		Workflows: []parser.Workflow{{
			Name: "main",
			Steps: []parser.Step{
				{Name: "wait", Type: "k8s_job_wait", Params: map[string]any{"job_name": "{{ job }}"}},
			},
		}},
		Vars: map[string]any{"job": "dashboard-job-1"},
	}
	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{"k8s_job_wait": &recordExec{}})
	if _, err := eng.Run(context.Background(), "main", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	created, completed := -1, -1
	for i, ev := range cb.all {
		switch e := ev.(type) {
		case callback.JobCreatedEvent:
			if e.JobName != "dashboard-job-1" || e.Namespace != "ai-pipeline" || e.StepName != "wait" || e.PodSelector != "job-name=dashboard-job-1" {
				t.Fatalf("job_created = %#v", e)
			}
			created = i
		case callback.StepCompletedEvent:
			completed = i
		}
	}
	if created < 0 || created > completed {
		t.Fatalf("job_created at %d, step_completed at %d: want job_created first", created, completed)
	}
}
