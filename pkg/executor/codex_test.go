package executor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vokramd/markov/pkg/openshell"
)

func codexThread() map[string]any {
	return map[string]any{"type": "thread.started", "thread_id": "thread-1"}
}

func codexMessage(id, text string) map[string]any {
	return map[string]any{"type": "item.completed", "item": map[string]any{"id": id, "type": "agent_message", "text": text}}
}

func codexCommand(phase, id, command string, exit any, output string) map[string]any {
	status := "in_progress"
	if phase == "item.completed" {
		status = "completed"
	}
	return map[string]any{"type": phase, "item": map[string]any{
		"id": id, "type": "command_execution", "command": command, "aggregated_output": output, "exit_code": exit, "status": status,
	}}
}

func codexTurnDone(in, cached, out int) map[string]any {
	return map[string]any{"type": "turn.completed", "usage": map[string]any{
		"input_tokens": in, "cached_input_tokens": cached, "output_tokens": out, "reasoning_output_tokens": 0,
	}}
}

func TestCodexRunsAndStreamsEvents(t *testing.T) {
	bin, record := fakeClaude(t, fakeClaudeOpts{events: []map[string]any{
		codexThread(),
		{"type": "turn.started"},
		codexMessage("item_0", "I'll look."),
		codexCommand("item.started", "item_1", "cat a.txt", nil, ""),
		codexCommand("item.completed", "item_1", "cat a.txt", 0, "hello\n"),
		codexMessage("item_2", "hello"),
		codexTurnDone(1000, 800, 50),
	}})
	workdir := t.TempDir()
	rec := &progressRecorder{}
	res, err := NewCodex().Execute(rec.ctx(), map[string]any{
		"binary":       bin,
		"prompt":       "do the thing",
		"chdir":        workdir,
		"model":        "gpt-5.5",
		"effort":       "low",
		"sandbox_mode": "read-only",
		"config":       map[string]any{"shell_environment_policy.inherit": "all", "tools.web_search": false},
		"add_dirs":     []any{"/a"},
		"env":          map[string]any{"X": "1"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := res.Output
	if out["result"] != "hello" || out["session_id"] != "thread-1" || out["num_turns"] != 1 || out["is_error"] != false || out["tool_calls"] != 1 {
		t.Errorf("unexpected output: %v", out)
	}
	// new tokens exclude cache reads: (1000-800) + 50
	if out["tokens"] != 250 || out["model"] != "gpt-5.5" || out["prompt_chars"] != len("do the thing") {
		t.Errorf("tokens %v model %v prompt_chars %v", out["tokens"], out["model"], out["prompt_chars"])
	}
	if got := strings.Join(rec.kinds, ","); got != "init,text,tool_use,tool_result,text,usage,result" {
		t.Errorf("progress kinds = %s", got)
	}

	r, _ := os.ReadFile(record)
	got := string(r)
	resolved, _ := filepath.EvalSymlinks(workdir)
	for _, want := range []string{
		resolved + "\n",
		"---STDIN\ndo the thing\n",
		"---ARGS\nexec\n--json\n--skip-git-repo-check\n--model\ngpt-5.5\n",
		"-c\nsandbox_mode=\"read-only\"\n-c\nmodel_reasoning_effort=\"low\"\n-c\nshell_environment_policy.inherit=\"all\"\n-c\ntools.web_search=false\n",
		"--add-dir\n/a\n-\n---ENV",
		"X:1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("record missing %q:\n%s", want, got)
		}
	}
}

func TestCodexResumesASession(t *testing.T) {
	bin, record := fakeClaude(t, fakeClaudeOpts{events: []map[string]any{codexThread(), codexMessage("i", "ok"), codexTurnDone(1, 0, 1)}})
	if _, err := NewCodex().Execute(context.Background(), map[string]any{"binary": bin, "prompt": "and then?", "resume": "thread-1", "git_repo_check": true}); err != nil {
		t.Fatal(err)
	}
	r, _ := os.ReadFile(record)
	if !strings.Contains(string(r), "---ARGS\nexec\nresume\n--json\nthread-1\n-\n") {
		t.Errorf("args:\n%s", r)
	}
}

func TestCodexEnforcesMaxToolCallsAndKillsTheProcess(t *testing.T) {
	var events []map[string]any
	events = append(events, codexThread())
	for _, id := range []string{"c1", "c2", "c3"} {
		events = append(events, codexCommand("item.started", id, "sleep 1", nil, ""))
	}
	bin, _ := fakeClaude(t, fakeClaudeOpts{events: events, hang: true})
	start := time.Now()
	res, err := NewCodex().Execute(context.Background(), map[string]any{"binary": bin, "prompt": "x", "limits": map[string]any{"max_tool_calls": 2}})
	if err == nil || !strings.Contains(err.Error(), "limit exceeded: max_tool_calls") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("the process was not stopped at the limit")
	}
	if res.Output["limit_exceeded"] != "max_tool_calls" || res.Output["tool_calls"] != 3 || res.Output["is_error"] != true {
		t.Errorf("output %v", res.Output)
	}
}

func TestCodexMaxTokensFailsOnceTheTurnReportsUsage(t *testing.T) {
	bin, _ := fakeClaude(t, fakeClaudeOpts{events: []map[string]any{codexThread(), codexMessage("i", "ok"), codexTurnDone(5000, 0, 100)}})
	_, err := NewCodex().Execute(context.Background(), map[string]any{"binary": bin, "prompt": "x", "limits": map[string]any{"max_tokens": 1000}})
	if err == nil || !strings.Contains(err.Error(), "max_tokens") {
		t.Errorf("err = %v", err)
	}
}

func TestCodexFailedTurnFailsTheStep(t *testing.T) {
	bin, _ := fakeClaude(t, fakeClaudeOpts{exit: 1, events: []map[string]any{
		codexThread(),
		{"type": "error", "message": "model not supported"},
		{"type": "turn.failed", "error": map[string]any{"message": "model not supported"}},
	}})
	res, err := NewCodex().Execute(context.Background(), map[string]any{"binary": bin, "prompt": "x"})
	if err == nil || err.Error() != "codex: model not supported" {
		t.Fatalf("err = %v", err)
	}
	if res.Output["is_error"] != true || len(res.Output["errors"].([]any)) != 1 {
		t.Errorf("output %v", res.Output)
	}
}

func TestCodexWithoutACompletedTurnFails(t *testing.T) {
	bin, _ := fakeClaude(t, fakeClaudeOpts{events: []map[string]any{codexThread(), codexCommand("item.started", "c1", "make test", nil, "")}})
	_, err := NewCodex().Execute(context.Background(), map[string]any{"binary": bin, "prompt": "x"})
	if err == nil || !strings.Contains(err.Error(), "without completing its turn") || !strings.Contains(err.Error(), "`make test`") {
		t.Errorf("err = %v", err)
	}
}

func TestCodexJSONSchemaIsPassedAndItsAnswerReturned(t *testing.T) {
	bin, record := fakeClaude(t, fakeClaudeOpts{events: []map[string]any{codexThread(), codexMessage("i", `{"label":"bug"}`), codexTurnDone(1, 0, 1)}})
	schema := map[string]any{"type": "object", "properties": map[string]any{"label": map[string]any{"type": "string"}}}
	res, err := NewCodex().Execute(context.Background(), map[string]any{"binary": bin, "prompt": "x", "json_schema": schema})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := json.Marshal(res.Output["structured_output"]); string(got) != `{"label":"bug"}` {
		t.Errorf("structured_output = %s", got)
	}
	r, _ := os.ReadFile(record)
	args := strings.Split(string(r), "\n")
	for i, a := range args {
		if a == "--output-schema" && i+1 < len(args) {
			if _, err := os.Stat(args[i+1]); err == nil {
				t.Error("the schema file was not removed after the run")
			}
			return
		}
	}
	t.Errorf("no --output-schema in:\n%s", r)
}

func TestCodexAnswerThatIsNotJSONFailsWithASchema(t *testing.T) {
	bin, _ := fakeClaude(t, fakeClaudeOpts{events: []map[string]any{codexThread(), codexMessage("i", "a bug, I think"), codexTurnDone(1, 0, 1)}})
	_, err := NewCodex().Execute(context.Background(), map[string]any{"binary": bin, "prompt": "x", "json_schema": `{"type":"object"}`})
	if err == nil || !strings.Contains(err.Error(), "does not follow json_schema") {
		t.Errorf("err = %v", err)
	}
}

func TestCodexRejectsInvalidParams(t *testing.T) {
	for name, params := range map[string]map[string]any{
		"no prompt":          {},
		"bad sandbox_mode":   {"prompt": "x", "sandbox_mode": "yolo"},
		"unknown limit":      {"prompt": "x", "limits": map[string]any{"max_turns": 3}},
		"profile on resume":  {"prompt": "x", "resume": "t", "profile": "p"},
		"add_dirs on resume": {"prompt": "x", "resume": "t", "add_dirs": []any{"/a"}},
		"map config value":   {"prompt": "x", "config": map[string]any{"a": map[string]any{"b": 1}}},
		"schema in sandbox":  {"prompt": "x", "sandbox": "sb", "json_schema": map[string]any{"type": "object"}},
		"missing chdir":      {"prompt": "x", "chdir": "/does/not/exist"},
	} {
		if _, err := NewCodex().Execute(context.Background(), params); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestCodexRunsInASandbox(t *testing.T) {
	ft := &sandboxTransport{events: []map[string]any{codexThread(), codexMessage("i", "done"), codexTurnDone(10, 0, 5)}}
	e := NewCodex().WithOpenShell(openshell.Config{Transport: "cli"})
	e.newTransport = func(openshell.Config) (openshell.Transport, error) { return ft, nil }
	res, err := e.Execute(context.Background(), map[string]any{
		"prompt": "fix it", "sandbox": "sb1", "chdir": "/sandbox/repo", "dangerously_bypass_approvals_and_sandbox": true,
		"env": map[string]any{"OPENAI_BASE_URL": "https://relay-api.local"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := strings.Join(ft.req.Command, " ")
	if ft.req.Sandbox != "sb1" || ft.req.Workdir != "/sandbox/repo" || ft.prompt != "fix it" || !strings.HasPrefix(cmd, "codex exec --json") ||
		!strings.Contains(cmd, "--dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("exec request %+v prompt %q", ft.req, ft.prompt)
	}
	if len(ft.req.Env) != 1 || res.Output["result"] != "done" {
		t.Errorf("env %v output %v", ft.req.Env, res.Output)
	}
}
