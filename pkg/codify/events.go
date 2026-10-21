package codify

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jctanner/markov/pkg/callback"
)

// CodifyWorkflow names the virtual workflow codify's own phases are reported as: its steps are
// live, retro, candidate, judge and promote. The target workflow's own events are forwarded
// unchanged, so a viewer sees both.
const CodifyWorkflow = "codify"

// emitter sends codify's events to the configured callbacks.
type emitter struct {
	cbs   []callback.Callback
	runID string
	mu    sync.Mutex
}

func newEmitter(cbs []callback.Callback) *emitter {
	return &emitter{cbs: cbs, runID: uuid.New().String()[:8]}
}

func (e *emitter) fire(fn func(callback.Callback) error) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, cb := range e.cbs {
		if err := fn(cb); err != nil {
			log.Printf("callback error: %v", err)
		}
	}
}

func (e *emitter) header(ty string) callback.EventHeader {
	return callback.EventHeader{Timestamp: time.Now(), RunID: e.runID, EventType: ty}
}

func (e *emitter) runStarted(target string, vars map[string]any) {
	e.fire(func(cb callback.Callback) error {
		return cb.OnRunStarted(callback.RunStartedEvent{EventHeader: e.header("run_started"), WorkflowName: target, Vars: vars})
	})
}

func (e *emitter) runFinished(target string, start time.Time, err error) {
	d := time.Since(start).Seconds()
	e.fire(func(cb callback.Callback) error {
		if err != nil {
			return cb.OnRunFailed(callback.RunFailedEvent{EventHeader: e.header("run_failed"), WorkflowName: target, Error: err.Error(), Duration: d})
		}
		return cb.OnRunCompleted(callback.RunCompletedEvent{EventHeader: e.header("run_completed"), WorkflowName: target, Duration: d})
	})
}

// phase is one of codify's own steps (live, retro, candidate, judge, promote), reported as a step
// of the virtual codify workflow.
type phase struct {
	e     *emitter
	step  string
	ty    string
	start time.Time
}

func (e *emitter) begin(step, ty string, params map[string]any) *phase {
	p := &phase{e: e, step: step, ty: ty, start: time.Now()}
	e.fire(func(cb callback.Callback) error {
		return cb.OnStepStarted(callback.StepStartedEvent{EventHeader: e.header("step_started"), WorkflowName: CodifyWorkflow, StepName: step, StepType: ty, ResolvedType: ty, Params: params})
	})
	return p
}

func (p *phase) end(output map[string]any, failure string) {
	if p == nil || p.e == nil {
		return
	}
	d := time.Since(p.start).Seconds()
	p.e.fire(func(cb callback.Callback) error {
		if failure != "" {
			return cb.OnStepFailed(callback.StepFailedEvent{EventHeader: p.e.header("step_failed"), WorkflowName: CodifyWorkflow, StepName: p.step, StepType: p.ty, Error: failure, Duration: d})
		}
		return cb.OnStepCompleted(callback.StepCompletedEvent{EventHeader: p.e.header("step_completed"), WorkflowName: CodifyWorkflow, StepName: p.step, StepType: p.ty, Output: output, Duration: d})
	})
}

// progress is a progress sink for an agent step of codify (the retro agent, the judge): its
// claude stream goes out as step_progress events.
func (e *emitter) progress(step string) func(kind string, data map[string]any) {
	return func(kind string, data map[string]any) {
		e.fire(func(cb callback.Callback) error {
			return cb.OnStepProgress(callback.StepProgressEvent{EventHeader: e.header("step_progress"), WorkflowName: CodifyWorkflow, StepName: step, StepType: "claude", Kind: kind, Data: data})
		})
	}
}

// forward passes on one event of a child run (the target workflow, live or candidate). Its step
// and progress events go through unchanged; its own start and end become sub-run events of the
// codify phase that ran it, so they do not read as the end of the whole codify run.
func (e *emitter) forward(raw []byte, parentStep string) {
	if e == nil || len(e.cbs) == 0 {
		return
	}
	var ev struct {
		Timestamp    time.Time `json:"timestamp"`
		RunID        string    `json:"run_id"`
		EventType    string    `json:"event_type"`
		WorkflowName string    `json:"workflow_name"`
		Error        string    `json:"error"`
		Duration     float64   `json:"duration_seconds"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return
	}
	hdr := func(ty string) callback.EventHeader {
		return callback.EventHeader{Timestamp: ev.Timestamp, RunID: ev.RunID, EventType: ty}
	}
	switch ev.EventType {
	case "run_started":
		e.fire(func(cb callback.Callback) error {
			return cb.OnSubRunStarted(callback.SubRunStartedEvent{EventHeader: hdr("sub_run_started"), ParentRunID: e.runID, ParentStep: parentStep, WorkflowName: ev.WorkflowName})
		})
	case "run_completed":
		e.fire(func(cb callback.Callback) error {
			return cb.OnSubRunCompleted(callback.SubRunCompletedEvent{EventHeader: hdr("sub_run_completed"), ParentRunID: e.runID, ParentStep: parentStep, WorkflowName: ev.WorkflowName, Duration: ev.Duration})
		})
	case "run_failed":
		e.fire(func(cb callback.Callback) error {
			return cb.OnSubRunFailed(callback.SubRunFailedEvent{EventHeader: hdr("sub_run_failed"), ParentRunID: e.runID, ParentStep: parentStep, WorkflowName: ev.WorkflowName, Error: ev.Error, Duration: ev.Duration})
		})
	default:
		e.fire(func(cb callback.Callback) error { return callback.Dispatch(cb, raw) })
	}
}

// follow passes on the complete lines appended to path until stop closes, then the rest.
func follow(path string, stop <-chan struct{}, fn func([]byte)) {
	var f *os.File
	var r *bufio.Reader
	var partial []byte
	drain := func() {
		if f == nil {
			var err error
			if f, err = os.Open(path); err != nil {
				return
			}
			r = bufio.NewReaderSize(f, 1<<20)
		}
		for {
			line, err := r.ReadBytes('\n')
			if err == io.EOF {
				partial = append(partial, line...)
				return
			}
			if err != nil {
				return
			}
			fn(append(partial, line[:len(line)-1]...))
			partial = nil
		}
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			drain()
			if f != nil {
				f.Close()
			}
			return
		case <-tick.C:
			drain()
		}
	}
}
