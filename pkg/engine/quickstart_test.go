package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vokramd/markov/pkg/executor"
	"github.com/vokramd/markov/pkg/parser"
)

// The README's Quick Start example runs as written, with the real shell and write_file executors.
func TestQuickstartExampleRuns(t *testing.T) {
	file, err := parser.ParseFile(filepath.Join("..", "..", "examples", "quickstart.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "report.md")
	eng, _ := newTestEngine(t, file, map[string]executor.Executor{
		"shell_exec": executor.NewShellExec(),
		"write_file": executor.NewWriteFile(),
	})
	if _, err := eng.Run(context.Background(), "", map[string]any{"report": report}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{"- api is up\n", "- web is up\n", "- worker is up\n", "Cache warm after 3 rounds (stopped by until)."} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
}
