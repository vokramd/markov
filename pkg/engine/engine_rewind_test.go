package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vokramd/markov/pkg/callback"
	"github.com/vokramd/markov/pkg/executor"
	"github.com/vokramd/markov/pkg/parser"
	"github.com/vokramd/markov/pkg/state"
)

// scripted runs shell_exec steps without a shell: the output is the command text, calls are
// counted per command, and commands listed in fail make the step fail.
type scripted struct {
	mu    sync.Mutex
	calls map[string]int
	fail  map[string]bool
}

func newScripted() *scripted { return &scripted{calls: map[string]int{}, fail: map[string]bool{}} }

func (s *scripted) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	cmd := fmt.Sprint(params["command"])
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[cmd]++
	if s.fail[cmd] {
		return nil, fmt.Errorf("%s failed", cmd)
	}
	return &executor.Result{Output: map[string]any{"stdout": cmd}}, nil
}

func (s *scripted) count(cmd string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[cmd]
}

func sh(name, cmd string) parser.Step {
	return parser.Step{Name: name, Type: "shell_exec", Params: map[string]any{"command": cmd}}
}

func openStore(t *testing.T) state.Store {
	t.Helper()
	st, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "rewind.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// session is one engine over a shared store, so a later session can resume an earlier one.
type session struct {
	eng *Engine
	cb  *mockCallback
	ex  *scripted
}

func newSession(t *testing.T, st state.Store, wf *parser.WorkflowFile, ex *scripted) *session {
	t.Helper()
	eng := New(wf, st, map[string]executor.Executor{"shell_exec": ex})
	cb := &mockCallback{}
	eng.SetCallbacks([]callback.Callback{cb})
	return &session{eng: eng, cb: cb, ex: ex}
}

func (s *session) run(runID string) error {
	s.eng.RunID = runID
	_, err := s.eng.Run(context.Background(), "", nil)
	return err
}

func (s *session) resume(runID string) error {
	return s.eng.ResumeWithVars(context.Background(), runID, nil)
}

func (s *session) resumed() callback.RunResumedEvent {
	s.cb.mu.Lock()
	defer s.cb.mu.Unlock()
	for i := len(s.cb.all) - 1; i >= 0; i-- {
		if e, ok := s.cb.all[i].(callback.RunResumedEvent); ok {
			return e
		}
	}
	return callback.RunResumedEvent{}
}

func linearFile(aCmd string) *parser.WorkflowFile {
	return &parser.WorkflowFile{Entrypoint: "main", Workflows: []parser.Workflow{{
		Name: "main", Steps: []parser.Step{sh("a", aCmd), sh("b", "b"), sh("c", "c")},
	}}}
}

func storedStdout(t *testing.T, st state.Store, runID, workflow, step string) string {
	t.Helper()
	r, err := st.GetStep(context.Background(), runID, workflow, step)
	if err != nil || r == nil {
		return ""
	}
	return r.OutputJSON
}

func TestStepRowsCarryTheHashOfTheirDefinition(t *testing.T) {
	st := openStore(t)
	s := newSession(t, st, linearFile("a"), newScripted())
	if err := s.run("r1"); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.GetSteps(context.Background(), "r1")
	if len(rows) != 3 {
		t.Fatalf("rows = %d", len(rows))
	}
	hashes := map[string]bool{}
	for _, r := range rows {
		if len(r.DefinitionHash) != 16 {
			t.Errorf("%s hash = %q", r.StepName, r.DefinitionHash)
		}
		hashes[r.DefinitionHash] = true
	}
	if len(hashes) != 3 {
		t.Errorf("distinct steps should have distinct hashes: %v", hashes)
	}
	// The hash follows the definition: same content, same hash; a changed param, a different one.
	e := s.eng
	if e.definitionHash(sh("a", "x")) != e.definitionHash(sh("a", "x")) || e.definitionHash(sh("a", "x")) == e.definitionHash(sh("a", "y")) {
		t.Error("definition hash is not a function of the definition")
	}
}

func TestResumeReportsAnEditedCompletedStepButReusesItUnlessRewound(t *testing.T) {
	st := openStore(t)
	first := newScripted()
	first.fail["b"] = true
	if err := newSession(t, st, linearFile("a"), first).run("r1"); err == nil {
		t.Fatal("b should have failed")
	}

	// Step a is edited, b is fixed; resume without a rewind keeps a's old result.
	second := newScripted()
	s2 := newSession(t, st, linearFile("a-edited"), second)
	if err := s2.resume("r1"); err != nil {
		t.Fatal(err)
	}
	if second.count("a-edited") != 0 || second.count("b") != 1 || second.count("c") != 1 {
		t.Errorf("calls = %v; a must be reused, b and c run", second.calls)
	}
	ev := s2.resumed()
	if len(ev.ChangedSteps) != 1 || ev.ChangedSteps[0].Step != "a" || ev.ChangedSteps[0].Workflow != "main" || ev.Rewound {
		t.Errorf("resumed event = %+v", ev)
	}
	if !strings.Contains(storedStdout(t, st, "r1", "main", "a"), `"a"`) {
		t.Error("the old output is still stored")
	}
}

func TestRewindChangedReRunsFromTheEarliestEditedStep(t *testing.T) {
	st := openStore(t)
	first := newScripted()
	first.fail["b"] = true
	newSession(t, st, linearFile("a"), first).run("r1")

	second := newScripted()
	s2 := newSession(t, st, linearFile("a-edited"), second)
	s2.eng.RewindChanged = true
	if err := s2.resume("r1"); err != nil {
		t.Fatal(err)
	}
	if second.count("a-edited") != 1 || second.count("b") != 1 || second.count("c") != 1 {
		t.Errorf("calls = %v; the edited step and everything after it run", second.calls)
	}
	if !strings.Contains(storedStdout(t, st, "r1", "main", "a"), "a-edited") {
		t.Error("the new output replaced the old")
	}
	if ev := s2.resumed(); !ev.Rewound || len(ev.ChangedSteps) != 1 {
		t.Errorf("resumed event = %+v", ev)
	}
	// Nothing changed: --rewind-changed is a no-op and only the failed tail runs.
	st2 := openStore(t)
	f := newScripted()
	f.fail["b"] = true
	newSession(t, st2, linearFile("a"), f).run("r2")
	g := newScripted()
	s3 := newSession(t, st2, linearFile("a"), g)
	s3.eng.RewindChanged = true
	if err := s3.resume("r2"); err != nil {
		t.Fatal(err)
	}
	if g.count("a") != 0 || g.count("b") != 1 {
		t.Errorf("calls = %v", g.calls)
	}
}

func TestExplicitRewindClearsFromThatStepEvenOnACompletedRun(t *testing.T) {
	st := openStore(t)
	newSession(t, st, linearFile("a"), newScripted()).run("r1") // completes
	ex := newScripted()
	s := newSession(t, st, linearFile("a"), ex)
	if err := s.resume("r1"); err == nil || !strings.Contains(err.Error(), "--rewind") {
		t.Fatalf("a completed run cannot simply be resumed: %v", err)
	}
	s.eng.Rewind = []RewindTarget{{Workflow: "main", Step: "b"}}
	if err := s.resume("r1"); err != nil {
		t.Fatal(err)
	}
	if ex.count("a") != 0 || ex.count("b") != 1 || ex.count("c") != 1 {
		t.Errorf("calls = %v; b and c rerun, a is kept", ex.calls)
	}
	run, _ := st.GetRun(context.Background(), "r1")
	if run.Status != state.RunCompleted {
		t.Errorf("status = %s", run.Status)
	}
	// Several targets: the earliest wins.
	ex2 := newScripted()
	s2 := newSession(t, st, linearFile("a"), ex2)
	s2.eng.Rewind = []RewindTarget{{Workflow: "main", Step: "c"}, {Workflow: "main", Step: "a"}}
	if err := s2.resume("r1"); err != nil {
		t.Fatal(err)
	}
	if ex2.count("a") != 1 || ex2.count("b") != 1 || ex2.count("c") != 1 {
		t.Errorf("calls = %v", ex2.calls)
	}
}

func TestRewindTargetsAreValidatedWithHelpfulErrors(t *testing.T) {
	file := &parser.WorkflowFile{Entrypoint: "main", Workflows: []parser.Workflow{
		{Name: "main", Steps: []parser.Step{{Name: "call kid", Workflow: "kid"}, sh("a", "a")}, Rescue: []parser.Step{sh("undo", "undo")}},
		{Name: "kid", Steps: []parser.Step{sh("k", "k")}},
	}}
	st := openStore(t)
	newSession(t, st, file, newScripted()).run("r1")
	try := func(target RewindTarget) string {
		s := newSession(t, st, file, newScripted())
		s.eng.Rewind = []RewindTarget{target}
		err := s.resume("r1")
		if err == nil {
			t.Fatalf("%+v should be refused", target)
		}
		return err.Error()
	}
	if msg := try(RewindTarget{Workflow: "kid", Step: "k"}); !strings.Contains(msg, `"call kid"`) || !strings.Contains(msg, "entrypoint workflow") {
		t.Errorf("sub-workflow target: %s", msg)
	}
	if msg := try(RewindTarget{Workflow: "main", Step: "nope"}); !strings.Contains(msg, `no step "nope"`) || !strings.Contains(msg, `"a"`) {
		t.Errorf("unknown step: %s", msg)
	}
	if msg := try(RewindTarget{Workflow: "main", Step: "undo"}); !strings.Contains(msg, "rescue") {
		t.Errorf("rescue target: %s", msg)
	}
}

func TestEditingAStepInASubWorkflowRewindsToTheCallingStep(t *testing.T) {
	build := func(xCmd string) *parser.WorkflowFile {
		return &parser.WorkflowFile{Entrypoint: "main", Workflows: []parser.Workflow{
			{Name: "main", Steps: []parser.Step{sh("before", "before"), {Name: "go", Workflow: "kid"}, sh("after", "after")}},
			{Name: "kid", Steps: []parser.Step{sh("x", xCmd), sh("y", "y")}},
		}}
	}
	st := openStore(t)
	first := newScripted()
	first.fail["y"] = true
	if err := newSession(t, st, build("x"), first).run("r1"); err == nil {
		t.Fatal("y should fail")
	}
	// Without a rewind the edited x is reused (and reported, with the workflow it lives in).
	reuse := newScripted()
	s := newSession(t, st, build("x2"), reuse)
	if err := s.resume("r1"); err != nil {
		t.Fatal(err)
	}
	if reuse.count("x2") != 0 || reuse.count("y") != 1 {
		t.Errorf("calls = %v", reuse.calls)
	}
	ev := s.resumed()
	if len(ev.ChangedSteps) != 1 || ev.ChangedSteps[0].Workflow != "kid" || ev.ChangedSteps[0].Step != "x" {
		t.Errorf("changed = %+v", ev.ChangedSteps)
	}

	// Fresh run, then --rewind-changed: the call step is the target, so the sub-run starts over; "before" is kept.
	st2 := openStore(t)
	f2 := newScripted()
	f2.fail["y"] = true
	newSession(t, st2, build("x"), f2).run("r2")
	ex := newScripted()
	s2 := newSession(t, st2, build("x2"), ex)
	s2.eng.RewindChanged = true
	if err := s2.resume("r2"); err != nil {
		t.Fatal(err)
	}
	if ex.count("before") != 0 || ex.count("x2") != 1 || ex.count("y") != 1 || ex.count("after") != 1 {
		t.Errorf("calls = %v", ex.calls)
	}
}

func TestRewindClearsForEachIterations(t *testing.T) {
	build := func(cmd string) *parser.WorkflowFile {
		return &parser.WorkflowFile{Entrypoint: "main", Workflows: []parser.Workflow{{
			Name: "main",
			Vars: map[string]any{"items": []any{"one", "two"}},
			Steps: []parser.Step{
				{Name: "fan", Type: "shell_exec", ForEach: "items", As: "item", Concurrency: 1, Params: map[string]any{"command": cmd + " {{ item }}"}},
				sh("last", "last"),
			},
		}}}
	}
	st := openStore(t)
	newSession(t, st, build("v1"), newScripted()).run("r1")
	rows, _ := st.GetSteps(context.Background(), "r1")
	var names []string
	for _, r := range rows {
		names = append(names, r.StepName)
	}
	if strings.Join(names, ",") != "fan[0],fan[1],last" && strings.Join(names, ",") != "fan[1],fan[0],last" {
		t.Fatalf("rows = %v", names)
	}
	ex := newScripted()
	s := newSession(t, st, build("v2"), ex)
	s.eng.RewindChanged = true
	if err := s.resume("r1"); err != nil {
		t.Fatal(err)
	}
	if ex.count("v2 one") != 1 || ex.count("v2 two") != 1 || ex.count("last") != 1 {
		t.Errorf("calls = %v; both iterations and the step after run again", ex.calls)
	}
}

func TestRowsWithoutAHashAreNeverReportedAsChanged(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	st.CreateRun(ctx, &state.Run{RunID: "old", WorkflowFile: "x", Entrypoint: "main", Status: state.RunFailed, VarsJSON: "{}"})
	// A row saved by a version without hashes (bypassing the hashing wrapper).
	st.SaveStep(ctx, &state.StepResult{RunID: "old", WorkflowName: "main", StepName: "a", Status: state.StepCompleted, OutputJSON: `{"stdout":"old"}`})
	ex := newScripted()
	s := newSession(t, st, linearFile("anything-else"), ex)
	s.eng.RewindChanged = true
	if err := s.resume("old"); err != nil {
		t.Fatal(err)
	}
	if ex.count("anything-else") != 0 || len(s.resumed().ChangedSteps) != 0 || s.resumed().Rewound {
		t.Errorf("an unhashed row must be trusted: calls %v, event %+v", ex.calls, s.resumed())
	}
}
