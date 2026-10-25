package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jctanner/markov/pkg/openshell"
	"github.com/jctanner/markov/pkg/outputlimit"
)

func limited(n int64) context.Context {
	return outputlimit.WithLimit(context.Background(), n)
}

// A command that would print forever is killed at the limit, not read to the end.
func TestShellExecStopsAtTheOutputLimit(t *testing.T) {
	start := time.Now()
	res, err := NewShellExec().Execute(limited(1<<20), map[string]any{"command": "yes"})
	lim, ok := outputlimit.As(err)
	if !ok || lim.Stream != "stdout" || lim.Limit != 1<<20 || res != nil {
		t.Fatalf("err %v, res %v; want a stdout limit error and no output", err, res)
	}
	if !strings.HasPrefix(err.Error(), "shell_exec: stdout reached") {
		t.Errorf("message %q", err.Error())
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %s: the command was not stopped", time.Since(start))
	}
	// Without a limit in the context (or under it) nothing changes.
	if res, err := NewShellExec().Execute(context.Background(), map[string]any{"command": "echo hi"}); err != nil || res.Output["stdout"] != "hi\n" {
		t.Fatalf("no limit: %v %v", res, err)
	}
}

func TestScriptExecStopsAtTheOutputLimit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "loud.sh"), []byte("head -c 3000000 /dev/zero\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := NewScriptExec().Execute(limited(1<<20), map[string]any{"interpreter": "sh", "path": "loud.sh", "_script_dir": dir})
	if _, ok := outputlimit.As(err); !ok {
		t.Fatalf("err = %v, want a limit error", err)
	}
}

// Every page of a listing counts against one limit.
func TestHTTPRequestLimitCountsEveryPage(t *testing.T) {
	srv := pagedServer(t, 2000, 100, "link")
	params := map[string]any{"url": srv.URL + "/list?page=1", "paginate": map[string]any{"max_pages": 50}}
	// One page is about 500 bytes; twenty pages are about 10 KB.
	_, err := NewHTTPRequest().Execute(limited(4000), params)
	lim, ok := outputlimit.As(err)
	if !ok || lim.Stream != "body" {
		t.Fatalf("err = %v, want a body limit error", err)
	}
	if res, err := NewHTTPRequest().Execute(limited(1<<20), params); err != nil || len(numbers(t, res.Output["body"])) != 2000 {
		t.Fatalf("under the limit: %v", err)
	}
}

func TestOpenShellExecStopsAtTheOutputLimit(t *testing.T) {
	ex, _ := openShellStep(t, "exec", openshell.Config{})
	// The fake writes "line one\nline two" and "warn\n": 22 bytes.
	_, err := ex.Execute(limited(10), map[string]any{"sandbox": "sb", "command": "echo"})
	if _, ok := outputlimit.As(err); !ok {
		t.Fatalf("err = %v, want a limit error", err)
	}
}
