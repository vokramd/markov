package callback

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Every event a JSONL callback writes is delivered again, unchanged, by Dispatch.
func TestDispatchReplaysEveryLoggedEventType(t *testing.T) {
	dir := t.TempDir()
	first, _ := NewJSONLCallback(filepath.Join(dir, "a.jsonl"))
	hdr := func(ty string) EventHeader {
		return EventHeader{Timestamp: time.Date(2026, 10, 20, 1, 2, 3, 0, time.UTC), RunID: "r", EventType: ty}
	}
	first.OnRunStarted(RunStartedEvent{EventHeader: hdr("run_started"), WorkflowName: "w"})
	first.OnRunCompleted(RunCompletedEvent{EventHeader: hdr("run_completed"), WorkflowName: "w"})
	first.OnRunFailed(RunFailedEvent{EventHeader: hdr("run_failed"), Error: "boom"})
	first.OnRunPaused(RunPausedEvent{EventHeader: hdr("run_paused")})
	first.OnRunResumed(RunResumedEvent{EventHeader: hdr("run_resumed")})
	first.OnStepStarted(StepStartedEvent{EventHeader: hdr("step_started"), StepName: "s", StepType: "claude"})
	first.OnStepCompleted(StepCompletedEvent{EventHeader: hdr("step_completed"), StepName: "s", Output: map[string]any{"x": 1.0}})
	first.OnStepFailed(StepFailedEvent{EventHeader: hdr("step_failed"), StepName: "s", Error: "e"})
	first.OnStepSkipped(StepSkippedEvent{EventHeader: hdr("step_skipped"), StepName: "s"})
	first.OnStepProgress(StepProgressEvent{EventHeader: hdr("step_progress"), StepName: "s", Kind: "text", Data: map[string]any{"text": "hi"}})
	first.OnDebug(DebugEvent{EventHeader: hdr("debug"), Kind: "paused"})
	first.OnJobCreated(JobCreatedEvent{EventHeader: hdr("job_created"), JobName: "j"})
	first.OnGateEvaluated(GateEvaluatedEvent{EventHeader: hdr("gate_evaluated"), StepName: "g"})
	first.OnSubRunStarted(SubRunStartedEvent{EventHeader: hdr("sub_run_started"), ParentRunID: "p"})
	first.OnSubRunCompleted(SubRunCompletedEvent{EventHeader: hdr("sub_run_completed")})
	first.OnSubRunFailed(SubRunFailedEvent{EventHeader: hdr("sub_run_failed"), Error: "e"})
	first.OnWorkflowReloaded(WorkflowReloadedEvent{EventHeader: hdr("workflow_reloaded"), Reload: 2})
	first.Close()

	second, _ := NewJSONLCallback(filepath.Join(dir, "b.jsonl"))
	f, _ := os.Open(filepath.Join(dir, "a.jsonl"))
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		if err := Dispatch(second, sc.Bytes()); err != nil {
			t.Fatalf("line %d: %v", n, err)
		}
		n++
	}
	f.Close()
	second.Close()
	a, _ := os.ReadFile(filepath.Join(dir, "a.jsonl"))
	b, _ := os.ReadFile(filepath.Join(dir, "b.jsonl"))
	if n != 17 || string(a) != string(b) {
		t.Fatalf("replayed %d events; logs differ:\n%s\n---\n%s", n, a, b)
	}
	if err := Dispatch(second, []byte(`{"event_type":"something_new"}`)); err != nil {
		t.Fatalf("unknown types are ignored: %v", err)
	}
}
