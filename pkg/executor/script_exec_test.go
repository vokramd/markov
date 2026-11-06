package executor

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestScriptExecRunsInlineContentWithArgsAndEnv(t *testing.T) {
	t.Setenv("MARKOV_TEST_VALUE", "old-value")
	exec := NewScriptExec()
	result, err := exec.Execute(context.Background(), map[string]any{
		"interpreter": "sh",
		"content":     "printf '%s:%s' \"$1\" \"$MARKOV_TEST_VALUE\"",
		"args":        []any{"argument"},
		"env":         map[string]any{"MARKOV_TEST_VALUE": "environment"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := result.Output["stdout"], "argument:environment"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := result.Output["exit_code"], 0; got != want {
		t.Fatalf("exit_code = %v, want %d", got, want)
	}
}

func TestScriptExecRunsPathFromScriptsDirectory(t *testing.T) {
	scriptsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(scriptsDir, "report.sh"), []byte("printf 'from-file:%s' \"$1\""), 0600); err != nil {
		t.Fatal(err)
	}

	exec := NewScriptExec()
	result, err := exec.Execute(context.Background(), map[string]any{
		"interpreter": "sh",
		"path":        "report.sh",
		"args":        []any{"ok"},
		"_script_dir": scriptsDir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := result.Output["stdout"], "from-file:ok"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestScriptExecRejectsInvalidScriptSources(t *testing.T) {
	exec := NewScriptExec()
	for name, params := range map[string]map[string]any{
		"neither content nor path": {
			"interpreter": "sh",
		},
		"both content and path": {
			"interpreter": "sh",
			"content":     "echo inline",
			"path":        "file.sh",
		},
		"path escapes scripts directory": {
			"interpreter": "sh",
			"path":        "../file.sh",
			"_script_dir": t.TempDir(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := exec.Execute(context.Background(), params)
			if err == nil {
				t.Fatal("Execute() error = nil, want validation error")
			}
			if !strings.Contains(err.Error(), "script_exec:") {
				t.Fatalf("error = %q, want script_exec prefix", err)
			}
		})
	}
}

func TestScriptExecReturnsOutputOnScriptFailure(t *testing.T) {
	exec := NewScriptExec()
	result, err := exec.Execute(context.Background(), map[string]any{
		"interpreter": "sh",
		"content":     "echo failed >&2; exit 7",
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want script failure")
	}
	if result == nil {
		t.Fatal("Execute() result = nil, want captured output")
	}
	if got, want := result.Output["exit_code"], 7; got != want {
		t.Fatalf("exit_code = %v, want %d", got, want)
	}
	if got := result.Output["stderr"]; got != "failed\n" {
		t.Fatalf("stderr = %q, want failed newline", got)
	}
}

func TestScriptExecAcceptsScalarArgsAndEnv(t *testing.T) {
	result, err := NewScriptExec().Execute(context.Background(), map[string]any{
		"interpreter": "sh",
		"content":     "printf '%s %s %s' \"$1\" \"$2\" \"$N\"",
		"args":        []any{3, true},
		"env":         map[string]any{"N": 1.5},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := result.Output["stdout"], "3 true 1.5"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if _, err := NewScriptExec().Execute(context.Background(), map[string]any{
		"interpreter": "sh", "content": "true", "args": []any{map[string]any{}},
	}); err == nil {
		t.Fatal("a map argument should be rejected")
	}
}

func TestScriptExecStdin(t *testing.T) {
	big := strings.Repeat("x", 300*1024) // larger than one argument may be
	result, err := NewScriptExec().Execute(context.Background(), map[string]any{
		"interpreter": "sh", "content": "wc -c", "stdin": big,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := strings.TrimSpace(result.Output["stdout"].(string)); got != "307200" {
		t.Fatalf("stdin bytes = %s", got)
	}
	result, err = NewScriptExec().Execute(context.Background(), map[string]any{
		"interpreter": "sh", "content": "cat", "stdin": []any{map[string]any{"a": 1}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := result.Output["stdout"]; got != `[{"a":1}]` {
		t.Fatalf("JSON stdin = %q", got)
	}
}

// With live_output, each line is reported while the script is still running: the script waits
// for a file that the progress handler creates when it sees the first line.
func TestScriptExecLiveOutputReportsLinesAsTheyAreWritten(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "seen")
	var mu sync.Mutex
	var lines []string
	ctx := WithProgress(context.Background(), func(kind string, data map[string]any) {
		if kind != "output" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, data["stream"].(string)+":"+data["text"].(string))
		if data["text"] == "first" {
			_ = os.WriteFile(gate, nil, 0600)
		}
	})
	result, err := NewScriptExec().Execute(ctx, map[string]any{
		"interpreter": "sh",
		"content":     "echo first\nfor i in $(seq 100); do [ -e \"$1\" ] && break; sleep 0.05; done\n[ -e \"$1\" ] || exit 3\necho oops >&2\nprintf last",
		"args":        []any{gate},
		"live_output": true,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v (the first line was not reported while the script ran)", err)
	}
	if got, want := result.Output["stdout"], "first\nlast"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	mu.Lock()
	defer mu.Unlock()
	sort.Strings(lines[1:]) // stdout and stderr lines after the first may interleave either way
	if got, want := strings.Join(lines, ","), "stdout:first,stderr:oops,stdout:last"; got != want {
		t.Fatalf("output progress = %s, want %s", got, want)
	}
}

func TestScriptExecReportsNoOutputProgressByDefault(t *testing.T) {
	reported := false
	ctx := WithProgress(context.Background(), func(string, map[string]any) { reported = true })
	if _, err := NewScriptExec().Execute(ctx, map[string]any{"interpreter": "sh", "content": "echo hi"}); err != nil {
		t.Fatal(err)
	}
	if reported {
		t.Fatal("progress reported without live_output")
	}
	if _, err := NewScriptExec().Execute(ctx, map[string]any{"interpreter": "sh", "content": "echo hi", "live_output": "yes"}); err == nil {
		t.Fatal("non-boolean live_output accepted")
	}
}
