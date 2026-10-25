package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/jctanner/markov/pkg/callback"
	"github.com/jctanner/markov/pkg/executor"
	"github.com/jctanner/markov/pkg/outputlimit"
	"github.com/jctanner/markov/pkg/parser"
)

func size(n int64) outputlimit.Size { return outputlimit.Size{Bytes: n, Set: true} }

// failedEvents are the step_failed events a run sent.
func failedEvents(cb *mockCallback) []callback.StepFailedEvent {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	var out []callback.StepFailedEvent
	for _, e := range cb.all {
		if f, ok := e.(callback.StepFailedEvent); ok {
			out = append(out, f)
		}
	}
	return out
}

func TestOutputLimitPrecedence(t *testing.T) {
	file := &parser.WorkflowFile{StepTypes: map[string]parser.StepType{"typed": {Base: "shell_exec", MaxOutput: size(300)}}}
	eng := New(file, nil, map[string]executor.Executor{})
	ctx := context.Background()
	if got := eng.outputLimit(ctx, &parser.Step{Type: "shell_exec"}); got != outputlimit.Default {
		t.Errorf("default = %d", got)
	}
	eng.MaxOutput = size(100)
	if got := eng.outputLimit(ctx, &parser.Step{Type: "shell_exec"}); got != 100 {
		t.Errorf("--max-output = %d", got)
	}
	file.MaxOutput = size(200)
	if got := eng.outputLimit(ctx, &parser.Step{Type: "shell_exec"}); got != 200 {
		t.Errorf("file = %d", got)
	}
	if got := eng.outputLimit(ctx, &parser.Step{Type: "typed"}); got != 300 {
		t.Errorf("step type = %d", got)
	}
	if got := eng.outputLimit(ctx, &parser.Step{Type: "typed", MaxOutput: size(0)}); got != 0 {
		t.Errorf("step 0 = %d, want no limit", got)
	}
}

// A set_fact that bundles registers into something huge fails at its limit, with a breakdown in
// its step_failed event and nothing of the value in the event or the store.
func TestSetFactOverItsLimitFails(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		MaxOutput:  size(2000),
		Workflows: []parser.Workflow{{Name: "main", Steps: []parser.Step{
			{Name: "listing", Type: "emit", Register: "listing"},
			{Name: "bundle", Type: "set_fact", Vars: map[string]any{"inputs": map[string]any{"a": "{{ listing }}", "b": "{{ listing }}"}}},
			{Name: "after", Type: "emit"},
		}}},
	}
	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{"emit": emits{"rows": strings.Repeat("r", 1500)}})
	_, err := eng.Run(context.Background(), "", nil)
	if _, ok := outputlimit.As(err); !ok {
		t.Fatalf("Run = %v, want a max_output failure", err)
	}
	f := failedEvents(cb)
	if len(f) == 0 || f[0].StepName != "bundle" || f[0].ErrorKind != "max_output" || f[0].LimitBytes != 2000 || f[0].OutputBytes < 3000 || f[0].Output != nil {
		t.Fatalf("step_failed = %+v", f)
	}
	if len(f[0].Largest) == 0 || f[0].Largest[0].Path != "inputs" {
		t.Errorf("largest = %+v", f[0].Largest)
	}
}

// An executor's output over the limit fails the step; ignore_errors records it and carries on.
func TestExecutorOutputOverItsLimit(t *testing.T) {
	big := emits{"stdout": strings.Repeat("x", 5000)}
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{{Name: "main", Steps: []parser.Step{
			{Name: "loud", Type: "emit", Register: "loud", MaxOutput: size(1000), IgnoreErrors: true},
			{Name: "use", Type: "capture", Params: map[string]any{"got": "{{ loud }}"}},
			{Name: "unlimited", Type: "emit", MaxOutput: size(0)},
		}}},
	}
	capture := &captureExec{}
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"emit": big, "capture": capture})
	if _, err := eng.Run(context.Background(), "", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := capture.params["got"].(map[string]any)
	if got["failed"] != true || !strings.Contains(got["error"].(string), "over max_output 1000 B") || got["stdout"] != nil {
		t.Fatalf("registered = %#v, want the failure without the output", got)
	}
}

// A for_each register, or a sub-workflow's, over the limit fails its step.
func TestRegistersOverTheirLimit(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"items": []any{1, 2, 3, 4}},
		Workflows: []parser.Workflow{
			{Name: "main", Steps: []parser.Step{
				{Name: "each", ForEach: "items", As: "item", Type: "emit", Register: "each", MaxOutput: size(1500)},
			}},
			{Name: "calls", Steps: []parser.Step{
				{Name: "call", Workflow: "child", Register: "out", MaxOutput: size(500)},
			}},
			{Name: "child", Steps: []parser.Step{{Name: "made", Type: "emit", Register: "made"}}},
		},
	}
	execs := map[string]executor.Executor{"emit": emits{"rows": strings.Repeat("r", 600)}}
	eng, cb := newTestEngine(t, wfFile, execs)
	if _, err := eng.Run(context.Background(), "main", nil); err == nil || !strings.Contains(err.Error(), "over max_output") {
		t.Fatalf("for_each Run = %v", err)
	}
	if f := failedEvents(cb); len(f) == 0 || f[len(f)-1].StepName != "each" || f[len(f)-1].ErrorKind != "max_output" {
		t.Fatalf("for_each step_failed = %+v", f)
	}
	eng, cb = newTestEngine(t, wfFile, execs)
	if _, err := eng.Run(context.Background(), "calls", nil); err == nil || !strings.Contains(err.Error(), "over max_output") {
		t.Fatalf("call Run = %v", err)
	}
	if f := failedEvents(cb); len(f) == 0 || f[len(f)-1].StepName != "call" || f[len(f)-1].ErrorKind != "max_output" {
		t.Fatalf("call step_failed = %+v", f)
	}
}
