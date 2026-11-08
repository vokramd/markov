package engine

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vokramd/markov/pkg/callback"
	"github.com/vokramd/markov/pkg/executor"
	"github.com/vokramd/markov/pkg/parser"
	"github.com/vokramd/markov/pkg/state"
)

// visitExec plays shell_exec for the recursion example: it counts the nodes visited, keeps the
// last command (the report), and fails the first visit of failOnce.
type visitExec struct {
	mu       sync.Mutex
	visits   map[string]int
	last     string
	failOnce string
}

func (v *visitExec) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	cmd, _ := params["command"].(string)
	v.mu.Lock()
	defer v.mu.Unlock()
	v.last = cmd
	if path, ok := strings.CutPrefix(cmd, "echo 'visiting "); ok {
		path = strings.TrimSuffix(path, "'")
		v.visits[path]++
		if path == v.failOnce && v.visits[path] == 1 {
			return nil, errors.New("visit failed")
		}
	}
	return &executor.Result{Output: map[string]any{"stdout": ""}}, nil
}

var exampleLeaves = []string{
	"docs/guides/install.md", "docs/guides/codify.md", "docs/reference/steps/claude.md",
	"docs/reference/steps/codex.md", "docs/reference/cli.md", "docs/README.md",
}

func parseRecursionExample(t *testing.T) *parser.WorkflowFile {
	t.Helper()
	file, err := parser.ParseFile(filepath.Join("..", "..", "examples", "recursion.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func TestRecursionExampleWalksTheTree(t *testing.T) {
	visit := &visitExec{visits: map[string]int{}}
	eng, _ := newTestEngine(t, parseRecursionExample(t), map[string]executor.Executor{"shell_exec": visit})
	if _, err := eng.Run(context.Background(), "", nil); err != nil {
		t.Fatal(err)
	}
	if len(visit.visits) != 10 {
		t.Errorf("visited %d nodes, want 10: %v", len(visit.visits), visit.visits)
	}
	// The leaves come back up in tree order, whatever order the calls finished in.
	want := "printf '%s\\n' '" + strings.Join(exampleLeaves, "' '") + "' "
	if visit.last != want {
		t.Errorf("report = %q\nwant     %q", visit.last, want)
	}
}

func TestResumedRecursionSkipsFinishedSubtrees(t *testing.T) {
	file := parseRecursionExample(t)
	store, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	visit := &visitExec{visits: map[string]int{}, failOnce: "docs/reference/steps/codex.md"}
	first := New(file, store, map[string]executor.Executor{"shell_exec": visit})
	first.SetCallbacks([]callback.Callback{&mockCallback{}})
	runID, err := first.Run(context.Background(), "", nil)
	if err == nil {
		t.Fatal("the run should fail at codex.md")
	}

	second := New(file, store, map[string]executor.Executor{"shell_exec": visit})
	second.SetCallbacks([]callback.Callback{&mockCallback{}})
	if err := second.Resume(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	for path, n := range visit.visits {
		want := 1
		if path == visit.failOnce {
			want = 2
		}
		if n != want {
			t.Errorf("%s visited %d times, want %d", path, n, want)
		}
	}
	if len(visit.visits) != 10 {
		t.Errorf("visited %d nodes, want 10", len(visit.visits))
	}
}

func TestCallsDeeperThanMaxCallDepthFailWithTheChain(t *testing.T) {
	file, err := parser.Parse([]byte(`
entrypoint: main
max_call_depth: 5
workflows:
  - name: main
    steps:
      - name: start
        workflow: loop
  - name: loop
    steps:
      - name: again
        workflow: loop
`))
	if err != nil {
		t.Fatal(err)
	}
	eng, _ := newTestEngine(t, file, nil)
	_, err = eng.Run(context.Background(), "", nil)
	if err == nil {
		t.Fatal("a workflow that calls itself with no base case should fail")
	}
	for _, want := range []string{"deeper than max_call_depth 5", "main.start → loop, loop.again → loop (×5)", "base case"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestMaxCallDepthDefaultsAndMustNotBeNegative(t *testing.T) {
	file, err := parser.Parse([]byte("entrypoint: main\nworkflows:\n  - name: main\n    steps: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if file.MaxCallDepth != parser.DefaultMaxCallDepth {
		t.Errorf("MaxCallDepth = %d, want %d", file.MaxCallDepth, parser.DefaultMaxCallDepth)
	}
	if _, err := parser.Parse([]byte("entrypoint: main\nmax_call_depth: -1\nworkflows:\n  - name: main\n    steps: []\n")); err == nil {
		t.Error("a negative max_call_depth should not parse")
	}
}
