package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sort"
	"time"

	"github.com/jctanner/markov/pkg/callback"
	"github.com/jctanner/markov/pkg/executor"
	"github.com/jctanner/markov/pkg/parser"
	"github.com/jctanner/markov/pkg/source"
	"github.com/jctanner/markov/pkg/state"
)

// Hot reload of the workflow definition: the reload_workflow step (ADR-0006, plan 003).
//
// The engine holds the current definition behind an atomic pointer. Every workflow execution is
// pinned, through its context, to the definition it started with, so the steps after a reload in
// the same workflow keep their old definitions (and step types, rules, decisions and scripts
// directory). Sub-workflows are resolved against the current definition when they are called,
// which is where a reload takes effect.

type fileKey struct{}

// withFile pins a workflow execution to a definition.
func withFile(ctx context.Context, file *parser.WorkflowFile) context.Context {
	return context.WithValue(ctx, fileKey{}, file)
}

// current is the latest definition.
func (e *Engine) current() *parser.WorkflowFile {
	return e.file.Load()
}

// fileFor is the definition the execution in ctx is pinned to, or the current one.
func (e *Engine) fileFor(ctx context.Context) *parser.WorkflowFile {
	if file, ok := ctx.Value(fileKey{}).(*parser.WorkflowFile); ok && file != nil {
		return file
	}
	return e.current()
}

func (e *Engine) digest() string {
	e.digestMu.Lock()
	defer e.digestMu.Unlock()
	return e.sourceDigest
}

func (e *Engine) setDigest(d string) {
	e.digestMu.Lock()
	defer e.digestMu.Unlock()
	e.sourceDigest = d
}

// usesReload reports whether a reload_workflow step is reachable from the workflow named start
// through sub-workflow calls. A templated call reaches its workflow_names, or every workflow when
// it lists none. Running a workflow that cannot reach a reload (a codified task in a project
// whose driver loop reloads) therefore needs no --allow-reload.
func usesReload(file *parser.WorkflowFile, start string) bool {
	seen := map[string]bool{}
	queue := []string{start}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		wf := file.GetWorkflow(name)
		if wf == nil {
			continue
		}
		for _, steps := range [][]parser.Step{wf.Steps, wf.Rescue, wf.Always} {
			for _, st := range steps {
				switch {
				case st.Workflow != "" && !parser.IsTemplated(st.Workflow):
					queue = append(queue, st.Workflow)
				case st.Workflow != "" && len(st.WorkflowNames) > 0:
					queue = append(queue, st.WorkflowNames...)
				case st.Workflow != "":
					for _, w := range file.Workflows {
						queue = append(queue, w.Name)
					}
				case st.Type != "":
					if base, _ := file.ResolveStepType(&st); base == "reload_workflow" {
						return true
					}
				}
			}
		}
	}
	return false
}

// checkReloadAllowed refuses to start a run that could replace its own definition unless the
// caller opted in, because such a run cannot be resumed. A reload that only a later definition
// introduces is still refused, by the step itself.
func (e *Engine) checkReloadAllowed(file *parser.WorkflowFile, start string) error {
	if !e.AllowReload && usesReload(file, start) {
		return errors.New("the workflow has reload_workflow steps, which replace its definition while it runs; pass --allow-reload to allow it (a run that reloads cannot be resumed)")
	}
	return nil
}

type stepInfoKey struct{}

type stepInfo struct {
	runID, workflow, step, stepType string
}

func withStepInfo(ctx context.Context, info stepInfo) context.Context {
	return context.WithValue(ctx, stepInfoKey{}, info)
}

// loadSource parses and fingerprints a workflow source.
func (e *Engine) loadSource(path string) (*parser.WorkflowFile, string, error) {
	load := e.Loader
	if load == nil {
		load = parser.ParseFile
	}
	file, err := load(path)
	if err != nil {
		return nil, "", err
	}
	fp, err := source.Tree(path, e.SourceExcludes)
	if err != nil {
		return nil, "", fmt.Errorf("fingerprinting workflow source: %w", err)
	}
	return file, fp.Digest, nil
}

// reloadExecutor implements reload_workflow. It needs the engine, so New registers it.
type reloadExecutor struct {
	e *Engine
}

func (r *reloadExecutor) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	e := r.e
	if !e.AllowReload {
		return nil, errors.New("reload_workflow needs --allow-reload")
	}
	info, _ := ctx.Value(stepInfoKey{}).(stepInfo)
	src, _ := params["source"].(string)
	if src == "" {
		src = e.SourcePath
	}
	if src == "" {
		return nil, errors.New("reload_workflow: no source param and the run has no source path")
	}
	require, err := stringList(params["require"])
	if err != nil {
		return nil, fmt.Errorf("reload_workflow: require: %w", err)
	}

	// One reload at a time, so concurrent for_each iterations cannot interleave swaps.
	e.reloadMu.Lock()
	defer e.reloadMu.Unlock()
	old := e.current()
	oldDigest := e.digest()
	out := map[string]any{"reloaded": false, "source": src, "old_digest": oldDigest}

	if d := e.debugger; d != nil && d.controlOpen() {
		// A client is attached (the IDE): describe the candidate and wait for its answer.
		pending := map[string]any{"source": src, "current_digest": oldDigest}
		if file, digest, err := e.loadSource(src); err != nil {
			pending["error"] = err.Error()
		} else {
			pending["candidate_digest"] = digest
			pending["changes"] = diffFiles(old, file)
		}
		p := debugPoint{runID: info.runID, workflow: info.workflow, step: info.step, stepType: info.stepType, section: sectionSteps, phase: phaseBefore}
		cmd, err := d.awaitReload(ctx, p, pending)
		if err != nil {
			return &executor.Result{Output: out}, err
		}
		if cmd.Cmd == "reject_reload" {
			out["rejected"] = true
			return &executor.Result{Output: out}, nil
		}
		if cmd.Source != "" {
			src = cmd.Source
			out["source"] = src
		}
	}

	file, digest, err := e.loadSource(src)
	if err != nil {
		out["error"] = err.Error()
		return &executor.Result{Output: out}, fmt.Errorf("reload_workflow: loading %s: %w (the current definition stays)", src, err)
	}
	for _, name := range require {
		if file.GetWorkflow(name) == nil {
			out["error"] = fmt.Sprintf("workflow %q is missing", name)
			return &executor.Result{Output: out}, fmt.Errorf("reload_workflow: %s has no workflow %q (require); the current definition stays", src, name)
		}
	}

	changes := diffFiles(old, file)
	e.file.Store(file)
	e.setDigest(digest)
	e.reloads++
	n := e.reloads
	now := time.Now()
	if err := e.store.SaveReload(ctx, &state.Reload{
		RunID: e.rootRunID, StepRunID: info.runID, Workflow: info.workflow, Step: info.step,
		Source: src, OldDigest: oldDigest, NewDigest: digest, ReloadedAt: now,
	}); err != nil {
		// The swap has happened; the record is what stops a resume, so say so loudly.
		log.Printf("[run:%s] reload %d applied but not recorded: %v", info.runID, n, err)
	}
	log.Printf("[run:%s] reloaded workflow from %s (reload %d, digest %s -> %s)", info.runID, src, n, oldDigest, digest)
	e.fireEvent(func(cb callback.Callback) error {
		return cb.OnWorkflowReloaded(callback.WorkflowReloadedEvent{
			EventHeader:  callback.EventHeader{Timestamp: now, RunID: info.runID, EventType: "workflow_reloaded"},
			WorkflowName: info.workflow,
			StepName:     info.step,
			Source:       src,
			OldDigest:    oldDigest,
			NewDigest:    digest,
			Reload:       n,
			Changes:      changes,
		})
	})
	if e.debugger != nil {
		e.debugger.rebind(file)
	}

	out["reloaded"] = true
	out["new_digest"] = digest
	out["reload"] = n
	out["changes"] = changes
	return &executor.Result{Output: out}, nil
}

func stringList(v any) ([]string, error) {
	switch val := v.(type) {
	case nil:
		return nil, nil
	case string:
		if val == "" {
			return nil, nil
		}
		return []string{val}, nil
	case []string:
		return val, nil
	case []any:
		out := make([]string, 0, len(val))
		for _, item := range val {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("expected a list of workflow names, got %v", item)
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, fmt.Errorf("expected a list of workflow names, got %T", v)
}

// diffFiles summarizes what a reload changes: workflows added and removed, the steps added,
// removed and changed in each remaining workflow (rescue and always steps prefixed as in state
// names), and which other top-level sections changed.
func diffFiles(old, new *parser.WorkflowFile) map[string]any {
	added, removed := []string{}, []string{}
	var changed []map[string]any
	for _, wf := range new.Workflows {
		if old.GetWorkflow(wf.Name) == nil {
			added = append(added, wf.Name)
		}
	}
	for _, wf := range old.Workflows {
		nw := new.GetWorkflow(wf.Name)
		if nw == nil {
			removed = append(removed, wf.Name)
			continue
		}
		if c := diffWorkflow(&wf, nw); c != nil {
			changed = append(changed, c)
		}
	}
	other := []string{}
	for _, f := range []struct {
		name     string
		old, new any
	}{
		{"entrypoint", old.Entrypoint, new.Entrypoint},
		{"vars", old.Vars, new.Vars},
		{"rules", old.Rules, new.Rules},
		{"step_types", old.StepTypes, new.StepTypes},
		{"connections", old.Connections, new.Connections},
		{"decisions", old.Decisions, new.Decisions},
	} {
		if !sameJSON(f.old, f.new) {
			other = append(other, f.name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	if changed == nil {
		changed = []map[string]any{}
	}
	return map[string]any{
		"added_workflows":   added,
		"removed_workflows": removed,
		"changed_workflows": changed,
		"other_changed":     other,
	}
}

func diffWorkflow(old, new *parser.Workflow) map[string]any {
	stepsOf := func(wf *parser.Workflow) (map[string]parser.Step, []string) {
		m := map[string]parser.Step{}
		var order []string
		for _, sec := range []string{sectionSteps, sectionRescue, sectionAlways} {
			for _, st := range sectionOfStepList(wf, sec) {
				key := st.Name
				if sec != sectionSteps {
					key = sec + "/" + st.Name
				}
				m[key] = st
				order = append(order, key)
			}
		}
		return m, order
	}
	om, oorder := stepsOf(old)
	nm, norder := stepsOf(new)
	added, removed, changed := []string{}, []string{}, []string{}
	for _, k := range norder {
		if _, ok := om[k]; !ok {
			added = append(added, k)
		}
	}
	for _, k := range oorder {
		ns, ok := nm[k]
		if !ok {
			removed = append(removed, k)
		} else if !sameJSON(om[k], ns) {
			changed = append(changed, k)
		}
	}
	// Reordering steps, or changing the workflow's vars, changes it even when no step differs.
	otherChanged := !sameJSON(old.Vars, new.Vars) || !reflect.DeepEqual(oorder, norder)
	if len(added)+len(removed)+len(changed) == 0 && !otherChanged {
		return nil
	}
	return map[string]any{
		"workflow":      old.Name,
		"added_steps":   added,
		"removed_steps": removed,
		"changed_steps": changed,
	}
}

func sameJSON(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ja) == string(jb)
}
