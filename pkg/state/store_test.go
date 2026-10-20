package state

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenStoreSelectsSQLiteForPaths(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	defer store.Close()
	if _, ok := store.(*SQLiteStore); !ok {
		t.Fatalf("store = %T, want *SQLiteStore", store)
	}
}

func TestSQLiteStoreMigratesLegacyRunsTableForSourceDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE runs (
			run_id TEXT PRIMARY KEY,
			workflow_file TEXT NOT NULL,
			entrypoint TEXT NOT NULL,
			status TEXT NOT NULL,
			vars_json TEXT NOT NULL DEFAULT '{}',
			parent_run_id TEXT,
			parent_step TEXT,
			for_each_key TEXT,
			started_at TIMESTAMP NOT NULL,
			completed_at TIMESTAMP
		);
		CREATE TABLE steps (
			run_id TEXT NOT NULL,
			workflow_name TEXT NOT NULL,
			step_name TEXT NOT NULL,
			status TEXT NOT NULL,
			output_json TEXT,
			artifacts_json TEXT,
			error TEXT,
			started_at TIMESTAMP,
			completed_at TIMESTAMP,
			PRIMARY KEY (run_id, workflow_name, step_name)
		);`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()
	run := &Run{
		RunID:        "source-migration",
		WorkflowFile: "workflow.yaml",
		SourceDigest: "digest",
		Entrypoint:   "main",
		Status:       RunRunning,
		VarsJSON:     `{}`,
		StartedAt:    time.Now(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	got, err := store.GetRun(context.Background(), run.RunID)
	if err != nil || got.SourceDigest != "digest" {
		t.Fatalf("GetRun() = %#v, %v; want source digest", got, err)
	}
}

func TestRedactStoreLocation(t *testing.T) {
	path := "/tmp/markov-state.db"
	if got := RedactStoreLocation(path); got != path {
		t.Fatalf("RedactStoreLocation(sqlite) = %q, want %q", got, path)
	}

	got := RedactStoreLocation("postgres://markov:secret@postgres:5432/markov_state?sslmode=disable")
	if strings.Contains(got, "secret") || strings.Contains(got, "sslmode=disable") {
		t.Fatalf("RedactStoreLocation() leaked DSN details: %q", got)
	}
	if got != "postgres://<redacted>@postgres:5432/markov_state?<redacted>" {
		t.Fatalf("RedactStoreLocation() = %q, want redacted postgres location", got)
	}
}

func TestSQLiteStoreContract(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("OpenStore(sqlite) error = %v", err)
	}
	defer store.Close()
	testStoreContract(t, store)
}

func TestPostgresStoreContract(t *testing.T) {
	dsn := os.Getenv("MARKOV_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MARKOV_TEST_POSTGRES_DSN is not set")
	}

	store, err := OpenStore(dsn)
	if err != nil {
		t.Fatalf("OpenStore(postgres) error = %v", err)
	}
	defer store.Close()
	if _, ok := store.(*PostgresStore); !ok {
		t.Fatalf("store = %T, want *PostgresStore", store)
	}
	testStoreContract(t, store)

	second, err := OpenStore(dsn)
	if err != nil {
		t.Fatalf("OpenStore(postgres second open) error = %v", err)
	}
	second.Close()
}

func testStoreContract(t *testing.T, store Store) {
	t.Helper()

	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	runID := "run-" + suffix
	childID := "child-" + suffix
	started := time.Now().UTC().Truncate(time.Microsecond)
	completed := started.Add(5 * time.Second)

	run := &Run{
		RunID:        runID,
		WorkflowFile: "/workflows/pipeline",
		SourceDigest: "original-digest",
		Entrypoint:   "main",
		Status:       RunRunning,
		VarsJSON:     `{"env":"test"}`,
		StartedAt:    started,
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}

	child := &Run{
		RunID:        childID,
		WorkflowFile: "/workflows/pipeline",
		Entrypoint:   "per-item",
		Status:       RunCompleted,
		VarsJSON:     `{}`,
		ParentRunID:  runID,
		ParentStep:   "fanout",
		ForEachKey:   "item-a",
		StartedAt:    started.Add(time.Second),
		CompletedAt:  &completed,
	}
	if err := store.CreateRun(ctx, child); err != nil {
		t.Fatalf("CreateRun(child) error = %v", err)
	}

	run.Status = RunFailed
	run.CompletedAt = &completed
	if err := store.UpdateRun(ctx, run); err != nil {
		t.Fatalf("UpdateRun() error = %v", err)
	}

	got, err := store.GetRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if got.RunID != runID || got.WorkflowFile != run.WorkflowFile || got.SourceDigest != run.SourceDigest || got.Entrypoint != "main" || got.Status != RunFailed {
		t.Fatalf("GetRun() = %#v, want updated run", got)
	}
	if got.CompletedAt == nil {
		t.Fatalf("GetRun().CompletedAt = nil, want timestamp")
	}

	check := &SourceCheck{
		RunID:          runID,
		CheckedAt:      completed,
		Mode:           "warn",
		ExpectedDigest: "original-digest",
		ObservedDigest: "changed-digest",
		SourceDrifted:  true,
	}
	if err := store.SaveSourceCheck(ctx, check); err != nil {
		t.Fatalf("SaveSourceCheck() error = %v", err)
	}
	checks, err := store.GetSourceChecks(ctx, runID)
	if err != nil {
		t.Fatalf("GetSourceChecks() error = %v", err)
	}
	if len(checks) != 1 || checks[0].Mode != "warn" || !checks[0].SourceDrifted || checks[0].ObservedDigest != "changed-digest" {
		t.Fatalf("GetSourceChecks() = %#v, want persisted drift check", checks)
	}

	if reloads, err := store.GetReloads(ctx, runID); err != nil || len(reloads) != 0 {
		t.Fatalf("GetReloads() before any = %#v, %v; want none", reloads, err)
	}
	for i, digest := range []string{"v1", "v2"} {
		reload := &Reload{
			RunID: runID, StepRunID: childID, Workflow: "iterate", Step: "reload",
			Source: "/wf", OldDigest: fmt.Sprintf("v%d", i), NewDigest: digest, ReloadedAt: completed,
		}
		if err := store.SaveReload(ctx, reload); err != nil {
			t.Fatalf("SaveReload() error = %v", err)
		}
	}
	reloads, err := store.GetReloads(ctx, runID)
	if err != nil {
		t.Fatalf("GetReloads() error = %v", err)
	}
	if len(reloads) != 2 || reloads[0].NewDigest != "v1" || reloads[1].OldDigest != "v1" || reloads[1].StepRunID != childID || reloads[1].Step != "reload" {
		t.Fatalf("GetReloads() = %#v, want two reloads in order", reloads)
	}

	children, err := store.GetChildRuns(ctx, runID)
	if err != nil {
		t.Fatalf("GetChildRuns() error = %v", err)
	}
	if len(children) != 1 || children[0].RunID != childID || children[0].ForEachKey != "item-a" {
		t.Fatalf("GetChildRuns() = %#v, want child run", children)
	}

	stepStart := started.Add(2 * time.Second)
	stepEnd := started.Add(3 * time.Second)
	step := &StepResult{
		RunID:         runID,
		WorkflowName:  "main",
		StepName:      "fetch",
		Status:        StepRunning,
		OutputJSON:    `{"old":true}`,
		ArtifactsJSON: `{"artifact":"old"}`,
		Error:         "still running",
		StartedAt:     &stepStart,
	}
	if err := store.SaveStep(ctx, step); err != nil {
		t.Fatalf("SaveStep() initial error = %v", err)
	}

	step.Status = StepCompleted
	step.OutputJSON = `{"ok":true}`
	step.ArtifactsJSON = `{"artifact":"new"}`
	step.Error = ""
	step.CompletedAt = &stepEnd
	if err := store.SaveStep(ctx, step); err != nil {
		t.Fatalf("SaveStep() upsert error = %v", err)
	}

	gotStep, err := store.GetStep(ctx, runID, "main", "fetch")
	if err != nil {
		t.Fatalf("GetStep() error = %v", err)
	}
	if gotStep == nil || gotStep.Status != StepCompleted || gotStep.OutputJSON != `{"ok":true}` || gotStep.Error != "" {
		t.Fatalf("GetStep() = %#v, want updated completed step", gotStep)
	}

	missingStep, err := store.GetStep(ctx, runID, "main", "missing")
	if err != nil {
		t.Fatalf("GetStep(missing) error = %v", err)
	}
	if missingStep != nil {
		t.Fatalf("GetStep(missing) = %#v, want nil", missingStep)
	}

	steps, err := store.GetSteps(ctx, runID)
	if err != nil {
		t.Fatalf("GetSteps() error = %v", err)
	}
	if len(steps) != 1 || steps[0].StepName != "fetch" {
		t.Fatalf("GetSteps() = %#v, want fetch step", steps)
	}

	runs, err := store.ListRuns(ctx)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if !containsRun(runs, runID) || !containsRun(runs, childID) {
		t.Fatalf("ListRuns() missing test runs: %#v", runs)
	}
}

func containsRun(runs []*Run, runID string) bool {
	for _, run := range runs {
		if run.RunID == runID {
			return true
		}
	}
	return false
}

func TestStepDefinitionHashRoundTripsAndDeleteStepsRemovesRows(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "hash.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for _, s := range []*StepResult{
		{RunID: "r", WorkflowName: "w", StepName: "a", Status: StepCompleted, DefinitionHash: "abc123"},
		{RunID: "r", WorkflowName: "w", StepName: "b[1]", Status: StepCompleted},
		{RunID: "r", WorkflowName: "w", StepName: "c", Status: StepCompleted, DefinitionHash: "def456"},
		{RunID: "other", WorkflowName: "w", StepName: "a", Status: StepCompleted, DefinitionHash: "keep"},
	} {
		if err := store.SaveStep(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.GetStep(ctx, "r", "w", "a")
	if err != nil || got == nil || got.DefinitionHash != "abc123" {
		t.Fatalf("GetStep = %+v, %v", got, err)
	}
	if b, _ := store.GetStep(ctx, "r", "w", "b[1]"); b == nil || b.DefinitionHash != "" {
		t.Errorf("a row saved without a hash reads back empty: %+v", b)
	}
	// Re-saving updates the hash.
	store.SaveStep(ctx, &StepResult{RunID: "r", WorkflowName: "w", StepName: "a", Status: StepCompleted, DefinitionHash: "new"})
	if got, _ := store.GetStep(ctx, "r", "w", "a"); got.DefinitionHash != "new" {
		t.Errorf("hash after update = %q", got.DefinitionHash)
	}
	if err := store.DeleteSteps(ctx, "r", []string{"a", "b[1]", "missing"}); err != nil {
		t.Fatal(err)
	}
	rows, _ := store.GetSteps(ctx, "r")
	if len(rows) != 1 || rows[0].StepName != "c" || rows[0].DefinitionHash != "def456" {
		t.Errorf("rows after delete = %+v", rows)
	}
	if other, _ := store.GetStep(ctx, "other", "w", "a"); other == nil || other.DefinitionHash != "keep" {
		t.Error("another run's rows must not be touched")
	}
}
