package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jctanner/markov/pkg/callback"
	"github.com/jctanner/markov/pkg/executor"
	"github.com/jctanner/markov/pkg/parser"
)

// reloadWF is a workflow whose main calls `work`, reloads from {{ next_source }}, calls `work`
// again and runs `tail`. Each version's commands are prefixed with its tag.
func reloadWF(tag, extraWork string) string {
	return fmt.Sprintf(`entrypoint: main
workflows:
  - name: main
    steps:
      - name: first
        workflow: work
      - name: reload
        type: reload_workflow
        register: reloaded
        ignore_errors: true
        params:
          source: "{{ next_source }}"
      - name: second
        workflow: work
      - name: tail
        type: shell_exec
        params:
          command: %[1]s-tail
  - name: work
    steps:
      - name: do
        type: shell_exec
        params:
          command: %[1]s-work
%[2]s`, tag, extraWork)
}

const extraStep = `      - name: extra
        type: shell_exec
        params:
          command: v2-extra
`

// writeWF writes a single-file workflow into its own directory and returns its path.
func writeWF(t *testing.T, name, content string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "workflow.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func newReloadEngine(t *testing.T, source string) (*Engine, *mockCallback, *countExec) {
	t.Helper()
	file, err := parser.ParseFile(source)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	exec := &countExec{}
	eng, cb := newTestEngine(t, file, map[string]executor.Executor{"shell_exec": exec})
	eng.SourcePath = source
	eng.AllowReload = true
	return eng, cb, exec
}

func reloadOutput(t *testing.T, cb *mockCallback) map[string]any {
	t.Helper()
	cb.mu.Lock()
	defer cb.mu.Unlock()
	for _, ev := range cb.all {
		if c, ok := ev.(callback.StepCompletedEvent); ok && c.StepName == "reload" {
			return c.Output
		}
	}
	t.Fatalf("no step_completed for the reload step")
	return nil
}

func reloadedEvents(cb *mockCallback) []callback.WorkflowReloadedEvent {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	var out []callback.WorkflowReloadedEvent
	for _, ev := range cb.all {
		if r, ok := ev.(callback.WorkflowReloadedEvent); ok {
			out = append(out, r)
		}
	}
	return out
}

func TestReloadAppliesFromTheNextSubWorkflowCall(t *testing.T) {
	v1 := writeWF(t, "v1", reloadWF("v1", ""))
	v2 := writeWF(t, "v2", reloadWF("v2", extraStep))
	eng, cb, exec := newReloadEngine(t, v1)

	runID, err := eng.Run(context.Background(), "", map[string]any{"next_source": v2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// main keeps its own (v1) steps after the reload; the second call to work is v2.
	if got := fmt.Sprint(exec.Commands()); got != "[v1-work v2-work v2-extra v1-tail]" {
		t.Fatalf("commands = %s, want [v1-work v2-work v2-extra v1-tail]", got)
	}
	out := reloadOutput(t, cb)
	if out["reloaded"] != true || out["reload"] != 1 || out["source"] != v2 || out["new_digest"] == out["old_digest"] {
		t.Fatalf("reload output = %#v", out)
	}
	events := reloadedEvents(cb)
	if len(events) != 1 || events[0].Reload != 1 || events[0].Source != v2 || events[0].StepName != "reload" {
		t.Fatalf("workflow_reloaded events = %#v", events)
	}
	changed := fmt.Sprint(events[0].Changes["changed_workflows"])
	if !strings.Contains(changed, "work") || !strings.Contains(changed, "extra") || !strings.Contains(changed, "main") {
		t.Fatalf("changes = %#v, want main and work changed with work gaining extra", events[0].Changes)
	}
	reloads, err := eng.store.GetReloads(context.Background(), runID)
	if err != nil || len(reloads) != 1 || reloads[0].Step != "reload" || reloads[0].Workflow != "main" {
		t.Fatalf("recorded reloads = %#v, %v", reloads, err)
	}
}

func TestReloadOfAnInvalidSourceKeepsTheCurrentDefinition(t *testing.T) {
	v1 := writeWF(t, "v1", reloadWF("v1", ""))
	broken := writeWF(t, "broken", "entrypoint: main\nworkflows: [\n")
	eng, cb, exec := newReloadEngine(t, v1)

	if _, err := eng.Run(context.Background(), "", map[string]any{"next_source": broken}); err != nil {
		t.Fatalf("Run: %v (the reload step ignores errors)", err)
	}
	if got := fmt.Sprint(exec.Commands()); got != "[v1-work v1-work v1-tail]" {
		t.Fatalf("commands = %s, want v1 throughout", got)
	}
	out := reloadOutput(t, cb)
	if out["reloaded"] != false || out["failed"] != true || !strings.Contains(fmt.Sprint(out["error"]), "loading") {
		t.Fatalf("reload output = %#v, want a failed, not-reloaded result", out)
	}
	if len(reloadedEvents(cb)) != 0 {
		t.Fatal("a failed reload must not emit workflow_reloaded")
	}
}

func TestReloadRequireRejectsADefinitionMissingAWorkflow(t *testing.T) {
	v1 := writeWF(t, "v1", reloadWF("v1", ""))
	v2 := writeWF(t, "v2", reloadWF("v2", ""))
	content := strings.Replace(reloadWF("v1", ""), `source: "{{ next_source }}"`, `source: "{{ next_source }}"
          require: [main, work, gone]`, 1)
	if err := os.WriteFile(v1, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	eng, cb, exec := newReloadEngine(t, v1)
	if _, err := eng.Run(context.Background(), "", map[string]any{"next_source": v2}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := fmt.Sprint(exec.Commands()); got != "[v1-work v1-work v1-tail]" {
		t.Fatalf("commands = %s, want v1 throughout", got)
	}
	if out := reloadOutput(t, cb); out["reloaded"] != false || !strings.Contains(fmt.Sprint(out["error"]), `"gone"`) {
		t.Fatalf("reload output = %#v", out)
	}
}

func TestReloadNeedsAllowReload(t *testing.T) {
	v1 := writeWF(t, "v1", reloadWF("v1", ""))
	eng, _, exec := newReloadEngine(t, v1)
	eng.AllowReload = false
	_, err := eng.Run(context.Background(), "", map[string]any{"next_source": v1})
	if err == nil || !strings.Contains(err.Error(), "--allow-reload") {
		t.Fatalf("Run error = %v, want a refusal naming --allow-reload", err)
	}
	if len(exec.Commands()) != 0 {
		t.Fatal("no step may run when reload is not allowed")
	}
}

// failingTail fails the tail step so the run can be resumed.
type failingTail struct{ countExec }

func (f *failingTail) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	f.countExec.Execute(ctx, params)
	if strings.HasSuffix(fmt.Sprint(params["command"]), "-tail") {
		return nil, fmt.Errorf("tail failed")
	}
	return &executor.Result{Output: map[string]any{}}, nil
}

func TestARunThatReloadedCannotBeResumed(t *testing.T) {
	v1 := writeWF(t, "v1", reloadWF("v1", ""))
	v2 := writeWF(t, "v2", reloadWF("v2", ""))
	file, err := parser.ParseFile(v1)
	if err != nil {
		t.Fatal(err)
	}
	eng, _ := newTestEngine(t, file, map[string]executor.Executor{"shell_exec": &failingTail{}})
	eng.SourcePath = v1
	eng.AllowReload = true
	runID, err := eng.Run(context.Background(), "", map[string]any{"next_source": v2})
	if err == nil {
		t.Fatal("Run succeeded, want the tail step to fail")
	}
	err = eng.Resume(context.Background(), runID)
	if err == nil || !strings.Contains(err.Error(), "can't be resumed") {
		t.Fatalf("Resume error = %v, want a refusal", err)
	}
}

func TestReloadDuringConcurrentIterations(t *testing.T) {
	// Each iteration reloads (alternating sources) and then calls work; run with -race.
	wf := func(tag string) string {
		return fmt.Sprintf(`entrypoint: main
vars:
  sources: []
workflows:
  - name: main
    steps:
      - name: each
        for_each: sources
        as: src
        concurrency: 4
        workflow: iter
        vars:
          next: "{{ src }}"
  - name: iter
    vars:
      next:
    steps:
      - name: reload
        type: reload_workflow
        params:
          source: "{{ next }}"
      - name: call
        workflow: work
  - name: work
    steps:
      - name: do
        type: shell_exec
        params:
          command: %s-work
`, tag)
	}
	a := writeWF(t, "a", wf("a"))
	b := writeWF(t, "b", wf("b"))
	eng, cb, exec := newReloadEngine(t, a)
	sources := []any{a, b, a, b, a, b, a, b}
	if _, err := eng.Run(context.Background(), "", map[string]any{"sources": sources}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := len(exec.Commands()); n != len(sources) {
		t.Fatalf("work ran %d times, want %d", n, len(sources))
	}
	events := reloadedEvents(cb)
	if len(events) != len(sources) {
		t.Fatalf("%d workflow_reloaded events, want %d", len(events), len(sources))
	}
	seen := map[int]bool{}
	for _, e := range events {
		seen[e.Reload] = true
	}
	for i := 1; i <= len(sources); i++ {
		if !seen[i] {
			t.Fatalf("reload numbers %v, want 1..%d each once", seen, len(sources))
		}
	}
}

// startReloadDebug runs the reload workflow with a debugger attached, as the IDE does.
func startReloadDebug(t *testing.T, v1 string, vars map[string]any, setup func(d *Debugger)) (*debugRun, *countExec) {
	t.Helper()
	file, err := parser.ParseFile(v1)
	if err != nil {
		t.Fatal(err)
	}
	exec := &countExec{}
	eng, base := newTestEngine(t, file, map[string]executor.Executor{"shell_exec": exec})
	eng.SourcePath = v1
	eng.AllowReload = true
	cb := &watchCB{mockCallback: base, debug: make(chan callback.DebugEvent, 100)}
	eng.SetCallbacks([]callback.Callback{cb})
	d := NewDebugger(file)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d.SetCancel(cancel)
	eng.SetDebugger(d)
	if setup != nil {
		setup(d)
	}
	r := &debugRun{t: t, eng: eng, d: d, cb: cb, done: make(chan error, 1)}
	go func() {
		id, err := eng.Run(ctx, "", vars)
		r.id = id
		r.done <- err
	}()
	return r, exec
}

func TestReloadHandshakeLoadsTheSourceTheClientSends(t *testing.T) {
	v1 := writeWF(t, "v1", reloadWF("v1", ""))
	v2 := writeWF(t, "v2", reloadWF("v2", ""))
	snapshot := writeWF(t, "snapshot", reloadWF("v3", ""))
	r, exec := startReloadDebug(t, v1, map[string]any{"next_source": v2}, nil)

	pending := r.next("reload_pending")
	if pending.StepName != "reload" || pending.Data["source"] != v2 || pending.Data["candidate_digest"] == nil || pending.Data["changes"] == nil {
		t.Fatalf("reload_pending = %#v", pending)
	}
	r.send(`{"cmd":"continue"}`) // not a reload answer: rejected, still waiting
	if e := r.next("error"); !strings.Contains(fmt.Sprint(e.Data["error"]), "reload is pending") {
		t.Fatalf("error = %#v", e.Data)
	}
	r.send(fmt.Sprintf(`{"cmd":"reload","source":%q}`, snapshot))
	if err := r.finish(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := fmt.Sprint(exec.Commands()); got != "[v1-work v3-work v1-tail]" {
		t.Fatalf("commands = %s, want the client's source (v3) after the reload", got)
	}
	if out := reloadOutput(t, r.cb.mockCallback); out["source"] != snapshot || out["reloaded"] != true {
		t.Fatalf("reload output = %#v", out)
	}
}

func TestReloadHandshakeReject(t *testing.T) {
	v1 := writeWF(t, "v1", reloadWF("v1", ""))
	v2 := writeWF(t, "v2", reloadWF("v2", ""))
	r, exec := startReloadDebug(t, v1, map[string]any{"next_source": v2}, nil)
	r.next("reload_pending")
	r.send(`{"cmd":"reject_reload"}`)
	if err := r.finish(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := fmt.Sprint(exec.Commands()); got != "[v1-work v1-work v1-tail]" {
		t.Fatalf("commands = %s, want v1 throughout", got)
	}
	if out := reloadOutput(t, r.cb.mockCallback); out["reloaded"] != false || out["rejected"] != true {
		t.Fatalf("reload output = %#v", out)
	}
	if len(reloadedEvents(r.cb.mockCallback)) != 0 {
		t.Fatal("a rejected reload must not emit workflow_reloaded")
	}
}

func TestReloadRebindsBreakpoints(t *testing.T) {
	v1 := writeWF(t, "v1", reloadWF("v1", extraStepV1))
	v2 := writeWF(t, "v2", reloadWF("v2", ""))
	r, _ := startReloadDebug(t, v1, map[string]any{"next_source": v2}, func(d *Debugger) {
		d.SetBreakpoints([]Breakpoint{{Workflow: "work", Step: "do", Phase: phaseAfter, Skip: 100}, {Workflow: "work", Step: "only_in_v1", Skip: 100}})
	})
	r.next("reload_pending")
	r.send(`{"cmd":"reload"}`)
	set := r.next("breakpoints_set")
	if set.Data["reason"] != "workflow_reloaded" {
		t.Fatalf("breakpoints_set = %#v", set.Data)
	}
	resolved := fmt.Sprint(set.Data["resolved"])
	unresolved := fmt.Sprint(set.Data["unresolved"])
	if !strings.Contains(resolved, "do") || strings.Contains(resolved, "only_in_v1") || !strings.Contains(unresolved, "only_in_v1") {
		t.Fatalf("resolved = %s, unresolved = %s", resolved, unresolved)
	}
	if err := r.finish(); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

const extraStepV1 = `      - name: only_in_v1
        type: shell_exec
        params:
          command: v1-only
`

func TestStepsAfterAReloadKeepTheirWorkflowsDefinitionOfStepTypes(t *testing.T) {
	wf := func(tag string) string {
		return fmt.Sprintf(`entrypoint: main
step_types:
  say:
    base: shell_exec
    defaults:
      command: %[1]s-say
workflows:
  - name: main
    steps:
      - name: reload
        type: reload_workflow
        params:
          source: "{{ next_source }}"
      - name: tail
        type: say
      - name: call
        workflow: work
  - name: work
    steps:
      - name: do
        type: say
`, tag)
	}
	v1 := writeWF(t, "v1", wf("v1"))
	v2 := writeWF(t, "v2", wf("v2"))
	eng, _, exec := newReloadEngine(t, v1)
	if _, err := eng.Run(context.Background(), "", map[string]any{"next_source": v2}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// main started on v1, so its own `say` stays v1; the sub-workflow called afterwards is v2.
	if got := fmt.Sprint(exec.Commands()); got != "[v1-say v2-say]" {
		t.Fatalf("commands = %s, want [v1-say v2-say]", got)
	}
}

func TestAllowReloadIsOnlyNeededWhenAReloadIsReachable(t *testing.T) {
	src := writeWF(t, "v1", `entrypoint: loop
workflows:
  - name: loop
    steps:
      - name: call
        workflow: "{{ which }}"
        workflow_names: [middle]
  - name: middle
    steps:
      - name: reload
        type: reload_workflow
  - name: task
    steps:
      - name: do
        type: shell_exec
        params:
          command: task
`)
	eng, _, exec := newReloadEngine(t, src)
	eng.AllowReload = false
	if _, err := eng.Run(context.Background(), "task", nil); err != nil {
		t.Fatalf("running task (no reload reachable): %v", err)
	}
	if got := fmt.Sprint(exec.Commands()); got != "[task]" {
		t.Fatalf("commands = %s", got)
	}
	// loop reaches the reload through a templated call's workflow_names.
	if _, err := eng.Run(context.Background(), "loop", map[string]any{"which": "middle"}); err == nil || !strings.Contains(err.Error(), "--allow-reload") {
		t.Fatalf("running loop error = %v, want a refusal", err)
	}
}

// heldWF is a workflow whose main calls `work`, holds at `hold` (see holdExec), calls `work` again
// and runs `tail`; it has no reload step, so only a request_reload can change it.
func heldWF(tag string) string {
	return fmt.Sprintf(`entrypoint: main
workflows:
  - name: main
    steps:
      - name: first
        workflow: work
      - name: hold
        type: shell_exec
        params:
          command: %[1]s-hold
      - name: second
        workflow: work
      - name: tail
        type: shell_exec
        params:
          command: %[1]s-tail
  - name: work
    steps:
      - name: do
        type: shell_exec
        params:
          command: %[1]s-work
`, tag)
}

// holdExec records commands like countExec and blocks a "-hold" command until released.
type holdExec struct {
	countExec
	held    chan struct{}
	release chan struct{}
}

func (h *holdExec) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	res, err := h.countExec.Execute(ctx, params)
	if strings.HasSuffix(fmt.Sprint(params["command"]), "-hold") {
		h.held <- struct{}{}
		<-h.release
	}
	return res, err
}

// startHeld runs source with a control channel, as the IDE runs every workflow, and waits until
// main is held between its two calls to work.
func startHeld(t *testing.T, source string, allowReload bool) (*debugRun, *holdExec) {
	t.Helper()
	file, err := parser.ParseFile(source)
	if err != nil {
		t.Fatal(err)
	}
	exec := &holdExec{held: make(chan struct{}, 1), release: make(chan struct{})}
	eng, base := newTestEngine(t, file, map[string]executor.Executor{"shell_exec": exec})
	eng.SourcePath = source
	eng.AllowReload = allowReload
	cb := &watchCB{mockCallback: base, debug: make(chan callback.DebugEvent, 100)}
	eng.SetCallbacks([]callback.Callback{cb})
	d := NewDebugger(file)
	eng.SetDebugger(d)
	r := &debugRun{t: t, eng: eng, d: d, cb: cb, done: make(chan error, 1)}
	go func() {
		id, err := eng.Run(context.Background(), "", nil)
		r.id = id
		r.done <- err
	}()
	select {
	case <-exec.held:
	case <-time.After(5 * time.Second):
		t.Fatal("the run never reached hold")
	}
	return r, exec
}

func TestRequestedReloadAppliesAtTheNextSubWorkflowCall(t *testing.T) {
	v1 := writeWF(t, "v1", heldWF("v1"))
	v2 := writeWF(t, "v2", heldWF("v2")+extraStep)
	r, exec := startHeld(t, v1, true)

	r.send(fmt.Sprintf(`{"cmd":"request_reload","source":%q}`, v2))
	queued := r.next("reload_queued")
	changed := fmt.Sprint(queued.Data["changes"])
	if queued.Data["source"] != v2 || queued.Data["entry"] != "main" || !strings.Contains(changed, "extra") {
		t.Fatalf("reload_queued = %#v", queued.Data)
	}
	if len(reloadedEvents(r.cb.mockCallback)) != 0 {
		t.Fatal("a request must wait for the next sub-workflow call")
	}
	close(exec.release)
	if err := r.finish(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// main keeps its own (v1) steps; the call after the request runs v2's work.
	if got := fmt.Sprint(exec.Commands()); got != "[v1-work v1-hold v2-work v2-extra v1-tail]" {
		t.Fatalf("commands = %s", got)
	}
	events := reloadedEvents(r.cb.mockCallback)
	if len(events) != 1 || events[0].StepName != "second" || events[0].Source != v2 || events[0].Reload != 1 {
		t.Fatalf("workflow_reloaded = %#v, want one, at the call step", events)
	}
	if reloads, err := r.eng.store.GetReloads(context.Background(), r.id); err != nil || len(reloads) != 1 {
		t.Fatalf("recorded reloads = %#v, %v (a requested reload also makes the run unresumable)", reloads, err)
	}
}

func TestRequestedReloadThatChangesNothingIsDropped(t *testing.T) {
	v1 := writeWF(t, "v1", heldWF("v1"))
	again := writeWF(t, "again", heldWF("v1"))
	v2 := writeWF(t, "v2", heldWF("v2"))
	r, exec := startHeld(t, v1, true)

	r.send(fmt.Sprintf(`{"cmd":"request_reload","source":%q}`, again))
	r.next("reload_unchanged")
	// A change, then undone: the second request drops the first.
	r.send(fmt.Sprintf(`{"cmd":"request_reload","source":%q}`, v2))
	r.next("reload_queued")
	r.send(fmt.Sprintf(`{"cmd":"request_reload","source":%q}`, again))
	r.next("reload_unchanged")
	close(exec.release)
	if err := r.finish(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := fmt.Sprint(exec.Commands()); got != "[v1-work v1-hold v1-work v1-tail]" {
		t.Fatalf("commands = %s, want v1 throughout", got)
	}
	if n := len(reloadedEvents(r.cb.mockCallback)); n != 0 {
		t.Fatalf("%d workflow_reloaded events, want none", n)
	}
}

func TestRequestedReloadIsRefusedWhenItCannotApply(t *testing.T) {
	v1 := writeWF(t, "v1", heldWF("v1"))
	broken := writeWF(t, "broken", "entrypoint: main\nworkflows: [\n")
	noEntry := writeWF(t, "noentry", strings.NewReplacer("entrypoint: main", "entrypoint: other", "name: main", "name: other").Replace(heldWF("v2")))
	r, exec := startHeld(t, v1, true)

	for _, c := range []struct{ source, want string }{{broken, "parsing"}, {noEntry, `no workflow "main"`}} {
		r.send(fmt.Sprintf(`{"cmd":"request_reload","source":%q}`, c.source))
		if e := r.next("reload_failed"); !strings.Contains(fmt.Sprint(e.Data["error"]), c.want) {
			t.Fatalf("reload_failed = %#v, want %q", e.Data, c.want)
		}
	}
	close(exec.release)
	if err := r.finish(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := fmt.Sprint(exec.Commands()); got != "[v1-work v1-hold v1-work v1-tail]" {
		t.Fatalf("commands = %s, want v1 throughout", got)
	}

	// A run started without --allow-reload refuses before loading anything.
	r, exec = startHeld(t, v1, false)
	r.send(`{"cmd":"request_reload"}`)
	if e := r.next("reload_failed"); !strings.Contains(fmt.Sprint(e.Data["error"]), "--allow-reload") {
		t.Fatalf("reload_failed = %#v", e.Data)
	}
	close(exec.release)
	if err := r.finish(); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
