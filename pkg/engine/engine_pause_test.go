package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/vokramd/markov/pkg/executor"
	"github.com/vokramd/markov/pkg/parser"
	"github.com/vokramd/markov/pkg/state"
)

func TestGatePausePersistsReceiptAndRequiresExplicitResumeInput(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"approved": false},
		Rules: []parser.Rule{
			{
				Name:     "await_approval",
				Salience: 10,
				When:     "approved != true",
				Action:   "pause",
			},
		},
		Workflows: []parser.Workflow{{
			Name: "main",
			Steps: []parser.Step{
				{Name: "approval_gate", Type: "gate", Rules: []string{"await_approval"}},
				{Name: "after_approval", Type: "shell_exec", Params: map[string]any{"command": "echo released"}},
			},
		}},
	}

	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{
		"shell_exec": &mockExec{output: map[string]any{"stdout": "released"}},
	})

	runID, err := eng.Run(context.Background(), "", nil)
	if !IsPaused(err) {
		t.Fatalf("Run() error = %v, want PauseError", err)
	}

	run, err := eng.store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != state.RunPaused {
		t.Fatalf("run status = %q, want %q", run.Status, state.RunPaused)
	}
	if run.CompletedAt != nil {
		t.Fatalf("paused run CompletedAt = %v, want nil", run.CompletedAt)
	}

	pausedStep, err := eng.store.GetStep(context.Background(), runID, "main", "approval_gate")
	if err != nil {
		t.Fatal(err)
	}
	if pausedStep == nil || pausedStep.Status != state.StepPaused {
		t.Fatalf("gate step = %#v, want paused", pausedStep)
	}
	var receipt map[string]any
	if err := json.Unmarshal([]byte(pausedStep.OutputJSON), &receipt); err != nil {
		t.Fatalf("unmarshaling receipt: %v", err)
	}
	if receipt["action"] != "pause" {
		t.Fatalf("receipt action = %v, want pause", receipt["action"])
	}

	if err := eng.Resume(context.Background(), runID); err == nil {
		t.Fatal("Resume() error = nil, want explicit-input error")
	}
	if run, err = eng.store.GetRun(context.Background(), runID); err != nil || run.Status != state.RunPaused {
		t.Fatalf("run after rejected resume = %#v, %v; want paused", run, err)
	}

	if err := eng.ResumeWithVars(context.Background(), runID, map[string]any{"approved": true}); err != nil {
		t.Fatalf("ResumeWithVars() error = %v", err)
	}
	run, err = eng.store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != state.RunCompleted {
		t.Fatalf("run status after approval = %q, want %q", run.Status, state.RunCompleted)
	}
	if run.CompletedAt == nil {
		t.Fatal("completed run CompletedAt = nil, want timestamp")
	}

	gateStep, err := eng.store.GetStep(context.Background(), runID, "main", "approval_gate")
	if err != nil {
		t.Fatal(err)
	}
	if gateStep.Status != state.StepCompleted {
		t.Fatalf("gate step after approval = %q, want completed", gateStep.Status)
	}
	afterStep, err := eng.store.GetStep(context.Background(), runID, "main", "after_approval")
	if err != nil {
		t.Fatal(err)
	}
	if afterStep.Status != state.StepCompleted {
		t.Fatalf("after-approval step = %q, want completed", afterStep.Status)
	}

	events := cb.getEvents()
	if !containsEvent(events, "run_paused") {
		t.Fatalf("events = %v, want run_paused", events)
	}
	if !containsEvent(events, "run_resumed") || !containsEvent(events, "run_completed") {
		t.Fatalf("events = %v, want run_resumed and run_completed", events)
	}
}

func TestGatePausePropagatesThroughSubWorkflow(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"approved": false},
		Rules: []parser.Rule{{
			Name:     "await_approval",
			Salience: 10,
			When:     "approved != true",
			Action:   "pause",
		}},
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{Name: "review", Workflow: "child"},
				},
			},
			{
				Name: "child",
				Steps: []parser.Step{
					{Name: "approval_gate", Type: "gate", Rules: []string{"await_approval"}},
				},
			},
		},
	}

	eng, _ := newTestEngine(t, wfFile, nil)
	runID, err := eng.Run(context.Background(), "", nil)
	if !IsPaused(err) {
		t.Fatalf("Run() error = %v, want PauseError", err)
	}

	child, err := eng.store.GetRun(context.Background(), runID+"-review")
	if err != nil {
		t.Fatal(err)
	}
	if child.Status != state.RunPaused {
		t.Fatalf("child status = %q, want %q", child.Status, state.RunPaused)
	}

	if err := eng.ResumeWithVars(context.Background(), runID, map[string]any{"approved": true}); err != nil {
		t.Fatalf("ResumeWithVars() error = %v", err)
	}
	run, err := eng.store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != state.RunCompleted {
		t.Fatalf("parent status = %q, want %q", run.Status, state.RunCompleted)
	}
	child, err = eng.store.GetRun(context.Background(), runID+"-review")
	if err != nil {
		t.Fatal(err)
	}
	if child.Status != state.RunCompleted {
		t.Fatalf("child status after approval = %q, want %q", child.Status, state.RunCompleted)
	}
}

func containsEvent(events []string, want string) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}
