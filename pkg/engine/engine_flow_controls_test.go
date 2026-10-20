package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jctanner/markov/pkg/executor"
	"github.com/jctanner/markov/pkg/parser"
)

// recordExec records each call's params and fails when params["fail"] is true, returning
// partial output as k8s_job_wait does for a failed Job.
type recordExec struct {
	mu    sync.Mutex
	calls []map[string]any
}

func (r *recordExec) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	r.mu.Lock()
	r.calls = append(r.calls, params)
	r.mu.Unlock()
	if params["fail"] == true {
		return &executor.Result{Output: map[string]any{"status": "failed"}}, errors.New("job failed")
	}
	return &executor.Result{Output: map[string]any{"status": "completed", "msg": params["msg"]}}, nil
}

func (r *recordExec) messages() []any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []any
	for _, c := range r.calls {
		out = append(out, c["msg"])
	}
	return out
}

func runMain(t *testing.T, wf *parser.WorkflowFile, rec *recordExec) (map[string]any, error) {
	t.Helper()
	eng, _ := newTestEngine(t, wf, map[string]executor.Executor{"shell_exec": rec})
	runCtx := map[string]any{}
	for k, v := range wf.Vars {
		runCtx[k] = v
	}
	err := eng.executeWorkflow(context.Background(), "test-run", wf.GetWorkflow("main"), runCtx)
	return runCtx, err
}

func TestIgnoreErrorsRecordsFailureAndContinues(t *testing.T) {
	rec := &recordExec{}
	wf := &parser.WorkflowFile{Entrypoint: "main", Workflows: []parser.Workflow{{
		Name: "main",
		Steps: []parser.Step{
			{Name: "wait", Type: "shell_exec", Params: map[string]any{"fail": true}, IgnoreErrors: true, Register: "waited"},
			{Name: "after", Type: "shell_exec", Params: map[string]any{"msg": "{{ waited.status }} {{ waited.failed }}"}},
		},
	}}}
	runCtx, err := runMain(t, wf, rec)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	waited := runCtx["waited"].(map[string]any)
	if waited["failed"] != true || waited["error"] != "job failed" || waited["status"] != "failed" {
		t.Fatalf("registered output: %#v", waited)
	}
	if got := rec.messages(); !reflect.DeepEqual(got, []any{nil, "failed True"}) {
		t.Fatalf("calls: %#v", got)
	}
}

func TestWithoutIgnoreErrorsTheRunFails(t *testing.T) {
	rec := &recordExec{}
	wf := &parser.WorkflowFile{Entrypoint: "main", Workflows: []parser.Workflow{{
		Name: "main",
		Steps: []parser.Step{
			{Name: "wait", Type: "shell_exec", Params: map[string]any{"fail": true}},
			{Name: "after", Type: "shell_exec", Params: map[string]any{"msg": "ran"}},
		},
	}}}
	if _, err := runMain(t, wf, rec); err == nil {
		t.Fatal("expected failure")
	}
	if len(rec.calls) != 1 {
		t.Fatalf("step after a failure ran: %d calls", len(rec.calls))
	}
}

func TestIgnoreErrorsOnForEachKeepsGoing(t *testing.T) {
	rec := &recordExec{}
	wf := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars: map[string]any{"items": []any{
			map[string]any{"id": "a", "fail": false},
			map[string]any{"id": "b", "fail": true},
			map[string]any{"id": "c", "fail": false},
		}},
		Workflows: []parser.Workflow{
			{Name: "main", Steps: []parser.Step{{
				Name: "each", ForEach: "items", ForEachKey: "id", As: "item", Concurrency: 1,
				Workflow: "one", IgnoreErrors: true, Register: "done",
			}}},
			{Name: "one", Steps: []parser.Step{
				// The loop item is visible in the sub-workflow without passing it through vars.
				{Name: "work", Type: "shell_exec", Params: map[string]any{"msg": "{{ item.id }}", "fail": "{{ item.fail }}"}},
			}},
		},
	}
	runCtx, err := runMain(t, wf, rec)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got := rec.messages(); !reflect.DeepEqual(got, []any{"a", "b", "c"}) {
		t.Fatalf("calls: %#v", got)
	}
	done := runCtx["done"].([]map[string]any)
	if len(done) != 3 || done[1]["failed"] != true || done[0]["failed"] != nil {
		t.Fatalf("results: %#v", done)
	}
}

func TestForEachWhenFiltersItems(t *testing.T) {
	rec := &recordExec{}
	wf := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars: map[string]any{
			"tests": []any{map[string]any{"id": "S1"}, map[string]any{"id": "S2"}, map[string]any{"id": "S3"}},
			"only":  "S1, S3",
		},
		Workflows: []parser.Workflow{{Name: "main", Steps: []parser.Step{{
			Name: "each", Type: "shell_exec", ForEach: "tests", As: "test", Concurrency: 1,
			ForEachWhen: "not only or test.id in only | csv",
			Params:      map[string]any{"msg": "{{ test.id }}"},
		}}}},
	}
	if _, err := runMain(t, wf, rec); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got := rec.messages(); !reflect.DeepEqual(got, []any{"S1", "S3"}) {
		t.Fatalf("calls: %#v", got)
	}
}

func TestForEachOverFilterExpression(t *testing.T) {
	rec := &recordExec{}
	wf := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"repeats": 3, "variants": "", "own": []any{"haiku"}},
		Workflows: []parser.Workflow{{Name: "main", Steps: []parser.Step{
			{Name: "rounds", Type: "shell_exec", ForEach: "repeats | seq", As: "round", Concurrency: 1,
				Params: map[string]any{"msg": "{{ round }}"}},
			{Name: "variants", Type: "shell_exec", ForEach: "variants | csv | default:own", As: "v", Concurrency: 1,
				Params: map[string]any{"msg": "{{ v }}"}},
		}}},
	}
	if _, err := runMain(t, wf, rec); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got := rec.messages(); !reflect.DeepEqual(got, []any{1, 2, 3, "haiku"}) {
		t.Fatalf("calls: %#v", got)
	}
}

func TestDescriptionDoesNotChangeDefinitionHash(t *testing.T) {
	eng, _ := newTestEngine(t, &parser.WorkflowFile{Entrypoint: "main"}, nil)
	step := parser.Step{Name: "a", Type: "shell_exec", Params: map[string]any{"command": "true"}}
	described := step
	described.Description = "Explains the step."
	if eng.definitionHash(step) != eng.definitionHash(described) {
		t.Fatal("description changed the definition hash")
	}
	changed := step
	changed.IgnoreErrors = true
	if eng.definitionHash(step) == eng.definitionHash(changed) {
		t.Fatal("ignore_errors did not change the definition hash")
	}
}

func TestSetFactKeepsNativeValuesAndRendersMaps(t *testing.T) {
	eng, _ := newTestEngine(t, &parser.WorkflowFile{Entrypoint: "main"}, nil)
	runCtx := map[string]any{
		"tiers": map[string]any{"smoke": map[string]any{"repeats": 3}},
		"tier":  "smoke", "round": 2, "test": map[string]any{"id": "S1"},
	}
	facts, err := eng.evalFacts(map[string]any{
		"tier_def": "{{ tiers[tier] }}",
		"names":    "{{ 'a, b' | csv }}",
		"run": map[string]any{
			"id":     "r{{ round }}-{{ test.id }}",
			"repeat": "{{ round }}",
			"note":   "plain text stays text",
		},
		"count": "{{ round + 1 }}",
	}, runCtx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"tier_def": map[string]any{"repeats": 3},
		"names":    []any{"a", "b"},
		"run":      map[string]any{"id": "r2-S1", "repeat": 2, "note": "plain text stays text"},
		"count":    3,
	}
	if !reflect.DeepEqual(facts, want) {
		t.Fatalf("facts = %#v", facts)
	}
}

func TestSetFactFactsSeeContextBeforeTheStep(t *testing.T) {
	eng, _ := newTestEngine(t, &parser.WorkflowFile{Entrypoint: "main"}, nil)
	// Go map order is random, so repeat: a fact must never see a sibling's new value.
	for i := 0; i < 50; i++ {
		runCtx := map[string]any{"a": "old"}
		facts, err := eng.evalFacts(map[string]any{
			"a": "new", "b": "{{ a }}-b", "c": "{{ a }}-c", "d": "{{ a }}-d",
		}, runCtx)
		if err != nil {
			t.Fatal(err)
		}
		if facts["b"] != "old-b" || facts["c"] != "old-c" || facts["d"] != "old-d" {
			t.Fatalf("facts = %#v, want b, c and d rendered from a = old", facts)
		}
		if runCtx["a"] != "old" {
			t.Fatalf("evalFacts changed the context: %#v", runCtx)
		}
	}
}

func TestFailedWhen(t *testing.T) {
	cases := []struct {
		name       string
		params     map[string]any
		failedWhen string
		ignore     bool
		wantErr    bool
		wantFailed any
	}{
		{"success turned into failure", map[string]any{"msg": "bad"}, "result.msg == 'bad'", false, true, nil},
		{"executor error turned into success", map[string]any{"fail": true}, "result.status != 'failed'", false, false, nil},
		{"register name is bound too", map[string]any{"msg": "ok"}, "out.msg != 'ok'", false, false, nil},
		{"error text is visible", map[string]any{"fail": true}, "'job failed' in result.error", false, true, nil},
		{"with ignore_errors", map[string]any{"msg": "bad"}, "result.msg == 'bad'", true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordExec{}
			wf := &parser.WorkflowFile{Entrypoint: "main", Workflows: []parser.Workflow{{
				Name: "main",
				Steps: []parser.Step{{Name: "s", Type: "shell_exec", Params: tc.params,
					FailedWhen: tc.failedWhen, IgnoreErrors: tc.ignore, Register: "out"}},
			}}}
			runCtx, err := runMain(t, wf, rec)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr {
				if got := runCtx["out"].(map[string]any)["failed"]; got != tc.wantFailed {
					t.Fatalf("failed = %v, want %v", got, tc.wantFailed)
				}
			}
		})
	}
}

func TestForEachStopsAfterAFailureWithConcurrencyOne(t *testing.T) {
	rec := &recordExec{}
	wf := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars: map[string]any{"items": []any{
			map[string]any{"id": "a", "fail": false},
			map[string]any{"id": "b", "fail": true},
			map[string]any{"id": "c", "fail": false},
		}},
		Workflows: []parser.Workflow{
			{Name: "main", Steps: []parser.Step{{
				Name: "each", ForEach: "items", ForEachKey: "id", As: "item", Concurrency: 1, Workflow: "one",
			}}},
			{Name: "one", Steps: []parser.Step{
				{Name: "work", Type: "shell_exec", Params: map[string]any{"msg": "{{ item.id }}", "fail": "{{ item.fail }}"}},
			}},
		},
	}
	if _, err := runMain(t, wf, rec); err == nil {
		t.Fatal("expected the loop to fail")
	}
	if got := rec.messages(); !reflect.DeepEqual(got, []any{"a", "b"}) {
		t.Fatalf("items run: %#v (c must not start after b failed)", got)
	}
}

func TestResumeKeepsWholeNumbersAsInts(t *testing.T) {
	rec := &recordExec{}
	wf := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"weight": 2, "allow": false},
		Workflows: []parser.Workflow{{Name: "main", Steps: []parser.Step{
			{Name: "count", Type: "set_fact", Vars: map[string]any{"total": "{{ weight + 1 }}"}},
			{Name: "gate", Type: "assert", That: []string{"allow"}},
			{Name: "use", Type: "shell_exec", Params: map[string]any{"msg": "{{ weight }}/{{ total }}"}},
		}}},
	}
	eng, _ := newTestEngine(t, wf, map[string]executor.Executor{"shell_exec": rec})
	runID, err := eng.Run(context.Background(), "main", nil)
	if err == nil {
		t.Fatal("first attempt should fail at the assert")
	}
	if err := eng.ResumeWithVars(context.Background(), runID, map[string]any{"allow": true}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got := rec.messages(); !reflect.DeepEqual(got, []any{"2/3"}) {
		t.Fatalf("after resume: %#v (numbers must not come back as floats)", got)
	}
}

func templatedWorkflowFile() *parser.WorkflowFile {
	return &parser.WorkflowFile{
		Entrypoint: "main",
		Vars: map[string]any{
			"tests": []any{map[string]any{"id": "S1", "arm": "workflow"}, map[string]any{"id": "S2", "arm": "bash"}},
			"go":    false,
		},
		Workflows: []parser.Workflow{
			{Name: "main", Steps: []parser.Step{
				{Name: "each", ForEach: "tests", ForEachKey: "id", As: "test", Concurrency: 1, Workflow: "submit-{{ test.arm }}"},
				{Name: "after", Type: "assert", That: []string{"go"}},
			}},
			{Name: "submit-workflow", Steps: []parser.Step{{Name: "s", Type: "shell_exec", Params: map[string]any{"msg": "wf {{ test.id }}"}}}},
			{Name: "submit-bash", Steps: []parser.Step{{Name: "s", Type: "shell_exec", Params: map[string]any{"msg": "bash {{ test.id }}"}}}},
		},
	}
}

func TestTemplatedSubWorkflowPerItem(t *testing.T) {
	rec := &recordExec{}
	wf := templatedWorkflowFile()
	wf.Vars["go"] = true
	if _, err := runMain(t, wf, rec); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got := rec.messages(); !reflect.DeepEqual(got, []any{"wf S1", "bash S2"}) {
		t.Fatalf("calls: %#v", got)
	}
}

func TestTemplatedSubWorkflowUnknownName(t *testing.T) {
	wf := templatedWorkflowFile()
	wf.Vars["tests"] = []any{map[string]any{"id": "S9", "arm": "nope"}}
	_, err := runMain(t, wf, &recordExec{})
	if err == nil || !strings.Contains(err.Error(), `sub-workflow "submit-nope" (from "submit-{{ test.arm }}") not found`) {
		t.Fatalf("err = %v", err)
	}
}

func TestResumeRefusesSubRunInADifferentWorkflow(t *testing.T) {
	rec := &recordExec{}
	wf := templatedWorkflowFile()
	eng, _ := newTestEngine(t, wf, map[string]executor.Executor{"shell_exec": rec})
	runID, err := eng.Run(context.Background(), "main", nil)
	if err == nil {
		t.Fatal("first attempt should fail at the assert")
	}
	// The same item now resolves to the other workflow.
	err = eng.ResumeWithVars(context.Background(), runID, map[string]any{
		"go":    true,
		"tests": []any{map[string]any{"id": "S1", "arm": "bash"}, map[string]any{"id": "S2", "arm": "bash"}},
	})
	if err == nil || !strings.Contains(err.Error(), `ran workflow "submit-workflow", but "submit-{{ test.arm }}" now resolves to "submit-bash"`) {
		t.Fatalf("err = %v", err)
	}
	// Resolving the same way resumes normally.
	if err := eng.ResumeWithVars(context.Background(), runID, map[string]any{"go": true}); err != nil {
		t.Fatalf("resume with the same resolution: %v", err)
	}
}

func TestMarkovRunIDVar(t *testing.T) {
	rec := &recordExec{}
	wf := &parser.WorkflowFile{Entrypoint: "main", Workflows: []parser.Workflow{{Name: "main", Steps: []parser.Step{
		{Name: "s", Type: "shell_exec", Params: map[string]any{"msg": "{{ markov_run_id }}"}},
	}}}}
	eng, _ := newTestEngine(t, wf, map[string]executor.Executor{"shell_exec": rec})
	eng.RunID = "run-123"
	if _, err := eng.Run(context.Background(), "main", nil); err != nil {
		t.Fatal(err)
	}
	if got := rec.messages(); !reflect.DeepEqual(got, []any{"run-123"}) {
		t.Fatalf("calls: %#v", got)
	}
}

func TestWorkflowNamesLimitTemplatedTargets(t *testing.T) {
	wf := templatedWorkflowFile()
	wf.Vars["go"] = true
	wf.Workflows[0].Steps[0].WorkflowNames = []string{"submit-workflow", "submit-bash"}
	if _, err := runMain(t, wf, &recordExec{}); err != nil {
		t.Fatalf("listed targets: %v", err)
	}
	wf.Workflows[0].Steps[0].WorkflowNames = []string{"submit-workflow"}
	_, err := runMain(t, wf, &recordExec{})
	if err == nil || !strings.Contains(err.Error(), `resolved to "submit-bash"; expected one of submit-workflow (workflow_names)`) {
		t.Fatalf("err = %v", err)
	}
}
