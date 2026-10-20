package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jctanner/markov/pkg/callback"
	"github.com/jctanner/markov/pkg/executor"
	"github.com/jctanner/markov/pkg/jsonvalue"
	"github.com/jctanner/markov/pkg/parser"
	"github.com/jctanner/markov/pkg/source"
	"github.com/jctanner/markov/pkg/state"
	"github.com/jctanner/markov/pkg/template"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Engine struct {
	// file is the current workflow definition. A reload_workflow step replaces it; each workflow
	// execution keeps the definition it started with (see fileFor), so only sub-workflow calls
	// made after a reload see the new one.
	file       atomic.Pointer[parser.WorkflowFile]
	store      state.Store
	tmpl       *template.Engine
	executors  map[string]executor.Executor
	k8s        kubernetes.Interface
	restConfig *rest.Config
	forks      int
	Verbose    bool
	RunID      string
	SourcePath string
	// SourceExcludes contains local state-store paths that must not become part
	// of the workflow source fingerprint.
	SourceExcludes      []string
	SourceIntegrityMode SourceIntegrityMode
	// AllowReload lets reload_workflow steps replace the workflow definition (--allow-reload).
	AllowReload bool
	// EventLogs are the absolute paths of the run's jsonl callback files, exposed to the workflow
	// as markov_event_logs.
	EventLogs []string
	// Loader parses a workflow source for reload_workflow; nil uses parser.ParseFile.
	Loader       func(path string) (*parser.WorkflowFile, error)
	digestMu     sync.Mutex
	sourceDigest string
	// rootRunID is the top-level run, which reloads are recorded against.
	rootRunID string
	reloadMu  sync.Mutex
	reloads   int
	callbacks []callback.Callback
	debugger  *Debugger
	// Rewind and RewindChanged make ResumeWithVars re-run from a step (see rewind.go).
	Rewind        []RewindTarget
	RewindChanged bool
}

// SourceIntegrityMode controls behavior when source files differ at resume.
type SourceIntegrityMode string

const (
	SourceIntegrityWarn   SourceIntegrityMode = "warn"
	SourceIntegrityStrict SourceIntegrityMode = "strict"
	SourceIntegrityOff    SourceIntegrityMode = "off"
)

// ParseSourceIntegrityMode validates a source-integrity CLI value.
func ParseSourceIntegrityMode(value string) (SourceIntegrityMode, error) {
	mode := SourceIntegrityMode(strings.ToLower(strings.TrimSpace(value)))
	if mode == "" {
		return SourceIntegrityWarn, nil
	}
	switch mode {
	case SourceIntegrityWarn, SourceIntegrityStrict, SourceIntegrityOff:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid source integrity mode %q (want warn, strict, or off)", value)
	}
}

// PauseError signals that a gate intentionally stopped the workflow. It is
// distinct from a step failure so callers can preserve a resumable paused run.
type PauseError struct {
	WorkflowName string
	StepName     string
	FiredRules   []string
	Facts        map[string]any
}

func (e *PauseError) Error() string {
	return fmt.Sprintf("paused by gate step %q", e.StepName)
}

func pauseError(err error) (*PauseError, bool) {
	var paused *PauseError
	return paused, errors.As(err, &paused)
}

// IsPaused reports whether execution stopped at a gate rather than failed.
func IsPaused(err error) bool {
	_, ok := pauseError(err)
	return ok
}

func New(file *parser.WorkflowFile, store state.Store, executors map[string]executor.Executor) *Engine {
	if executors == nil {
		executors = map[string]executor.Executor{}
	}
	if _, ok := executors["jev"]; !ok {
		executors["jev"] = executor.NewJev(file.Connections)
	}
	forks := file.Forks
	if forks <= 0 {
		forks = 5
	}
	e := &Engine{
		tmpl:      template.New(),
		executors: executors,
		forks:     forks,
	}
	e.file.Store(file)
	if _, ok := executors["reload_workflow"]; !ok {
		executors["reload_workflow"] = &reloadExecutor{e: e}
	}
	e.store = &hashingStore{Store: store, e: e}
	return e
}

func (e *Engine) SetK8sClient(client kubernetes.Interface, cfg *rest.Config) {
	e.k8s = client
	e.restConfig = cfg
}

func (e *Engine) SetCallbacks(cbs []callback.Callback) {
	e.callbacks = cbs
}

func (e *Engine) CloseCallbacks() {
	for _, cb := range e.callbacks {
		if err := cb.Close(); err != nil {
			log.Printf("callback close error: %v", err)
		}
	}
}

func (e *Engine) fireEvent(fn func(callback.Callback) error) {
	e.verbose("firing callback event to %d callback(s)", len(e.callbacks))
	for _, cb := range e.callbacks {
		if err := fn(cb); err != nil {
			log.Printf("callback error: %v", err)
		}
	}
}

func (e *Engine) verbose(format string, args ...any) {
	if e.Verbose {
		log.Printf(format, args...)
	}
}

func (e *Engine) Run(ctx context.Context, workflowName string, vars map[string]any) (string, error) {
	file := e.current()
	if workflowName == "" {
		workflowName = file.Entrypoint
	}

	wf := file.GetWorkflow(workflowName)
	if wf == nil {
		return "", fmt.Errorf("workflow %q not found", workflowName)
	}
	if err := e.checkReloadAllowed(file, workflowName); err != nil {
		return "", err
	}
	sourceDigest, err := e.currentSourceDigest()
	if err != nil {
		return "", err
	}
	e.setDigest(sourceDigest)

	runCtx := e.buildContext(vars, wf.Vars)

	runID := e.RunID
	if runID == "" {
		runID = uuid.New().String()[:8]
	}
	e.rootRunID = runID
	if e.debugger != nil {
		e.debugger.noteRun(runID)
	}
	// Built-in: the run's own ID, for unique output paths. It is saved with the run's vars, so a
	// resume sees the same value.
	runCtx["markov_run_id"] = runID
	runCtx["markov_event_logs"] = e.eventLogs()
	varsJSON, _ := json.Marshal(runCtx)

	run := &state.Run{
		RunID:        runID,
		WorkflowFile: e.SourcePath,
		SourceDigest: sourceDigest,
		Entrypoint:   workflowName,
		Status:       state.RunRunning,
		VarsJSON:     string(varsJSON),
		StartedAt:    time.Now(),
	}
	if err := e.store.CreateRun(ctx, run); err != nil {
		return "", fmt.Errorf("creating run: %w", err)
	}

	e.fireEvent(func(cb callback.Callback) error {
		return cb.OnRunStarted(callback.RunStartedEvent{
			EventHeader:  callback.EventHeader{Timestamp: run.StartedAt, RunID: runID, EventType: "run_started"},
			WorkflowName: workflowName,
			WorkflowFile: e.SourcePath,
			SourceDigest: sourceDigest,
			Vars:         runCtx,
			Forks:        e.forks,
			Namespace:    file.Namespace,
		})
	})

	log.Printf("[run:%s] starting workflow %q", runID, workflowName)
	e.verbose("[run:%s]   forks: %d", runID, e.forks)
	e.verbose("[run:%s]   namespace: %s", runID, file.Namespace)
	e.verbose("[run:%s]   vars: %s", runID, string(varsJSON))
	e.verbose("[run:%s]   steps: %d", runID, len(wf.Steps))

	err = e.executeWorkflow(withFile(ctx, file), runID, wf, runCtx)

	now := time.Now()
	duration := now.Sub(run.StartedAt).Seconds()
	if paused, ok := pauseError(err); ok {
		run.Status = state.RunPaused
		run.CompletedAt = nil
		log.Printf("[run:%s] workflow %q paused at gate %q", runID, workflowName, paused.StepName)
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnRunPaused(callback.RunPausedEvent{
				EventHeader:  callback.EventHeader{Timestamp: now, RunID: runID, EventType: "run_paused"},
				WorkflowName: workflowName,
				StepName:     paused.StepName,
				FiredRules:   paused.FiredRules,
				Facts:        paused.Facts,
				Duration:     duration,
			})
		})
	} else if err != nil {
		run.CompletedAt = &now
		run.Status = state.RunFailed
		log.Printf("[run:%s] workflow %q failed: %v", runID, workflowName, err)
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnRunFailed(callback.RunFailedEvent{
				EventHeader:  callback.EventHeader{Timestamp: now, RunID: runID, EventType: "run_failed"},
				WorkflowName: workflowName,
				Error:        err.Error(),
				Duration:     duration,
			})
		})
	} else {
		run.CompletedAt = &now
		run.Status = state.RunCompleted
		log.Printf("[run:%s] workflow %q completed", runID, workflowName)
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnRunCompleted(callback.RunCompletedEvent{
				EventHeader:  callback.EventHeader{Timestamp: now, RunID: runID, EventType: "run_completed"},
				WorkflowName: workflowName,
				Duration:     duration,
			})
		})
	}
	e.store.UpdateRun(ctx, run)

	return runID, err
}

func (e *Engine) Resume(ctx context.Context, runID string) error {
	return e.ResumeWithVars(ctx, runID, nil)
}

// ResumeWithVars resumes a failed or paused run. Paused runs require at least
// one explicit override so an operator records the input that releases the
// gate instead of accidentally continuing it unchanged.
func (e *Engine) ResumeWithVars(ctx context.Context, runID string, vars map[string]any) error {
	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if e.debugger != nil {
		e.debugger.noteRun(runID)
	}
	rewinding := len(e.Rewind) > 0 || e.RewindChanged
	if run.Status != state.RunFailed && run.Status != state.RunPaused && !(rewinding && run.Status == state.RunCompleted) {
		return fmt.Errorf("run %q has status %q; only failed or paused runs can be resumed (a completed run can be rewound with --rewind)", runID, run.Status)
	}
	if run.Status == state.RunPaused && len(vars) == 0 {
		return fmt.Errorf("paused run %q requires at least one --var override to resume", runID)
	}
	reloads, err := e.store.GetReloads(ctx, runID)
	if err != nil {
		return err
	}
	if len(reloads) > 0 {
		last := reloads[len(reloads)-1]
		return fmt.Errorf("run %q reloaded its workflow %d time(s) while running (last from %s, digest %s), so its completed steps came from more than one definition; it can't be resumed, start a new run", runID, len(reloads), last.Source, last.NewDigest)
	}
	file := e.current()
	if err := e.checkReloadAllowed(file, run.Entrypoint); err != nil {
		return err
	}
	check, err := e.checkSourceIntegrity(ctx, run)
	if err != nil {
		return err
	}
	e.setDigest(run.SourceDigest)
	e.rootRunID = runID

	wf := file.GetWorkflow(run.Entrypoint)
	if wf == nil {
		return fmt.Errorf("workflow %q not found", run.Entrypoint)
	}

	changed, rewound, err := e.prepareResume(ctx, run)
	if err != nil {
		return err
	}

	var storedVars map[string]any
	decodeState(run.VarsJSON, &storedVars)
	if storedVars == nil {
		storedVars = make(map[string]any)
	}
	runCtx := make(map[string]any, len(storedVars)+len(vars))
	for k, v := range storedVars {
		runCtx[k] = v
	}

	steps, err := e.store.GetSteps(ctx, runID)
	if err != nil {
		return err
	}
	for _, s := range steps {
		if strings.HasPrefix(s.StepName, "__jev__/") {
			continue
		}
		if s.Status == state.StepCompleted {
			if isSetFactStep(wf, s.StepName) && s.OutputJSON != "" {
				var facts map[string]any
				decodeState(s.OutputJSON, &facts)
				for k, v := range facts {
					runCtx[k] = v
				}
			} else if isGateStep(wf, s.StepName) && s.OutputJSON != "" {
				var output map[string]any
				decodeState(s.OutputJSON, &output)
				if facts, ok := output["facts"].(map[string]any); ok {
					for k, v := range facts {
						runCtx[k] = v
					}
				}
			} else if s.ArtifactsJSON != "" {
				var stepData map[string]any
				decodeState(s.ArtifactsJSON, &stepData)
				runCtx[s.StepName] = stepData
			} else if s.OutputJSON != "" {
				var output map[string]any
				decodeState(s.OutputJSON, &output)
				runCtx[s.StepName] = output
			}
		}
	}
	for k, v := range vars {
		storedVars[k] = v
		runCtx[k] = v
	}
	// The logs of this process, not the ones the run was started with.
	runCtx["markov_event_logs"] = e.eventLogs()
	varsJSON, _ := json.Marshal(storedVars)
	run.VarsJSON = string(varsJSON)

	run.Status = state.RunRunning
	run.CompletedAt = nil
	e.store.UpdateRun(ctx, run)

	completedCount := 0
	for _, s := range steps {
		if s.Status == state.StepCompleted {
			completedCount++
		}
	}
	e.fireEvent(func(cb callback.Callback) error {
		return cb.OnRunResumed(callback.RunResumedEvent{
			EventHeader:          callback.EventHeader{Timestamp: time.Now(), RunID: runID, EventType: "run_resumed"},
			WorkflowName:         run.Entrypoint,
			CompletedSteps:       completedCount,
			RemainingSteps:       len(wf.Steps) - completedCount,
			SourceIntegrityMode:  check.Mode,
			ExpectedSourceDigest: check.ExpectedDigest,
			ObservedSourceDigest: check.ObservedDigest,
			SourceDrifted:        check.SourceDrifted,
			ChangedSteps:         changedStepEvents(e, changed),
			Rewound:              rewound,
		})
	})

	log.Printf("[run:%s] resuming workflow %q", runID, run.Entrypoint)

	resumeStart := time.Now()
	err = e.executeWorkflow(ctx, runID, wf, runCtx)

	now := time.Now()
	duration := now.Sub(resumeStart).Seconds()
	if paused, ok := pauseError(err); ok {
		run.Status = state.RunPaused
		run.CompletedAt = nil
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnRunPaused(callback.RunPausedEvent{
				EventHeader:  callback.EventHeader{Timestamp: now, RunID: runID, EventType: "run_paused"},
				WorkflowName: run.Entrypoint,
				StepName:     paused.StepName,
				FiredRules:   paused.FiredRules,
				Facts:        paused.Facts,
				Duration:     duration,
			})
		})
	} else if err != nil {
		run.CompletedAt = &now
		run.Status = state.RunFailed
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnRunFailed(callback.RunFailedEvent{
				EventHeader:  callback.EventHeader{Timestamp: now, RunID: runID, EventType: "run_failed"},
				WorkflowName: run.Entrypoint,
				Error:        err.Error(),
				Duration:     duration,
			})
		})
	} else {
		run.CompletedAt = &now
		run.Status = state.RunCompleted
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnRunCompleted(callback.RunCompletedEvent{
				EventHeader:  callback.EventHeader{Timestamp: now, RunID: runID, EventType: "run_completed"},
				WorkflowName: run.Entrypoint,
				Duration:     duration,
			})
		})
	}
	e.store.UpdateRun(ctx, run)

	return err
}

func (e *Engine) currentSourceDigest() (string, error) {
	if e.SourcePath == "" {
		return "", nil
	}
	fingerprint, err := source.Tree(e.SourcePath, e.SourceExcludes)
	if err != nil {
		return "", fmt.Errorf("fingerprinting workflow source: %w", err)
	}
	return fingerprint.Digest, nil
}

func (e *Engine) sourceIntegrityMode() SourceIntegrityMode {
	if e.SourceIntegrityMode == "" {
		return SourceIntegrityWarn
	}
	return e.SourceIntegrityMode
}

func (e *Engine) checkSourceIntegrity(ctx context.Context, run *state.Run) (*state.SourceCheck, error) {
	mode := e.sourceIntegrityMode()
	check := &state.SourceCheck{
		RunID:          run.RunID,
		CheckedAt:      time.Now(),
		Mode:           string(mode),
		ExpectedDigest: run.SourceDigest,
	}
	if mode != SourceIntegrityOff {
		observed, err := e.currentSourceDigest()
		if err != nil {
			if saveErr := e.store.SaveSourceCheck(ctx, check); saveErr != nil {
				return nil, fmt.Errorf("fingerprinting workflow source: %w (also saving source check: %v)", err, saveErr)
			}
			return nil, err
		}
		check.ObservedDigest = observed
		check.SourceDrifted = run.SourceDigest != "" && observed != run.SourceDigest
	}
	if err := e.store.SaveSourceCheck(ctx, check); err != nil {
		return nil, err
	}
	if mode == SourceIntegrityStrict {
		if run.SourceDigest == "" {
			return nil, fmt.Errorf("cannot resume run %q in strict source-integrity mode: it has no recorded source digest", run.RunID)
		}
		if check.SourceDrifted {
			return nil, fmt.Errorf("cannot resume run %q: workflow source changed (recorded digest %s, current digest %s)", run.RunID, run.SourceDigest, check.ObservedDigest)
		}
	}
	if check.SourceDrifted && mode == SourceIntegrityWarn {
		log.Printf("[run:%s] workflow source drift accepted (recorded digest %s, current digest %s)", run.RunID, run.SourceDigest, check.ObservedDigest)
	}
	return check, nil
}

func (e *Engine) executeWorkflow(ctx context.Context, runID string, wf *parser.Workflow, runCtx map[string]any) error {
	// Built-in: the (sub-)run executing these steps; markov_run_id stays the top-level run.
	runCtx["markov_current_run_id"] = runID
	mainErr := e.executeSteps(ctx, runID, wf, runCtx)
	if IsPaused(mainErr) {
		// A pause is deliberately nonterminal. In particular, cleanup such as
		// unlocking a resource must not run while a human approval is pending.
		return mainErr
	}

	var errs []error
	if mainErr != nil {
		errs = append(errs, mainErr)
		errs = append(errs, e.executeTerminalSteps(ctx, runID, wf, "rescue", wf.Rescue, runCtx)...)
	}
	errs = append(errs, e.executeTerminalSteps(ctx, runID, wf, "always", wf.Always, runCtx)...)
	return errors.Join(errs...)
}

func (e *Engine) executeSteps(ctx context.Context, runID string, wf *parser.Workflow, runCtx map[string]any) error {
	for _, step := range wf.Steps {
		if err := e.executeStep(ctx, runID, wf.Name, step, runCtx); err != nil {
			return fmt.Errorf("step %q: %w", step.Name, err)
		}
	}
	return nil
}

// executeTerminalSteps runs every handler in a workflow lifecycle section.
// Terminal steps intentionally bypass completed-step deduplication: a resumed
// run that fails again must perform its teardown again.
func (e *Engine) executeTerminalSteps(ctx context.Context, runID string, wf *parser.Workflow, section string, steps []parser.Step, runCtx map[string]any) []error {
	var errs []error
	for _, step := range steps {
		stateName := section + "/" + step.Name
		if err := e.executeStepWithOptions(ctx, runID, wf.Name, step, stateName, runCtx, true); err != nil {
			if IsPaused(err) {
				errs = append(errs, fmt.Errorf("%s step %q may not pause", section, step.Name))
			} else {
				errs = append(errs, fmt.Errorf("%s step %q: %w", section, step.Name, err))
			}
		}
	}
	return errs
}

func (e *Engine) executeStep(ctx context.Context, runID string, workflowName string, step parser.Step, runCtx map[string]any) error {
	return e.executeStepWithStateName(ctx, runID, workflowName, step, step.Name, runCtx)
}

func (e *Engine) executeStepWithStateName(ctx context.Context, runID string, workflowName string, step parser.Step, stateStepName string, runCtx map[string]any) error {
	return e.executeStepWithOptions(ctx, runID, workflowName, step, stateStepName, runCtx, false)
}

func (e *Engine) executeStepWithOptions(ctx context.Context, runID string, workflowName string, step parser.Step, stateStepName string, runCtx map[string]any, force bool) error {
	if e.debugger == nil {
		return e.executeStepCore(ctx, runID, workflowName, step, stateStepName, runCtx, force)
	}
	before, _ := e.store.GetStep(ctx, runID, workflowName, stateStepName)
	if err := e.executeStepCore(ctx, runID, workflowName, step, stateStepName, runCtx, force); err != nil {
		return err
	}
	// Pause "after" only when this call actually completed the step (not when it was skipped or
	// replayed from a previous run).
	after, _ := e.store.GetStep(ctx, runID, workflowName, stateStepName)
	if after == nil || after.Status != state.StepCompleted {
		return nil
	}
	if before != nil && before.Status == state.StepCompleted && before.CompletedAt != nil && after.CompletedAt != nil && !after.CompletedAt.After(*before.CompletedAt) {
		return nil
	}
	var output map[string]any
	if after.OutputJSON != "" {
		json.Unmarshal([]byte(after.OutputJSON), &output)
	}
	return e.debugPause(ctx, runID, workflowName, step, stateStepName, phaseAfter, runCtx, output)
}

func (e *Engine) executeStepCore(ctx context.Context, runID string, workflowName string, step parser.Step, stateStepName string, runCtx map[string]any, force bool) error {
	existing, _ := e.store.GetStep(ctx, runID, workflowName, stateStepName)
	if !force && existing != nil && existing.Status == state.StepCompleted {
		log.Printf("[run:%s] skipping completed step %q", runID, step.Name)
		if existing.OutputJSON != "" {
			var output map[string]any
			decodeState(existing.OutputJSON, &output)
			if step.Type == "set_fact" {
				for k, v := range output {
					runCtx[k] = v
				}
			} else if step.Type == "gate" {
				if facts, ok := output["facts"].(map[string]any); ok {
					for k, v := range facts {
						runCtx[k] = v
					}
				}
			} else if step.Register != "" {
				runCtx[step.Register] = output
			}
		}
		return nil
	}

	if step.When != "" {
		ok, err := e.evalDecisionBool(ctx, runID, workflowName, stateStepName, step.When, runCtx, step.Timeout)
		if err != nil {
			return e.failStep(ctx, runID, workflowName, stateStepName, step.Type, time.Now(), fmt.Errorf("evaluating when: %w", err))
		}
		if !ok {
			log.Printf("[run:%s] skipping step %q (when: %q is false)", runID, step.Name, step.When)
			now := time.Now()
			e.store.SaveStep(ctx, &state.StepResult{
				RunID:        runID,
				WorkflowName: workflowName,
				StepName:     stateStepName,
				Status:       state.StepSkipped,
				StartedAt:    &now,
				CompletedAt:  &now,
			})
			e.fireEvent(func(cb callback.Callback) error {
				return cb.OnStepSkipped(callback.StepSkippedEvent{
					EventHeader:  callback.EventHeader{Timestamp: now, RunID: runID, EventType: "step_skipped"},
					WorkflowName: workflowName,
					StepName:     step.Name,
					Reason:       fmt.Sprintf("when condition %q evaluated to false", step.When),
				})
			})
			return nil
		}
	}

	// The step will run: give the debugger a chance to pause before it starts.
	if err := e.debugPause(ctx, runID, workflowName, step, stateStepName, phaseBefore, runCtx, nil); err != nil {
		return e.failStep(ctx, runID, workflowName, stateStepName, step.Type, time.Now(), err)
	}

	if step.ForEach != "" {
		feStart := time.Now()
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnStepStarted(callback.StepStartedEvent{
				EventHeader:  callback.EventHeader{Timestamp: feStart, RunID: runID, EventType: "step_started"},
				WorkflowName: workflowName,
				StepName:     step.Name,
				StepType:     step.Type,
				ResolvedType: "for_each",
			})
		})
		err := e.executeForEach(ctx, runID, workflowName, step, runCtx)
		feEnd := time.Now()
		if err != nil {
			if IsPaused(err) {
				return err
			}
			e.fireEvent(func(cb callback.Callback) error {
				return cb.OnStepFailed(callback.StepFailedEvent{
					EventHeader:  callback.EventHeader{Timestamp: feEnd, RunID: runID, EventType: "step_failed"},
					WorkflowName: workflowName,
					StepName:     step.Name,
					StepType:     step.Type,
					Error:        err.Error(),
					Duration:     feEnd.Sub(feStart).Seconds(),
				})
			})
			return err
		}
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnStepCompleted(callback.StepCompletedEvent{
				EventHeader:  callback.EventHeader{Timestamp: feEnd, RunID: runID, EventType: "step_completed"},
				WorkflowName: workflowName,
				StepName:     step.Name,
				StepType:     step.Type,
				Duration:     feEnd.Sub(feStart).Seconds(),
			})
		})
		return nil
	}

	if step.Workflow != "" {
		swStart := time.Now()
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnStepStarted(callback.StepStartedEvent{
				EventHeader:  callback.EventHeader{Timestamp: swStart, RunID: runID, EventType: "step_started"},
				WorkflowName: workflowName,
				StepName:     step.Name,
				StepType:     step.Type,
				ResolvedType: "workflow",
			})
		})
		err := e.executeSubWorkflow(ctx, runID, workflowName, step, runCtx)
		swEnd := time.Now()
		if err != nil {
			if IsPaused(err) {
				return err
			}
			e.fireEvent(func(cb callback.Callback) error {
				return cb.OnStepFailed(callback.StepFailedEvent{
					EventHeader:  callback.EventHeader{Timestamp: swEnd, RunID: runID, EventType: "step_failed"},
					WorkflowName: workflowName,
					StepName:     step.Name,
					StepType:     step.Type,
					Error:        err.Error(),
					Duration:     swEnd.Sub(swStart).Seconds(),
				})
			})
			return err
		}
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnStepCompleted(callback.StepCompletedEvent{
				EventHeader:  callback.EventHeader{Timestamp: swEnd, RunID: runID, EventType: "step_completed"},
				WorkflowName: workflowName,
				StepName:     step.Name,
				StepType:     step.Type,
				Duration:     swEnd.Sub(swStart).Seconds(),
			})
		})
		return nil
	}

	now := time.Now()

	base, mergedParams := e.fileFor(ctx).ResolveStepType(&step)

	e.fireEvent(func(cb callback.Callback) error {
		return cb.OnStepStarted(callback.StepStartedEvent{
			EventHeader:  callback.EventHeader{Timestamp: now, RunID: runID, EventType: "step_started"},
			WorkflowName: workflowName,
			StepName:     step.Name,
			StepType:     step.Type,
			ResolvedType: base,
			Params:       mergedParams,
		})
	})

	if base == "set_fact" {
		log.Printf("[run:%s] setting facts for step %q", runID, step.Name)
		if len(step.Vars) == 0 {
			return e.failStep(ctx, runID, workflowName, stateStepName, base, now, fmt.Errorf("set_fact step has no vars defined"))
		}
		facts, err := e.evalFacts(step.Vars, runCtx)
		if err != nil {
			return e.failStep(ctx, runID, workflowName, stateStepName, base, now, fmt.Errorf("evaluating facts: %w", err))
		}
		for k, v := range facts {
			runCtx[k] = v
			e.verbose("[run:%s]   %s = %v", runID, k, v)
		}
		factsJSON, _ := json.Marshal(facts)
		completed := time.Now()
		e.store.SaveStep(ctx, &state.StepResult{
			RunID:        runID,
			WorkflowName: workflowName,
			StepName:     stateStepName,
			Status:       state.StepCompleted,
			OutputJSON:   string(factsJSON),
			StartedAt:    &now,
			CompletedAt:  &completed,
		})
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnStepCompleted(callback.StepCompletedEvent{
				EventHeader:  callback.EventHeader{Timestamp: completed, RunID: runID, EventType: "step_completed"},
				WorkflowName: workflowName,
				StepName:     step.Name,
				StepType:     step.Type,
				Output:       facts,
				Duration:     completed.Sub(now).Seconds(),
			})
		})
		log.Printf("[run:%s] step %q completed", runID, step.Name)
		return nil
	}

	if base == "assert" {
		log.Printf("[run:%s] evaluating assertions for step %q", runID, step.Name)
		if len(step.That) == 0 {
			return e.failStep(ctx, runID, workflowName, stateStepName, base, now, fmt.Errorf("assert step has no conditions defined"))
		}
		for _, expr := range step.That {
			ok, err := e.evalDecisionBool(ctx, runID, workflowName, stateStepName, expr, runCtx, step.Timeout)
			if err != nil {
				return e.failStep(ctx, runID, workflowName, stateStepName, base, now, fmt.Errorf("evaluating assertion %q: %w", expr, err))
			}
			if !ok {
				msg := step.Msg
				if msg == "" {
					msg = fmt.Sprintf("assertion failed: %s", expr)
				}
				return e.failStep(ctx, runID, workflowName, stateStepName, base, now, fmt.Errorf("%s", msg))
			}
			e.verbose("[run:%s]   assert %q: ok", runID, expr)
		}
		completed := time.Now()
		e.store.SaveStep(ctx, &state.StepResult{
			RunID:        runID,
			WorkflowName: workflowName,
			StepName:     stateStepName,
			Status:       state.StepCompleted,
			StartedAt:    &now,
			CompletedAt:  &completed,
		})
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnStepCompleted(callback.StepCompletedEvent{
				EventHeader:  callback.EventHeader{Timestamp: completed, RunID: runID, EventType: "step_completed"},
				WorkflowName: workflowName,
				StepName:     step.Name,
				StepType:     step.Type,
				Duration:     completed.Sub(now).Seconds(),
			})
		})
		log.Printf("[run:%s] step %q completed", runID, step.Name)
		return nil
	}

	if base == "gate" {
		log.Printf("[run:%s] evaluating gate %q (%d rules)", runID, step.Name, len(step.Rules))
		resolvedFacts, err := e.resolveDecisionFacts(ctx, runID, workflowName, stateStepName, step.Facts, runCtx, step.Timeout)
		if err != nil {
			return e.failStep(ctx, runID, workflowName, stateStepName, base, now, err)
		}
		result, err := e.evaluateGate(e.fileFor(ctx), runID, step.Rules, resolvedFacts, runCtx)
		if err != nil {
			return e.failStep(ctx, runID, workflowName, stateStepName, base, now, fmt.Errorf("gate evaluation: %w", err))
		}

		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnGateEvaluated(callback.GateEvaluatedEvent{
				EventHeader:  callback.EventHeader{Timestamp: time.Now(), RunID: runID, EventType: "gate_evaluated"},
				WorkflowName: workflowName,
				StepName:     step.Name,
				Action:       result.Action,
				FiredRules:   result.FiredRules,
				Facts:        result.Facts,
			})
		})

		log.Printf("[run:%s] gate %q: action=%s, fired=%v", runID, step.Name, result.Action, result.FiredRules)

		outputJSON, _ := json.Marshal(map[string]any{
			"action":      result.Action,
			"fired_rules": result.FiredRules,
			"facts":       result.Facts,
		})
		completed := time.Now()
		if result.Action == "pause" {
			e.store.SaveStep(ctx, &state.StepResult{
				RunID:        runID,
				WorkflowName: workflowName,
				StepName:     stateStepName,
				Status:       state.StepPaused,
				OutputJSON:   string(outputJSON),
				StartedAt:    &now,
				CompletedAt:  &completed,
			})
			return &PauseError{
				WorkflowName: workflowName,
				StepName:     step.Name,
				FiredRules:   result.FiredRules,
				Facts:        result.Facts,
			}
		}
		e.store.SaveStep(ctx, &state.StepResult{
			RunID:        runID,
			WorkflowName: workflowName,
			StepName:     stateStepName,
			Status:       state.StepCompleted,
			OutputJSON:   string(outputJSON),
			StartedAt:    &now,
			CompletedAt:  &completed,
		})
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnStepCompleted(callback.StepCompletedEvent{
				EventHeader:  callback.EventHeader{Timestamp: completed, RunID: runID, EventType: "step_completed"},
				WorkflowName: workflowName,
				StepName:     step.Name,
				StepType:     step.Type,
				Duration:     completed.Sub(now).Seconds(),
			})
		})
		log.Printf("[run:%s] step %q completed", runID, step.Name)
		return nil
	}

	if base == "load_artifact" {
		log.Printf("[run:%s] loading artifacts for step %q", runID, step.Name)
		if len(step.Artifacts) == 0 {
			return e.failStep(ctx, runID, workflowName, stateStepName, base, now, fmt.Errorf("load_artifact step has no artifacts defined"))
		}
		artifacts, err := e.loadArtifacts(step.Artifacts, runCtx)
		if err != nil {
			return e.failStep(ctx, runID, workflowName, stateStepName, base, now, fmt.Errorf("loading artifacts: %w", err))
		}
		runCtx[step.Name] = map[string]any{"artifacts": artifacts}
		e.verbose("[run:%s]   loaded %d artifact(s)", runID, len(artifacts))
		completed := time.Now()
		e.store.SaveStep(ctx, &state.StepResult{
			RunID:        runID,
			WorkflowName: workflowName,
			StepName:     stateStepName,
			Status:       state.StepCompleted,
			StartedAt:    &now,
			CompletedAt:  &completed,
		})
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnStepCompleted(callback.StepCompletedEvent{
				EventHeader:  callback.EventHeader{Timestamp: completed, RunID: runID, EventType: "step_completed"},
				WorkflowName: workflowName,
				StepName:     step.Name,
				StepType:     step.Type,
				Duration:     completed.Sub(now).Seconds(),
			})
		})
		log.Printf("[run:%s] step %q completed", runID, step.Name)
		return nil
	}

	e.store.SaveStep(ctx, &state.StepResult{
		RunID:        runID,
		WorkflowName: workflowName,
		StepName:     stateStepName,
		Status:       state.StepRunning,
		StartedAt:    &now,
	})

	log.Printf("[run:%s] executing step %q (type: %s)", runID, step.Name, step.Type)
	e.verbose("[run:%s]   resolved type: %s -> %s", runID, step.Type, base)

	renderedParams, err := e.tmpl.RenderMap(mergedParams, runCtx)
	if err != nil {
		return e.failStep(ctx, runID, workflowName, stateStepName, base, now, fmt.Errorf("rendering params: %w", err))
	}
	if base == "script_exec" {
		renderedParams["_script_dir"] = e.fileFor(ctx).ScriptDir
	}

	if base == "k8s_job" {
		e.injectJobMeta(renderedParams, runID, workflowName, step.Name)
		e.verbose("[run:%s]   job name: %s", runID, renderedParams["_job_name"])
		e.verbose("[run:%s]   image: %s", runID, renderedParams["image"])
		if cmd, ok := renderedParams["command"]; ok {
			e.verbose("[run:%s]   command: %v", runID, cmd)
		}
		if args, ok := renderedParams["args"]; ok {
			e.verbose("[run:%s]   args: %v", runID, args)
		}
		if ns, ok := renderedParams["namespace"]; ok {
			e.verbose("[run:%s]   namespace: %s", runID, ns)
		}
	} else {
		for k, v := range renderedParams {
			e.verbose("[run:%s]   param %s: %v", runID, k, v)
		}
	}

	exec, ok := e.executors[base]
	if !ok {
		return e.failStep(ctx, runID, workflowName, stateStepName, base, now, fmt.Errorf("no executor for type %q", base))
	}

	var execCtx context.Context
	var cancel context.CancelFunc
	if step.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(step.Timeout)*time.Second)
		e.verbose("[run:%s]   timeout: %ds", runID, step.Timeout)
	} else {
		execCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	if k8sExec, ok := exec.(*executor.K8sJob); ok {
		stepName := step.Name
		stepType := step.Type
		k8sExec.SetOnJobCreated(func(info executor.K8sJobInfo) {
			e.fireEvent(func(cb callback.Callback) error {
				return cb.OnJobCreated(callback.JobCreatedEvent{
					EventHeader:  callback.EventHeader{Timestamp: time.Now(), RunID: runID, EventType: "job_created"},
					WorkflowName: workflowName,
					StepName:     stepName,
					StepType:     stepType,
					JobName:      info.JobName,
					Namespace:    info.Namespace,
					PodSelector:  fmt.Sprintf("job-name=%s", info.JobName),
				})
			})
		})
		defer k8sExec.SetOnJobCreated(nil)
	}
	// k8s_job_wait watches a Job something else created. Report it the same way as soon as the
	// watch starts, so callback consumers (markovd's live log view) can follow the Job while it
	// runs instead of only seeing its name in the output when it finishes.
	if base == "k8s_job_wait" {
		if jobName, _ := renderedParams["job_name"].(string); jobName != "" {
			namespace, _ := renderedParams["namespace"].(string)
			if namespace == "" {
				namespace = e.fileFor(ctx).Namespace
			}
			e.fireEvent(func(cb callback.Callback) error {
				return cb.OnJobCreated(callback.JobCreatedEvent{
					EventHeader:  callback.EventHeader{Timestamp: time.Now(), RunID: runID, EventType: "job_created"},
					WorkflowName: workflowName,
					StepName:     step.Name,
					StepType:     step.Type,
					JobName:      jobName,
					Namespace:    namespace,
					PodSelector:  fmt.Sprintf("job-name=%s", jobName),
				})
			})
		}
	}

	execCtx = executor.WithProgress(execCtx, func(kind string, data map[string]any) {
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnStepProgress(callback.StepProgressEvent{
				EventHeader:  callback.EventHeader{Timestamp: time.Now(), RunID: runID, EventType: "step_progress"},
				WorkflowName: workflowName,
				StepName:     step.Name,
				StepType:     step.Type,
				Kind:         kind,
				Data:         data,
			})
		})
	})

	if base == "reload_workflow" {
		execCtx = withStepInfo(execCtx, stepInfo{runID: runID, workflow: workflowName, step: step.Name, stepType: step.Type})
	}

	result, err := exec.Execute(execCtx, renderedParams)
	var output map[string]any
	if result != nil {
		output = result.Output
	}
	if step.FailedWhen != "" {
		err = e.evalFailedWhen(step, output, err, runCtx)
	}
	if err != nil {
		if !step.IgnoreErrors {
			return e.failStep(ctx, runID, workflowName, stateStepName, base, now, err)
		}
		output = ignoredFailure(output, err)
		log.Printf("[run:%s] step %q failed, ignored (ignore_errors): %v", runID, step.Name, err)
	}
	if step.Register != "" {
		runCtx[step.Register] = output
		e.verbose("[run:%s]   registered %q: %v", runID, step.Register, output)
	}

	if len(step.Artifacts) > 0 {
		artifacts, err := e.loadArtifacts(step.Artifacts, runCtx)
		if err != nil {
			return e.failStep(ctx, runID, workflowName, stateStepName, base, now, fmt.Errorf("loading artifacts: %w", err))
		}
		stepData := map[string]any{"artifacts": artifacts}
		if output != nil {
			for k, v := range output {
				stepData[k] = v
			}
		}
		runCtx[step.Name] = stepData
		e.verbose("[run:%s]   loaded %d artifact(s) for %q", runID, len(artifacts), step.Name)
	}

	outputJSON, _ := json.Marshal(output)
	artifactsJSON := ""
	if stepData, ok := runCtx[step.Name]; ok {
		aj, _ := json.Marshal(stepData)
		artifactsJSON = string(aj)
	}
	completed := time.Now()
	if err := e.store.SaveStep(ctx, &state.StepResult{
		RunID:         runID,
		WorkflowName:  workflowName,
		StepName:      stateStepName,
		Status:        state.StepCompleted,
		OutputJSON:    string(outputJSON),
		ArtifactsJSON: artifactsJSON,
		StartedAt:     &now,
		CompletedAt:   &completed,
	}); err != nil {
		return fmt.Errorf("persisting completed step %q: %w", stateStepName, err)
	}

	e.fireEvent(func(cb callback.Callback) error {
		return cb.OnStepCompleted(callback.StepCompletedEvent{
			EventHeader:  callback.EventHeader{Timestamp: completed, RunID: runID, EventType: "step_completed"},
			WorkflowName: workflowName,
			StepName:     step.Name,
			StepType:     step.Type,
			Output:       output,
			Duration:     completed.Sub(now).Seconds(),
		})
	})

	log.Printf("[run:%s] step %q completed", runID, step.Name)
	return nil
}

func (e *Engine) executeSubWorkflow(ctx context.Context, runID string, workflowName string, step parser.Step, runCtx map[string]any) error {
	wf, wfFile, err := e.resolveSubWorkflow(step, runCtx)
	if err != nil {
		return err
	}

	subVars := make(map[string]any)
	for k, v := range runCtx {
		subVars[k] = v
	}
	for k, v := range wf.Vars {
		if v != nil {
			subVars[k] = v
		}
	}
	if step.Vars != nil {
		rendered, err := e.tmpl.RenderMap(step.Vars, runCtx)
		if err != nil {
			return fmt.Errorf("rendering sub-workflow vars: %w", err)
		}
		for k, v := range rendered {
			if s, ok := v.(string); ok {
				subVars[k] = coerceString(s)
			} else {
				subVars[k] = v
			}
		}
	}

	subRunID := fmt.Sprintf("%s-%s", runID, step.Name)
	if err := e.checkSubRunWorkflow(ctx, subRunID, step, wf.Name); err != nil {
		return err
	}
	varsJSON, _ := json.Marshal(subVars)
	subRun := &state.Run{
		RunID:        subRunID,
		WorkflowFile: e.SourcePath,
		SourceDigest: e.digest(),
		Entrypoint:   wf.Name,
		Status:       state.RunRunning,
		VarsJSON:     string(varsJSON),
		ParentRunID:  runID,
		ParentStep:   step.Name,
		StartedAt:    time.Now(),
	}
	e.store.CreateRun(ctx, subRun)

	e.fireEvent(func(cb callback.Callback) error {
		return cb.OnSubRunStarted(callback.SubRunStartedEvent{
			EventHeader:  callback.EventHeader{Timestamp: subRun.StartedAt, RunID: subRunID, EventType: "sub_run_started"},
			ParentRunID:  runID,
			ParentStep:   step.Name,
			WorkflowName: wf.Name,
		})
	})

	log.Printf("[run:%s] starting sub-workflow %q as %s", runID, wf.Name, subRunID)

	err = e.executeWorkflow(withFile(ctx, wfFile), subRunID, wf, subVars)

	subNow := time.Now()
	duration := subNow.Sub(subRun.StartedAt).Seconds()
	if paused, ok := pauseError(err); ok {
		subRun.Status = state.RunPaused
		subRun.CompletedAt = nil
		e.store.UpdateRun(ctx, subRun)
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnRunPaused(callback.RunPausedEvent{
				EventHeader:  callback.EventHeader{Timestamp: subNow, RunID: subRunID, EventType: "run_paused"},
				WorkflowName: wf.Name,
				StepName:     paused.StepName,
				FiredRules:   paused.FiredRules,
				Facts:        paused.Facts,
				Duration:     duration,
			})
		})
		return err
	} else if err != nil {
		subRun.CompletedAt = &subNow
		subRun.Status = state.RunFailed
		e.store.UpdateRun(ctx, subRun)
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnSubRunFailed(callback.SubRunFailedEvent{
				EventHeader:  callback.EventHeader{Timestamp: subNow, RunID: subRunID, EventType: "sub_run_failed"},
				ParentRunID:  runID,
				ParentStep:   step.Name,
				WorkflowName: wf.Name,
				Error:        err.Error(),
				Duration:     duration,
			})
		})
		if !step.IgnoreErrors {
			return err
		}
		log.Printf("[run:%s] sub-workflow %q failed, ignored (ignore_errors): %v", runID, wf.Name, err)
		if step.Register != "" {
			runCtx[step.Register] = ignoredFailure(subVars, err)
		}
		return nil
	}
	subRun.CompletedAt = &subNow
	subRun.Status = state.RunCompleted
	e.store.UpdateRun(ctx, subRun)

	e.fireEvent(func(cb callback.Callback) error {
		return cb.OnSubRunCompleted(callback.SubRunCompletedEvent{
			EventHeader:  callback.EventHeader{Timestamp: subNow, RunID: subRunID, EventType: "sub_run_completed"},
			ParentRunID:  runID,
			ParentStep:   step.Name,
			WorkflowName: wf.Name,
			Duration:     duration,
		})
	})

	if step.Register != "" {
		runCtx[step.Register] = subVars
	}

	log.Printf("[run:%s] sub-workflow %q completed", runID, wf.Name)
	return nil
}

func (e *Engine) executeForEach(ctx context.Context, runID string, workflowName string, step parser.Step, runCtx map[string]any) error {
	listVal, err := e.resolveForEachList(step.ForEach, runCtx)
	if err != nil {
		return fmt.Errorf("resolving for_each: %w", err)
	}

	if step.ForEachSort != "" {
		for i, item := range listVal {
			if extractItemField(item, step.ForEachSort) == nil {
				return fmt.Errorf("for_each_sort %q not found on item at index %d", step.ForEachSort, i)
			}
		}
		sort.SliceStable(listVal, func(i, j int) bool {
			ki := fmt.Sprintf("%v", extractItemField(listVal[i], step.ForEachSort))
			kj := fmt.Sprintf("%v", extractItemField(listVal[j], step.ForEachSort))
			return ki < kj
		})
	}

	if step.ForEachKey != "" {
		seen := make(map[string]int)
		for i, item := range listVal {
			val := extractItemField(item, step.ForEachKey)
			if val == nil {
				return fmt.Errorf("for_each_key %q not found on item at index %d", step.ForEachKey, i)
			}
			key := fmt.Sprintf("%v", val)
			if prev, ok := seen[key]; ok {
				return fmt.Errorf("duplicate for_each_key %q at indices %d and %d", key, prev, i)
			}
			seen[key] = i
		}
	}

	concurrency := step.Concurrency
	if concurrency <= 0 {
		concurrency = e.forks
	}

	log.Printf("[run:%s] for_each %q: %d items, concurrency %d", runID, step.Name, len(listVal), concurrency)

	sem := make(chan struct{}, concurrency)
	var mu sync.Mutex
	// One slot per item, so registered results keep the input order whatever order the
	// iterations finish in; items that were skipped or never started leave their slot nil.
	slots := make([]map[string]any, len(listVal))
	var firstErr error
	var errOnce sync.Once
	// failed is set with firstErr, so the loop can check it without a data race.
	var failed atomic.Bool

	var wg sync.WaitGroup

	for i, item := range listVal {
		if failed.Load() {
			break
		}

		if step.ForEachWhen != "" {
			whenCtx := make(map[string]any, len(runCtx)+1)
			for k, v := range runCtx {
				whenCtx[k] = v
			}
			whenCtx[step.As] = item
			ok, err := e.tmpl.EvalBool(step.ForEachWhen, whenCtx)
			if err != nil {
				errOnce.Do(func() { failed.Store(true); firstErr = fmt.Errorf("evaluating for_each_when for item %d: %w", i, err) })
				break
			}
			if !ok {
				log.Printf("[run:%s] for_each %q: skipping item %d (for_each_when: %q is false)", runID, step.Name, i, step.ForEachWhen)
				continue
			}
		}

		sem <- struct{}{}
		// Waiting for a slot can outlast an earlier item's failure: check again, so no new item
		// starts after one has failed (with concurrency 1, the next item used to run anyway).
		if failed.Load() {
			<-sem
			break
		}
		wg.Add(1)

		go func(idx int, itemVal any) {
			defer wg.Done()
			defer func() { <-sem }()

			itemCtx := make(map[string]any)
			for k, v := range runCtx {
				itemCtx[k] = v
			}
			itemCtx[step.As] = itemVal

			if step.Workflow != "" {
				wf, wfFile, err := e.resolveSubWorkflow(step, itemCtx)
				if err != nil {
					errOnce.Do(func() { failed.Store(true); firstErr = err })
					return
				}

				subVars := make(map[string]any)
				for k, v := range runCtx {
					subVars[k] = v
				}
				for k, v := range wf.Vars {
					subVars[k] = v
				}
				subVars[step.As] = itemVal
				if step.Vars != nil {
					rendered, err := e.tmpl.RenderMap(step.Vars, itemCtx)
					if err != nil {
						errOnce.Do(func() { failed.Store(true); firstErr = fmt.Errorf("rendering sub-workflow vars: %w", err) })
						return
					}
					for k, v := range rendered {
						if s, ok := v.(string); ok {
							subVars[k] = coerceString(s)
						} else {
							subVars[k] = v
						}
					}
				}

				forEachKey := fmt.Sprintf("%d", idx)
				if step.ForEachKey != "" {
					forEachKey = fmt.Sprintf("%v", extractItemField(itemVal, step.ForEachKey))
				}

				subRunID := fmt.Sprintf("%s-%s-%s", runID, step.Name, forEachKey)
				if err := e.checkSubRunWorkflow(ctx, subRunID, step, wf.Name); err != nil {
					errOnce.Do(func() { failed.Store(true); firstErr = err })
					return
				}
				varsJSON, _ := json.Marshal(subVars)
				subRun := &state.Run{
					RunID:        subRunID,
					WorkflowFile: e.SourcePath,
					SourceDigest: e.digest(),
					Entrypoint:   wf.Name,
					Status:       state.RunRunning,
					VarsJSON:     string(varsJSON),
					ParentRunID:  runID,
					ParentStep:   step.Name,
					ForEachKey:   forEachKey,
					StartedAt:    time.Now(),
				}
				e.store.CreateRun(ctx, subRun)
				e.fireEvent(func(cb callback.Callback) error {
					return cb.OnSubRunStarted(callback.SubRunStartedEvent{
						EventHeader:  callback.EventHeader{Timestamp: subRun.StartedAt, RunID: subRunID, EventType: "sub_run_started"},
						ParentRunID:  runID,
						ParentStep:   step.Name,
						WorkflowName: wf.Name,
						ForEachKey:   forEachKey,
					})
				})

				err = e.executeWorkflow(withFile(ctx, wfFile), subRunID, wf, subVars)

				feNow := time.Now()
				feDuration := feNow.Sub(subRun.StartedAt).Seconds()
				if paused, ok := pauseError(err); ok {
					subRun.Status = state.RunPaused
					subRun.CompletedAt = nil
					errOnce.Do(func() { failed.Store(true); firstErr = err })
					e.fireEvent(func(cb callback.Callback) error {
						return cb.OnRunPaused(callback.RunPausedEvent{
							EventHeader:  callback.EventHeader{Timestamp: feNow, RunID: subRunID, EventType: "run_paused"},
							WorkflowName: wf.Name,
							StepName:     paused.StepName,
							FiredRules:   paused.FiredRules,
							Facts:        paused.Facts,
							Duration:     feDuration,
						})
					})
				} else if err != nil {
					subRun.CompletedAt = &feNow
					subRun.Status = state.RunFailed
					if step.IgnoreErrors {
						log.Printf("[run:%s] for_each %q item %s failed, ignored (ignore_errors): %v", runID, step.Name, forEachKey, err)
						subVars = ignoredFailure(subVars, err)
					} else {
						errOnce.Do(func() { failed.Store(true); firstErr = err })
					}
					e.fireEvent(func(cb callback.Callback) error {
						return cb.OnSubRunFailed(callback.SubRunFailedEvent{
							EventHeader:  callback.EventHeader{Timestamp: feNow, RunID: subRunID, EventType: "sub_run_failed"},
							ParentRunID:  runID,
							ParentStep:   step.Name,
							WorkflowName: wf.Name,
							ForEachKey:   forEachKey,
							Error:        err.Error(),
							Duration:     feDuration,
						})
					})
				} else {
					subRun.CompletedAt = &feNow
					subRun.Status = state.RunCompleted
					e.fireEvent(func(cb callback.Callback) error {
						return cb.OnSubRunCompleted(callback.SubRunCompletedEvent{
							EventHeader:  callback.EventHeader{Timestamp: feNow, RunID: subRunID, EventType: "sub_run_completed"},
							ParentRunID:  runID,
							ParentStep:   step.Name,
							WorkflowName: wf.Name,
							ForEachKey:   forEachKey,
							Duration:     feDuration,
						})
					})
				}
				e.store.UpdateRun(ctx, subRun)

				mu.Lock()
				slots[idx] = subVars
				mu.Unlock()
			} else {
				// The step keeps its register: it registers its output in this item's own
				// context, which is what the loop's register collects.
				subStep := step
				subStep.ForEach = ""

				forEachKey := fmt.Sprintf("%d", idx)
				if step.ForEachKey != "" {
					forEachKey = fmt.Sprintf("%v", extractItemField(itemVal, step.ForEachKey))
				}
				stateStepName := fmt.Sprintf("%s[%s]", step.Name, forEachKey)

				err := e.executeStepWithStateName(ctx, runID, workflowName, subStep, stateStepName, itemCtx)
				if err != nil {
					if !step.IgnoreErrors || IsPaused(err) {
						errOnce.Do(func() { failed.Store(true); firstErr = err })
						return
					}
					itemCtx = ignoredFailure(itemCtx, err)
				}

				mu.Lock()
				slots[idx] = itemCtx
				mu.Unlock()
			}
		}(i, item)
	}

	wg.Wait()

	if step.Register != "" {
		var results []map[string]any
		for _, r := range slots {
			if r != nil {
				results = append(results, r)
			}
		}
		runCtx[step.Register] = results
	}

	if firstErr != nil {
		return firstErr
	}

	return nil
}

func (e *Engine) resolveForEachList(expr string, ctx map[string]any) ([]any, error) {
	val := resolveContextPath(expr, ctx)
	if val != nil {
		switch v := val.(type) {
		case []any:
			return v, nil
		case []string:
			list := make([]any, len(v))
			for i, s := range v {
				list[i] = s
			}
			return list, nil
		}
	}

	native, err := e.tmpl.Eval(expr, ctx)
	if err != nil {
		return nil, err
	}
	if list, ok := asList(native); ok {
		return list, nil
	}

	rendered, err := e.tmpl.Render("{{ "+expr+" }}", ctx)
	if err != nil {
		return nil, err
	}

	parsed, err := jsonvalue.Decode([]byte(rendered))
	list, ok := parsed.([]any)
	if err != nil || !ok {
		return nil, fmt.Errorf("for_each expression %q did not resolve to a list", expr)
	}
	return list, nil
}

// resolveSubWorkflow finds the workflow a step calls. The name may be a template, rendered
// against the step's context (for for_each, the item's), so one step can call different
// workflows: `workflow: "submit-{{ test.arm }}"`.
// eventLogs is EventLogs as a template-friendly list.
func (e *Engine) eventLogs() []any {
	logs := make([]any, len(e.EventLogs))
	for i, p := range e.EventLogs {
		logs[i] = p
	}
	return logs
}

// resolveSubWorkflow looks the called workflow up in the current definition, which is where a
// reload takes effect; the caller runs the sub-workflow pinned to the returned file.
func (e *Engine) resolveSubWorkflow(step parser.Step, ctx map[string]any) (*parser.Workflow, *parser.WorkflowFile, error) {
	file := e.current()
	name := step.Workflow
	if parser.IsTemplated(name) {
		rendered, err := e.tmpl.Render(name, ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("rendering workflow name %q: %w", step.Workflow, err)
		}
		name = strings.TrimSpace(rendered)
		if len(step.WorkflowNames) > 0 && !slices.Contains(step.WorkflowNames, name) {
			return nil, nil, fmt.Errorf("workflow %q resolved to %q; expected one of %s (workflow_names)",
				step.Workflow, name, strings.Join(step.WorkflowNames, ", "))
		}
	}
	wf := file.GetWorkflow(name)
	if wf == nil {
		if name != step.Workflow {
			return nil, nil, fmt.Errorf("sub-workflow %q (from %q) not found", name, step.Workflow)
		}
		return nil, nil, fmt.Errorf("sub-workflow %q not found", name)
	}
	return wf, file, nil
}

// checkSubRunWorkflow refuses to resume a sub-run in a different workflow from the one it ran
// before. Step state is keyed by workflow and step name, so the other workflow's finished steps
// would be ignored and its work silently redone or mixed in. This can only happen when a
// templated workflow name resolves differently on resume.
func (e *Engine) checkSubRunWorkflow(ctx context.Context, subRunID string, step parser.Step, name string) error {
	prev, err := e.store.GetRun(ctx, subRunID)
	if err != nil || prev == nil || prev.Entrypoint == "" || prev.Entrypoint == name {
		return nil
	}
	return fmt.Errorf("sub-run %s ran workflow %q, but %q now resolves to %q; it can't be resumed in place (start a new run, or rewind to before step %q)",
		subRunID, prev.Entrypoint, step.Workflow, name, step.Name)
}

// decodeState reads JSON saved in the state store back into workflow values. Whole numbers come
// back as int, as they were before saving; plain json.Unmarshal would make them float64, which
// templates print as "2.000000" after a resume.
func decodeState(data string, out *map[string]any) {
	dec := json.NewDecoder(strings.NewReader(data))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return
	}
	*out, _ = jsonvalue.Normalize(raw).(map[string]any)
}

// asList accepts any slice value, such as the []any or []string a filter returns.
func asList(value any) ([]any, bool) {
	if list, ok := value.([]any); ok {
		return list, true
	}
	v := reflect.ValueOf(value)
	if !v.IsValid() || v.Kind() != reflect.Slice {
		return nil, false
	}
	list := make([]any, v.Len())
	for i := range list {
		list[i] = v.Index(i).Interface()
	}
	return list, true
}

// evalFailedWhen decides a step's failure from its failed_when expression. The expression sees
// the run context plus the step's output as `result` (and under the register name), with an
// executor error, if any, in result.error.
func (e *Engine) evalFailedWhen(step parser.Step, output map[string]any, execErr error, runCtx map[string]any) error {
	result := make(map[string]any, len(output)+1)
	for k, v := range output {
		result[k] = v
	}
	if execErr != nil {
		result["error"] = execErr.Error()
	}
	evalCtx := make(map[string]any, len(runCtx)+2)
	for k, v := range runCtx {
		evalCtx[k] = v
	}
	evalCtx["result"] = result
	if step.Register != "" {
		evalCtx[step.Register] = result
	}
	failed, err := e.tmpl.EvalBool(step.FailedWhen, evalCtx)
	if err != nil {
		return fmt.Errorf("evaluating failed_when: %w", err)
	}
	if failed {
		if execErr != nil {
			return fmt.Errorf("failed_when %q is true: %w", step.FailedWhen, execErr)
		}
		return fmt.Errorf("failed_when %q is true", step.FailedWhen)
	}
	return nil
}

// ignoredFailure marks a step's output as failed for a step with ignore_errors, keeping
// whatever output the step produced.
func ignoredFailure(output map[string]any, err error) map[string]any {
	marked := make(map[string]any, len(output)+2)
	for k, v := range output {
		marked[k] = v
	}
	marked["failed"] = true
	marked["error"] = err.Error()
	return marked
}

func resolveContextPath(path string, ctx map[string]any) any {
	parts := strings.Split(path, ".")
	var current any = ctx
	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = m[part]
		if !ok {
			return nil
		}
	}
	return current
}

func extractItemField(item any, field string) any {
	m, ok := item.(map[string]any)
	if !ok {
		return nil
	}
	v, ok := m[field]
	if !ok {
		return nil
	}
	return v
}

var k8sNameUnsafe = regexp.MustCompile(`[^a-z0-9\-.]`)

func sanitizeK8sName(s string, maxLen int) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("_", "-", " ", "-").Replace(s)
	s = k8sNameUnsafe.ReplaceAllString(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-.")
	if len(s) > maxLen {
		hash := sha256.Sum256([]byte(s))
		suffix := hex.EncodeToString(hash[:4])
		s = s[:maxLen-len(suffix)-1] + "-" + suffix
		s = strings.TrimRight(s, "-.")
	}
	return s
}

func sanitizeK8sLabel(s string, maxLen int) string {
	s = strings.NewReplacer("_", "-", " ", "-").Replace(s)
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	s = strings.Trim(s, "-._")
	return s
}

func (e *Engine) injectJobMeta(params map[string]any, runID, workflowName, stepName string) {
	if _, ok := params["_job_name"]; !ok {
		prefix := "markov"
		if p, ok := params["name_prefix"].(string); ok && p != "" {
			prefix = p
		}
		raw := fmt.Sprintf("%s-%s-%s", prefix, runID, stepName)
		params["_job_name"] = sanitizeK8sName(raw, 63)
	}

	labels, _ := params["_labels"].(map[string]string)
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["app"] = "markov"
	labels["markov/run-id"] = sanitizeK8sLabel(runID, 63)
	labels["markov/workflow"] = sanitizeK8sLabel(workflowName, 63)
	labels["markov/step"] = sanitizeK8sLabel(stepName, 63)
	params["_labels"] = labels
}

func (e *Engine) failStep(ctx context.Context, runID, workflowName, stepName, stepType string, startedAt time.Time, err error) error {
	now := time.Now()
	e.store.SaveStep(ctx, &state.StepResult{
		RunID:        runID,
		WorkflowName: workflowName,
		StepName:     stepName,
		Status:       state.StepFailed,
		Error:        err.Error(),
		StartedAt:    &startedAt,
		CompletedAt:  &now,
	})
	e.fireEvent(func(cb callback.Callback) error {
		return cb.OnStepFailed(callback.StepFailedEvent{
			EventHeader:  callback.EventHeader{Timestamp: now, RunID: runID, EventType: "step_failed"},
			WorkflowName: workflowName,
			StepName:     stepName,
			StepType:     stepType,
			Error:        err.Error(),
			Duration:     now.Sub(startedAt).Seconds(),
		})
	})
	return err
}

func isSetFactStep(wf *parser.Workflow, stepName string) bool {
	for _, s := range wf.Steps {
		if s.Name == stepName {
			return s.Type == "set_fact"
		}
	}
	return false
}

func isGateStep(wf *parser.Workflow, stepName string) bool {
	for _, s := range wf.Steps {
		if s.Name == stepName {
			return s.Type == "gate"
		}
	}
	return false
}

func (e *Engine) buildContext(cliVars map[string]any, workflowVars map[string]any) map[string]any {
	ctx := make(map[string]any)
	for k, v := range e.current().Vars {
		ctx[k] = v
	}
	for k, v := range workflowVars {
		if v != nil {
			ctx[k] = v
		}
	}
	for k, v := range cliVars {
		ctx[k] = v
	}
	return ctx
}
