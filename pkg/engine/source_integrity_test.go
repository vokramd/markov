package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vokramd/markov/pkg/callback"
	"github.com/vokramd/markov/pkg/executor"
	"github.com/vokramd/markov/pkg/parser"
)

func TestStrictSourceIntegrityRejectsChangedScript(t *testing.T) {
	eng, sourceRoot := sourceIntegrityTestEngine(t)
	runID, err := eng.Run(context.Background(), "", nil)
	if err == nil {
		t.Fatal("Run() error = nil, want failure")
	}

	writeSourceFile(t, filepath.Join(sourceRoot, "scripts", "release.py"), "print('changed')\n")
	eng.SourceIntegrityMode = SourceIntegrityStrict
	err = eng.Resume(context.Background(), runID)
	if err == nil || !strings.Contains(err.Error(), "workflow source changed") {
		t.Fatalf("Resume() error = %v, want source mismatch", err)
	}

	run, err := eng.store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.SourceDigest == "" {
		t.Fatal("Run.SourceDigest is empty")
	}
	checks, err := eng.store.GetSourceChecks(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || checks[0].Mode != string(SourceIntegrityStrict) || !checks[0].SourceDrifted {
		t.Fatalf("source checks = %#v, want strict drift record", checks)
	}
}

func TestWarnSourceIntegrityRecordsDriftAndResumes(t *testing.T) {
	eng, sourceRoot := sourceIntegrityTestEngine(t)
	runID, err := eng.Run(context.Background(), "", nil)
	if err == nil {
		t.Fatal("Run() error = nil, want failure")
	}
	writeSourceFile(t, filepath.Join(sourceRoot, "scripts", "release.py"), "print('changed')\n")

	err = eng.Resume(context.Background(), runID)
	if err == nil || !strings.Contains(err.Error(), "planned failure") {
		t.Fatalf("Resume() error = %v, want step failure after accepted drift", err)
	}
	checks, err := eng.store.GetSourceChecks(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || checks[0].Mode != string(SourceIntegrityWarn) || !checks[0].SourceDrifted {
		t.Fatalf("source checks = %#v, want warn drift record", checks)
	}

	cb, ok := eng.callbacks[0].(*mockCallback)
	if !ok {
		t.Fatal("callback is not mockCallback")
	}
	var resumed callback.RunResumedEvent
	found := false
	for _, event := range cb.all {
		if candidate, ok := event.(callback.RunResumedEvent); ok {
			resumed = candidate
			found = true
		}
	}
	if !found || !resumed.SourceDrifted || resumed.SourceIntegrityMode != string(SourceIntegrityWarn) {
		t.Fatalf("run_resumed event = %#v, want warn source drift", resumed)
	}
}

func TestOffSourceIntegritySkipsComparison(t *testing.T) {
	eng, sourceRoot := sourceIntegrityTestEngine(t)
	runID, err := eng.Run(context.Background(), "", nil)
	if err == nil {
		t.Fatal("Run() error = nil, want failure")
	}
	writeSourceFile(t, filepath.Join(sourceRoot, "scripts", "release.py"), "print('changed')\n")
	eng.SourceIntegrityMode = SourceIntegrityOff

	if err := eng.Resume(context.Background(), runID); err == nil {
		t.Fatal("Resume() error = nil, want planned step failure")
	}
	checks, err := eng.store.GetSourceChecks(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || checks[0].Mode != string(SourceIntegrityOff) || checks[0].ObservedDigest != "" || checks[0].SourceDrifted {
		t.Fatalf("source checks = %#v, want off record without comparison", checks)
	}
}

func TestParseSourceIntegrityMode(t *testing.T) {
	if mode, err := ParseSourceIntegrityMode("STRICT"); err != nil || mode != SourceIntegrityStrict {
		t.Fatalf("ParseSourceIntegrityMode() = %q, %v; want strict, nil", mode, err)
	}
	if _, err := ParseSourceIntegrityMode("invalid"); err == nil {
		t.Fatal("ParseSourceIntegrityMode(invalid) error = nil")
	}
}

func sourceIntegrityTestEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	sourceRoot := t.TempDir()
	writeSourceFile(t, filepath.Join(sourceRoot, "workflow.yaml"), "entrypoint: main\n")
	writeSourceFile(t, filepath.Join(sourceRoot, "scripts", "release.py"), "print('original')\n")
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Workflows: []parser.Workflow{{
			Name:  "main",
			Steps: []parser.Step{{Name: "fail", Type: "test"}},
		}},
	}
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{
		"test": &mockExec{err: errors.New("planned failure")},
	})
	eng.SourcePath = sourceRoot
	return eng, sourceRoot
}

func writeSourceFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
