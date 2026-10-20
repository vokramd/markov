package engine

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/jctanner/markov/pkg/executor"
	"github.com/jctanner/markov/pkg/parser"
)

// paramsByID records the params of every call, keyed by the "id" param, and fails a call whose
// "fail" param is true until allowed.
type paramsByID struct {
	mu    sync.Mutex
	calls map[string]map[string]any
	allow bool
}

func (r *paramsByID) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = map[string]map[string]any{}
	}
	r.calls[fmt.Sprint(params["id"])] = params
	if params["fail"] == true && !r.allow {
		return nil, fmt.Errorf("failing until allowed")
	}
	return &executor.Result{Output: map[string]any{}}, nil
}

func builtinStep(id string) parser.Step {
	return parser.Step{Name: id, Type: "rec", Params: map[string]any{
		"id":   id,
		"run":  "{{ markov_run_id }}",
		"cur":  "{{ markov_current_run_id }}",
		"logs": "{{ markov_event_logs }}",
	}}
}

func TestBuiltinRunIDsAndEventLogs(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"items": []any{"x"}},
		Workflows: []parser.Workflow{
			{Name: "main", Steps: []parser.Step{
				builtinStep("top"),
				{Name: "call", Workflow: "child"},
				{Name: "each", ForEach: "items", As: "item", Workflow: "child2"},
				{Name: "last", Type: "rec", Params: map[string]any{"id": "last", "fail": true, "logs": "{{ markov_event_logs }}"}},
			}},
			{Name: "child", Steps: []parser.Step{builtinStep("in_child")}},
			{Name: "child2", Steps: []parser.Step{builtinStep("in_each")}},
		},
	}
	rec := &paramsByID{}
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"rec": rec})
	eng.EventLogs = []string{"/logs/a.jsonl", "/logs/b.jsonl"}

	runID, err := eng.Run(context.Background(), "", nil)
	if err == nil {
		t.Fatal("Run succeeded, want the last step to fail")
	}
	want := map[string]string{"top": runID, "in_child": runID + "-call", "in_each": runID + "-each-0"}
	for id, cur := range want {
		p := rec.calls[id]
		if p["run"] != runID || p["cur"] != cur {
			t.Errorf("%s: markov_run_id = %v, markov_current_run_id = %v; want %s, %s", id, p["run"], p["cur"], runID, cur)
		}
		if fmt.Sprint(p["logs"]) != "[/logs/a.jsonl /logs/b.jsonl]" {
			t.Errorf("%s: markov_event_logs = %#v", id, p["logs"])
		}
	}

	// A resumed run sees the logs of the resuming process.
	eng.EventLogs = []string{"/logs/resumed.jsonl"}
	rec.allow = true
	if err := eng.Resume(context.Background(), runID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got := fmt.Sprint(rec.calls["last"]["logs"]); got != "[/logs/resumed.jsonl]" {
		t.Fatalf("after resume markov_event_logs = %s, want [/logs/resumed.jsonl]", got)
	}
}

func TestEventLogsIsAnEmptyListWithoutJSONLCallbacks(t *testing.T) {
	wfFile := &parser.WorkflowFile{Entrypoint: "main", Workflows: []parser.Workflow{{Name: "main", Steps: []parser.Step{
		{Name: "n", Type: "rec", Params: map[string]any{"id": "n", "count": "{{ markov_event_logs | length }}"}},
	}}}}
	rec := &paramsByID{}
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"rec": rec})
	if _, err := eng.Run(context.Background(), "", nil); err != nil {
		t.Fatal(err)
	}
	if got := rec.calls["n"]["count"]; got != 0 {
		t.Fatalf("markov_event_logs | length = %#v, want 0", got)
	}
}
