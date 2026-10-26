package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jctanner/markov/pkg/executor"
	"github.com/jctanner/markov/pkg/parser"
	"github.com/jctanner/markov/pkg/state"
)

type lifecycleExecutor struct {
	calls    []string
	failures map[string]error
}

func (e *lifecycleExecutor) Execute(_ context.Context, params map[string]any) (*executor.Result, error) {
	label, _ := params["label"].(string)
	e.calls = append(e.calls, label)
	if err := e.failures[label]; err != nil {
		return nil, err
	}
	return &executor.Result{Output: map[string]any{"label": label}}, nil
}

func TestWorkflowAlwaysRunsAfterSuccess(t *testing.T) {
	exec := &lifecycleExecutor{}
	wfFile := teardownWorkflow([]parser.Step{{Name: "main", Type: "test", Params: map[string]any{"label": "main"}}}, nil,
		[]parser.Step{{Name: "unlock", Type: "test", Params: map[string]any{"label": "always"}}})
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"test": exec})

	if _, err := eng.Run(context.Background(), "", nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := strings.Join(exec.calls, ","), "main,always"; got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

func TestWorkflowAlwaysFailureDoesNotRunRescue(t *testing.T) {
	exec := &lifecycleExecutor{failures: map[string]error{"always": fmt.Errorf("cleanup failed")}}
	wfFile := teardownWorkflow(
		[]parser.Step{{Name: "main", Type: "test", Params: map[string]any{"label": "main"}}},
		[]parser.Step{{Name: "report", Type: "test", Params: map[string]any{"label": "rescue"}}},
		[]parser.Step{{Name: "unlock", Type: "test", Params: map[string]any{"label": "always"}}},
	)
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"test": exec})

	if _, err := eng.Run(context.Background(), "", nil); err == nil || !strings.Contains(err.Error(), "cleanup failed") {
		t.Fatalf("Run() error = %v, want always failure", err)
	}
	if got, want := strings.Join(exec.calls, ","), "main,always"; got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

func TestWorkflowFailureRunsRescueAndAlways(t *testing.T) {
	exec := &lifecycleExecutor{failures: map[string]error{"main": fmt.Errorf("boom")}}
	wfFile := teardownWorkflow(
		[]parser.Step{{Name: "main", Type: "test", Params: map[string]any{"label": "main"}}},
		[]parser.Step{{Name: "report", Type: "test", Params: map[string]any{"label": "rescue"}}},
		[]parser.Step{{Name: "unlock", Type: "test", Params: map[string]any{"label": "always"}}},
	)
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"test": exec})

	runID, err := eng.Run(context.Background(), "", nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Run() error = %v, want main failure", err)
	}
	if got, want := strings.Join(exec.calls, ","), "main,rescue,always"; got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}

	for _, stateName := range []string{"rescue/report", "always/unlock"} {
		step, err := eng.store.GetStep(context.Background(), runID, "main", stateName)
		if err != nil || step == nil || step.Status != state.StepCompleted {
			t.Fatalf("step %q = %#v, %v; want completed", stateName, step, err)
		}
	}
}

func TestWorkflowRunsAlwaysEvenWhenRescueFails(t *testing.T) {
	exec := &lifecycleExecutor{failures: map[string]error{
		"main":   fmt.Errorf("main failed"),
		"rescue": fmt.Errorf("rescue failed"),
	}}
	wfFile := teardownWorkflow(
		[]parser.Step{{Name: "main", Type: "test", Params: map[string]any{"label": "main"}}},
		[]parser.Step{{Name: "report", Type: "test", Params: map[string]any{"label": "rescue"}}},
		[]parser.Step{{Name: "unlock", Type: "test", Params: map[string]any{"label": "always"}}},
	)
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"test": exec})

	if _, err := eng.Run(context.Background(), "", nil); err == nil || !strings.Contains(err.Error(), "rescue failed") {
		t.Fatalf("Run() error = %v, want joined failure", err)
	}
	if got, want := strings.Join(exec.calls, ","), "main,rescue,always"; got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

func TestWorkflowTeardownRerunsAfterResumedFailure(t *testing.T) {
	exec := &lifecycleExecutor{failures: map[string]error{"main": fmt.Errorf("boom")}}
	wfFile := teardownWorkflow(
		[]parser.Step{{Name: "main", Type: "test", Params: map[string]any{"label": "main"}}},
		nil,
		[]parser.Step{{Name: "unlock", Type: "test", Params: map[string]any{"label": "always"}}},
	)
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"test": exec})

	runID, err := eng.Run(context.Background(), "", nil)
	if err == nil {
		t.Fatal("Run() error = nil, want failure")
	}
	if err := eng.Resume(context.Background(), runID); err == nil {
		t.Fatal("Resume() error = nil, want failure")
	}
	if got, want := strings.Join(exec.calls, ","), "main,always,main,always"; got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

// stopExecutor cancels the run from its "main" step, as a Ctrl-C or the IDE's stop does mid-step,
// and records whether each later step's context was already done when it started.
type stopExecutor struct {
	stop  context.CancelFunc
	calls []string
}

func (e *stopExecutor) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	label, _ := params["label"].(string)
	if label == "main" {
		e.stop()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	e.calls = append(e.calls, fmt.Sprintf("%s:%v", label, ctx.Err()))
	return &executor.Result{Output: map[string]any{"label": label}}, nil
}

func TestCancelledRunStillRunsRescueAndAlways(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec := &stopExecutor{stop: cancel}
	wfFile := teardownWorkflow(
		[]parser.Step{{Name: "main", Type: "test", Params: map[string]any{"label": "main"}}},
		[]parser.Step{{Name: "report", Type: "test", Params: map[string]any{"label": "rescue"}}},
		[]parser.Step{{Name: "delete-sandbox", Type: "test", Params: map[string]any{"label": "always"}}},
	)
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"test": exec})

	runID, err := eng.Run(ctx, "", nil)
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("Run() error = %v, want the cancellation", err)
	}
	if got, want := strings.Join(exec.calls, ","), "rescue:<nil>,always:<nil>"; got != want {
		t.Fatalf("teardown calls = %q, want %q (handlers run on a live context)", got, want)
	}
	for _, stateName := range []string{"rescue/report", "always/delete-sandbox"} {
		step, err := eng.store.GetStep(context.Background(), runID, "main", stateName)
		if err != nil || step == nil || step.Status != state.StepCompleted {
			t.Fatalf("step %q = %#v, %v; want completed", stateName, step, err)
		}
	}
	run, err := eng.store.GetRun(context.Background(), runID)
	if err != nil || run.Status != state.RunFailed {
		t.Fatalf("run = %#v, %v; want its failure saved despite the cancellation", run, err)
	}
}

func TestTerminalContextOutlivesCancellationByTheGrace(t *testing.T) {
	type key struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "v"))
	termCtx, stop := terminalContext(parent, 50*time.Millisecond)
	defer stop()

	cancel()
	if err := termCtx.Err(); err != nil {
		t.Fatalf("terminal context done at once: %v", err)
	}
	if termCtx.Value(key{}) != "v" {
		t.Error("terminal context lost the run's values")
	}
	select {
	case <-termCtx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("terminal context still live long after the grace")
	}
}

func TestPausedWorkflowDoesNotRunTeardown(t *testing.T) {
	exec := &lifecycleExecutor{}
	wfFile := teardownWorkflow(
		[]parser.Step{{Name: "approval", Type: "gate", Rules: []string{"wait"}}},
		[]parser.Step{{Name: "report", Type: "test", Params: map[string]any{"label": "rescue"}}},
		[]parser.Step{{Name: "unlock", Type: "test", Params: map[string]any{"label": "always"}}},
	)
	wfFile.Vars = map[string]any{"approved": false}
	wfFile.Rules = []parser.Rule{{Name: "wait", When: "approved != true", Action: "pause"}}
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"test": exec})

	_, err := eng.Run(context.Background(), "", nil)
	if !IsPaused(err) {
		t.Fatalf("Run() error = %v, want PauseError", err)
	}
	if len(exec.calls) != 0 {
		t.Fatalf("teardown calls = %v, want none", exec.calls)
	}
}

func TestLifecycleHandlerMayNotPause(t *testing.T) {
	exec := &lifecycleExecutor{}
	wfFile := teardownWorkflow(
		[]parser.Step{{Name: "main", Type: "test", Params: map[string]any{"label": "main"}}},
		nil,
		[]parser.Step{
			{Name: "wait", Type: "gate", Rules: []string{"pause"}},
			{Name: "cleanup", Type: "test", Params: map[string]any{"label": "cleanup"}},
		},
	)
	wfFile.Rules = []parser.Rule{{Name: "pause", When: "approved != true", Action: "pause"}}
	wfFile.Vars = map[string]any{"approved": false}
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"test": exec})

	_, err := eng.Run(context.Background(), "", nil)
	if err == nil || IsPaused(err) || !strings.Contains(err.Error(), `always step "wait" may not pause`) {
		t.Fatalf("Run() error = %v, want non-paused lifecycle error", err)
	}
	if got, want := strings.Join(exec.calls, ","), "main,cleanup"; got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

func TestParserRejectsDuplicateTeardownStepNames(t *testing.T) {
	_, err := parser.Parse([]byte(`
entrypoint: main
workflows:
  - name: main
    steps:
      - name: cleanup
        type: shell_exec
        params: {command: "true"}
    always:
      - name: cleanup
        type: shell_exec
        params: {command: "true"}
`))
	if err == nil || !strings.Contains(err.Error(), "duplicate step name") {
		t.Fatalf("Parse() error = %v, want duplicate-name error", err)
	}
}

func teardownWorkflow(steps, rescue, always []parser.Step) *parser.WorkflowFile {
	return &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{{
			Name:   "main",
			Steps:  steps,
			Rescue: rescue,
			Always: always,
		}},
	}
}
