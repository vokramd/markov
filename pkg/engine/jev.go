package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jctanner/markov/pkg/callback"
	"github.com/jctanner/markov/pkg/jev"
	"github.com/jctanner/markov/pkg/state"
)

// A decision is a durable child operation of a step, including when that step
// is skipped or paused. Its identity includes inputs and non-secret config.
func (e *Engine) decision(ctx context.Context, runID, workflow, step, name string, input, refresh any) (map[string]any, error) {
	d, ok := e.fileFor(ctx).Decisions[name]
	if !ok {
		return nil, fmt.Errorf("unknown Jev decision %q", name)
	}
	if input == nil {
		return nil, fmt.Errorf("decision %q: state is required", name)
	}
	conn, ok := e.fileFor(ctx).Connections[d.Connection]
	if !ok {
		return nil, fmt.Errorf("unknown Jev connection %q", d.Connection)
	}
	identity, err := json.Marshal(map[string]any{"step": step, "name": name, "definition": d, "connection": conn, "state": input, "refresh": refresh})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(identity)
	hash := hex.EncodeToString(digest[:])
	key := "__jev__/" + step + "/" + hash
	saved, err := e.store.GetStep(ctx, runID, workflow, key)
	if err != nil {
		return nil, err
	}
	if saved != nil && saved.Status == state.StepCompleted {
		var out map[string]any
		if err := json.Unmarshal([]byte(saved.OutputJSON), &out); err != nil {
			return nil, fmt.Errorf("reading decision receipt: %w", err)
		}
		return decisionAnswer(out)
	}
	started := time.Now()
	receipt := &state.StepResult{RunID: runID, WorkflowName: workflow, StepName: key, Status: state.StepRunning, StartedAt: &started}
	if err := e.store.SaveStep(ctx, receipt); err != nil {
		return nil, err
	}
	e.fireEvent(func(cb callback.Callback) error {
		return cb.OnStepStarted(callback.StepStartedEvent{
			EventHeader: callback.EventHeader{Timestamp: started, RunID: runID, EventType: "step_started"}, WorkflowName: workflow, StepName: key, StepType: "jev", ResolvedType: "jev",
		})
	})
	exec, ok := e.executors["jev"]
	if !ok {
		return nil, fmt.Errorf("Jev executor not registered")
	}
	q := d.Question()
	result, err := exec.Execute(ctx, map[string]any{"connection": d.Connection, "state": input, "questions": map[string]jev.Question{"result": q}})
	if err != nil {
		return nil, e.failStep(ctx, runID, workflow, key, "jev", started, err)
	}
	if _, err := decisionAnswer(result.Output); err != nil {
		return nil, e.failStep(ctx, runID, workflow, key, "jev", started, err)
	}
	result.Output["decision"] = map[string]any{"name": name, "version": d.Version, "fingerprint": hash, "parent_step": step}
	data, err := json.Marshal(result.Output)
	if err != nil {
		return nil, err
	}
	completed := time.Now()
	receipt.Status = state.StepCompleted
	receipt.OutputJSON = string(data)
	receipt.CompletedAt = &completed
	if err := e.store.SaveStep(ctx, receipt); err != nil {
		return nil, fmt.Errorf("persisting decision: %w", err)
	}
	e.fireEvent(func(cb callback.Callback) error {
		return cb.OnStepCompleted(callback.StepCompletedEvent{
			EventHeader: callback.EventHeader{Timestamp: completed, RunID: runID, EventType: "step_completed"}, WorkflowName: workflow, StepName: key, StepType: "jev", Output: result.Output, Duration: completed.Sub(started).Seconds(),
		})
	})
	return decisionAnswer(result.Output)
}

func decisionAnswer(out map[string]any) (map[string]any, error) {
	answers, ok := out["answers"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("decision receipt missing answers")
	}
	answer, ok := answers["result"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("decision receipt missing result")
	}
	return answer, nil
}

func decisionContext(ctx context.Context, seconds int) (context.Context, context.CancelFunc) {
	if seconds <= 0 {
		seconds = 120
	}
	return context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
}

func (e *Engine) resolveDecisionFacts(ctx context.Context, runID, workflow, step string, facts, runCtx map[string]any, timeout int) (map[string]any, error) {
	ctx, cancel := decisionContext(ctx, timeout)
	defer cancel()
	// Each fact sees the same original context, independent of Go map iteration.
	rendered, err := e.tmpl.RenderMap(facts, runCtx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(rendered))
	for name := range rendered {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		spec, ok := rendered[name].(map[string]any)
		if !ok {
			continue
		}
		raw, exists := spec["decision"]
		if !exists {
			continue
		}
		dn, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("fact %q: invalid decision name", name)
		}
		answer, err := e.decision(ctx, runID, workflow, step, dn, spec["state"], spec["refresh"])
		if err != nil {
			return nil, fmt.Errorf("fact %q: %w", name, err)
		}
		sel, _ := spec["select"].(string)
		if sel == "" {
			sel = e.fileFor(ctx).Decisions[dn].Type
		}
		value, ok := answer[sel]
		if !ok {
			return nil, fmt.Errorf("decision %q has no field %q", dn, sel)
		}
		rendered[name] = value
	}
	return rendered, nil
}

func (e *Engine) evalDecisionBool(ctx context.Context, runID, workflow, step, expr string, runCtx map[string]any, timeout int) (bool, error) {
	ctx, cancel := decisionContext(ctx, timeout)
	defer cancel()
	scope := make(map[string]any, len(runCtx)+1)
	for k, v := range runCtx {
		scope[k] = v
	}
	functions := map[string]any{}
	for _, kind := range []string{"noul", "choice", "score"} {
		functions[kind] = func(name string, input any, refresh ...any) (any, error) {
			d, ok := e.fileFor(ctx).Decisions[name]
			if !ok {
				return nil, fmt.Errorf("unknown Jev decision %q", name)
			}
			if d.Type != kind {
				return nil, fmt.Errorf("decision %q is %s, not %s", name, d.Type, kind)
			}
			if len(refresh) > 1 {
				return nil, fmt.Errorf("decision takes at most one refresh token")
			}
			var token any
			if len(refresh) == 1 {
				token = refresh[0]
			}
			answer, err := e.decision(ctx, runID, workflow, step, name, input, token)
			if err != nil {
				return nil, err
			}
			return answer[kind], nil
		}
	}
	scope["jev"] = functions
	return e.tmpl.EvalBool(expr, scope)
}
