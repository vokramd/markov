package codify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutputSizesDoc(t *testing.T) {
	events := []map[string]any{
		{"event_type": "step_completed", "workflow_name": "agent", "step_name": "list", "output": map[string]any{"body": strings.Repeat("x", 3000)}},
		{"event_type": "step_completed", "workflow_name": "agent", "step_name": "huge", "oversized_event_bytes": float64(40 << 20)},
		{"event_type": "step_completed", "workflow_name": "agent", "step_name": "loop"}, // a for_each's closing event
		{"event_type": "step_failed", "workflow_name": "agent", "step_name": "bundle", "error_kind": "max_output", "output_bytes": float64(562603118), "limit_bytes": float64(64 << 20),
			"largest": []any{map[string]any{"path": "inputs.pull_reviews", "bytes": float64(447800000), "items": float64(3)}}},
		{"event_type": "step_failed", "workflow_name": "agent", "step_name": "other", "error": "boom"},
	}
	sizes, fails := outputSizes(events)
	if len(sizes) != 2 || len(fails) != 1 || fails[0].Step != "agent.bundle" || fails[0].Largest[0].Items != 3 {
		t.Fatalf("sizes %+v fails %+v", sizes, fails)
	}
	doc := outputSizesDoc(sizes, fails)
	for _, want := range []string{
		"**agent.bundle**: output reached 537 MB, over its max_output 64 MB",
		"inputs.pull_reviews: 427 MB, 3 items",
		"| agent.huge **(over a quarter of the limit)** | 1 | 40 MB / 40 MB / 40 MB | 40 MB |",
		"| agent.list | 1 |",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("doc lacks %q:\n%s", want, doc)
		}
	}
	if strings.Index(doc, "agent.huge") > strings.Index(doc, "agent.list |") {
		t.Error("largest step first")
	}
	if got := liveLimitFailure(&RunResult{Err: errString("x")}, events); !strings.Contains(got, "agent.bundle produced 537 MB") {
		t.Errorf("liveLimitFailure = %q", got)
	}
	if got := liveLimitFailure(&RunResult{Err: errString("ran out of memory: the kernel killed it")}, nil); !strings.Contains(got, "out of memory") {
		t.Errorf("liveLimitFailure (oom) = %q", got)
	}
	if got := liveLimitFailure(&RunResult{Err: errString("exit status 1")}, nil); got != "" {
		t.Errorf("an ordinary failure is not a limit failure: %q", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestPromptsTellOfALiveLimitFailure(t *testing.T) {
	why := "step target.bundle produced 3 KB, over its max_output 1 KB"
	retro, err := render(defaultRetroPrompt, RetroInputs{Target: "target", Loose: true, LiveFailure: why, EditableFiles: []string{"workflows/target.yaml"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"did not complete: " + why, "When a step produces too much", "output_sizes.md"} {
		if !strings.Contains(retro, want) {
			t.Errorf("retro prompt lacks %q", want)
		}
	}
	judge, err := render(judgeLoosePrompt, map[string]any{"Target": "t", "LiveFailure": why, "Repeat": &Repeat{File: "r.json", Live: -1, Candidate: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(judge, "did not complete") || strings.Contains(judge, "-1") || !strings.Contains(judge, "0 for the proposed version") {
		t.Errorf("loose judge:\n%s", judge)
	}
	strict, err := render(judgePrompt, map[string]any{"Target": "t", "LiveFailure": why})
	if err != nil || !strings.Contains(strict, "did not complete") {
		t.Errorf("strict judge: %v\n%s", err, strict)
	}
}

// A current version with a step over its max_output does not stop the loop: the retro is told,
// and a candidate that completes is judged on doing the job and promoted.
func TestCodifyGoesOnWhenTheCurrentVersionRunsOutOfRoom(t *testing.T) {
	base, _, hook := server(t)
	root := project(t, base)
	write(t, root, "vars.yaml", "base: "+base+"\nbig: "+strings.Repeat("x", 3000)+"\n")
	write(t, root, "workflows/target.yaml", `name: target
steps:
  - name: bundle
    type: set_fact
    max_output: 1KB
    vars:
      bundled: "{{ big }}{{ big }}"
  - name: agent
    type: claude
    params:
      prompt: "ROLE=task list the stories at {{ base }}/stories.json into stories.txt"
`)
	told := filepath.Join(t.TempDir(), "told")
	fakeDir, fake := fakeClaude(t, `  case "$prompt" in *"did not complete: step target.bundle produced"*) touch `+told+` ;; esac
`+codifyAll)
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := config(t, root, fake, hook)
	cfg.LiveHosts = []string{"codify.test"}
	rows, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rows) == 0 || rows[0].Validation.Status != "passed" || rows[0].Promoted == nil {
		t.Fatalf("rows = %+v", rows)
	}
	if _, err := os.Stat(told); err != nil {
		t.Fatal("the retro was not told the current version ran out of room")
	}
	sizes, _ := os.ReadFile(filepath.Join(root+".codify", "iter-0001", "evidence", "output_sizes.md"))
	if !strings.Contains(string(sizes), "**target.bundle**") {
		t.Fatalf("output_sizes.md = %s", sizes)
	}
}

func TestRetroModelIsItsOwn(t *testing.T) {
	c := Config{Model: "claude-haiku-5-5"}
	if c.modelFor("retro") != "claude-haiku-5-5" || c.modelFor("judge") != "claude-haiku-5-5" {
		t.Fatal("without --retro-model both use --model")
	}
	c.RetroModel = "claude-sonnet-5-5"
	if c.modelFor("retro") != "claude-sonnet-5-5" || c.modelFor("judge") != "claude-haiku-5-5" {
		t.Fatal("--retro-model is the retro agent's only")
	}
}

// Loose mode moves one kind of item end to end per pass, and a smaller step after such a move
// is rejected.
func TestLoosePassesMoveOneKindOfItem(t *testing.T) {
	p, err := render(defaultRetroPrompt, RetroInputs{Target: "t", Loose: true, EditableFiles: []string{"workflows/t.yaml"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ONE coherent change: one kind of item, end to end", "Group them by the kind of item", "make a smaller change this pass: ONE job of that kind"} {
		if !strings.Contains(p, want) {
			t.Errorf("loose prompt lacks %q", want)
		}
	}
	if strings.Contains(p, "move the earliest one in the data flow\n   into native steps") {
		t.Error("loose prompt still moves one job at a time")
	}
}
