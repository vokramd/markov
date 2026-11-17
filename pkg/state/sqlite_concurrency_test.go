package state

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The helper is a separate process: an in-process connection pool cannot
// reproduce the production scheduler's concurrent engine invocations.
func TestSQLiteProcessHelper(t *testing.T) {
	path := os.Getenv("MARKOV_SQLITE_TEST_PATH")
	if path == "" {
		return
	}
	fmt.Println("starting")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.CreateRun(ctx, &Run{RunID: "child", WorkflowFile: "fixture.yaml", Entrypoint: "main", Status: RunRunning, VarsJSON: "{}", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	fmt.Println("ready")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	// Force a replacement connection; its busy timeout must also be set.
	s.db.SetMaxIdleConns(0)
	if err := s.SaveStep(ctx, &StepResult{RunID: "child", WorkflowName: "main", StepName: "dispatch", Status: StepCompleted, OutputJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteWaitsForOtherProcess(t *testing.T) {
	for _, stage := range []string{"migration", "checkpoint"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			s, err := NewSQLiteStore(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			lock, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			lock.SetMaxOpenConns(1)
			acquire := func() {
				t.Helper()
				if _, err := lock.Exec("BEGIN IMMEDIATE"); err != nil {
					t.Fatal(err)
				}
			}
			release := func() {
				t.Helper()
				if _, err := lock.Exec("COMMIT"); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "migration" {
				acquire()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSQLiteProcessHelper$")
			cmd.Env = append(os.Environ(), "MARKOV_SQLITE_TEST_PATH="+path)
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			lines := make(chan string, 16)
			go func() {
				defer close(lines)
				scanner := bufio.NewScanner(output)
				for scanner.Scan() {
					lines <- scanner.Text()
				}
			}()
			expect := func(want string) {
				t.Helper()
				select {
				case got := <-lines:
					if got != want {
						t.Fatalf("child=%q, want %q", got, want)
					}
				case <-ctx.Done():
					t.Fatal("child timed out")
				}
			}
			expect("starting")
			if stage == "migration" {
				// A child without a busy handler exits while this lock remains held.
				select {
				case got := <-lines:
					t.Fatalf("migration did not wait: %q", got)
				case <-time.After(150 * time.Millisecond):
				}
				release()
			}
			expect("ready")
			acquire()
			if _, err := fmt.Fprintln(input, "write"); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-lines:
				t.Fatalf("checkpoint did not wait: %q", got)
			case <-time.After(150 * time.Millisecond):
			}
			release()
			_ = input.Close()
			var rest []string
			for line := range lines {
				rest = append(rest, line)
			}
			if err := cmd.Wait(); err != nil {
				t.Fatalf("helper: %v: %s", err, strings.Join(rest, "\n"))
			}
			step, err := s.GetStep(context.Background(), "child", "main", "dispatch")
			if err != nil || step == nil || string(step.Status) != "completed" {
				t.Fatalf("missing checkpoint: %+v %v", step, err)
			}
		})
	}
}

func TestSQLiteBusyTimeoutAndConnectionReplacement(t *testing.T) {
	for _, tc := range []struct {
		name, options string
		timeout       int
	}{
		{"default", "", 5000}, {"explicit", "?_pragma=busy_timeout%3D30", 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			s, err := NewSQLiteStore(path + tc.options)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			s.db.SetMaxIdleConns(0)
			var timeout int
			if err := s.db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
				t.Fatal(err)
			}
			if timeout != tc.timeout {
				t.Fatalf("busy_timeout=%d, want %d", timeout, tc.timeout)
			}
			if tc.name != "explicit" {
				return
			}
			lock, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if _, err := lock.Exec("BEGIN IMMEDIATE"); err != nil {
				t.Fatal(err)
			}
			defer lock.Exec("ROLLBACK")
			started := time.Now()
			err = s.SaveStep(context.Background(), &StepResult{RunID: "locked", WorkflowName: "main", StepName: "step", Status: StepCompleted})
			if err == nil || !strings.Contains(err.Error(), "SQLITE_BUSY") {
				t.Fatalf("expected bounded busy failure, got %v", err)
			}
			if time.Since(started) > 2*time.Second {
				t.Fatal("configured timeout not bounded")
			}
		})
	}
}
