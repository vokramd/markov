package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jctanner/markov/pkg/openshell"
)

type fakeClaudeOpts struct {
	events []map[string]any
	exit   int
	// hang keeps the process alive after emitting events, to test kills.
	hang bool
}

// fakeClaude writes an executable that records its stdin, argv, cwd and env,
// then replays canned stream-json events.
func fakeClaude(t *testing.T, o fakeClaudeOpts) (bin, record string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "fake-claude")
	record = filepath.Join(dir, "record")
	fixture := filepath.Join(dir, "events.jsonl")
	var lines []string
	for _, e := range o.events {
		b, _ := json.Marshal(e)
		lines = append(lines, string(b))
	}
	if err := os.WriteFile(fixture, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"{ pwd; echo \"---STDIN\"; cat; echo; echo \"---ARGS\"; for a in \"$@\"; do echo \"$a\"; done\n" +
		"echo \"---ENV\"; echo \"KEY:${ANTHROPIC_API_KEY-unset} X:$X\"; } > " + record + "\n" +
		"cat " + fixture + "\n"
	if o.hang {
		script += "sleep 30\n"
	}
	script += fmt.Sprintf("exit %d\n", o.exit)
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return bin, record
}

func initEvent() map[string]any {
	return map[string]any{"type": "system", "subtype": "init", "session_id": "sess-1", "model": "m", "cwd": "/w"}
}

func assistantEvent(id string, blocks []any, usage map[string]any) map[string]any {
	return map[string]any{
		"type":               "assistant",
		"parent_tool_use_id": nil,
		"message":            map[string]any{"id": id, "role": "assistant", "content": blocks, "usage": usage},
	}
}

func textBlock(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func usage(in, out, create, read int) map[string]any {
	return map[string]any{
		"input_tokens": in, "output_tokens": out,
		"cache_creation_input_tokens": create, "cache_read_input_tokens": read,
	}
}

func resultEvent(text string, isError bool, turns int) map[string]any {
	return map[string]any{
		"type": "result", "subtype": "success", "is_error": isError, "result": text,
		"num_turns": turns, "total_cost_usd": 0.5, "session_id": "sess-1",
		"stop_reason": "end_turn", "duration_ms": 1234, "permission_denials": []any{},
	}
}

type progressRecorder struct {
	mu    sync.Mutex
	kinds []string
}

func (p *progressRecorder) ctx() context.Context {
	return WithProgress(context.Background(), func(kind string, _ map[string]any) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.kinds = append(p.kinds, kind)
	})
}

func TestClaudeRunsAndStreamsEvents(t *testing.T) {
	bin, record := fakeClaude(t, fakeClaudeOpts{events: []map[string]any{
		initEvent(),
		assistantEvent("m1", []any{map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": "echo hi"}}}, usage(2, 16, 100, 5000)),
		{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "hi", "is_error": false}}}},
		assistantEvent("m2", []any{textBlock("done")}, usage(2, 14, 10, 5100)),
		resultEvent("done", false, 2),
	}})
	workdir := t.TempDir()
	rec := &progressRecorder{}
	res, err := NewClaude().Execute(rec.ctx(), map[string]any{
		"binary":          bin,
		"prompt":          "do the thing",
		"chdir":           workdir,
		"model":           "opus",
		"permission_mode": "bypass",
		"allowed_tools":   []any{"Bash", "Read"},
		"add_dirs":        []any{"/a", "/b"},
		"env":             map[string]any{"X": "1"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	out := res.Output
	if out["result"] != "done" || out["session_id"] != "sess-1" || out["num_turns"] != 2 || out["is_error"] != false {
		t.Errorf("unexpected output: %v", out)
	}
	// counted tokens exclude cache reads: (2+16+100) + (2+14+10)
	if out["tokens"] != 144 {
		t.Errorf("tokens = %v, want 144", out["tokens"])
	}
	if out["total_cost_usd"] != 0.5 {
		t.Errorf("cost = %v", out["total_cost_usd"])
	}
	if got := strings.Join(rec.kinds, ","); got != "init,tool_use,usage,tool_result,text,usage,result" {
		t.Errorf("progress kinds = %s", got)
	}
	events := out["events"].([]any)
	if len(events) != 5 { // usage progress is not stored
		t.Errorf("stored %d events, want 5", len(events))
	}

	r, _ := os.ReadFile(record)
	got := string(r)
	resolved, _ := filepath.EvalSymlinks(workdir)
	for _, want := range []string{
		resolved + "\n",
		"---STDIN\ndo the thing\n",
		"-p\n--output-format\nstream-json\n--verbose\n",
		"--permission-mode\nbypassPermissions\n",
		"--model\nopus\n",
		"--allowedTools\nBash,Read\n",
		"--add-dir\n/a\n--add-dir\n/b\n",
		"X:1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("record missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--bare") {
		t.Error("--bare must be opt-in so the OAuth session is used")
	}
	if !strings.Contains(got, "KEY:unset") && os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Errorf("step must not inject an API key:\n%s", got)
	}
}

func TestClaudeSkillBuildsSlashCommand(t *testing.T) {
	bin, record := fakeClaude(t, fakeClaudeOpts{events: []map[string]any{initEvent(), resultEvent("ok", false, 1)}})
	if _, err := NewClaude().Execute(context.Background(), map[string]any{
		"binary": bin, "skill": "review-pr", "args": "123 --strict",
	}); err != nil {
		t.Fatal(err)
	}
	r, _ := os.ReadFile(record)
	if !strings.Contains(string(r), "---STDIN\n/review-pr 123 --strict\n") {
		t.Errorf("unexpected stdin:\n%s", r)
	}
}

func manyTurns(n int, u map[string]any) []map[string]any {
	events := []map[string]any{initEvent()}
	for i := 0; i < n; i++ {
		events = append(events, assistantEvent(fmt.Sprintf("m%d", i), []any{textBlock("x")}, u))
	}
	return events
}

func TestClaudeEnforcesMaxTurnsAndKillsProcessGroup(t *testing.T) {
	bin, _ := fakeClaude(t, fakeClaudeOpts{events: manyTurns(5, usage(1, 1, 0, 0)), hang: true})
	start := time.Now()
	res, err := NewClaude().Execute(context.Background(), map[string]any{
		"binary": bin, "prompt": "p", "limits": map[string]any{"max_turns": 2},
	})
	if err == nil || !strings.Contains(err.Error(), "limit exceeded: max_turns") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %v; process group was not killed", time.Since(start))
	}
	if res.Output["limit_exceeded"] != "max_turns" || res.Output["num_turns"] != 3 {
		t.Errorf("output = %v", res.Output)
	}
}

func TestClaudeEnforcesMaxTokensCountingEachMessageOnce(t *testing.T) {
	// The same message id repeated (one event per content block) counts once.
	u := usage(10, 40, 0, 100000)
	events := []map[string]any{
		initEvent(),
		assistantEvent("m1", []any{textBlock("a")}, u),
		assistantEvent("m1", []any{textBlock("b")}, u),
		resultEvent("ok", false, 1),
	}
	bin, _ := fakeClaude(t, fakeClaudeOpts{events: events})
	res, err := NewClaude().Execute(context.Background(), map[string]any{
		"binary": bin, "prompt": "p", "limits": map[string]any{"max_tokens": 60},
	})
	if err != nil {
		t.Fatalf("50 tokens must stay under max_tokens=60: %v", err)
	}
	if res.Output["tokens"] != 50 {
		t.Errorf("tokens = %v, want 50", res.Output["tokens"])
	}

	bin, _ = fakeClaude(t, fakeClaudeOpts{events: manyTurns(3, usage(10, 40, 0, 0)), hang: true})
	res, err = NewClaude().Execute(context.Background(), map[string]any{
		"binary": bin, "prompt": "p", "limits": map[string]any{"max_tokens": 60},
	})
	if err == nil || res.Output["limit_exceeded"] != "max_tokens" {
		t.Fatalf("err = %v, output = %v", err, res.Output)
	}
}

func TestClaudeEnforcesMaxDuration(t *testing.T) {
	bin, _ := fakeClaude(t, fakeClaudeOpts{events: []map[string]any{initEvent()}, hang: true})
	start := time.Now()
	res, err := NewClaude().Execute(context.Background(), map[string]any{
		"binary": bin, "prompt": "p", "limits": map[string]any{"max_duration": 1},
	})
	if err == nil || res.Output["limit_exceeded"] != "max_duration" {
		t.Fatalf("err = %v, output = %v", err, res.Output)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func TestClaudeErrorResultFailsStep(t *testing.T) {
	bin, _ := fakeClaude(t, fakeClaudeOpts{events: []map[string]any{initEvent(), resultEvent("boom", true, 1)}, exit: 1})
	res, err := NewClaude().Execute(context.Background(), map[string]any{"binary": bin, "prompt": "p"})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if res.Output["is_error"] != true {
		t.Errorf("output = %v", res.Output)
	}
}

func TestClaudeMissingResultFails(t *testing.T) {
	bin, _ := fakeClaude(t, fakeClaudeOpts{events: []map[string]any{initEvent()}, exit: 3})
	_, err := NewClaude().Execute(context.Background(), map[string]any{"binary": bin, "prompt": "p"})
	if err == nil {
		t.Fatal("expected error when no result event arrives")
	}
}

func TestClaudeEventsAreCapped(t *testing.T) {
	events := manyTurns(10, usage(1, 1, 0, 0))
	events = append(events, resultEvent("ok", false, 10))
	bin, _ := fakeClaude(t, fakeClaudeOpts{events: events})
	res, err := NewClaude().Execute(context.Background(), map[string]any{"binary": bin, "prompt": "p", "events_limit": 3})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(res.Output["events"].([]any)); n != 3 || res.Output["events_truncated"] != true {
		t.Errorf("events = %d, truncated = %v", n, res.Output["events_truncated"])
	}
}

func TestClaudeRejectsInvalidParams(t *testing.T) {
	for name, params := range map[string]map[string]any{
		"no prompt or skill":    {},
		"both prompt and skill": {"prompt": "a", "skill": "b"},
		"empty prompt":          {"prompt": " "},
		"bad permission mode":   {"prompt": "p", "permission_mode": "yolo"},
		"unknown limit":         {"prompt": "p", "limits": map[string]any{"max_vibes": 1}},
		"negative limit":        {"prompt": "p", "limits": map[string]any{"max_turns": -1}},
		"bad chdir":             {"prompt": "p", "chdir": "/nonexistent-dir"},
		"bad bare":              {"prompt": "p", "bare": "yes"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewClaude().Execute(context.Background(), params); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

// sandboxTransport replays canned stream-json as a sandbox would, recording the exec request.
type sandboxTransport struct {
	fakeTransport
	events []map[string]any
	req    openshell.ExecRequest
	prompt string
	// block keeps the stream open until the context ends, like a CLI that is still working.
	block bool
}

func (f *sandboxTransport) Exec(ctx context.Context, req openshell.ExecRequest, stdout, stderr io.Writer) (int, error) {
	f.req = req
	b, _ := io.ReadAll(req.Stdin)
	f.prompt = string(b)
	for _, ev := range f.events {
		line, _ := json.Marshal(ev)
		if _, err := fmt.Fprintf(stdout, "%s\n", line); err != nil {
			return -1, err
		}
	}
	if f.block {
		<-ctx.Done()
		return -1, ctx.Err()
	}
	return 0, nil
}

func TestClaudeRunsInASandbox(t *testing.T) {
	ft := &sandboxTransport{events: []map[string]any{
		initEvent(),
		assistantEvent("m1", []any{textBlock("hello from the sandbox")}, usage(10, 5, 0, 0)),
		resultEvent("done", false, 1),
	}}
	var gotCfg openshell.Config
	e := NewClaude().WithOpenShell(openshell.Config{Transport: "cli", Workspace: "ws"})
	e.newTransport = func(c openshell.Config) (openshell.Transport, error) { gotCfg = c; return ft, nil }
	var rec progressRecorder
	res, err := e.Execute(rec.ctx(), map[string]any{
		"prompt": "fix the bug", "sandbox": "sb1", "transport": "sdk",
		"chdir": "/sandbox/repo", "model": "claude-haiku-5-5", "env": map[string]any{"ANTHROPIC_BASE_URL": "https://relay-api.local"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ft.req.Sandbox != "sb1" || ft.req.Workdir != "/sandbox/repo" || ft.prompt != "fix the bug" {
		t.Errorf("exec request %+v prompt %q", ft.req, ft.prompt)
	}
	if ft.req.Command[0] != "claude" || !strings.Contains(strings.Join(ft.req.Command, " "), "--output-format stream-json") || !strings.Contains(strings.Join(ft.req.Command, " "), "--model claude-haiku-5-5") {
		t.Errorf("command %q", ft.req.Command)
	}
	if len(ft.req.Env) != 1 || ft.req.Env["ANTHROPIC_BASE_URL"] != "https://relay-api.local" {
		t.Errorf("only the step's env goes into the sandbox, not the runner's: %v", ft.req.Env)
	}
	if gotCfg.Transport != "sdk" || gotCfg.Workspace != "ws" {
		t.Errorf("transport config %+v", gotCfg)
	}
	if res.Output["result"] != "done" || res.Output["exit_code"] != 0 {
		t.Errorf("output %v", res.Output)
	}
	rec.mu.Lock()
	kinds := strings.Join(rec.kinds, ",")
	rec.mu.Unlock()
	if !strings.Contains(kinds, "text") {
		t.Errorf("the transcript did not stream: %s", kinds)
	}
}

func TestClaudeInASandboxEnforcesLimitsByEndingTheExec(t *testing.T) {
	ft := &sandboxTransport{events: manyTurns(5, usage(1, 1, 0, 0)), block: true}
	e := NewClaude()
	e.newTransport = func(openshell.Config) (openshell.Transport, error) { return ft, nil }
	done := make(chan struct{})
	var err error
	go func() {
		_, err = e.Execute(context.Background(), map[string]any{"prompt": "x", "sandbox": "sb1", "limits": map[string]any{"max_turns": 2}})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the step did not end after the limit was exceeded")
	}
	if err == nil || !strings.Contains(err.Error(), "max_turns") {
		t.Errorf("err = %v", err)
	}
}
