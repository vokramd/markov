package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestParseVarFlagsCoercesNativeValues(t *testing.T) {
	got := parseVarFlags([]string{
		"enabled=true",
		"disabled=false",
		"count=42",
		"ratio=2.5",
		`items=["alpha","bravo"]`,
		`config={"mode":"safe"}`,
		"environment=prod",
		"empty=",
		"token=prefix=suffix",
	})
	want := map[string]any{
		"enabled":     true,
		"disabled":    false,
		"count":       42,
		"ratio":       2.5,
		"items":       []any{"alpha", "bravo"},
		"config":      map[string]any{"mode": "safe"},
		"environment": "prod",
		"empty":       "",
		"token":       "prefix=suffix",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseVarFlags() = %#v, want %#v", got, want)
	}
}

func TestDefaultStateStorePath_OutOfCluster(t *testing.T) {
	t.Setenv(stateStoreEnv, "")

	old := saTokenPath
	saTokenPath = "/nonexistent/path/token"
	defer func() { saTokenPath = old }()

	got := defaultStateStorePath()
	if got != "./markov-state.db" {
		t.Errorf("defaultStateStorePath() = %q, want ./markov-state.db", got)
	}
}

func TestDefaultStateStorePath_InCluster(t *testing.T) {
	t.Setenv(stateStoreEnv, "")

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("fake-token"), 0644); err != nil {
		t.Fatal(err)
	}

	old := saTokenPath
	saTokenPath = tokenFile
	defer func() { saTokenPath = old }()

	got := defaultStateStorePath()
	if got != "/tmp/markov-state.db" {
		t.Errorf("defaultStateStorePath() = %q, want /tmp/markov-state.db", got)
	}
}

func TestDefaultStateStorePath_EnvironmentOverride(t *testing.T) {
	t.Setenv(stateStoreEnv, "postgres://markov:secret@postgres:5432/markov?sslmode=disable")

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("fake-token"), 0644); err != nil {
		t.Fatal(err)
	}

	old := saTokenPath
	saTokenPath = tokenFile
	defer func() { saTokenPath = old }()

	got := defaultStateStorePath()
	want := "postgres://markov:secret@postgres:5432/markov?sslmode=disable"
	if got != want {
		t.Errorf("defaultStateStorePath() = %q, want %q", got, want)
	}
}

func TestAFailedCommandPrintsNoUsageButAWrongArgumentDoes(t *testing.T) {
	run := func(args ...string) string {
		root := &cobra.Command{Use: "markov"}
		quietUsageOnceArgsParse(root)
		root.AddCommand(&cobra.Command{
			Use:  "run <file>",
			Args: cobra.ExactArgs(1),
			RunE: func(*cobra.Command, []string) error { return errors.New("step \"x\" failed") },
		})
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		if root.Execute() == nil {
			t.Fatalf("%v should fail", args)
		}
		return out.String()
	}
	if out := run("run", "wf.yaml"); strings.Contains(out, "Usage:") || !strings.Contains(out, `step "x" failed`) {
		t.Errorf("a failed run should print its error without the usage:\n%s", out)
	}
	if out := run("run"); !strings.Contains(out, "Usage:") {
		t.Errorf("a missing argument should print the usage:\n%s", out)
	}
	if out := run("run", "wf.yaml", "--no-such-flag"); !strings.Contains(out, "Usage:") {
		t.Errorf("an unknown flag should print the usage:\n%s", out)
	}
}

func TestAddStateStoreFlagRedactsPostgresDefault(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	addStateStoreFlag(cmd, "postgres://markov:secret@postgres:5432/markov?sslmode=disable")

	flag := cmd.Flags().Lookup("state-store")
	if flag == nil {
		t.Fatal("state-store flag was not registered")
	}

	want := "postgres://<redacted>@postgres:5432/markov?<redacted>"
	if flag.DefValue != want {
		t.Errorf("DefValue = %q, want %q", flag.DefValue, want)
	}
	if flag.Value.String() != "postgres://markov:secret@postgres:5432/markov?sslmode=disable" {
		t.Errorf("runtime value was not preserved")
	}
}

func TestResolveNamespace_ExplicitWorkflow(t *testing.T) {
	old := saNamespacePath
	saNamespacePath = "/nonexistent"
	defer func() { saNamespacePath = old }()

	got := resolveNamespace("prod", "staging")
	if got != "prod" {
		t.Errorf("resolveNamespace() = %q, want prod", got)
	}
}

func TestResolveNamespace_FlagFallback(t *testing.T) {
	old := saNamespacePath
	saNamespacePath = "/nonexistent"
	defer func() { saNamespacePath = old }()

	got := resolveNamespace("", "staging")
	if got != "staging" {
		t.Errorf("resolveNamespace() = %q, want staging", got)
	}
}

func TestResolveNamespace_ServiceAccountFallback(t *testing.T) {
	nsFile := filepath.Join(t.TempDir(), "namespace")
	if err := os.WriteFile(nsFile, []byte("ai-pipeline\n"), 0644); err != nil {
		t.Fatal(err)
	}

	old := saNamespacePath
	saNamespacePath = nsFile
	defer func() { saNamespacePath = old }()

	got := resolveNamespace("", "")
	if got != "ai-pipeline" {
		t.Errorf("resolveNamespace() = %q, want ai-pipeline", got)
	}
}

func TestResolveNamespace_DefaultFallback(t *testing.T) {
	old := saNamespacePath
	saNamespacePath = "/nonexistent"
	defer func() { saNamespacePath = old }()

	got := resolveNamespace("", "")
	if got != "default" {
		t.Errorf("resolveNamespace() = %q, want default", got)
	}
}
