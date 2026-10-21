package callback

import (
	"encoding/json"
	"fmt"
)

// Dispatch delivers one event, as read back from an event log (JSON), to cb by its event_type,
// so one process can pass on another's events (markov codify forwards its child runs'). Unknown
// event types are ignored.
func Dispatch(cb Callback, raw []byte) error {
	var head EventHeader
	if err := json.Unmarshal(raw, &head); err != nil {
		return fmt.Errorf("event: %w", err)
	}
	decode := func(v any) error { return json.Unmarshal(raw, v) }
	switch head.EventType {
	case "run_started":
		var e RunStartedEvent
		if err := decode(&e); err != nil {
			return err
		}
		return cb.OnRunStarted(e)
	case "run_completed":
		var e RunCompletedEvent
		if err := decode(&e); err != nil {
			return err
		}
		return cb.OnRunCompleted(e)
	case "run_failed":
		var e RunFailedEvent
		if err := decode(&e); err != nil {
			return err
		}
		return cb.OnRunFailed(e)
	case "run_paused":
		var e RunPausedEvent
		if err := decode(&e); err != nil {
			return err
		}
		return cb.OnRunPaused(e)
	case "run_resumed":
		var e RunResumedEvent
		if err := decode(&e); err != nil {
			return err
		}
		return cb.OnRunResumed(e)
	case "step_started":
		var e StepStartedEvent
		if err := decode(&e); err != nil {
			return err
		}
		return cb.OnStepStarted(e)
	case "step_completed":
		var e StepCompletedEvent
		if err := decode(&e); err != nil {
			return err
		}
		return cb.OnStepCompleted(e)
	case "step_failed":
		var e StepFailedEvent
		if err := decode(&e); err != nil {
			return err
		}
		return cb.OnStepFailed(e)
	case "step_skipped":
		var e StepSkippedEvent
		if err := decode(&e); err != nil {
			return err
		}
		return cb.OnStepSkipped(e)
	case "step_progress":
		var e StepProgressEvent
		if err := decode(&e); err != nil {
			return err
		}
		return cb.OnStepProgress(e)
	case "debug":
		var e DebugEvent
		if err := decode(&e); err != nil {
			return err
		}
		return cb.OnDebug(e)
	case "job_created":
		var e JobCreatedEvent
		if err := decode(&e); err != nil {
			return err
		}
		return cb.OnJobCreated(e)
	case "gate_evaluated":
		var e GateEvaluatedEvent
		if err := decode(&e); err != nil {
			return err
		}
		return cb.OnGateEvaluated(e)
	case "sub_run_started":
		var e SubRunStartedEvent
		if err := decode(&e); err != nil {
			return err
		}
		return cb.OnSubRunStarted(e)
	case "sub_run_completed":
		var e SubRunCompletedEvent
		if err := decode(&e); err != nil {
			return err
		}
		return cb.OnSubRunCompleted(e)
	case "sub_run_failed":
		var e SubRunFailedEvent
		if err := decode(&e); err != nil {
			return err
		}
		return cb.OnSubRunFailed(e)
	case "workflow_reloaded":
		var e WorkflowReloadedEvent
		if err := decode(&e); err != nil {
			return err
		}
		return cb.OnWorkflowReloaded(e)
	}
	return nil
}
