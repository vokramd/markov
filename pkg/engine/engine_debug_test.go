package engine

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jctanner/markov/pkg/callback"
	"github.com/jctanner/markov/pkg/executor"
	"github.com/jctanner/markov/pkg/parser"
)

// watchCB records every event like mockCallback and also forwards debug events to a channel so a
// test can react to pauses.
type watchCB struct {
	*mockCallback
	debug chan callback.DebugEvent
}

func (w *watchCB) OnDebug(e callback.DebugEvent) error {
	w.mockCallback.record("debug", e)
	w.debug <- e
	return nil
}

type debugRun struct {
	t    *testing.T
	eng  *Engine
	d    *Debugger
	cb   *watchCB
	done chan error
	id   string
}

func shell(name string) parser.Step {
	return parser.Step{Name: name, Type: "shell_exec", Params: map[string]any{"command": "echo " + name}}
}

// startDebug runs the workflow in the background with a debugger attached.
func startDebug(t *testing.T, wf *parser.WorkflowFile, setup func(d *Debugger)) *debugRun {
	t.Helper()
	eng, base := newTestEngine(t, wf, map[string]executor.Executor{"shell_exec": &mockExec{output: map[string]any{"stdout": "out"}}})
	cb := &watchCB{mockCallback: base, debug: make(chan callback.DebugEvent, 100)}
	eng.SetCallbacks([]callback.Callback{cb})
	d := NewDebugger(wf)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d.SetCancel(cancel)
	eng.SetDebugger(d)
	if setup != nil {
		setup(d)
	}
	r := &debugRun{t: t, eng: eng, d: d, cb: cb, done: make(chan error, 1)}
	go func() {
		id, err := eng.Run(ctx, "", nil)
		r.id = id
		r.done <- err
	}()
	return r
}

// next returns the next debug event of the given kind, failing on timeout.
func (r *debugRun) next(kind string) callback.DebugEvent {
	r.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-r.cb.debug:
			if e.Kind == kind {
				return e
			}
		case <-deadline:
			r.t.Fatalf("timed out waiting for a %q debug event", kind)
		}
	}
}

func (r *debugRun) finish() error {
	r.t.Helper()
	select {
	case err := <-r.done:
		return err
	case <-time.After(5 * time.Second):
		r.t.Fatal("run did not finish")
		return nil
	}
}

func (r *debugRun) send(cmd string) { r.d.Handle(cmd) }

func linear(names ...string) *parser.WorkflowFile {
	var steps []parser.Step
	for _, n := range names {
		steps = append(steps, shell(n))
	}
	return &parser.WorkflowFile{Entrypoint: "main", Workflows: []parser.Workflow{{Name: "main", Steps: steps}}}
}

func stepStarted(cb *mockCallback, step string) bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	for _, e := range cb.all {
		if s, ok := e.(callback.StepStartedEvent); ok && s.StepName == step {
			return true
		}
	}
	return false
}

func TestDebugBreakpointPausesBeforeTheStepWithItsVariables(t *testing.T) {
	wf := linear("a", "b", "c")
	wf.Workflows[0].Steps[0] = parser.Step{Name: "a", Type: "set_fact", Vars: map[string]any{"level": 7}}
	r := startDebug(t, wf, func(d *Debugger) {
		_, rej := d.SetBreakpoints([]Breakpoint{{Workflow: "main", Step: "b"}})
		if len(rej) > 0 {
			t.Fatalf("rejected: %v", rej)
		}
	})
	p := r.next("paused")
	if p.StepName != "b" || p.WorkflowName != "main" || p.Data["reason"] != "breakpoint" || p.Data["phase"] != "before" {
		t.Fatalf("paused event = %+v", p)
	}
	vars := p.Data["variables"].(map[string]any)
	if vars["level"] != float64(7) {
		t.Errorf("variables = %v, want the fact set by step a", vars)
	}
	if stepStarted(r.cb.mockCallback, "b") {
		t.Error("step b started before the pause was released")
	}
	r.send(`{"cmd":"continue"}`)
	r.next("resumed")
	if err := r.finish(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !stepStarted(r.cb.mockCallback, "c") {
		t.Error("the run did not continue to step c")
	}
}

func TestDebugAfterPhaseReportsTheStepOutput(t *testing.T) {
	r := startDebug(t, linear("a", "b"), func(d *Debugger) {
		d.SetBreakpoints([]Breakpoint{{Workflow: "main", Step: "a", Phase: "after"}})
	})
	p := r.next("paused")
	if p.StepName != "a" || p.Data["phase"] != "after" {
		t.Fatalf("paused = %+v", p)
	}
	out, _ := p.Data["output"].(map[string]any)
	if out["stdout"] != "out" {
		t.Errorf("output = %v", p.Data["output"])
	}
	if !stepStarted(r.cb.mockCallback, "a") || stepStarted(r.cb.mockCallback, "b") {
		t.Error("an after pause comes once a has run and before b starts")
	}
	r.send(`{"cmd":"continue"}`)
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestDebugStepModeThenUntilThenContinue(t *testing.T) {
	r := startDebug(t, linear("a", "b", "c", "d"), func(d *Debugger) { d.SetStepMode(true) })
	if p := r.next("paused"); p.StepName != "a" || p.Data["reason"] != "step" {
		t.Fatalf("first pause = %+v", p)
	}
	r.send(`{"cmd":"step"}`)
	if p := r.next("paused"); p.StepName != "b" || p.Data["reason"] != "step" {
		t.Fatalf("second pause = %+v", p)
	}
	r.send(`{"cmd":"until","workflow":"main","step":"d"}`)
	p := r.next("paused")
	if p.StepName != "d" || p.Data["reason"] != "until" {
		t.Fatalf("until pause = %+v", p)
	}
	if !stepStarted(r.cb.mockCallback, "c") {
		t.Error("until should have run c on the way")
	}
	r.send(`{"cmd":"continue"}`)
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestDebugConditionSkipAndLogpoint(t *testing.T) {
	wf := linear("a", "b", "c")
	wf.Vars = nil
	wf.Workflows[0].Steps[0] = parser.Step{Name: "a", Type: "set_fact", Vars: map[string]any{"n": 2}}
	r := startDebug(t, wf, func(d *Debugger) {
		_, rej := d.SetBreakpoints([]Breakpoint{
			{Workflow: "main", Step: "b", When: "n == 99"},                                // false: never fires
			{Workflow: "main", Step: "b", Log: "n is {{ n }} before b"},                   // logpoint: no pause
			{Workflow: "main", Step: "c", When: "n == 2", Skip: 1},                        // true, but the first hit is skipped
			{Workflow: "main", Step: "c", When: "n == 2", Log: "second rule saw {{ n }}"}, // another logpoint
		})
		if len(rej) > 0 {
			t.Fatalf("rejected %v", rej)
		}
	})
	if err := r.finish(); err != nil {
		t.Fatalf("a run with no firing breakpoint must not block: %v", err)
	}
	var logs []string
	r.cb.mu.Lock()
	for _, e := range r.cb.all {
		if d, ok := e.(callback.DebugEvent); ok && d.Kind == "logpoint" {
			logs = append(logs, d.Data["message"].(string))
		}
		if d, ok := e.(callback.DebugEvent); ok && d.Kind == "paused" {
			t.Errorf("unexpected pause: %+v", d)
		}
	}
	r.cb.mu.Unlock()
	if strings.Join(logs, "|") != "n is 2 before b|second rule saw 2" {
		t.Errorf("logpoints = %q", logs)
	}
}

func TestDebugConditionErrorPausesAndSaysWhy(t *testing.T) {
	r := startDebug(t, linear("a", "b"), func(d *Debugger) {
		d.SetBreakpoints([]Breakpoint{{Workflow: "main", Step: "b", When: "{{ this is not an expression"}})
	})
	p := r.next("paused")
	if p.Data["reason"] != "condition_error" || p.Data["error"] == nil || p.Data["condition"] == nil {
		t.Fatalf("paused = %+v", p)
	}
	r.send(`{"cmd":"continue"}`)
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestDebugForEachIterationBreakpointAndStepInto(t *testing.T) {
	wf := &parser.WorkflowFile{Entrypoint: "main", Workflows: []parser.Workflow{{
		Name: "main",
		Vars: map[string]any{"hosts": []any{"web-1", "web-2", "web-3"}},
		Steps: []parser.Step{{Name: "each", Type: "shell_exec", ForEach: "hosts", As: "host", Concurrency: 1,
			Params: map[string]any{"command": "echo {{ host }}"}}},
	}}}
	// A breakpoint with an iteration pauses only on that iteration, with the loop variable set.
	r := startDebug(t, wf, func(d *Debugger) {
		d.SetBreakpoints([]Breakpoint{{Workflow: "main", Step: "each", Iteration: "1"}})
	})
	p := r.next("paused")
	if p.Data["iteration"] != "1" {
		t.Fatalf("paused = %+v", p)
	}
	if v := p.Data["variables"].(map[string]any); v["host"] != "web-2" {
		t.Errorf("variables = %v, want host=web-2", v)
	}
	r.send(`{"cmd":"continue"}`)
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}

	// Without an iteration the breakpoint pauses once, on the step as a whole; step mode goes into each iteration.
	r = startDebug(t, wf, func(d *Debugger) {
		d.SetBreakpoints([]Breakpoint{{Workflow: "main", Step: "each"}})
	})
	if p := r.next("paused"); p.Data["iteration"] != "" {
		t.Fatalf("the loop as a whole should pause first: %+v", p)
	}
	r.send(`{"cmd":"step"}`)
	var seen []string
	for i := 0; i < 3; i++ {
		p := r.next("paused")
		seen = append(seen, fmt.Sprint(p.Data["iteration"]))
		if i < 2 {
			r.send(`{"cmd":"step"}`)
		}
	}
	r.send(`{"cmd":"continue"}`)
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen, ",") != "0,1,2" {
		t.Errorf("iterations stepped = %v", seen)
	}
}

func TestDebugSubWorkflowHitsAreDistinguishedByRunID(t *testing.T) {
	wf := &parser.WorkflowFile{Entrypoint: "main", Workflows: []parser.Workflow{
		{Name: "main", Steps: []parser.Step{{Name: "first", Workflow: "child"}, {Name: "second", Workflow: "child"}}},
		{Name: "child", Steps: []parser.Step{shell("x")}},
	}}
	r := startDebug(t, wf, func(d *Debugger) {
		d.SetBreakpoints([]Breakpoint{{Workflow: "child", Step: "x", Skip: 1}})
	})
	p := r.next("paused")
	if !strings.HasSuffix(p.RunID, "-second") || p.WorkflowName != "child" {
		t.Fatalf("with skip 1 only the second call should pause; got run %q workflow %q", p.RunID, p.WorkflowName)
	}
	r.send(`{"cmd":"continue"}`)
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestDebugRescueAndAlwaysSections(t *testing.T) {
	wf := &parser.WorkflowFile{Entrypoint: "main", Workflows: []parser.Workflow{{
		Name:   "main",
		Steps:  []parser.Step{{Name: "boom", Type: "shell_exec", Params: map[string]any{"command": "x"}}},
		Rescue: []parser.Step{shell("cleanup")},
		Always: []parser.Step{shell("final")},
	}}}
	eng, base := newTestEngine(t, wf, map[string]executor.Executor{"shell_exec": &failFirst{}})
	cb := &watchCB{mockCallback: base, debug: make(chan callback.DebugEvent, 20)}
	eng.SetCallbacks([]callback.Callback{cb})
	d := NewDebugger(wf)
	eng.SetDebugger(d)
	_, rej := d.SetBreakpoints([]Breakpoint{{Workflow: "main", Step: "cleanup", Section: "rescue"}, {Workflow: "main", Step: "final", Section: "always"}})
	if len(rej) > 0 {
		t.Fatal(rej)
	}
	done := make(chan error, 1)
	go func() { _, err := eng.Run(context.Background(), "", nil); done <- err }()
	r := &debugRun{t: t, eng: eng, d: d, cb: cb, done: done}
	if p := r.next("paused"); p.StepName != "cleanup" || p.Data["section"] != "rescue" {
		t.Fatalf("rescue pause = %+v", p)
	}
	r.send(`{"cmd":"continue"}`)
	if p := r.next("paused"); p.StepName != "final" || p.Data["section"] != "always" {
		t.Fatalf("always pause = %+v", p)
	}
	r.send(`{"cmd":"continue"}`)
	r.finish() // the run itself fails (boom); only the pauses matter here
}

// failFirst fails the step whose command is "x" and succeeds otherwise.
type failFirst struct{}

func (failFirst) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	if params["command"] == "x" {
		return nil, fmt.Errorf("boom")
	}
	return &executor.Result{Output: map[string]any{"stdout": "ok"}}, nil
}

func TestDebugEvaluateWhilePausedAndNotPaused(t *testing.T) {
	wf := linear("a", "b")
	wf.Workflows[0].Steps[0] = parser.Step{Name: "a", Type: "set_fact", Vars: map[string]any{"n": 20}}
	r := startDebug(t, wf, func(d *Debugger) {
		d.SetBreakpoints([]Breakpoint{{Workflow: "main", Step: "b"}})
	})
	r.next("paused")
	r.send(`{"cmd":"evaluate","id":"w1","expression":"n + 1"}`)
	e := r.next("evaluated")
	if e.Data["id"] != "w1" || e.Data["result"] != "21" {
		t.Errorf("evaluated = %+v", e.Data)
	}
	r.send(`{"cmd":"evaluate","id":"w2","expression":"{{ nope.deeper }}"}`)
	if e := r.next("evaluated"); e.Data["id"] != "w2" {
		t.Errorf("evaluated = %+v", e.Data)
	}
	r.send(`{"cmd":"continue"}`)
	r.finish()
	r.send(`{"cmd":"evaluate","id":"w3","expression":"n"}`)
	if e := r.next("evaluated"); e.Data["error"] == nil {
		t.Errorf("evaluating without a pause should be an error: %+v", e.Data)
	}
}

func TestDebugSetBreakpointsAcknowledgesResolvedAndUnresolved(t *testing.T) {
	r := startDebug(t, linear("a", "b"), nil)
	// The run is already going; breakpoints can be set at any time. (It may finish first; the acknowledgement is what is checked.)
	r.send(`{"cmd":"set_breakpoints","breakpoints":[{"workflow":"main","step":"b"},{"workflow":"main","step":"nope"},{"workflow":"ghost","step":"a"},{"workflow":"main","step":"a","section":"weird"},{"workflow":"main","step":"a","phase":"during"}]}`)
	e := r.next("breakpoints_set")
	resolved := e.Data["resolved"].([]Breakpoint)
	if len(resolved) != 1 || resolved[0].Step != "b" || resolved[0].Section != "steps" || resolved[0].Phase != "before" {
		t.Errorf("resolved = %+v", resolved)
	}
	un := e.Data["unresolved"].([]map[string]any)
	if len(un) != 4 {
		t.Fatalf("unresolved = %v", un)
	}
	joined := fmt.Sprint(un)
	for _, want := range []string{`has no step "nope"`, `unknown workflow "ghost"`, `unknown section "weird"`, `unknown phase "during"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("unresolved lacks %q: %s", want, joined)
		}
	}
	r.d.closeControl()
	r.finish()
}

func TestDebugTerminateStopsTheRunAndLeavesItResumable(t *testing.T) {
	r := startDebug(t, linear("a", "b", "c"), func(d *Debugger) {
		d.SetBreakpoints([]Breakpoint{{Workflow: "main", Step: "b"}})
	})
	r.next("paused")
	r.send(`{"cmd":"terminate"}`)
	err := r.finish()
	if err == nil || !strings.Contains(err.Error(), "terminated") {
		t.Fatalf("run error = %v, want terminated", err)
	}
	if stepStarted(r.cb.mockCallback, "c") {
		t.Error("step c ran after terminate")
	}
	run, gerr := r.eng.store.GetRun(context.Background(), r.id)
	if gerr != nil || run.Status != "failed" {
		t.Errorf("run = %+v err %v, want failed (resumable)", run, gerr)
	}
}

func TestDebugControlChannelClosingWhilePausedAbortsAndWhileRunningDisables(t *testing.T) {
	r := startDebug(t, linear("a", "b"), func(d *Debugger) {
		d.SetBreakpoints([]Breakpoint{{Workflow: "main", Step: "b"}})
	})
	r.next("paused")
	r.d.closeControl()
	err := r.finish()
	if err == nil || !strings.Contains(err.Error(), "control channel closed") {
		t.Fatalf("error = %v", err)
	}

	// Closed before the run reaches its breakpoint: debugging is off and the run just completes.
	wf := linear("a", "b")
	eng, _ := newTestEngine(t, wf, map[string]executor.Executor{"shell_exec": &mockExec{output: map[string]any{}}})
	d := NewDebugger(wf)
	eng.SetDebugger(d)
	d.SetBreakpoints([]Breakpoint{{Workflow: "main", Step: "b"}})
	d.closeControl()
	if _, err := eng.Run(context.Background(), "", nil); err != nil {
		t.Fatalf("run with a closed control channel: %v", err)
	}
}

func TestDebugOnlyOnePauseAtATimeUnderConcurrentIterations(t *testing.T) {
	wf := &parser.WorkflowFile{Entrypoint: "main", Workflows: []parser.Workflow{{
		Name: "main",
		Vars: map[string]any{"items": []any{"a", "b", "c", "d"}},
		Steps: []parser.Step{{Name: "fan", Type: "shell_exec", ForEach: "items", As: "item", Concurrency: 4,
			Params: map[string]any{"command": "echo {{ item }}"}}},
	}}}
	r := startDebug(t, wf, func(d *Debugger) {
		d.SetBreakpoints([]Breakpoint{
			{Workflow: "main", Step: "fan", Iteration: "0"}, {Workflow: "main", Step: "fan", Iteration: "1"},
			{Workflow: "main", Step: "fan", Iteration: "2"}, {Workflow: "main", Step: "fan", Iteration: "3"},
		})
	})
	var mu sync.Mutex
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		p := r.next("paused")
		mu.Lock()
		it := fmt.Sprint(p.Data["iteration"])
		if seen[it] {
			t.Errorf("iteration %s paused twice", it)
		}
		seen[it] = true
		mu.Unlock()
		r.send(`{"cmd":"continue"}`)
	}
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 4 {
		t.Errorf("paused iterations = %v", seen)
	}
}

func TestSplitStateNameIsUnambiguousForAwkwardNames(t *testing.T) {
	cases := []struct{ state, step, section, iteration string }{
		{"build", "build", "steps", ""},
		{"build[web-1]", "build", "steps", "web-1"},
		{"rescue/clean up", "clean up", "rescue", ""},
		{"always/x[y]", "x", "always", "y"},
		{"odd [name][k]", "odd [name]", "steps", "k"},
		{"odd [name]", "odd [name]", "steps", ""},
		{"a/b", "a/b", "steps", ""},
		{"build[a]b]", "build", "steps", "a]b"},
	}
	for _, c := range cases {
		sec, it := splitStateName(c.state, c.step)
		if sec != c.section || it != c.iteration {
			t.Errorf("splitStateName(%q, %q) = (%q, %q), want (%q, %q)", c.state, c.step, sec, it, c.section, c.iteration)
		}
	}
}

func TestResolveShorthandNeverSplitsOnADelimiter(t *testing.T) {
	wf := &parser.WorkflowFile{Workflows: []parser.Workflow{
		{Name: "my flow.v1:x", Steps: []parser.Step{shell("step one.a:b [c]"), shell("plain")}, Rescue: []parser.Step{shell("undo")}},
		{Name: "a", Steps: []parser.Step{shell("b.c")}},
		{Name: "a.b", Steps: []parser.Step{shell("c")}},
	}}
	d := NewDebugger(wf)
	ok := map[string]Breakpoint{
		"my flow.v1:x.step one.a:b [c]": {Workflow: "my flow.v1:x", Step: "step one.a:b [c]", Section: "steps"},
		"my flow.v1:x.undo":             {Workflow: "my flow.v1:x", Step: "undo", Section: "rescue"},
	}
	for in, want := range ok {
		got, err := d.ResolveShorthand(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("ResolveShorthand(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	// a + "b.c" and "a.b" + "c" read the same: refuse and point at the structured form.
	if _, err := d.ResolveShorthand("a.b.c"); err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "--breakpoint") {
		t.Errorf("ambiguous shorthand error = %v", err)
	}
	if _, err := d.ResolveShorthand("a.zzz"); err == nil || !strings.Contains(err.Error(), "matches no workflow.step") || !strings.Contains(err.Error(), `"plain"`) && !strings.Contains(err.Error(), "my flow.v1:x.plain") {
		t.Errorf("unknown shorthand error = %v", err)
	}
}

func TestDebugContinueDropsStepPausesQueuedBehindIt(t *testing.T) {
	wf := &parser.WorkflowFile{Entrypoint: "main", Workflows: []parser.Workflow{{
		Name: "main",
		Vars: map[string]any{"items": []any{"a", "b", "c", "d"}},
		Steps: []parser.Step{{Name: "fan", Type: "shell_exec", ForEach: "items", As: "item", Concurrency: 4,
			Params: map[string]any{"command": "echo {{ item }}"}}},
	}}}
	// Step mode with four concurrent iterations: all four decide to pause, one holds the pause.
	r := startDebug(t, wf, func(d *Debugger) { d.SetStepMode(true) })
	r.next("paused") // the loop as a whole
	r.send(`{"cmd":"step"}`)
	r.next("paused") // one iteration; the others queue behind it
	r.send(`{"cmd":"continue"}`)
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}
	r.cb.mu.Lock()
	defer r.cb.mu.Unlock()
	paused := 0
	for _, e := range r.cb.all {
		if d, ok := e.(callback.DebugEvent); ok && d.Kind == "paused" {
			paused++
		}
	}
	if paused != 2 {
		t.Errorf("paused %d times; continue should have dropped the queued step pauses (want 2: the loop, then one iteration)", paused)
	}
}

func repeatDebugFile(t *testing.T) *parser.WorkflowFile {
	t.Helper()
	return parseOrFatal(t, strings.Replace(counterWorkflow, "%s", `        repeat:
          until: "next >= limit"
          max: 10`, 1))
}

func TestDebugRepeatIterationBreakpointPausesBeforeThatIteration(t *testing.T) {
	r := startDebug(t, repeatDebugFile(t), func(d *Debugger) {
		if _, rejected := d.SetBreakpoints([]Breakpoint{{Workflow: "main", Step: "loop", Iteration: "2"}}); rejected != nil {
			t.Fatalf("rejected: %v", rejected)
		}
	})
	p := r.next("paused")
	vars, _ := p.Data["variables"].(map[string]any)
	rep, _ := vars["repeat"].(map[string]any)
	if p.WorkflowName != "main" || p.StepName != "loop" || p.Data["iteration"] != "2" || fmt.Sprint(rep["index"]) != "2" {
		t.Fatalf("paused = %+v", p)
	}
	if prev, _ := rep["previous"].(map[string]any); fmt.Sprint(prev["next"]) != "2" {
		t.Errorf("repeat.previous = %v, want the second iteration's results", rep["previous"])
	}
	r.send(`{"cmd":"continue"}`)
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(eventsJSON(t, r.cb.mockCallback), `"kind":"paused"`); n != 1 {
		t.Errorf("paused %d times, want once", n)
	}
}

func TestDebugWithinSelectsStepsInsideALoopIteration(t *testing.T) {
	r := startDebug(t, repeatDebugFile(t), func(d *Debugger) {
		d.SetBreakpoints([]Breakpoint{{Workflow: "count", Step: "bump", Within: []LoopSelector{{Workflow: "main", Step: "loop", Iteration: "1"}}}})
	})
	p := r.next("paused")
	loops, _ := p.Data["loops"].([]loopFrame)
	if p.WorkflowName != "count" || len(loops) != 1 || loops[0].Kind != "repeat" || loops[0].Step != "loop" || loops[0].Iteration != "1" {
		t.Fatalf("paused = %+v (loops %#v)", p, p.Data["loops"])
	}
	r.send(`{"cmd":"continue"}`)
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(eventsJSON(t, r.cb.mockCallback), `"kind":"paused"`); n != 1 {
		t.Errorf("paused %d times, want only in iteration 1", n)
	}

	// Without an iteration, within matches in every iteration of that loop.
	r = startDebug(t, repeatDebugFile(t), func(d *Debugger) {
		d.SetBreakpoints([]Breakpoint{{Workflow: "count", Step: "bump", Within: []LoopSelector{{Workflow: "main", Step: "loop"}}}})
	})
	var seen []string
	for i := 0; i < 3; i++ {
		p := r.next("paused")
		loops, _ := p.Data["loops"].([]loopFrame)
		seen = append(seen, loops[0].Iteration)
		r.send(`{"cmd":"continue"}`)
	}
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen, ",") != "0,1,2" {
		t.Errorf("paused in iterations %v", seen)
	}
}

func TestDebugStepModeGoesIntoAnIterationNotItsBoundary(t *testing.T) {
	r := startDebug(t, repeatDebugFile(t), func(d *Debugger) {
		d.SetBreakpoints([]Breakpoint{{Workflow: "main", Step: "loop"}})
	})
	if p := r.next("paused"); p.StepName != "loop" || p.Data["iteration"] != "" {
		t.Fatalf("the loop as a whole should pause first: %+v", p)
	}
	r.send(`{"cmd":"step"}`)
	if p := r.next("paused"); p.WorkflowName != "count" || p.StepName != "bump" {
		t.Fatalf("step should go to the iteration's first step, got %s.%s (iteration %v)", p.WorkflowName, p.StepName, p.Data["iteration"])
	}
	r.send(`{"cmd":"continue"}`)
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestDebugWithinMustNameALoop(t *testing.T) {
	d := NewDebugger(repeatDebugFile(t))
	_, rejected := d.SetBreakpoints([]Breakpoint{
		{Workflow: "count", Step: "bump", Within: []LoopSelector{{Workflow: "count", Step: "bump"}}},
		{Workflow: "count", Step: "bump", Within: []LoopSelector{{Workflow: "main", Step: "nope"}}},
	})
	if len(rejected) != 2 || !strings.Contains(fmt.Sprint(rejected[0]["error"]), "is not a loop") || !strings.Contains(fmt.Sprint(rejected[1]["error"]), "has no step") {
		t.Errorf("rejected = %v", rejected)
	}
}

func TestDebugWithinAForEachItem(t *testing.T) {
	wf := parseOrFatal(t, `
entrypoint: main
vars:
  hosts: [web-1, web-2, web-3]
workflows:
  - name: main
    steps:
      - name: each
        for_each: hosts
        as: host
        workflow: check
        vars:
          host: "{{ host }}"
  - name: check
    vars:
      host: ~
    steps:
      - {name: ping, type: shell_exec, params: {command: "echo {{ host }}"}}
`)
	r := startDebug(t, wf, func(d *Debugger) {
		d.SetBreakpoints([]Breakpoint{{Workflow: "check", Step: "ping", Within: []LoopSelector{{Workflow: "main", Step: "each", Iteration: "1"}}}})
	})
	p := r.next("paused")
	loops, _ := p.Data["loops"].([]loopFrame)
	vars, _ := p.Data["variables"].(map[string]any)
	if len(loops) != 1 || loops[0].Kind != "for_each" || loops[0].Iteration != "1" || vars["host"] != "web-2" {
		t.Fatalf("paused = %+v", p)
	}
	r.send(`{"cmd":"continue"}`)
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}
}
