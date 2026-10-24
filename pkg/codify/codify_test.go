package codify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jctanner/markov/pkg/callback"
)

var markovBin string

// TestMain builds the markov binary the codify loop runs as its child.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "codify-markov-")
	if err != nil {
		panic(err)
	}
	markovBin = filepath.Join(dir, "markov")
	cmd := exec.Command("go", "build", "-o", markovBin, "../../cmd/markov")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func write(t *testing.T, root, rel, text string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// project writes a directory workflow: main calls target, target is one claude step.
func project(t *testing.T, base string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proj")
	write(t, root, "meta.yaml", "entrypoint: main\n")
	write(t, root, "vars.yaml", "base: "+base+"\n")
	write(t, root, "rules.yaml", "[]\n")
	write(t, root, "step_types.yaml", "{}\n")
	write(t, root, "workflows/main.yaml", "name: main\nsteps:\n  - name: call\n    workflow: target\n")
	write(t, root, "workflows/target.yaml", `name: target
steps:
  - name: agent
    type: claude
    params:
      prompt: "ROLE=task list the stories at {{ base }}/stories.json into stories.txt"
`)
	return root
}

const nativeTarget = `name: target
steps:
  - name: fetch
    type: http_request
    register: stories
    params:
      url: "{{ base }}/stories.json"
  - name: write
    type: write_file
    params:
      path: stories.txt
      content: |
        {% for s in stories.body %}- {{ s }}
        {% endfor %}
`

// fakeClaude plays the workflow's agent step, the retro agent and the judge, told apart by
// their prompts, and emits the stream-json events the claude executor reads.
func fakeClaude(t *testing.T, retroScript string) (dir, bin string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "claude")
	native := filepath.Join(dir, "native.yaml")
	write(t, dir, "native.yaml", nativeTarget)
	script := `#!/bin/sh
prompt=$(cat)
result() { printf '%s\n' '{"type":"system","subtype":"init","session_id":"s1","model":"fake"}' '{"type":"result","subtype":"success","is_error":false,"result":"done","num_turns":1,"total_cost_usd":0.01,"session_id":"s1"}'; }
case "$prompt" in
*"retrospective step of"*)
  retro=$(printf '%s' "$prompt" | grep -o '/[^ ]*retro.json' | head -1)
` + retroScript + `
  result ;;
*"You check one step"*)
  verdict=$(printf '%s' "$prompt" | grep -o '/[^ ]*verdict.json' | head -1)
  printf '{"equivalent": true, "reason": "same stories in the same order"}' > "$verdict"
  result ;;
# The judge's and the retro agent's prompts quote the task's prompt, so the task comes last.
*ROLE=task*)
  url=$(printf '%s' "$prompt" | grep -o 'http[^ ]*stories.json')
  curl -sS --fail "$url" | tr -d '[]"' | tr ',' '\n' | sed 's/^/* /' > stories.txt || exit 1
  result ;;
*) echo "unexpected prompt: $prompt" >&2; exit 1 ;;
esac
`
	script = strings.ReplaceAll(script, "NATIVE", native)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, bin
}

const codifyAll = `  cp NATIVE workflows/target.yaml
  printf '{"changed": true, "extracted": "fetch and write the stories", "summary": "native", "script_justification": null, "remaining_agent_steps": 0}' > "$retro"`

// server serves stories.json on a made-up host routed by the proxy hook, and counts hits.
func server(t *testing.T) (base string, hits *int64, hook func(*Proxy)) {
	hits = new(int64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(hits, 1)
		io.WriteString(w, `["alpha","beta","gamma"]`)
	}))
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	_, port, _ := net.SplitHostPort(addr)
	hook = func(p *Proxy) {
		tr := p.upstream.Transport.(*http.Transport)
		tr.DialContext = (&netDialer{addr: addr}).DialContext
	}
	return fmt.Sprintf("http://codify.test:%s", port), hits, hook
}

func config(t *testing.T, root, claude string, hook func(*Proxy)) Config {
	return Config{Source: root, Workflow: "target", Markov: markovBin, ClaudeBin: claude, MaxIterations: 4,
		RunTimeout: 2 * time.Minute, proxyHook: hook, Out: testWriter{t}}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(b), "\n"))
	return len(b), nil
}

func TestCodifyReplacesTheAgentStepAndStops(t *testing.T) {
	base, hits, hook := server(t)
	root := project(t, base)
	fakeDir, fake := fakeClaude(t, codifyAll)
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	mainBefore, _ := os.ReadFile(filepath.Join(root, "workflows/main.yaml"))

	rows, err := Run(context.Background(), config(t, root, fake, hook))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want two iterations", rows)
	}
	first := rows[0]
	if first.Task.AgentSteps != 1 || first.Validation.Status != "passed" || !first.Validation.Judged || first.Promoted == nil || *first.Promoted != "v0001" {
		t.Fatalf("first iteration = %+v", first)
	}
	if first.Validation.Candidate == nil || first.Validation.Candidate.AgentSteps != 0 {
		t.Fatalf("candidate metrics = %+v", first.Validation.Candidate)
	}
	second := rows[1]
	if second.Task.AgentSteps != 0 || second.Validation.Status != "no_change" {
		t.Fatalf("second iteration = %+v", second)
	}
	got, _ := os.ReadFile(filepath.Join(root, "workflows/target.yaml"))
	if string(got) != nativeTarget {
		t.Fatalf("target.yaml = %s", got)
	}
	if mainAfter, _ := os.ReadFile(filepath.Join(root, "workflows/main.yaml")); string(mainAfter) != string(mainBefore) {
		t.Fatal("main.yaml changed")
	}
	cdir := root + ".codify"
	for _, v := range []string{"v0000", "v0001"} {
		if _, err := os.Stat(filepath.Join(cdir, "versions", v, "workflows", "target.yaml")); err != nil {
			t.Fatalf("version %s: %v", v, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cdir, "versions", "v0000", "workflows", "main.yaml")); err == nil {
		t.Fatal("versions hold only the target's files")
	}
	// The live runs fetched; the candidate replayed the recording.
	if n := atomic.LoadInt64(hits); n != 2 {
		t.Fatalf("server hits = %d, want 2 (one live run per iteration, none for the candidate)", n)
	}
	requests, _ := os.ReadFile(filepath.Join(cdir, "iter-0001", "evidence", "requests.md"))
	if !strings.Contains(string(requests), "stories.json") || !strings.Contains(string(requests), "alpha") {
		t.Fatalf("requests.md = %s", requests)
	}
	metrics, _ := os.ReadFile(filepath.Join(cdir, "metrics.jsonl"))
	if strings.Count(string(metrics), "\n") != 2 || !strings.Contains(string(metrics), `"promoted_version":"v0001"`) {
		t.Fatalf("metrics.jsonl = %s", metrics)
	}
}

func TestCodifyRejectsEditsOutsideTheTarget(t *testing.T) {
	base, _, hook := server(t)
	root := project(t, base)
	fakeDir, fake := fakeClaude(t, `  echo "# edited" >> workflows/main.yaml
  printf '{"changed": true, "extracted": "x"}' > "$retro"`)
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := config(t, root, fake, hook)
	rows, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want three rejected iterations then a stop", len(rows))
	}
	for _, r := range rows {
		if r.Validation.Status != "failed" || !strings.Contains(strings.Join(r.Validation.Reasons, " "), "workflows/main.yaml") {
			t.Fatalf("row = %+v", r.Validation)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(root, "workflows/main.yaml")); strings.Contains(string(data), "edited") {
		t.Fatal("a rejected candidate reached the project")
	}
}

func TestCodifyRejectsACandidateThatFetchesSomethingElse(t *testing.T) {
	base, _, hook := server(t)
	root := project(t, base)
	fakeDir, fake := fakeClaude(t, `  sed 's#stories.json#other.json#' NATIVE > workflows/target.yaml
  printf '{"changed": true, "extracted": "x"}' > "$retro"`)
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := config(t, root, fake, hook)
	cfg.MaxIterations = 1
	rows, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	reasons := strings.Join(rows[0].Validation.Reasons, " ")
	if rows[0].Validation.Status != "failed" || !strings.Contains(reasons, "not in the recording") || !strings.Contains(reasons, "other.json") {
		t.Fatalf("validation = %+v", rows[0].Validation)
	}
}

func TestEditableSetFollowsCalls(t *testing.T) {
	root := project(t, "http://x")
	write(t, root, "workflows/helper.yaml", "name: target_helper\nsteps:\n  - name: s\n    type: shell_exec\n    params:\n      command: echo\n")
	write(t, root, "workflows/target.yaml", "name: target\nsteps:\n  - name: c\n    workflow: target_helper\n")
	p, err := LoadProject(root, "target")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.Reach, ",") != "target,target_helper" {
		t.Fatalf("reach = %v", p.Reach)
	}
	for rel, want := range map[string]bool{
		"workflows/target.yaml": true, "workflows/helper.yaml": true, "workflows/target_new.yaml": true,
		"scripts/target/x.py": true, "workflows/main.yaml": false, "vars.yaml": false, "scripts/other.py": false,
	} {
		if p.Editable(rel) != want {
			t.Errorf("Editable(%s) = %v, want %v", rel, !want, want)
		}
	}
	if _, err := LoadProject(root, "nope"); err == nil || !strings.Contains(err.Error(), "target") {
		t.Fatalf("unknown workflow error = %v", err)
	}
}

func TestCodifyStopsWhenTheWorkflowWritesNothing(t *testing.T) {
	base, _, hook := server(t)
	root := project(t, base)
	write(t, root, "workflows/target.yaml", "name: target\nsteps:\n  - name: agent\n    type: claude\n    params:\n      prompt: \"ROLE=quiet\"\n")
	fakeDir := t.TempDir()
	write(t, fakeDir, "claude", "#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"I cannot write files\",\"num_turns\":1,\"total_cost_usd\":0.01}'\n")
	os.Chmod(filepath.Join(fakeDir, "claude"), 0o755)
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out strings.Builder
	cfg := config(t, root, filepath.Join(fakeDir, "claude"), hook)
	cfg.Out = &out
	rows, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Validation.Status != "live_failed" || !strings.Contains(out.String(), "wrote no files") {
		t.Fatalf("rows = %+v\n%s", rows, out.String())
	}
}

func TestPassRunsOneIterationAndPromotes(t *testing.T) {
	base, _, hook := server(t)
	root := project(t, base)
	fakeDir, fake := fakeClaude(t, codifyAll)
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	row, err := Pass(context.Background(), config(t, root, fake, hook))
	if err != nil {
		t.Fatal(err)
	}
	if row.Validation.Status != "passed" || row.Promoted == nil || *row.Promoted != "v0001" || row.Iteration != "iter-0001" {
		t.Fatalf("row = %+v", row)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "workflows/target.yaml")); string(got) != nativeTarget {
		t.Fatalf("not installed: %s", got)
	}
	if metrics, _ := os.ReadFile(filepath.Join(root+".codify", "metrics.jsonl")); strings.Count(string(metrics), "\n") != 1 {
		t.Fatalf("metrics = %s", metrics)
	}
}

func TestStoppingRecordsTheIterationAndChangesNothing(t *testing.T) {
	base, _, hook := server(t)
	root := project(t, base)
	fakeDir, fake := fakeClaude(t, `  touch "$retro.started"
  sleep 20
`+codifyAll)
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	before, _ := os.ReadFile(filepath.Join(root, "workflows/target.yaml"))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Stop once the retro agent is working.
		for i := 0; i < 300; i++ {
			if m, _ := filepath.Glob(filepath.Join(root+".codify", "iter-*", "retro.json.started")); len(m) > 0 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
	}()
	start := time.Now()
	rows, err := Run(ctx, config(t, root, fake, hook))
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 15*time.Second {
		t.Fatal("the stop did not cancel the retro agent")
	}
	if len(rows) != 1 || rows[0].Validation.Status != "stopped" || rows[0].Promoted != nil {
		t.Fatalf("rows = %+v", rows)
	}
	if after, _ := os.ReadFile(filepath.Join(root, "workflows/target.yaml")); string(after) != string(before) {
		t.Fatal("a stopped iteration changed the project")
	}
	if metrics, _ := os.ReadFile(filepath.Join(root+".codify", "metrics.jsonl")); !strings.Contains(string(metrics), `"status":"stopped"`) {
		t.Fatalf("metrics = %s", metrics)
	}
}

// eventLog collects the events codify emits, as their JSON.
type eventLog struct {
	mu     sync.Mutex
	events []map[string]any
}

func (l *eventLog) add(v any) error {
	b, _ := json.Marshal(v)
	var m map[string]any
	json.Unmarshal(b, &m)
	l.mu.Lock()
	l.events = append(l.events, m)
	l.mu.Unlock()
	return nil
}

func (l *eventLog) OnRunStarted(e callback.RunStartedEvent) error             { return l.add(e) }
func (l *eventLog) OnRunCompleted(e callback.RunCompletedEvent) error         { return l.add(e) }
func (l *eventLog) OnRunFailed(e callback.RunFailedEvent) error               { return l.add(e) }
func (l *eventLog) OnRunPaused(e callback.RunPausedEvent) error               { return l.add(e) }
func (l *eventLog) OnRunResumed(e callback.RunResumedEvent) error             { return l.add(e) }
func (l *eventLog) OnStepStarted(e callback.StepStartedEvent) error           { return l.add(e) }
func (l *eventLog) OnStepCompleted(e callback.StepCompletedEvent) error       { return l.add(e) }
func (l *eventLog) OnStepFailed(e callback.StepFailedEvent) error             { return l.add(e) }
func (l *eventLog) OnStepSkipped(e callback.StepSkippedEvent) error           { return l.add(e) }
func (l *eventLog) OnStepProgress(e callback.StepProgressEvent) error         { return l.add(e) }
func (l *eventLog) OnDebug(e callback.DebugEvent) error                       { return l.add(e) }
func (l *eventLog) OnJobCreated(e callback.JobCreatedEvent) error             { return l.add(e) }
func (l *eventLog) OnGateEvaluated(e callback.GateEvaluatedEvent) error       { return l.add(e) }
func (l *eventLog) OnSubRunStarted(e callback.SubRunStartedEvent) error       { return l.add(e) }
func (l *eventLog) OnSubRunCompleted(e callback.SubRunCompletedEvent) error   { return l.add(e) }
func (l *eventLog) OnSubRunFailed(e callback.SubRunFailedEvent) error         { return l.add(e) }
func (l *eventLog) OnWorkflowReloaded(e callback.WorkflowReloadedEvent) error { return l.add(e) }
func (l *eventLog) Close() error                                              { return nil }

// count counts events matching type, workflow and step ("" matches any).
func (l *eventLog) count(ty, wf, step string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, e := range l.events {
		if e["event_type"] == ty && (wf == "" || e["workflow_name"] == wf) && (step == "" || e["step_name"] == step) {
			n++
		}
	}
	return n
}

func TestCodifyEmitsItsPhasesTheAgentsStreamsAndTheTargetsEvents(t *testing.T) {
	base, _, hook := server(t)
	root := project(t, base)
	fakeDir, fake := fakeClaude(t, codifyAll)
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	log := &eventLog{}
	cfg := config(t, root, fake, hook)
	cfg.Callbacks = []callback.Callback{log}
	if _, err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		ty, wf, step string
		want         int
	}{
		{"run_started", "", "", 1}, // one codify run; the child runs' own starts become sub-runs
		{"run_completed", "", "", 1},
		{"sub_run_started", "target", "", 3}, // live, candidate, live again
		{"sub_run_completed", "target", "", 3},
		{"step_started", "codify", "live", 2},
		{"step_completed", "codify", "retro", 1},
		{"step_completed", "codify", "candidate", 1},
		{"step_completed", "codify", "judge", 1},
		{"step_completed", "codify", "promote", 1},
		{"step_started", "target", "agent", 1},   // the target's own claude step (live run, v0)
		{"step_progress", "target", "agent", 2},  // its stream: init and result
		{"step_progress", "codify", "retro", 2},  // the retro agent's stream
		{"step_progress", "codify", "judge", 2},  // the judge's stream
		{"step_completed", "target", "fetch", 2}, // the native candidate, and the promoted version live
	} {
		if got := log.count(c.ty, c.wf, c.step); got != c.want {
			t.Errorf("%s %s.%s: %d events, want %d", c.ty, c.wf, c.step, got, c.want)
		}
	}
}

// cycleProject is a cycle that calls the agent workflow and a snapshot workflow, as a reset and
// snapshot cycle around an agent would.
func cycleProject(t *testing.T, base string) string {
	root := project(t, base)
	write(t, root, "workflows/snap.yaml", "name: snap\nsteps:\n  - name: s\n    type: shell_exec\n    params:\n      command: echo snapshot\n")
	write(t, root, "workflows/cycle.yaml", "name: cycle\nsteps:\n  - name: run-agent\n    workflow: target\n  - name: take-snapshot\n    workflow: snap\n")
	return root
}

func TestFixedWorkflowsAreNotEditable(t *testing.T) {
	root := cycleProject(t, "http://x")
	p, err := LoadProject(root, "cycle", "snap", "cycle")
	if err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]bool{
		"workflows/target.yaml": true, "workflows/cycle_helper.yaml": true, "scripts/cycle/x.py": true,
		"workflows/cycle.yaml": false, "workflows/snap.yaml": false, "workflows/main.yaml": false,
	} {
		if p.Editable(rel) != want {
			t.Errorf("Editable(%s) = %v, want %v", rel, !want, want)
		}
	}
	if got := strings.Join(p.EditableFiles(), ","); got != "workflows/target.yaml" {
		t.Errorf("editable files = %s", got)
	}
	if got := strings.Join(p.AgentSteps(), ","); got != "target.agent" {
		t.Errorf("agent steps = %s", got)
	}
	if got := p.FixedFiles([]string{"workflows/snap.yaml", "workflows/target.yaml", "vars.yaml"}); strings.Join(got, ",") != "workflows/snap.yaml" {
		t.Errorf("fixed files = %v", got)
	}
	if _, err := LoadProject(root, "cycle", "nope"); err == nil || !strings.Contains(err.Error(), `fixed workflow "nope"`) {
		t.Errorf("unknown fixed workflow: %v", err)
	}
	if _, err := LoadProject(root, "cycle", "cycle", "snap", "target"); err == nil || !strings.Contains(err.Error(), "nothing is left to codify") {
		t.Errorf("everything fixed: %v", err)
	}
}

func TestCodifyRejectsACandidateThatChangesAFixedWorkflow(t *testing.T) {
	base, _, hook := server(t)
	root := cycleProject(t, base)
	fakeDir, fake := fakeClaude(t, `  printf '%s' "$prompt" | grep -q 'These workflows are fixed: cycle, snap' && touch "$retro.saw-fixed"
  echo "  - name: cheat
    type: shell_exec
    params:
      command: echo moved" >> workflows/snap.yaml
  printf '{"changed": true, "extracted": "x"}' > "$retro"`)
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	snapBefore, _ := os.ReadFile(filepath.Join(root, "workflows/snap.yaml"))
	cfg := config(t, root, fake, hook)
	cfg.Workflow, cfg.Fixed, cfg.MaxIterations = "cycle", []string{"cycle", "snap"}, 1
	rows, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	reasons := strings.Join(rows[0].Validation.Reasons, " ")
	if rows[0].Validation.Status != "failed" || !strings.Contains(reasons, "fixed workflows changed") || !strings.Contains(reasons, "workflows/snap.yaml") {
		t.Fatalf("validation = %+v", rows[0].Validation)
	}
	if after, _ := os.ReadFile(filepath.Join(root, "workflows/snap.yaml")); string(after) != string(snapBefore) {
		t.Error("the fixed workflow changed in the project")
	}
	if seen, _ := filepath.Glob(filepath.Join(root+".codify", "iter-*", "retro.json.saw-fixed")); len(seen) == 0 {
		t.Error("the retro prompt did not name the fixed workflows")
	}
}

func TestLiveHostsAreTunnelledNotReplayed(t *testing.T) {
	c := Config{Source: t.TempDir(), Workflow: "w", Markov: "markov", LiveHosts: []string{"github.local"}}
	c.CodifyDir = filepath.Join(t.TempDir(), "codify")
	if err := c.defaults(); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(c.Passthrough, "github.local") || !slices.Contains(c.Passthrough, "anthropic.com") {
		t.Fatalf("passthrough = %v", c.Passthrough)
	}
	p := &Proxy{pass: c.Passthrough}
	for host, want := range map[string]bool{"github.local:443": true, "api.github.local": true, "notgithub.local": false, "example.com:443": false} {
		if p.passthrough(host) != want {
			t.Errorf("passthrough(%s) = %v", host, !want)
		}
	}
	if slices.Contains(DefaultPassthrough, "github.local") {
		t.Error("live hosts must not be added to the shared default list")
	}
}

func TestRetroPromptFollowsTheMode(t *testing.T) {
	base := RetroInputs{Target: "cycle", EditableFiles: []string{"workflows/agent.yaml"}, HelperPrefix: "cycle_", ScriptsDir: "scripts/cycle/", AgentSteps: []string{"agent.ask"}, Judged: true}
	strict, err := render(defaultRetroPrompt, base)
	if err != nil {
		t.Fatal(err)
	}
	base.Loose = true
	loose, err := render(defaultRetroPrompt, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"LOOSE mode", "atomic agent step", "same actions on the same items", "json_schema must answer in it"} {
		if !strings.Contains(loose, want) {
			t.Errorf("loose prompt lacks %q", want)
		}
		if strings.Contains(strict, want) {
			t.Errorf("strict prompt has %q", want)
		}
	}
	if !strings.Contains(strict, "While an agent produces an output") || strings.Contains(loose, "While an agent produces an output") {
		t.Error("only strict mode compares outputs by content")
	}
}

func TestStructuredAnswerMisses(t *testing.T) {
	started := func(wf, step string, schema bool) map[string]any {
		params := map[string]any{"prompt": "x"}
		if schema {
			params["json_schema"] = map[string]any{"type": "object"}
		}
		return map[string]any{"event_type": "step_started", "workflow_name": wf, "step_name": step, "step_type": "claude", "params": params}
	}
	done := func(wf, step string, answer any) map[string]any {
		return map[string]any{"event_type": "step_completed", "workflow_name": wf, "step_name": step, "step_type": "claude", "output": map[string]any{"structured_output": answer}}
	}
	events := []map[string]any{
		started("triage", "classify", true), done("triage", "classify", map[string]any{"label": "bug"}),
		started("triage", "classify", true), done("triage", "classify", nil), // a for_each call with no answer
		started("label", "pick", true), done("label", "pick", map[string]any{"label": "bug"}),
		{"event_type": "step_completed", "workflow_name": "label", "step_name": "pick", "step_type": "claude"}, // the for_each step closing
		started("agent", "broad", false), done("agent", "broad", nil), // no schema: free text is fine
		started("review", "judge-diff", true),
		{"event_type": "step_failed", "workflow_name": "review", "step_name": "judge-diff", "step_type": "claude"},
	}
	if got := strings.Join(StructuredAnswerMisses(events), ","); got != "triage.classify,review.judge-diff" {
		t.Errorf("misses = %s", got)
	}
}

func TestModeMustBeStrictOrLoose(t *testing.T) {
	c := Config{Source: t.TempDir(), Workflow: "w", Markov: "markov", Mode: "sloppy", CodifyDir: filepath.Join(t.TempDir(), "c")}
	if err := c.defaults(); err == nil || !strings.Contains(err.Error(), "strict or loose") {
		t.Errorf("err = %v", err)
	}
	c.Mode = ""
	if err := c.defaults(); err != nil || c.Mode != "strict" || c.Loose() {
		t.Errorf("default mode: %v %q", err, c.Mode)
	}
}

func TestLooseModeKeepsGoingWhileTheAgentStillJudgesSeveralThings(t *testing.T) {
	report := map[string]any{"jobs": []any{
		map[string]any{"job": "label each issue", "judgment": true, "where": "agent"},
		map[string]any{"job": "review each PR", "judgment": true, "where": "agent"},
		map[string]any{"job": "list issues", "judgment": false, "where": "agent"},
		map[string]any{"job": "pick a title", "judgment": true, "where": "atomic"},
	}}
	if got := judgmentJobsWithAgent(report); len(got) != 2 || got[0] != "label each issue" {
		t.Fatalf("judgment jobs still with the agent: %v", got)
	}
	if got := judgmentJobsWithAgent(map[string]any{}); len(got) != 0 {
		t.Fatalf("no report, no jobs: %v", got)
	}
}

func TestHistoryNamesEachPassMoveModeAndReasons(t *testing.T) {
	v := "v0001"
	doc := historyDoc([]Row{
		{Iteration: "iter-0001", Retro: map[string]any{"extracted": "fetch the issue list natively"}, Validation: Validation{Status: "failed", Reasons: []string{"labels differ"}}},
		{Iteration: "iter-0002", Mode: "loose", Retro: map[string]any{"extracted": "create labels", "move": "native"}, Validation: Validation{Status: "passed"}, Promoted: &v},
	})
	for _, want := range []string{"## iter-0001 (strict)", "fetch the issue list natively", "reason: labels differ", "## iter-0002 (loose)", "(move: native)", "promoted v0001"} {
		if !strings.Contains(doc, want) {
			t.Errorf("history lacks %q:\n%s", want, doc)
		}
	}
}
