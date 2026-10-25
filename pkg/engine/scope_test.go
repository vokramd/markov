package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jctanner/markov/pkg/executor"
	"github.com/jctanner/markov/pkg/parser"
)

// emits returns a fixed output, for a register a child must not see.
type emits map[string]any

func (o emits) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	return &executor.Result{Output: map[string]any(o)}, nil
}

func seen(id string, names ...string) parser.Step {
	params := map[string]any{"id": id}
	for _, n := range names {
		params[n] = "{{ " + n + " }}"
	}
	return parser.Step{Name: id, Type: "rec", Params: params}
}

func empty(v any) bool { return v == nil || v == "" }

// A called workflow sees the globals, its own defaults, what the call passes and renders, and
// the loop item; not the caller's registers or facts.
func TestCalledWorkflowSeesOnlyWhatItIsGiven(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"api": "https://example", "items": []any{"a"}},
		Workflows: []parser.Workflow{
			{Name: "main", Steps: []parser.Step{
				{Name: "listing", Type: "emit", Register: "listing"},
				{Name: "fact", Type: "set_fact", Vars: map[string]any{"local": "{{ 'mine' }}"}},
				{Name: "call", Workflow: "child", Pass: []string{"listing"}, Vars: map[string]any{"rendered": "{{ local }}!"}},
				{Name: "each", ForEach: "items", As: "item", Workflow: "child2"},
			}},
			{Name: "child", Vars: map[string]any{"limit": 3}, Steps: []parser.Step{
				seen("in_child", "api", "limit", "listing", "rendered", "local"),
			}},
			{Name: "child2", Steps: []parser.Step{seen("in_each", "api", "item", "listing", "local")}},
		},
	}
	rec := &paramsByID{}
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"rec": rec, "emit": emits{"rows": []any{1, 2}}})
	if _, err := eng.Run(context.Background(), "", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	c := rec.calls["in_child"]
	if c["api"] != "https://example" || c["limit"] != 3 || c["rendered"] != "mine!" {
		t.Errorf("child: api %v, limit %v, rendered %v", c["api"], c["limit"], c["rendered"])
	}
	if l, ok := c["listing"].(map[string]any); !ok || fmt.Sprint(l["rows"]) != "[1 2]" {
		t.Errorf("child: passed listing = %#v, want the register as it is", c["listing"])
	}
	if !empty(c["local"]) {
		t.Errorf("child sees the caller's fact local = %#v", c["local"])
	}
	e := rec.calls["in_each"]
	if e["api"] != "https://example" || e["item"] != "a" {
		t.Errorf("each: api %v, item %v", e["api"], e["item"])
	}
	if !empty(e["listing"]) || !empty(e["local"]) {
		t.Errorf("each sees the caller's listing %#v / local %#v", e["listing"], e["local"])
	}
}

// pass hands the value over as it is: not rendered to a string, so types are kept.
func TestPassKeepsTheValue(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{
			{Name: "main", Steps: []parser.Step{
				{Name: "listing", Type: "emit", Register: "listing"},
				{Name: "call", Workflow: "child", Pass: []string{"listing"}},
			}},
			{Name: "child", Steps: []parser.Step{{Name: "use", Type: "capture", Params: map[string]any{"got": "{{ listing.rows }}"}}}},
		},
	}
	capture := &captureExec{}
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"capture": capture, "emit": emits{"rows": []any{1, 2}}})
	if _, err := eng.Run(context.Background(), "", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rows, ok := capture.params["got"].([]any); !ok || len(rows) != 2 || rows[0] != 1 {
		t.Fatalf("got %#v (%T), want []any{1, 2}", capture.params["got"], capture.params["got"])
	}
}

// A call that sets a global's name shadows it for the called workflow and below; the caller and
// its other calls keep the run's value.
func TestShadowingAGlobalReachesGrandchildrenOnly(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"token": "run-token"},
		Workflows: []parser.Workflow{
			{Name: "main", Steps: []parser.Step{
				{Name: "call", Workflow: "child", Vars: map[string]any{"token": "minted"}},
				{Name: "other", Workflow: "grandchild"},
				seen("in_main", "token"),
			}},
			{Name: "child", Steps: []parser.Step{seen("in_child", "token"), {Name: "down", Workflow: "grandchild2"}}},
			{Name: "grandchild", Steps: []parser.Step{seen("in_other", "token")}},
			{Name: "grandchild2", Steps: []parser.Step{seen("in_grandchild", "token")}},
		},
	}
	rec := &paramsByID{}
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"rec": rec})
	if _, err := eng.Run(context.Background(), "", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for id, want := range map[string]string{"in_child": "minted", "in_grandchild": "minted", "in_other": "run-token", "in_main": "run-token"} {
		if got := rec.calls[id]["token"]; got != want {
			t.Errorf("%s: token = %v, want %s", id, got, want)
		}
	}
}

// A call's register keeps what the child was passed and made, not the globals.
func TestCalledWorkflowRegisterLeavesOutGlobals(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"big": strings.Repeat("x", 1000)},
		Workflows: []parser.Workflow{
			{Name: "main", Steps: []parser.Step{
				{Name: "call", Workflow: "child", Vars: map[string]any{"n": 1}, Register: "out"},
				{Name: "use", Type: "capture", Params: map[string]any{"got": "{{ out }}"}},
			}},
			{Name: "child", Steps: []parser.Step{{Name: "made", Type: "emit", Register: "made"}}},
		},
	}
	capture := &captureExec{}
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"capture": capture, "emit": emits{"ok": true}})
	if _, err := eng.Run(context.Background(), "", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := capture.params["got"].(map[string]any)
	if got["n"] != 1 || got["made"] == nil {
		t.Fatalf("register = %#v, want n and made", got)
	}
	for k := range got {
		if k == "big" || strings.HasPrefix(k, "markov_") {
			t.Errorf("register holds global %q", k)
		}
	}
}

// A parameter declared with no default (~) must be given: by the call, or with --var for the
// entry workflow. A global of the same name counts.
func TestRequiredParameters(t *testing.T) {
	file := func(callVars map[string]any) *parser.WorkflowFile {
		return &parser.WorkflowFile{
			Entrypoint: "main",
			Vars:       map[string]any{"token": "t"},
			Workflows: []parser.Workflow{
				{Name: "main", Steps: []parser.Step{{Name: "call", Workflow: "child", Vars: callVars}}},
				{Name: "child", Vars: map[string]any{"issue": nil, "token": nil}, Steps: []parser.Step{seen("in_child", "issue", "token")}},
			},
		}
	}
	rec := &paramsByID{}
	eng, _ := newTestEngine(t, file(nil), map[string]executor.Executor{"rec": rec})
	_, err := eng.Run(context.Background(), "", nil)
	if err == nil || !strings.Contains(err.Error(), `workflow "child" needs issue`) {
		t.Fatalf("Run = %v, want the missing parameter named", err)
	}
	eng, _ = newTestEngine(t, file(map[string]any{"issue": 7}), map[string]executor.Executor{"rec": rec})
	if _, err := eng.Run(context.Background(), "", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c := rec.calls["in_child"]; c["issue"] != 7 || c["token"] != "t" {
		t.Fatalf("child: issue %v, token %v", c["issue"], c["token"])
	}

	entry := &parser.WorkflowFile{Entrypoint: "main", Workflows: []parser.Workflow{
		{Name: "main", Vars: map[string]any{"target": nil}, Steps: []parser.Step{seen("top", "target")}},
	}}
	eng, _ = newTestEngine(t, entry, map[string]executor.Executor{"rec": rec})
	if _, err := eng.Run(context.Background(), "", nil); err == nil || !strings.Contains(err.Error(), "--var") {
		t.Fatalf("Run = %v, want the entry workflow's missing parameter", err)
	}
	if _, err := eng.Run(context.Background(), "", map[string]any{"target": "x"}); err != nil {
		t.Fatalf("Run with --var: %v", err)
	}
}

// A resumed run builds a called workflow's context the same way: the globals and what is
// passed, not the caller's other registers.
func TestResumedChildGetsTheSameScope(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"api": "https://example"},
		Workflows: []parser.Workflow{
			{Name: "main", Steps: []parser.Step{
				{Name: "listing", Type: "emit", Register: "listing"},
				{Name: "other", Type: "emit", Register: "other"},
				{Name: "call", Workflow: "child", Pass: []string{"listing"}},
			}},
			{Name: "child", Steps: []parser.Step{
				{Name: "first", Type: "rec", Params: map[string]any{"id": "first", "fail": true}},
				seen("after", "api", "listing", "other"),
			}},
		},
	}
	rec := &paramsByID{}
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"rec": rec, "emit": emits{"rows": []any{1}}})
	runID, err := eng.Run(context.Background(), "", nil)
	if err == nil {
		t.Fatal("Run succeeded, want the child's first step to fail")
	}
	rec.allow = true
	if err := eng.Resume(context.Background(), runID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	a := rec.calls["after"]
	if a["api"] != "https://example" || a["listing"] == nil || !empty(a["other"]) {
		t.Fatalf("after resume: api %v, listing %v, other %v", a["api"], a["listing"], a["other"])
	}
}
