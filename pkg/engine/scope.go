package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jctanner/markov/pkg/parser"
)

// Variable scope. The file-level vars are the run's globals: every workflow sees them, and no
// step changes them. A workflow's own vars are its parameters. What a workflow registers or sets
// is its own: a workflow it calls sees it only when the call passes it (pass: or vars:).

// builtinPrefix marks the built-in vars (markov_run_id and the like), which every workflow sees.
const builtinPrefix = "markov_"

// isGlobal reports whether name is one of the run's globals or a built-in.
func isGlobal(file *parser.WorkflowFile, name string) bool {
	if strings.HasPrefix(name, builtinPrefix) {
		return true
	}
	_, ok := file.Vars[name]
	return ok
}

// childContext is the starting context of a workflow called from caller: the globals and
// built-ins as the caller sees them (so a call that shadows a global reaches the grandchildren
// too); the child's own vars defaults; in a for_each, the item; what the call passes by name
// (pass:) and renders (vars:, against the caller). Nothing else of the caller's.
func (e *Engine) childContext(ctx context.Context, step parser.Step, wf *parser.Workflow, caller map[string]any) (map[string]any, error) {
	file := e.fileFor(ctx)
	sub := make(map[string]any)
	for k, v := range caller {
		if isGlobal(file, k) {
			sub[k] = v
		}
	}
	for k, v := range wf.Vars {
		if v != nil {
			sub[k] = v
		}
	}
	if step.ForEach != "" {
		sub[step.As] = caller[step.As]
	}
	for _, name := range step.Pass {
		v, ok := caller[name]
		if !ok {
			return nil, fmt.Errorf("pass: %q is not set here, so it cannot be passed to workflow %q", name, wf.Name)
		}
		sub[name] = v
	}
	if step.Vars != nil {
		rendered, err := e.tmpl.RenderMap(step.Vars, caller)
		if err != nil {
			return nil, fmt.Errorf("rendering sub-workflow vars: %w", err)
		}
		for k, v := range rendered {
			if s, ok := v.(string); ok {
				sub[k] = coerceString(s)
			} else {
				sub[k] = v
			}
		}
	}
	if missing := missingParams(wf, sub); len(missing) > 0 {
		return nil, fmt.Errorf("workflow %q needs %s (declared with no default); pass it with pass: or vars:", wf.Name, strings.Join(missing, ", "))
	}
	return sub, nil
}

// missingParams lists a workflow's parameters declared with no default (~) that ctx does not set.
func missingParams(wf *parser.Workflow, ctx map[string]any) []string {
	var missing []string
	for k, v := range wf.Vars {
		if v != nil {
			continue
		}
		if _, ok := ctx[k]; !ok {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	return missing
}

// childResult is what a call's register keeps of a called workflow's final context: what it was
// passed and what it made, without the globals and built-ins the caller already has.
func (e *Engine) childResult(ctx context.Context, sub map[string]any) map[string]any {
	file := e.fileFor(ctx)
	out := make(map[string]any, len(sub))
	for k, v := range sub {
		if !isGlobal(file, k) {
			out[k] = v
		}
	}
	return out
}
