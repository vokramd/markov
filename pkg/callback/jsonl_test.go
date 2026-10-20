package callback

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJSONLCallbackWritesEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	cb, err := NewJSONLCallback(path)
	if err != nil {
		t.Fatalf("NewJSONLCallback: %v", err)
	}

	ts := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)

	if err := cb.OnRunStarted(RunStartedEvent{
		EventHeader:  EventHeader{Timestamp: ts, RunID: "run1", EventType: "run_started"},
		WorkflowName: "deploy",
		Forks:        5,
	}); err != nil {
		t.Fatalf("OnRunStarted: %v", err)
	}

	if err := cb.OnStepStarted(StepStartedEvent{
		EventHeader:  EventHeader{Timestamp: ts, RunID: "run1", EventType: "step_started"},
		WorkflowName: "deploy",
		StepName:     "build",
		StepType:     "shell_exec",
		ResolvedType: "shell_exec",
	}); err != nil {
		t.Fatalf("OnStepStarted: %v", err)
	}

	if err := cb.OnStepCompleted(StepCompletedEvent{
		EventHeader:  EventHeader{Timestamp: ts, RunID: "run1", EventType: "step_completed"},
		WorkflowName: "deploy",
		StepName:     "build",
		StepType:     "shell_exec",
		Output:       map[string]any{"rc": float64(0)},
		Duration:     1.5,
	}); err != nil {
		t.Fatalf("OnStepCompleted: %v", err)
	}

	if err := cb.OnRunCompleted(RunCompletedEvent{
		EventHeader:  EventHeader{Timestamp: ts, RunID: "run1", EventType: "run_completed"},
		WorkflowName: "deploy",
		Duration:     3.0,
	}); err != nil {
		t.Fatalf("OnRunCompleted: %v", err)
	}

	if err := cb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var lines []map[string]any
	for scanner.Scan() {
		var m map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			t.Fatalf("invalid JSON line: %v", err)
		}
		lines = append(lines, m)
	}

	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}

	expectedTypes := []string{"run_started", "step_started", "step_completed", "run_completed"}
	for i, want := range expectedTypes {
		if got := lines[i]["event_type"]; got != want {
			t.Errorf("line %d event_type = %v, want %v", i, got, want)
		}
	}

	if lines[2]["duration_seconds"].(float64) != 1.5 {
		t.Errorf("duration = %v, want 1.5", lines[2]["duration_seconds"])
	}
}

func TestJSONLCallbackAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	ts := time.Now()

	cb1, _ := NewJSONLCallback(path)
	cb1.OnRunStarted(RunStartedEvent{
		EventHeader:  EventHeader{Timestamp: ts, RunID: "run1", EventType: "run_started"},
		WorkflowName: "first",
	})
	cb1.Close()

	cb2, _ := NewJSONLCallback(path)
	cb2.OnRunStarted(RunStartedEvent{
		EventHeader:  EventHeader{Timestamp: ts, RunID: "run2", EventType: "run_started"},
		WorkflowName: "second",
	})
	cb2.Close()

	f, _ := os.Open(path)
	defer f.Close()
	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		count++
	}
	if count != 2 {
		t.Errorf("got %d lines, want 2 (append mode)", count)
	}
}

func TestJSONLCallbackAllEventTypes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "all_events.jsonl")

	cb, err := NewJSONLCallback(path)
	if err != nil {
		t.Fatalf("NewJSONLCallback: %v", err)
	}

	ts := time.Now()
	hdr := func(et string) EventHeader {
		return EventHeader{Timestamp: ts, RunID: "r1", EventType: et}
	}

	cb.OnRunStarted(RunStartedEvent{EventHeader: hdr("run_started"), WorkflowName: "w"})
	cb.OnRunCompleted(RunCompletedEvent{EventHeader: hdr("run_completed"), WorkflowName: "w"})
	cb.OnRunFailed(RunFailedEvent{EventHeader: hdr("run_failed"), WorkflowName: "w", Error: "boom"})
	cb.OnRunPaused(RunPausedEvent{EventHeader: hdr("run_paused"), WorkflowName: "w", StepName: "gate"})
	cb.OnRunResumed(RunResumedEvent{EventHeader: hdr("run_resumed"), WorkflowName: "w"})
	cb.OnStepStarted(StepStartedEvent{EventHeader: hdr("step_started"), StepName: "s"})
	cb.OnStepCompleted(StepCompletedEvent{EventHeader: hdr("step_completed"), StepName: "s"})
	cb.OnStepFailed(StepFailedEvent{EventHeader: hdr("step_failed"), StepName: "s", Error: "fail"})
	cb.OnStepSkipped(StepSkippedEvent{EventHeader: hdr("step_skipped"), StepName: "s"})
	cb.OnGateEvaluated(GateEvaluatedEvent{EventHeader: hdr("gate_evaluated"), StepName: "g"})
	cb.OnSubRunStarted(SubRunStartedEvent{EventHeader: hdr("sub_run_started")})
	cb.OnSubRunCompleted(SubRunCompletedEvent{EventHeader: hdr("sub_run_completed")})
	cb.OnSubRunFailed(SubRunFailedEvent{EventHeader: hdr("sub_run_failed"), Error: "sub fail"})
	cb.OnWorkflowReloaded(WorkflowReloadedEvent{EventHeader: hdr("workflow_reloaded"), Source: "/wf", Reload: 1})
	cb.Close()

	f, _ := os.Open(path)
	defer f.Close()
	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		var m map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			t.Errorf("line %d: invalid JSON: %v", count, err)
		}
		count++
	}
	if count != 14 {
		t.Errorf("got %d event lines, want 14", count)
	}
}

func TestJSONLCallbackInvalidPath(t *testing.T) {
	_, err := NewJSONLCallback("/nonexistent/dir/events.jsonl")
	if err == nil {
		t.Error("expected error for invalid path")
	}
}
