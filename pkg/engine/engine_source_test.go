package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vokramd/markov/pkg/executor"
	"github.com/vokramd/markov/pkg/parser"
	"github.com/vokramd/markov/pkg/state"
)

func TestRunStoresWorkflowSourcePath(t *testing.T) {
	store, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{Name: "run_child", Workflow: "child"},
				},
			},
			{
				Name: "child",
				Steps: []parser.Step{
					{Name: "mark_done", Type: "set_fact", Vars: map[string]any{"done": true}},
				},
			},
		},
	}

	eng := New(wfFile, store, nil)
	eng.SourcePath = t.TempDir()
	if err := os.WriteFile(filepath.Join(eng.SourcePath, "workflow.yaml"), []byte("entrypoint: main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runID, err := eng.Run(context.Background(), "main", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	run, err := store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.WorkflowFile != eng.SourcePath {
		t.Fatalf("WorkflowFile = %q, want %q", run.WorkflowFile, eng.SourcePath)
	}

	children, err := store.GetChildRuns(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetChildRuns: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("len(children) = %d, want 1", len(children))
	}
	if children[0].WorkflowFile != eng.SourcePath {
		t.Fatalf("child WorkflowFile = %q, want %q", children[0].WorkflowFile, eng.SourcePath)
	}
}

func TestSubWorkflowJSONVarPreservedInRenderedBody(t *testing.T) {
	store, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	capture := &captureParamsExecutor{}
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{
						Name:     "run_child",
						Workflow: "child",
						Vars: map[string]any{
							"config": `{"key":"value"}`,
						},
					},
				},
			},
			{
				Name: "child",
				Steps: []parser.Step{
					{
						Name: "submit",
						Type: "capture",
						Params: map[string]any{
							"body": map[string]any{
								"args": map[string]any{
									"config": "{{ config }}",
								},
							},
						},
					},
				},
			},
		},
	}

	eng := New(wfFile, store, map[string]executor.Executor{"capture": capture})
	if _, err := eng.Run(context.Background(), "main", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	body, ok := capture.params["body"].(map[string]any)
	if !ok {
		t.Fatalf("body = %#v, want map", capture.params["body"])
	}
	args, ok := body["args"].(map[string]any)
	if !ok {
		t.Fatalf("args = %#v, want map", body["args"])
	}
	config, ok := args["config"].(map[string]any)
	if !ok {
		t.Fatalf("config = %#v, want map", args["config"])
	}
	if config["key"] != "value" {
		t.Fatalf("key = %v, want value", config["key"])
	}
}

func TestExtractFromJSONExprAcceptsAlias(t *testing.T) {
	path, ok := extractFromJSONExpr("{{ raw_config | from_json }}")
	if !ok {
		t.Fatal("extractFromJSONExpr() ok = false, want true")
	}
	if path != "raw_config" {
		t.Fatalf("path = %q, want raw_config", path)
	}
}

type captureParamsExecutor struct {
	params map[string]any
}

func (c *captureParamsExecutor) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	c.params = params
	return &executor.Result{Output: map[string]any{"ok": true}}, nil
}
