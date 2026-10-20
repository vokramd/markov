package callback

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

type JSONLCallback struct {
	f   *os.File
	enc *json.Encoder
	mu  sync.Mutex
}

func NewJSONLCallback(path string) (*JSONLCallback, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("opening callback file: %w", err)
	}
	return &JSONLCallback{
		f:   f,
		enc: json.NewEncoder(f),
	}, nil
}

func (j *JSONLCallback) write(event any) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.enc.Encode(event); err != nil {
		return err
	}
	return j.f.Sync()
}

func (j *JSONLCallback) OnRunStarted(event RunStartedEvent) error             { return j.write(event) }
func (j *JSONLCallback) OnRunCompleted(event RunCompletedEvent) error         { return j.write(event) }
func (j *JSONLCallback) OnRunFailed(event RunFailedEvent) error               { return j.write(event) }
func (j *JSONLCallback) OnRunPaused(event RunPausedEvent) error               { return j.write(event) }
func (j *JSONLCallback) OnRunResumed(event RunResumedEvent) error             { return j.write(event) }
func (j *JSONLCallback) OnStepStarted(event StepStartedEvent) error           { return j.write(event) }
func (j *JSONLCallback) OnStepCompleted(event StepCompletedEvent) error       { return j.write(event) }
func (j *JSONLCallback) OnStepFailed(event StepFailedEvent) error             { return j.write(event) }
func (j *JSONLCallback) OnStepSkipped(event StepSkippedEvent) error           { return j.write(event) }
func (j *JSONLCallback) OnStepProgress(event StepProgressEvent) error         { return j.write(event) }
func (j *JSONLCallback) OnDebug(event DebugEvent) error                       { return j.write(event) }
func (j *JSONLCallback) OnJobCreated(event JobCreatedEvent) error             { return j.write(event) }
func (j *JSONLCallback) OnGateEvaluated(event GateEvaluatedEvent) error       { return j.write(event) }
func (j *JSONLCallback) OnSubRunStarted(event SubRunStartedEvent) error       { return j.write(event) }
func (j *JSONLCallback) OnSubRunCompleted(event SubRunCompletedEvent) error   { return j.write(event) }
func (j *JSONLCallback) OnSubRunFailed(event SubRunFailedEvent) error         { return j.write(event) }
func (j *JSONLCallback) OnWorkflowReloaded(event WorkflowReloadedEvent) error { return j.write(event) }

func (j *JSONLCallback) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.f.Close()
}
