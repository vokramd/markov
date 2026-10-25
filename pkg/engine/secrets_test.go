package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jctanner/markov/pkg/callback"
	"github.com/jctanner/markov/pkg/executor"
	"github.com/jctanner/markov/pkg/parser"
	"github.com/jctanner/markov/pkg/state"
)

const (
	adminToken  = "ghp_admin_secret_value"
	githubToken = "ghp_bot_secret_value_1"
	mintedToken = "ghp_minted_secret_value"
)

// echoExec returns its rendered params as its output, so a test can see what a step was given.
type echoExec struct{ fail *bool }

func (e echoExec) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	if e.fail != nil && *e.fail {
		return &executor.Result{Output: params}, errors.New("request to " + params["url"].(string) + " failed")
	}
	return &executor.Result{Output: params}, nil
}

const secretsWorkflow = `
entrypoint: main
secrets: [ADMIN]
vars:
  ADMIN: ` + adminToken + `
  GITHUB_TOKEN: ` + githubToken + `
  GITHUB_USER: markov-bot
workflows:
  - name: main
    steps:
      - name: mint
        type: http_request
        register: mint
        secret: [token]
      - name: facts
        type: set_fact
        secret: true
        vars:
          auth_header: "token {{ GITHUB_TOKEN }}"
      - name: use
        type: shell_exec
        params:
          url: "https://github.local/user?t={{ mint.token }}&admin={{ ADMIN }}&user={{ GITHUB_USER }}"
`

func TestSecretsAreMaskedInEventsAndLogs(t *testing.T) {
	file, err := parser.Parse([]byte(secretsWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	eng, cb := newTestEngine(t, file, map[string]executor.Executor{
		"http_request": &mockExec{output: map[string]any{"token": mintedToken, "login": "markov-bot"}},
		"shell_exec":   echoExec{},
	})
	eng.Verbose = true
	var logs bytes.Buffer
	log.SetOutput(eng.Secrets().Writer(&logs))
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	runID, err := eng.Run(context.Background(), "main", nil)
	if err != nil {
		t.Fatal(err)
	}

	events := eventsJSON(t, cb)
	for _, s := range []string{adminToken, githubToken, mintedToken} {
		if strings.Contains(events, s) {
			t.Errorf("events contain %q", s)
		}
		if strings.Contains(logs.String(), s) {
			t.Errorf("log contains %q", s)
		}
	}
	for _, want := range []string{"<secret:ADMIN>", "<secret:GITHUB_TOKEN>", "<secret:mint.token>", "markov-bot"} {
		if !strings.Contains(events, want) {
			t.Errorf("events have no %s", want)
		}
	}

	// The steps were given the real values; only what was written out is masked.
	steps, err := eng.store.GetSteps(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		var out map[string]any
		json.Unmarshal([]byte(s.OutputJSON), &out)
		if s.StepName == "use" && !strings.Contains(fmt.Sprint(out["url"]), "t="+mintedToken+"&admin="+adminToken) {
			t.Errorf("use step got %s", s.OutputJSON)
		}
	}
}

func TestSecretsStayMaskedOnResume(t *testing.T) {
	file, err := parser.Parse([]byte(secretsWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	fail := true
	execs := map[string]executor.Executor{
		"http_request": &mockExec{output: map[string]any{"token": mintedToken}},
		"shell_exec":   echoExec{fail: &fail},
	}

	first := New(file, store, execs)
	firstCB := &mockCallback{}
	first.SetCallbacks([]callback.Callback{firstCB})
	runID, err := first.Run(context.Background(), "main", nil)
	if err == nil {
		t.Fatal("the use step should fail")
	}
	if events := eventsJSON(t, firstCB); strings.Contains(events, mintedToken) || !strings.Contains(events, "request to https://github.local/user?t=<secret:mint.token>") {
		t.Errorf("failure not masked: %s", events)
	}

	// A new process: it has never seen the minted token, which comes back from the store.
	fail = false
	second := New(file, store, execs)
	secondCB := &mockCallback{}
	second.SetCallbacks([]callback.Callback{secondCB})
	if err := second.Resume(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	events := eventsJSON(t, secondCB)
	if strings.Contains(events, mintedToken) || strings.Contains(events, adminToken) {
		t.Errorf("resumed run's events contain a secret: %s", events)
	}
	if !strings.Contains(events, "<secret:mint.token>") {
		t.Errorf("resumed run's events have no masked token: %s", events)
	}
}

func eventsJSON(t *testing.T, cb *mockCallback) string {
	t.Helper()
	cb.mu.Lock()
	defer cb.mu.Unlock()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cb.all); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
