package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS runs (
			run_id          TEXT PRIMARY KEY,
			workflow_file   TEXT NOT NULL,
			source_digest   TEXT,
			entrypoint      TEXT NOT NULL,
			status          TEXT NOT NULL,
			vars_json       TEXT NOT NULL DEFAULT '{}',
			parent_run_id   TEXT,
			parent_step     TEXT,
			for_each_key    TEXT,
			started_at      TIMESTAMP NOT NULL,
			completed_at    TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS steps (
			run_id          TEXT NOT NULL,
			workflow_name   TEXT NOT NULL,
			step_name       TEXT NOT NULL,
			status          TEXT NOT NULL,
			output_json     TEXT,
			artifacts_json  TEXT,
			error           TEXT,
			started_at      TIMESTAMP,
			completed_at    TIMESTAMP,
			definition_hash TEXT,
			PRIMARY KEY (run_id, workflow_name, step_name)
		);

		CREATE TABLE IF NOT EXISTS source_checks (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id           TEXT NOT NULL,
			checked_at       TIMESTAMP NOT NULL,
			mode             TEXT NOT NULL,
			expected_digest  TEXT,
			observed_digest  TEXT,
			source_drifted   BOOLEAN NOT NULL DEFAULT FALSE
		);

		CREATE TABLE IF NOT EXISTS reloads (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id       TEXT NOT NULL,
			step_run_id  TEXT NOT NULL,
			workflow     TEXT NOT NULL,
			step         TEXT NOT NULL,
			source       TEXT NOT NULL,
			old_digest   TEXT,
			new_digest   TEXT,
			reloaded_at  TIMESTAMP NOT NULL
		);
	`)
	if err != nil {
		return fmt.Errorf("migrating schema: %w", err)
	}
	if err := ensureSQLiteColumn(db, "runs", "source_digest", "TEXT"); err != nil {
		return err
	}
	if err := ensureSQLiteColumn(db, "steps", "definition_hash", "TEXT"); err != nil {
		return err
	}
	return nil
}

func ensureSQLiteColumn(db *sql.DB, table, column, definition string) error {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return fmt.Errorf("checking %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("scanning %s columns: %w", table, err)
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("checking %s columns: %w", table, err)
	}
	if _, err := db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + definition); err != nil {
		return fmt.Errorf("adding %s.%s: %w", table, column, err)
	}
	return nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) CreateRun(ctx context.Context, run *Run) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runs (run_id, workflow_file, source_digest, entrypoint, status, vars_json, parent_run_id, parent_step, for_each_key, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.RunID, run.WorkflowFile, nullStr(run.SourceDigest), run.Entrypoint, run.Status, run.VarsJSON,
		nullStr(run.ParentRunID), nullStr(run.ParentStep), nullStr(run.ForEachKey), run.StartedAt)
	if err != nil {
		return fmt.Errorf("creating run: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateRun(ctx context.Context, run *Run) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE runs SET status = ?, completed_at = ? WHERE run_id = ?`,
		run.Status, run.CompletedAt, run.RunID)
	if err != nil {
		return fmt.Errorf("updating run: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetRun(ctx context.Context, runID string) (*Run, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT run_id, workflow_file, source_digest, entrypoint, status, vars_json,
		       parent_run_id, parent_step, for_each_key, started_at, completed_at
		FROM runs WHERE run_id = ?`, runID)

	var run Run
	var sourceDigest, parentRunID, parentStep, forEachKey sql.NullString
	var completedAt sql.NullTime
	err := row.Scan(&run.RunID, &run.WorkflowFile, &sourceDigest, &run.Entrypoint, &run.Status, &run.VarsJSON,
		&parentRunID, &parentStep, &forEachKey, &run.StartedAt, &completedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("run %q not found", runID)
	}
	if err != nil {
		return nil, fmt.Errorf("getting run: %w", err)
	}
	run.ParentRunID = parentRunID.String
	run.SourceDigest = sourceDigest.String
	run.ParentStep = parentStep.String
	run.ForEachKey = forEachKey.String
	if completedAt.Valid {
		run.CompletedAt = &completedAt.Time
	}
	return &run, nil
}

func (s *SQLiteStore) ListRuns(ctx context.Context) ([]*Run, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, workflow_file, source_digest, entrypoint, status, vars_json,
		       parent_run_id, parent_step, for_each_key, started_at, completed_at
		FROM runs ORDER BY started_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing runs: %w", err)
	}
	defer rows.Close()

	var runs []*Run
	for rows.Next() {
		var run Run
		var sourceDigest, parentRunID, parentStep, forEachKey sql.NullString
		var completedAt sql.NullTime
		if err := rows.Scan(&run.RunID, &run.WorkflowFile, &sourceDigest, &run.Entrypoint, &run.Status, &run.VarsJSON,
			&parentRunID, &parentStep, &forEachKey, &run.StartedAt, &completedAt); err != nil {
			return nil, fmt.Errorf("scanning run: %w", err)
		}
		run.ParentRunID = parentRunID.String
		run.SourceDigest = sourceDigest.String
		run.ParentStep = parentStep.String
		run.ForEachKey = forEachKey.String
		if completedAt.Valid {
			run.CompletedAt = &completedAt.Time
		}
		runs = append(runs, &run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing runs: %w", err)
	}
	return runs, nil
}

func (s *SQLiteStore) GetChildRuns(ctx context.Context, parentRunID string) ([]*Run, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, workflow_file, source_digest, entrypoint, status, vars_json,
		       parent_run_id, parent_step, for_each_key, started_at, completed_at
		FROM runs WHERE parent_run_id = ? ORDER BY started_at`, parentRunID)
	if err != nil {
		return nil, fmt.Errorf("getting child runs: %w", err)
	}
	defer rows.Close()

	var runs []*Run
	for rows.Next() {
		var run Run
		var sourceDigest, parentRID, parentStep, forEachKey sql.NullString
		var completedAt sql.NullTime
		if err := rows.Scan(&run.RunID, &run.WorkflowFile, &sourceDigest, &run.Entrypoint, &run.Status, &run.VarsJSON,
			&parentRID, &parentStep, &forEachKey, &run.StartedAt, &completedAt); err != nil {
			return nil, fmt.Errorf("scanning child run: %w", err)
		}
		run.ParentRunID = parentRID.String
		run.SourceDigest = sourceDigest.String
		run.ParentStep = parentStep.String
		run.ForEachKey = forEachKey.String
		if completedAt.Valid {
			run.CompletedAt = &completedAt.Time
		}
		runs = append(runs, &run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("getting child runs: %w", err)
	}
	return runs, nil
}

func (s *SQLiteStore) SaveSourceCheck(ctx context.Context, check *SourceCheck) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO source_checks (run_id, checked_at, mode, expected_digest, observed_digest, source_drifted)
		VALUES (?, ?, ?, ?, ?, ?)`,
		check.RunID, check.CheckedAt, check.Mode, nullStr(check.ExpectedDigest), nullStr(check.ObservedDigest), check.SourceDrifted)
	if err != nil {
		return fmt.Errorf("saving source check: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetSourceChecks(ctx context.Context, runID string) ([]*SourceCheck, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, checked_at, mode, expected_digest, observed_digest, source_drifted
		FROM source_checks WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, fmt.Errorf("getting source checks: %w", err)
	}
	defer rows.Close()

	var checks []*SourceCheck
	for rows.Next() {
		var check SourceCheck
		var expected, observed sql.NullString
		if err := rows.Scan(&check.RunID, &check.CheckedAt, &check.Mode, &expected, &observed, &check.SourceDrifted); err != nil {
			return nil, fmt.Errorf("scanning source check: %w", err)
		}
		check.ExpectedDigest = expected.String
		check.ObservedDigest = observed.String
		checks = append(checks, &check)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("getting source checks: %w", err)
	}
	return checks, nil
}

func (s *SQLiteStore) SaveStep(ctx context.Context, step *StepResult) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO steps (run_id, workflow_name, step_name, status, output_json, artifacts_json, error, started_at, completed_at, definition_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (run_id, workflow_name, step_name) DO UPDATE SET
			status = excluded.status,
			output_json = excluded.output_json,
			artifacts_json = excluded.artifacts_json,
			error = excluded.error,
			started_at = excluded.started_at,
			completed_at = excluded.completed_at,
			definition_hash = excluded.definition_hash`,
		step.RunID, step.WorkflowName, step.StepName, step.Status,
		nullStr(step.OutputJSON), nullStr(step.ArtifactsJSON), nullStr(step.Error),
		step.StartedAt, step.CompletedAt, nullStr(step.DefinitionHash))
	if err != nil {
		return fmt.Errorf("saving step: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetSteps(ctx context.Context, runID string) ([]*StepResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, workflow_name, step_name, status, output_json, artifacts_json, error, started_at, completed_at, definition_hash
		FROM steps WHERE run_id = ? ORDER BY started_at`, runID)
	if err != nil {
		return nil, fmt.Errorf("getting steps: %w", err)
	}
	defer rows.Close()

	var steps []*StepResult
	for rows.Next() {
		var s StepResult
		var outputJSON, artifactsJSON, stepErr, defHash sql.NullString
		var startedAt, completedAt sql.NullTime
		if err := rows.Scan(&s.RunID, &s.WorkflowName, &s.StepName, &s.Status,
			&outputJSON, &artifactsJSON, &stepErr, &startedAt, &completedAt, &defHash); err != nil {
			return nil, fmt.Errorf("scanning step: %w", err)
		}
		s.OutputJSON = outputJSON.String
		s.ArtifactsJSON = artifactsJSON.String
		s.Error = stepErr.String
		s.DefinitionHash = defHash.String
		if startedAt.Valid {
			s.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			s.CompletedAt = &completedAt.Time
		}
		steps = append(steps, &s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("getting steps: %w", err)
	}
	return steps, nil
}

func (s *SQLiteStore) GetStep(ctx context.Context, runID, workflowName, stepName string) (*StepResult, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT run_id, workflow_name, step_name, status, output_json, artifacts_json, error, started_at, completed_at, definition_hash
		FROM steps WHERE run_id = ? AND workflow_name = ? AND step_name = ?`,
		runID, workflowName, stepName)

	var sr StepResult
	var outputJSON, artifactsJSON, stepErr, defHash sql.NullString
	var startedAt, completedAt sql.NullTime
	err := row.Scan(&sr.RunID, &sr.WorkflowName, &sr.StepName, &sr.Status,
		&outputJSON, &artifactsJSON, &stepErr, &startedAt, &completedAt, &defHash)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting step: %w", err)
	}
	sr.OutputJSON = outputJSON.String
	sr.ArtifactsJSON = artifactsJSON.String
	sr.Error = stepErr.String
	sr.DefinitionHash = defHash.String
	if startedAt.Valid {
		sr.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		sr.CompletedAt = &completedAt.Time
	}
	return &sr, nil
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func timePtr(t time.Time) *time.Time {
	return &t
}

// DeleteSteps removes the named step rows of a run.
func (s *SQLiteStore) DeleteSteps(ctx context.Context, runID string, stepNames []string) error {
	for _, name := range stepNames {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM steps WHERE run_id = ? AND step_name = ?`, runID, name); err != nil {
			return fmt.Errorf("deleting step %q: %w", name, err)
		}
	}
	return nil
}

func (s *SQLiteStore) SaveReload(ctx context.Context, r *Reload) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO reloads (run_id, step_run_id, workflow, step, source, old_digest, new_digest, reloaded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.RunID, r.StepRunID, r.Workflow, r.Step, r.Source, nullStr(r.OldDigest), nullStr(r.NewDigest), r.ReloadedAt)
	if err != nil {
		return fmt.Errorf("saving reload: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetReloads(ctx context.Context, runID string) ([]*Reload, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, step_run_id, workflow, step, source, old_digest, new_digest, reloaded_at
		FROM reloads WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, fmt.Errorf("getting reloads: %w", err)
	}
	defer rows.Close()

	var reloads []*Reload
	for rows.Next() {
		var r Reload
		var oldDigest, newDigest sql.NullString
		if err := rows.Scan(&r.RunID, &r.StepRunID, &r.Workflow, &r.Step, &r.Source, &oldDigest, &newDigest, &r.ReloadedAt); err != nil {
			return nil, fmt.Errorf("scanning reload: %w", err)
		}
		r.OldDigest = oldDigest.String
		r.NewDigest = newDigest.String
		reloads = append(reloads, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("getting reloads: %w", err)
	}
	return reloads, nil
}
