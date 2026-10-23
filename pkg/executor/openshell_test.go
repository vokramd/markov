package executor

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jctanner/markov/pkg/openshell"
)

// fakeTransport records what the executors asked for.
type fakeTransport struct {
	cfg      openshell.Config
	create   openshell.CreateRequest
	exec     openshell.ExecRequest
	stdin    string
	copies   []string
	deleted  []string
	exitCode int
	provider openshell.ProviderRequest
	network  []string
}

func (f *fakeTransport) Create(_ context.Context, req openshell.CreateRequest) (*openshell.Sandbox, error) {
	f.create = req
	return &openshell.Sandbox{Name: req.Name, ID: "id", Phase: "Ready", Workspace: "default"}, nil
}

func (f *fakeTransport) Upload(_ context.Context, sandbox, src, destDir string, gitIgnore bool) error {
	f.copies = append(f.copies, fmt.Sprintf("up %s %s %s %v", sandbox, src, destDir, gitIgnore))
	return nil
}

func (f *fakeTransport) Download(_ context.Context, sandbox, src, destDir string) error {
	f.copies = append(f.copies, fmt.Sprintf("down %s %s %s", sandbox, src, destDir))
	return nil
}

func (f *fakeTransport) Exec(_ context.Context, req openshell.ExecRequest, stdout, stderr io.Writer) (int, error) {
	f.exec = req
	if req.Stdin != nil {
		b, _ := io.ReadAll(req.Stdin)
		f.stdin = string(b)
	}
	fmt.Fprint(stdout, "line one\nline two")
	fmt.Fprint(stderr, "warn\n")
	return f.exitCode, nil
}

func (f *fakeTransport) Delete(_ context.Context, names []string) error {
	f.deleted = names
	return nil
}

func openShellStep(t *testing.T, op string, cfg openshell.Config) (*OpenShell, *fakeTransport) {
	t.Helper()
	ft := &fakeTransport{}
	ex := NewOpenShellSteps(cfg)["openshell_"+op].(*OpenShell)
	ex.newTransport = func(c openshell.Config) (openshell.Transport, error) {
		ft.cfg = c
		return ft, nil
	}
	return ex, ft
}

func TestOpenShellCreateDefaultsANameFromTheStep(t *testing.T) {
	ex, ft := openShellStep(t, "create", openshell.Config{})
	ctx := WithStep(context.Background(), StepContext{RunID: "ABCDEF123456", Workflow: "main", Step: "Make Sandbox!"})
	res, err := ex.Execute(ctx, map[string]any{"image": "localhost/x:1", "gpu": true, "cpu": 2, "env": map[string]any{"N": 1}})
	if err != nil {
		t.Fatal(err)
	}
	name := res.Output["name"].(string)
	if !regexp.MustCompile(`^mk-make-sand-[0-9a-f]{6}$`).MatchString(name) || len(name) > 19 {
		t.Errorf("default name %q (OpenShell allows DNS labels of at most 19 characters)", name)
	}
	if ft.create.Labels["markov-run"] != "ABCDEF123456" || ft.create.Labels["markov-step"] != "Make-Sandbox" || ft.create.Labels["markov-workflow"] != "main" {
		t.Errorf("labels %v", ft.create.Labels)
	}
	if ft.create.GPU != "any" || ft.create.CPU != "2" || ft.create.Env["N"] != "1" || res.Output["image"] != "localhost/x:1" {
		t.Errorf("request %+v, output %v", ft.create, res.Output)
	}
	again, _ := ex.Execute(ctx, map[string]any{"labels": map[string]any{"markov-step": "mine"}})
	if ft.create.Labels["markov-step"] != "mine" {
		t.Errorf("a label the step sets should win: %v", ft.create.Labels)
	}
	if again.Output["name"] == name {
		t.Error("two creates by one step got the same name; for_each items would collide")
	}
}

func TestOpenShellExecCommandFormsAndOutput(t *testing.T) {
	ex, ft := openShellStep(t, "exec", openshell.Config{})
	var mu sync.Mutex
	var lines []string
	ctx := WithProgress(context.Background(), func(kind string, data map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf("%s:%s:%s", kind, data["stream"], data["text"]))
	})
	res, err := ex.Execute(ctx, map[string]any{"sandbox": "sb", "command": "echo hi | wc", "timeout": "30", "stdin": map[string]any{"a": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ft.exec.Command, " ") != "sh -c echo hi | wc" || ft.exec.Timeout != 30 || ft.stdin != `{"a":1}` {
		t.Errorf("request %+v stdin %q", ft.exec, ft.stdin)
	}
	if res.Output["stdout"] != "line one\nline two" || res.Output["exit_code"] != 0 || res.Output["timed_out"] != false {
		t.Errorf("output %v", res.Output)
	}
	// "line two" has no newline, so it is reported when the command ends.
	want := "output:stdout:line one,output:stderr:warn,output:stdout:line two"
	if got := strings.Join(lines, ","); got != want {
		t.Errorf("progress %s, want %s", got, want)
	}

	if _, err := ex.Execute(context.Background(), map[string]any{"sandbox": "sb", "command": []any{"ls", "-l", "/my dir"}}); err != nil {
		t.Fatal(err)
	}
	if len(ft.exec.Command) != 3 || ft.exec.Command[2] != "/my dir" {
		t.Errorf("argv form: %q", ft.exec.Command)
	}
}

func TestOpenShellExecFailsOnNonZeroAndTimeoutButKeepsOutput(t *testing.T) {
	ex, ft := openShellStep(t, "exec", openshell.Config{})
	ft.exitCode = 3
	res, err := ex.Execute(context.Background(), map[string]any{"sandbox": "sb", "command": "false"})
	if err == nil || !strings.Contains(err.Error(), "exited with status 3") || res == nil || res.Output["exit_code"] != 3 {
		t.Errorf("err %v res %v", err, res)
	}
	ft.exitCode = openshell.TimeoutExitCode
	res, err = ex.Execute(context.Background(), map[string]any{"sandbox": "sb", "command": "sleep 9", "timeout": 1})
	if err == nil || !strings.Contains(err.Error(), "timed out after 1s") || res.Output["timed_out"] != true {
		t.Errorf("err %v res %v", err, res)
	}
	if _, err := ex.Execute(context.Background(), map[string]any{"sandbox": "sb"}); err == nil {
		t.Error("exec without a command succeeded")
	}
}

func TestOpenShellCopiesReportWhereSrcLanded(t *testing.T) {
	up, ft := openShellStep(t, "upload", openshell.Config{})
	res, err := up.Execute(context.Background(), map[string]any{"sandbox": "sb", "src": "./site/"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Output["path"] != "/sandbox/site" || ft.copies[0] != "up sb ./site/ /sandbox false" {
		t.Errorf("upload %v %v", res.Output, ft.copies)
	}
	down, ft := openShellStep(t, "download", openshell.Config{})
	res, err = down.Execute(context.Background(), map[string]any{"sandbox": "sb", "src": "/sandbox/out/report.md", "dest": "results"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Output["path"] != filepath.Join("results", "report.md") || ft.copies[0] != "down sb /sandbox/out/report.md results" {
		t.Errorf("download %v %v", res.Output, ft.copies)
	}
}

func TestOpenShellDeleteAndTransportOverride(t *testing.T) {
	ex, ft := openShellStep(t, "delete", openshell.Config{Transport: "cli", Workspace: "ws"})
	res, err := ex.Execute(context.Background(), map[string]any{"sandbox": []any{"a", "b"}, "transport": "sdk"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ft.deleted, ",") != "a,b" || len(res.Output["deleted"].([]any)) != 2 {
		t.Errorf("deleted %v output %v", ft.deleted, res.Output)
	}
	if ft.cfg.Transport != "sdk" || ft.cfg.Workspace != "ws" {
		t.Errorf("the step's transport should override the file's and keep the rest: %+v", ft.cfg)
	}
}

func (f *fakeTransport) EnsureProfile(_ context.Context, path string) (openshell.ProfileChange, error) {
	f.copies = append(f.copies, "profile "+path)
	return openshell.ProfileImported, nil
}

func (f *fakeTransport) EnsureProvider(_ context.Context, req openshell.ProviderRequest) (*openshell.ProviderResult, error) {
	f.provider = req
	keys := make([]string, 0, len(req.Credentials))
	for k := range req.Credentials {
		keys = append(keys, k)
	}
	return &openshell.ProviderResult{Created: true, CredentialKeys: keys}, nil
}

func TestOpenShellProviderEnsuresProfileThenProviderAndHidesSecrets(t *testing.T) {
	ex, ft := openShellStep(t, "provider", openshell.Config{})
	res, err := ex.Execute(context.Background(), map[string]any{
		"name": "relay", "type": "relay-claude", "profile": "openshell/relay.yaml",
		"credentials": map[string]any{"ANTHROPIC_API_KEY": "sk-very-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ft.copies[0] != "profile openshell/relay.yaml" || ft.provider.Name != "relay" || ft.provider.Type != "relay-claude" || ft.provider.Credentials["ANTHROPIC_API_KEY"] != "sk-very-secret" {
		t.Errorf("profile %v provider %+v", ft.copies, ft.provider)
	}
	if res.Output["created"] != true || res.Output["profile"] != "imported" {
		t.Errorf("output %v", res.Output)
	}
	if strings.Contains(fmt.Sprint(res.Output), "sk-very-secret") {
		t.Errorf("the secret is in the output: %v", res.Output)
	}
	if got := SecretParams("openshell_provider"); len(got) != 1 || got[0] != "credentials" {
		t.Errorf("secret params %v", got)
	}

	// type defaults to the name; no profile means none is touched.
	ex, ft = openShellStep(t, "provider", openshell.Config{})
	if _, err := ex.Execute(context.Background(), map[string]any{"name": "github"}); err != nil {
		t.Fatal(err)
	}
	if ft.provider.Type != "github" || len(ft.copies) != 0 {
		t.Errorf("provider %+v copies %v", ft.provider, ft.copies)
	}
	if _, err := ex.Execute(context.Background(), map[string]any{"type": "x"}); err == nil {
		t.Error("a provider without a name was accepted")
	}
}

func (f *fakeTransport) NetworkProblems(context.Context, string, time.Duration) ([]string, error) {
	return f.network, nil
}
