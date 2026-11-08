package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/vokramd/markov/pkg/callback"
	"github.com/vokramd/markov/pkg/parser"
	"github.com/vokramd/markov/pkg/state"
)

// repeat: a bounded do-while loop over a called workflow (docs/reference/repeat.md). Each
// iteration is a sub-run of the calling run, never of the previous iteration, so the loop stays
// flat however long it runs. A loop is not resumed in place (resume: false): a resumed run starts
// an unfinished loop over, in a new generation, and restores a finished one from its step row.

type repeatKey struct{}

// withRepeat marks ctx as running inside repeat step name; calls and fan-outs below inherit it.
func withRepeat(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, repeatKey{}, name)
}

// insideRepeat is the innermost repeat step ctx runs in, if any.
func insideRepeat(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(repeatKey{}).(string)
	return name, ok
}

// executeRepeat runs a repeat step's loop and registers its envelope:
// {last, count, stopped_by: until|max|error, failed?, error?}.
func (e *Engine) executeRepeat(ctx context.Context, runID, workflowName, stateStepName string, step parser.Step, runCtx map[string]any) error {
	r := step.Repeat
	started := time.Now()
	if step.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(step.Timeout)*time.Second)
		defer cancel()
	}
	max, err := e.repeatMax(r.Max, runCtx)
	if err != nil {
		return err
	}
	gen, err := e.nextRepeatGeneration(ctx, runID, workflowName, step.Name)
	if err != nil {
		return err
	}
	log.Printf("[run:%s] repeat %q: generation %d, at most %d iteration(s)", runID, step.Name, gen, max)

	env := map[string]any{"count": 0, "last": nil}
	var previous any
	for i := 0; i < max; i++ {
		if err := ctx.Err(); err != nil {
			return e.finishRepeat(ctx, runID, workflowName, stateStepName, step, runCtx, started, gen, env, fmt.Errorf("repeat %q stopped before iteration %d: %w", step.Name, i, err))
		}
		meta := map[string]any{"index": i, "previous": previous}
		caller := make(map[string]any, len(runCtx)+1)
		for k, v := range runCtx {
			caller[k] = v
		}
		caller["repeat"] = meta

		if err := e.debugPauseIteration(ctx, runID, workflowName, step, stateStepName, i, caller); err != nil {
			return e.finishRepeat(ctx, runID, workflowName, stateStepName, step, runCtx, started, gen, env, err)
		}
		index := i
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnStepProgress(callback.StepProgressEvent{
				EventHeader:  callback.EventHeader{Timestamp: time.Now(), RunID: runID, EventType: "step_progress"},
				WorkflowName: workflowName,
				StepName:     step.Name,
				StepType:     step.Type,
				Kind:         "repeat_iteration",
				Data:         map[string]any{"index": index, "generation": gen},
			})
		})
		iterCtx := withLoopFrame(withRepeat(ctx, step.Name), loopFrame{Kind: "repeat", Workflow: workflowName, Step: step.Name, Iteration: strconv.Itoa(i), Generation: gen})
		result, err := e.runRepeatIteration(iterCtx, runID, workflowName, step, caller, gen, i)
		env["count"] = i + 1
		env["last"] = result
		if err != nil {
			return e.finishRepeat(ctx, runID, workflowName, stateStepName, step, runCtx, started, gen, env, err)
		}

		// until sees the globals, as every expression does, then the iteration's results.
		view := make(map[string]any, len(result)+1)
		file := e.fileFor(ctx)
		for k, v := range runCtx {
			if isGlobal(file, k) {
				view[k] = v
			}
		}
		for k, v := range result {
			view[k] = v
		}
		view["repeat"] = meta
		done, err := e.tmpl.EvalBool(r.Until, view)
		if err != nil {
			return e.finishRepeat(ctx, runID, workflowName, stateStepName, step, runCtx, started, gen, env, fmt.Errorf("repeat until: %w", err))
		}
		if done {
			env["stopped_by"] = "until"
			return e.finishRepeat(ctx, runID, workflowName, stateStepName, step, runCtx, started, gen, env, nil)
		}
		previous = result
	}
	env["stopped_by"] = "max"
	if r.OnMax == "stop" {
		return e.finishRepeat(ctx, runID, workflowName, stateStepName, step, runCtx, started, gen, env, nil)
	}
	return e.finishRepeat(ctx, runID, workflowName, stateStepName, step, runCtx, started, gen, env,
		fmt.Errorf("repeat %q ran its max of %d iteration(s) without until (%s) becoming true", step.Name, max, r.Until))
}

// repeatMax is the loop's bound: max as written, or its expression rendered against the caller.
func (e *Engine) repeatMax(v any, runCtx map[string]any) (int, error) {
	switch m := v.(type) {
	case int:
		return m, nil
	case string:
		text := m
		if parser.IsTemplated(m) {
			rendered, err := e.tmpl.Render(m, runCtx)
			if err != nil {
				return 0, fmt.Errorf("rendering repeat max: %w", err)
			}
			text = rendered
		}
		n, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil || n < 1 {
			return 0, fmt.Errorf("repeat max must be a positive whole number; %q gave %q", m, text)
		}
		return n, nil
	}
	return 0, fmt.Errorf("repeat max must be a positive whole number, not %v", v)
}

// nextRepeatGeneration is one more than the highest generation of any iteration sub-run this
// step owns in runID: every surviving row counts, whatever its status.
func (e *Engine) nextRepeatGeneration(ctx context.Context, runID, workflowName, stepName string) (int, error) {
	children, err := e.store.GetChildRuns(ctx, runID)
	if err != nil {
		return 0, fmt.Errorf("looking up repeat step %q's earlier iterations: %w", stepName, err)
	}
	next := 0
	for _, c := range children {
		if o, ok := repeatOwner(c); ok && o.Workflow == workflowName && o.Step == stepName && o.Generation >= next {
			next = o.Generation + 1
		}
	}
	return next, nil
}

// repeatOwner is a run's owner when a repeat step made it.
func repeatOwner(run *state.Run) (subRunOwner, bool) {
	var o subRunOwner
	if run.Owner == "" || json.Unmarshal([]byte(run.Owner), &o) != nil || o.Kind != "repeat" {
		return o, false
	}
	return o, true
}

// currentGenerations drops the iteration sub-runs of loops that later started over, so readers of
// a run's children (rewind's changed-step check) see each loop's latest generation only.
func currentGenerations(children []*state.Run) []*state.Run {
	latest := map[string]int{}
	for _, c := range children {
		if o, ok := repeatOwner(c); ok {
			key := o.Workflow + "\x00" + o.Step
			if g, seen := latest[key]; !seen || o.Generation > g {
				latest[key] = o.Generation
			}
		}
	}
	out := children[:0:0]
	for _, c := range children {
		if o, ok := repeatOwner(c); ok && o.Generation < latest[o.Workflow+"\x00"+o.Step] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// runRepeatIteration runs iteration i of generation gen as its own sub-run and returns its
// results (what the workflow was passed and what its steps set), also when it failed.
func (e *Engine) runRepeatIteration(ctx context.Context, runID, workflowName string, step parser.Step, caller map[string]any, gen, i int) (map[string]any, error) {
	wf, subCtx, err := e.resolveSubWorkflow(ctx, stepInfo{runID: runID, workflow: workflowName, step: step.Name}, step, caller)
	if err != nil {
		return nil, err
	}
	subVars, err := e.childContext(ctx, step, wf, caller)
	if err != nil {
		return nil, err
	}
	e.noteVars(e.fileFor(ctx), subVars)

	owner := subRunOwner{Kind: "repeat", Parent: runID, Workflow: workflowName, Step: step.Name, Generation: gen, Index: i}
	subRunID := fmt.Sprintf("%s-%s@%d.%d~%s", runID, step.Name, gen, i, owner.hash())
	exists, err := e.claimSubRun(ctx, subRunID, owner, step, wf.Name)
	if err != nil {
		return nil, err
	}
	if exists {
		// A new generation's rows can't exist yet: another process started this loop too.
		return nil, fmt.Errorf("another process is running repeat step %q (sub-run %s already exists)", step.Name, subRunID)
	}
	subRun := &state.Run{
		RunID:        subRunID,
		WorkflowFile: e.SourcePath,
		SourceDigest: e.digest(),
		Entrypoint:   wf.Name,
		Status:       state.RunRunning,
		VarsJSON:     e.subRunVarsJSON(subVars),
		ParentRunID:  runID,
		ParentStep:   step.Name,
		Owner:        owner.encode(),
		StartedAt:    time.Now(),
	}
	if err := e.store.CreateRun(ctx, subRun); err != nil {
		// The insert is the claim on this generation: a second process starting the same loop
		// fails here rather than sharing its rows.
		return nil, fmt.Errorf("starting repeat step %q iteration %d (another process may be running this loop): %w", step.Name, i, err)
	}
	gp, ip := gen, i
	e.fireEvent(func(cb callback.Callback) error {
		return cb.OnSubRunStarted(callback.SubRunStartedEvent{
			EventHeader:  callback.EventHeader{Timestamp: subRun.StartedAt, RunID: subRunID, EventType: "sub_run_started"},
			ParentRunID:  runID,
			ParentStep:   step.Name,
			WorkflowName: wf.Name,
			Generation:   &gp,
			Iteration:    &ip,
		})
	})
	log.Printf("[run:%s] repeat %q: iteration %d of workflow %q as %s", runID, step.Name, i, wf.Name, subRunID)

	err = e.executeWorkflow(subCtx, subRunID, wf, subVars)
	if paused, ok := pauseError(err); ok {
		// Gates inside repeat fail instead of pausing (see the gate step), so this is a pause
		// from somewhere else; it can't be waited on either.
		err = fmt.Errorf("step %q paused inside repeat step %q; pausing inside repeat is not supported yet", paused.StepName, step.Name)
	}
	now := time.Now()
	duration := now.Sub(subRun.StartedAt).Seconds()
	subRun.CompletedAt = &now
	result := e.childResult(ctx, subVars)
	if err != nil {
		subRun.Status = state.RunFailed
		e.store.UpdateRun(context.WithoutCancel(ctx), subRun)
		e.fireEvent(func(cb callback.Callback) error {
			return cb.OnSubRunFailed(callback.SubRunFailedEvent{
				EventHeader:  callback.EventHeader{Timestamp: now, RunID: subRunID, EventType: "sub_run_failed"},
				ParentRunID:  runID,
				ParentStep:   step.Name,
				WorkflowName: wf.Name,
				Generation:   &gp,
				Iteration:    &ip,
				Error:        err.Error(),
				Duration:     duration,
			})
		})
		return result, err
	}
	subRun.Status = state.RunCompleted
	e.store.UpdateRun(context.WithoutCancel(ctx), subRun)
	e.fireEvent(func(cb callback.Callback) error {
		return cb.OnSubRunCompleted(callback.SubRunCompletedEvent{
			EventHeader:  callback.EventHeader{Timestamp: now, RunID: subRunID, EventType: "sub_run_completed"},
			ParentRunID:  runID,
			ParentStep:   step.Name,
			WorkflowName: wf.Name,
			Generation:   &gp,
			Iteration:    &ip,
			Duration:     duration,
		})
	})
	if err := e.checkOutput(ctx, &step, result); err != nil {
		return result, fmt.Errorf("repeat %q iteration %d: %w", step.Name, i, err)
	}
	return result, nil
}

// finishRepeat ends the loop: it registers the envelope (also on failure, so the caller's rescue
// can read it), reports why the loop stopped, and saves a finished loop's step row. That row is
// what a resumed run restores instead of running the loop again; it is stored masked, so a
// secret in it comes back as <secret:NAME>.
func (e *Engine) finishRepeat(ctx context.Context, runID, workflowName, stateStepName string, step parser.Step, runCtx map[string]any, started time.Time, gen int, env map[string]any, loopErr error) error {
	if loopErr != nil {
		if env["stopped_by"] == nil { // reaching max with on_max: fail keeps "max"
			env["stopped_by"] = "error"
		}
		env["error"] = loopErr.Error()
	}
	e.fireEvent(func(cb callback.Callback) error {
		return cb.OnStepProgress(callback.StepProgressEvent{
			EventHeader:  callback.EventHeader{Timestamp: time.Now(), RunID: runID, EventType: "step_progress"},
			WorkflowName: workflowName,
			StepName:     step.Name,
			StepType:     step.Type,
			Kind:         "repeat_stopped",
			Data:         map[string]any{"stopped_by": env["stopped_by"], "count": env["count"], "generation": gen},
		})
	})
	log.Printf("[run:%s] repeat %q stopped by %v after %v iteration(s)", runID, step.Name, env["stopped_by"], env["count"])
	if loopErr != nil {
		if step.Register != "" {
			runCtx[step.Register] = env
		}
		if !step.IgnoreErrors {
			return loopErr
		}
		env["failed"] = true
		log.Printf("[run:%s] repeat %q failed, ignored (ignore_errors): %v", runID, step.Name, loopErr)
	}
	if err := e.checkOutput(ctx, &step, env); err != nil {
		return err
	}
	if step.Register != "" {
		runCtx[step.Register] = env
	}
	now := time.Now()
	if err := e.store.SaveStep(context.WithoutCancel(ctx), &state.StepResult{
		RunID:        runID,
		WorkflowName: workflowName,
		StepName:     stateStepName,
		Status:       state.StepCompleted,
		OutputJSON:   e.subRunVarsJSON(env),
		StartedAt:    &started,
		CompletedAt:  &now,
	}); err != nil {
		return fmt.Errorf("recording repeat step %q: %w", step.Name, err)
	}
	return nil
}
