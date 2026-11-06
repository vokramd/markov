package engine

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jctanner/markov/pkg/callback"
	"github.com/jctanner/markov/pkg/executor"
	"github.com/jctanner/markov/pkg/parser"
	"github.com/jctanner/markov/pkg/state"
)

// counterWorkflow repeats `count`, which turns n into next = n + 1; each iteration is passed
// the previous one's next. %s is the repeat block and anything else on the step.
const counterWorkflow = `
entrypoint: main
vars:
  limit: 3
workflows:
  - name: main
    steps:
      - name: loop
        workflow: count
        register: loop
        vars:
          n: "{% if repeat.previous %}{{ repeat.previous.next }}{% else %}0{% endif %}"
          round: "{{ repeat.index }}"
%s
  - name: count
    vars:
      n: ~
      round: ~
    steps:
      - name: bump
        type: set_fact
        vars:
          next: "{{ n + 1 }}"
`

func parseOrFatal(t *testing.T, src string) *parser.WorkflowFile {
	t.Helper()
	file, err := parser.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return file
}

// loopEnvelope is the loop step's envelope, as the run left it (the root run's completed step).
func loopEnvelope(t *testing.T, eng *Engine, runID, step string) map[string]any {
	t.Helper()
	s, err := eng.store.GetStep(context.Background(), runID, "main", step)
	if err != nil || s == nil {
		t.Fatalf("no %s step row: %v", step, err)
	}
	var env map[string]any
	decodeState(s.OutputJSON, &env)
	return env
}

func repeatChildren(t *testing.T, eng *Engine, runID string) []subRunOwner {
	t.Helper()
	children, err := eng.store.GetChildRuns(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	var owners []subRunOwner
	for _, c := range children {
		if o, ok := repeatOwner(c); ok {
			if c.ParentRunID != runID {
				t.Errorf("iteration %s has parent %s, want the calling run %s", c.RunID, c.ParentRunID, runID)
			}
			owners = append(owners, o)
		}
	}
	sort.Slice(owners, func(i, j int) bool {
		if owners[i].Generation != owners[j].Generation {
			return owners[i].Generation < owners[j].Generation
		}
		return owners[i].Index < owners[j].Index
	})
	return owners
}

func TestRepeatStopsWhenUntilIsTrue(t *testing.T) {
	file := parseOrFatal(t, strings.Replace(counterWorkflow, "%s", `        repeat:
          until: "next >= limit"
          max: 10`, 1))
	eng, _ := newTestEngine(t, file, nil)
	runID, err := eng.Run(context.Background(), "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	env := loopEnvelope(t, eng, runID, "loop")
	last, _ := env["last"].(map[string]any)
	if env["count"] != 3 || env["stopped_by"] != "until" || last["next"] != 3 || last["round"] != 2 {
		t.Errorf("envelope = %v", env)
	}
	if _, leaked := last["repeat"]; leaked {
		t.Errorf("repeat leaked into the iteration's results: %v", last)
	}
	owners := repeatChildren(t, eng, runID)
	if len(owners) != 3 || owners[0].Index != 0 || owners[2].Index != 2 || owners[2].Generation != 0 {
		t.Errorf("iterations = %+v", owners)
	}
}

func TestRepeatUntilOnTheLastAllowedIterationSucceeds(t *testing.T) {
	file := parseOrFatal(t, strings.Replace(counterWorkflow, "%s", `        repeat:
          until: "next >= limit"
          max: "{{ limit }}"`, 1))
	eng, _ := newTestEngine(t, file, nil)
	runID, err := eng.Run(context.Background(), "main", nil)
	if err != nil {
		t.Fatalf("until became true on iteration max: %v", err)
	}
	if env := loopEnvelope(t, eng, runID, "loop"); env["count"] != 3 || env["stopped_by"] != "until" {
		t.Errorf("envelope = %v", env)
	}
}

func TestRepeatMax(t *testing.T) {
	stop := parseOrFatal(t, strings.Replace(counterWorkflow, "%s", `        repeat:
          until: "false"
          max: 2
          on_max: stop`, 1))
	eng, _ := newTestEngine(t, stop, nil)
	runID, err := eng.Run(context.Background(), "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if env := loopEnvelope(t, eng, runID, "loop"); env["count"] != 2 || env["stopped_by"] != "max" {
		t.Errorf("on_max stop: envelope = %v", env)
	}

	fail := parseOrFatal(t, strings.Replace(counterWorkflow, "%s", `        repeat:
          until: "false"
          max: 2`, 1))
	eng, _ = newTestEngine(t, fail, nil)
	if _, err := eng.Run(context.Background(), "main", nil); err == nil || !strings.Contains(err.Error(), "ran its max of 2") {
		t.Errorf("on_max fail: err = %v", err)
	}
}

func TestRepeatBadMaxFailsBeforeTheFirstIteration(t *testing.T) {
	file := parseOrFatal(t, strings.Replace(counterWorkflow, "%s", `        repeat:
          until: "false"
          max: "{{ limit > 1 }}"`, 1))
	eng, _ := newTestEngine(t, file, nil)
	runID, err := eng.Run(context.Background(), "main", nil)
	if err == nil || !strings.Contains(err.Error(), "positive whole number") {
		t.Fatalf("err = %v", err)
	}
	if owners := repeatChildren(t, eng, runID); len(owners) != 0 {
		t.Errorf("ran iterations: %+v", owners)
	}
}

func TestRepeatCarriesStructuredValues(t *testing.T) {
	file := parseOrFatal(t, `
entrypoint: main
workflows:
  - name: main
    steps:
      - name: loop
        workflow: echo
        register: loop
        vars:
          prev: "{{ repeat.previous }}"
          i: "{{ repeat.index }}"
        repeat:
          until: "i >= 1"
          max: 5
      - name: check
        type: assert
        that:
          - "loop.last.prev.i == 0"
          - "loop.last.prev.prev == None"
  - name: echo
    vars:
      prev: ~
      i: ~
    steps:
      - name: noop
        type: set_fact
        vars:
          seen: "i"
`)
	eng, _ := newTestEngine(t, file, nil)
	if _, err := eng.Run(context.Background(), "main", nil); err != nil {
		t.Fatal(err)
	}
}

const failingIterationWorkflow = `
entrypoint: main
workflows:
  - name: main
    steps:
      - name: loop
        workflow: count
        register: loop
%s
        vars:
          n: "{{ repeat.index }}"
        repeat:
          until: "false"
          max: 5
    rescue:
      - name: caught
        type: set_fact
        vars:
          seen_count: "{{ loop.count }}"
          seen_stop: "{{ loop.stopped_by }}"
  - name: count
    vars:
      n: ~
    steps:
      - name: check
        type: assert
        that: ["n != 1"]
`

func TestRepeatFailedIterationEndsTheLoop(t *testing.T) {
	file := parseOrFatal(t, strings.Replace(failingIterationWorkflow, "%s", "        ignore_errors: true", 1))
	eng, _ := newTestEngine(t, file, nil)
	runID, err := eng.Run(context.Background(), "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	env := loopEnvelope(t, eng, runID, "loop")
	last, _ := env["last"].(map[string]any)
	if env["count"] != 2 || env["stopped_by"] != "error" || env["failed"] != true || last["n"] != 1 {
		t.Errorf("envelope = %v", env)
	}

	// Without ignore_errors the step fails, and the caller's rescue reads the envelope.
	file = parseOrFatal(t, strings.Replace(failingIterationWorkflow, "%s", "", 1))
	eng, _ = newTestEngine(t, file, nil)
	runID, err = eng.Run(context.Background(), "main", nil)
	if err == nil {
		t.Fatal("the loop should fail")
	}
	s, _ := eng.store.GetStep(context.Background(), runID, "main", "rescue/caught")
	var facts map[string]any
	if s != nil {
		decodeState(s.OutputJSON, &facts)
	}
	if facts["seen_count"] != 2 || facts["seen_stop"] != "error" {
		t.Errorf("rescue saw %v", facts)
	}
}

func TestRepeatGatePauseFailsTheIterationAndRunsItsCleanup(t *testing.T) {
	file := parseOrFatal(t, `
entrypoint: main
vars:
  approved: false
rules:
  - name: wait
    when: "approved != true"
    action: pause
workflows:
  - name: main
    steps:
      - name: loop
        workflow: step
        register: loop
        ignore_errors: true
        repeat:
          until: "false"
          max: 3
  - name: step
    steps:
      - name: inner
        workflow: approval
    always:
      - name: cleanup
        type: set_fact
        vars:
          cleaned: "true"
  - name: approval
    steps:
      - name: approve
        type: gate
        rules: [wait]
`)
	eng, cb := newTestEngine(t, file, nil)
	runID, err := eng.Run(context.Background(), "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	env := loopEnvelope(t, eng, runID, "loop")
	if env["count"] != 1 || env["stopped_by"] != "error" || !strings.Contains(env["error"].(string), "pausing inside repeat is not supported yet") {
		t.Errorf("envelope = %v", env)
	}
	run, _ := eng.store.GetRun(context.Background(), runID)
	if run.Status != state.RunCompleted {
		t.Errorf("run status %s, want completed", run.Status)
	}
	if events := eventsJSON(t, cb); strings.Contains(events, `"run_paused"`) {
		t.Errorf("a pause was emitted: %s", events)
	}
	owners := repeatChildren(t, eng, runID)
	if len(owners) != 1 {
		t.Fatalf("iterations = %+v", owners)
	}
	children, _ := eng.store.GetChildRuns(context.Background(), runID)
	cleaned := false
	for _, c := range children {
		if s, _ := eng.store.GetStep(context.Background(), c.RunID, "step", "always/cleanup"); s != nil && s.Status == state.StepCompleted {
			cleaned = true
		}
	}
	if !cleaned {
		t.Error("the iteration's always did not run")
	}
}

// resumeWorkflow loops `work` three times; its shell step fails on iteration 1 while fail is
// set, and the step after the loop fails while late is set.
const resumeWorkflow = `
entrypoint: main
workflows:
  - name: main
    steps:
      - name: loop
        workflow: work
        register: loop
        vars:
          n: "{{ repeat.index }}"
        repeat:
          until: "n >= 2"
          max: 5
      - name: after
        type: http_request
        params:
          url: "https://github.local/after?t={{ loop.last.mint.token }}"
  - name: work
    vars:
      n: ~
    steps:
      - name: mint
        type: http_request
        register: mint
        secret: [token]
      - name: flaky
        type: shell_exec
        when: "n == 1"
        params:
          url: "https://github.local/flaky"
`

// switchExec fails while *fail is set, and otherwise echoes its params.
type switchExec struct{ fail *bool }

func (s switchExec) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	if *s.fail {
		return &executor.Result{Output: params}, errFail
	}
	return &executor.Result{Output: params}, nil
}

var errFail = &failErr{}

type failErr struct{}

func (*failErr) Error() string { return "switched to fail" }

// mintOrEcho mints a token for the mint step and echoes everything else's params.
type mintOrEcho struct{ fail *bool }

func (m mintOrEcho) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	if _, ok := params["url"]; ok {
		if m.fail != nil && *m.fail {
			return &executor.Result{Output: params}, errFail
		}
		return &executor.Result{Output: params}, nil
	}
	return &executor.Result{Output: map[string]any{"token": mintedToken}}, nil
}

func newStoreFile(t *testing.T) *state.SQLiteStore {
	t.Helper()
	store, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestRepeatResumeStartsAnUnfinishedLoopOver(t *testing.T) {
	file := parseOrFatal(t, resumeWorkflow)
	store := newStoreFile(t)
	flaky, late := true, false
	execs := map[string]executor.Executor{"shell_exec": switchExec{fail: &flaky}, "http_request": mintOrEcho{fail: &late}}
	first := New(file, store, execs)
	first.SetCallbacks([]callback.Callback{&mockCallback{}})
	runID, err := first.Run(context.Background(), "main", nil)
	if err == nil {
		t.Fatal("iteration 1 should fail")
	}

	flaky = false
	second := New(file, store, execs)
	second.SetCallbacks([]callback.Callback{&mockCallback{}})
	if err := second.Resume(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	owners := repeatChildren(t, second, runID)
	var gens []string
	for _, o := range owners {
		gens = append(gens, string(rune('0'+o.Generation))+"."+string(rune('0'+o.Index)))
	}
	if strings.Join(gens, " ") != "0.0 0.1 1.0 1.1 1.2" {
		t.Errorf("iterations (generation.index) = %v, want the loop started over as generation 1", gens)
	}
}

func TestRepeatFinishedLoopIsRestoredMaskedOnResume(t *testing.T) {
	file := parseOrFatal(t, resumeWorkflow)
	store := newStoreFile(t)
	flaky, late := false, true
	execs := map[string]executor.Executor{"shell_exec": switchExec{fail: &flaky}, "http_request": mintOrEcho{fail: &late}}
	first := New(file, store, execs)
	first.SetCallbacks([]callback.Callback{&mockCallback{}})
	runID, err := first.Run(context.Background(), "main", nil)
	if err == nil {
		t.Fatal("the step after the loop should fail")
	}

	late = false
	second := New(file, store, execs)
	cb := &mockCallback{}
	second.SetCallbacks([]callback.Callback{cb})
	if err := second.Resume(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	if owners := repeatChildren(t, second, runID); len(owners) != 3 || owners[2].Generation != 0 {
		t.Errorf("the finished loop ran again: %+v", owners)
	}
	// The envelope came back from its masked row: the token is a placeholder now (lossy by
	// design), and the real value was never stored in it.
	after, _ := store.GetStep(context.Background(), runID, "main", "after")
	var out map[string]any
	if after != nil {
		decodeState(after.OutputJSON, &out)
	}
	if out["url"] != "https://github.local/after?t=<secret:mint.token>" {
		t.Errorf("after step got %v", out)
	}
	row, _ := store.GetStep(context.Background(), runID, "main", "loop")
	if strings.Contains(row.OutputJSON, mintedToken) {
		t.Errorf("the loop's stored envelope holds the secret: %s", row.OutputJSON)
	}
	if strings.Contains(eventsJSON(t, cb), mintedToken) {
		t.Error("resumed run's events contain the secret")
	}
}

func TestRepeatInAlwaysStartsANewGenerationEachTime(t *testing.T) {
	file := parseOrFatal(t, `
entrypoint: main
workflows:
  - name: main
    steps:
      - name: work
        type: shell_exec
        params:
          url: "https://github.local/work"
    always:
      - name: tidy
        workflow: tick
        vars:
          i: "{{ repeat.index }}"
        repeat:
          until: "i >= 1"
          max: 3
  - name: tick
    vars:
      i: ~
    steps:
      - name: noop
        type: set_fact
        vars:
          done: "true"
`)
	store := newStoreFile(t)
	fail := true
	execs := map[string]executor.Executor{"shell_exec": switchExec{fail: &fail}}
	first := New(file, store, execs)
	first.SetCallbacks([]callback.Callback{&mockCallback{}})
	runID, err := first.Run(context.Background(), "main", nil)
	if err == nil {
		t.Fatal("work should fail")
	}
	fail = false
	second := New(file, store, execs)
	second.SetCallbacks([]callback.Callback{&mockCallback{}})
	if err := second.Resume(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	owners := repeatChildren(t, second, runID)
	if len(owners) != 4 || owners[0].Generation != 0 || owners[3].Generation != 1 {
		t.Errorf("iterations = %+v, want two generations of two", owners)
	}
}

func TestSubRunOwnershipIsCheckedBothWays(t *testing.T) {
	file := parseOrFatal(t, strings.Replace(counterWorkflow, "%s", `        repeat:
          until: "true"
          max: 1`, 1))
	eng, _ := newTestEngine(t, file, nil)
	ctx := context.Background()
	step := parser.Step{Name: "x", Workflow: "count"}
	repeatOwned := subRunOwner{Kind: "repeat", Parent: "R", Workflow: "main", Step: "x", Generation: 0, Index: 0}
	callOwned := subRunOwner{Kind: "call", Parent: "R", Workflow: "main", Step: "x-0"}
	for _, row := range []struct {
		id    string
		owner string
	}{{"R-taken-by-repeat", repeatOwned.encode()}, {"R-taken-by-call", callOwned.encode()}, {"R-legacy", ""}} {
		if err := eng.store.CreateRun(ctx, &state.Run{RunID: row.id, WorkflowFile: "f", Entrypoint: "count", Status: state.RunCompleted, VarsJSON: "{}", ParentRunID: "R", Owner: row.owner}); err != nil {
			t.Fatal(err)
		}
	}
	// An ordinary call meeting a repeat-owned row, and a repeat meeting a call-owned one.
	if _, err := eng.claimSubRun(ctx, "R-taken-by-repeat", callOwned, step, "count"); err == nil || !strings.Contains(err.Error(), "already used by repeat step") {
		t.Errorf("call over repeat row: %v", err)
	}
	if _, err := eng.claimSubRun(ctx, "R-taken-by-call", repeatOwned, step, "count"); err == nil || !strings.Contains(err.Error(), "already used by step") {
		t.Errorf("repeat over call row: %v", err)
	}
	// Rows from before owners were recorded stay usable by calls, never by repeat.
	if exists, err := eng.claimSubRun(ctx, "R-legacy", callOwned, step, "count"); err != nil || !exists {
		t.Errorf("call over legacy row: %v %v", exists, err)
	}
	if _, err := eng.claimSubRun(ctx, "R-legacy", repeatOwned, step, "count"); err == nil {
		t.Error("repeat took over a legacy row")
	}
	// The same owner resumes its own row.
	if exists, err := eng.claimSubRun(ctx, "R-taken-by-call", callOwned, step, "count"); err != nil || !exists {
		t.Errorf("owner over own row: %v %v", exists, err)
	}
}

func TestRepeatIterationIDsDontCollideWithConcatenatedOnes(t *testing.T) {
	a := subRunOwner{Kind: "repeat", Parent: "R", Workflow: "main", Step: "a-b"}
	b := subRunOwner{Kind: "repeat", Parent: "R-a", Workflow: "child", Step: "b"}
	idA := "R-a-b@0.0~" + a.hash()
	idB := "R-a" + "-b@0.0~" + b.hash()
	if idA == idB {
		t.Errorf("%s collides", idA)
	}
}

func TestRepeatSecondClaimOnAGenerationFails(t *testing.T) {
	file := parseOrFatal(t, strings.Replace(counterWorkflow, "%s", `        repeat:
          until: "true"
          max: 1`, 1))
	eng, _ := newTestEngine(t, file, nil)
	step := file.GetWorkflow("main").Steps[0]
	caller := map[string]any{"limit": 3, "repeat": map[string]any{"index": 0, "previous": nil}}
	ctx := withRepeat(context.Background(), step.Name)
	if _, err := eng.runRepeatIteration(ctx, "R", "main", step, caller, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.runRepeatIteration(ctx, "R", "main", step, caller, 0, 0); err == nil || !strings.Contains(err.Error(), "another process is running") {
		t.Errorf("second claim: %v", err)
	}
}

func TestCurrentGenerationsDropsOldGenerations(t *testing.T) {
	row := func(id string, gen int) *state.Run {
		o := subRunOwner{Kind: "repeat", Parent: "R", Workflow: "main", Step: "loop", Generation: gen}
		return &state.Run{RunID: id, Owner: o.encode()}
	}
	plain := &state.Run{RunID: "R-call"}
	got := currentGenerations([]*state.Run{row("g0a", 0), plain, row("g1a", 1), row("g0b", 0), row("g1b", 1)})
	var ids []string
	for _, r := range got {
		ids = append(ids, r.RunID)
	}
	if strings.Join(ids, " ") != "R-call g1a g1b" {
		t.Errorf("kept %v", ids)
	}
}

// slowExec takes d, or until its context ends.
type slowExec struct{ d time.Duration }

func (s slowExec) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	select {
	case <-time.After(s.d):
		return &executor.Result{Output: map[string]any{}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestRepeatTimeoutBoundsTheWholeLoop(t *testing.T) {
	file := parseOrFatal(t, `
entrypoint: main
workflows:
  - name: main
    steps:
      - name: loop
        workflow: slow
        timeout: 1
        repeat:
          until: "false"
          max: 100
  - name: slow
    steps:
      - {name: wait, type: shell_exec, params: {url: "x"}}
`)
	eng, _ := newTestEngine(t, file, map[string]executor.Executor{"shell_exec": slowExec{300 * time.Millisecond}})
	start := time.Now()
	runID, err := eng.Run(context.Background(), "main", nil)
	if err == nil {
		t.Fatal("the loop should time out")
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("took %s; the 1s timeout should bound the whole loop", took)
	}
	if n := len(repeatChildren(t, eng, runID)); n < 2 || n > 5 {
		t.Errorf("%d iterations ran in about a second of 300ms each", n)
	}
}
