package openshell

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeCLI writes an openshell stand-in that logs its argv (one call per line) and answers
// create with JSON and exec with the output and status the test asks for.
func fakeCLI(t *testing.T) (*CLI, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := `#!/bin/sh
echo "$*" >> "` + log + `"
for a in "$@"; do
  case "$a" in
    create) echo '{"name":"sb1","id":"id-1","phase":"Ready","workspace":"default","extra":1}'; exit 0 ;;
    exec) cat >/dev/null; echo "out-line"; echo "err-line" >&2; exit 7 ;;
  esac
done
exit 0
`
	bin := filepath.Join(dir, "openshell")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return NewCLI(Config{Binary: bin, Gateway: "gw", Workspace: "ws"}), log
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestCLICreatePassesFlagsAndReadsJSON(t *testing.T) {
	c, log := fakeCLI(t)
	sb, err := c.Create(context.Background(), CreateRequest{
		Name: "sb1", Image: "localhost/x:1", CPU: "2", Memory: "4Gi", GPU: "any",
		Env: map[string]string{"B": "2", "A": "1"}, Providers: []string{"p"}, Labels: map[string]string{"team": "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if *sb != (Sandbox{Name: "sb1", ID: "id-1", Phase: "Ready", Workspace: "default"}) {
		t.Errorf("sandbox = %+v", sb)
	}
	want := "--gateway gw --workspace ws sandbox create --name sb1 --detach --no-tty -o json --from localhost/x:1 --cpu 2 --memory 4Gi --gpu --env A=1 --env B=2 --provider p --label team=x"
	if got := calls(t, log)[0]; got != want {
		t.Errorf("argv\n got %s\nwant %s", got, want)
	}
}

func TestCLICreateDeletesASandboxThatFailedToProvision(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := `#!/bin/sh
echo "$*" >> "` + log + `"
case "$*" in
  *"sandbox create"*) echo 'Error: sandbox entered error phase while provisioning: HealthCheckFailed' >&2; exit 1 ;;
esac
exit 0
`
	bin := filepath.Join(dir, "openshell")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := NewCLI(Config{Binary: bin}).Create(context.Background(), CreateRequest{Name: "sb1"})
	if err == nil {
		t.Fatal("Create succeeded")
	}
	got := calls(t, log)
	if len(got) != 2 || got[1] != "sandbox delete sb1" {
		t.Errorf("calls = %q, want the create followed by \"sandbox delete sb1\"", got)
	}
}

func TestCLIExecReturnsExitCodeAndStreams(t *testing.T) {
	c, log := fakeCLI(t)
	var out, errOut bytes.Buffer
	code, err := c.Exec(context.Background(), ExecRequest{
		Sandbox: "sb1", Command: []string{"sh", "-c", "echo hi"}, Workdir: "/w", Timeout: 5,
		Env: map[string]string{"K": "v"}, Stdin: strings.NewReader("input"),
	}, &out, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 || out.String() != "out-line\n" || errOut.String() != "err-line\n" {
		t.Errorf("code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	// --timeout is not passed: OpenShell's own timeout leaves the command running.
	want := "--gateway gw --workspace ws sandbox exec --name sb1 --no-tty --workdir /w --env K=v -- sh -c echo hi"
	if got := calls(t, log)[0]; got != want {
		t.Errorf("argv\n got %s\nwant %s", got, want)
	}
}

func TestCLIExecTimeoutStopsTheCLIAndReports124(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "openshell")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	code, err := NewCLI(Config{Binary: bin}).Exec(context.Background(), ExecRequest{Sandbox: "sb", Command: []string{"true"}, Timeout: 1}, io.Discard, io.Discard)
	if err != nil || code != TimeoutExitCode {
		t.Errorf("code=%d err=%v", code, err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("took %s", took)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	if _, err := NewCLI(Config{Binary: bin}).Exec(ctx, ExecRequest{Sandbox: "sb", Command: []string{"true"}, Timeout: 5}, io.Discard, io.Discard); err == nil {
		t.Error("a cancelled exec reported success")
	}
}

func TestCLICopiesIntoDirectories(t *testing.T) {
	c, log := fakeCLI(t)
	src := filepath.Join(t.TempDir(), "site")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := c.Upload(context.Background(), "sb1", src, "/sandbox/in/", false); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err := c.Download(context.Background(), "sb1", "/sandbox/report.md", dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("download did not create its destination directory: %v", err)
	}
	got := calls(t, log)
	if want := "--gateway gw --workspace ws sandbox upload sb1 " + src + " /sandbox/in/ --no-git-ignore"; got[0] != want {
		t.Errorf("upload argv\n got %s\nwant %s", got[0], want)
	}
	if want := "--gateway gw --workspace ws sandbox download sb1 /sandbox/report.md " + filepath.Join(dest, "report.md"); got[1] != want {
		t.Errorf("download argv\n got %s\nwant %s", got[1], want)
	}
	if err := c.Upload(context.Background(), "sb1", filepath.Join(src, "missing"), "/sandbox", true); err == nil {
		t.Error("uploading a missing path succeeded")
	}
}

func TestCLIErrorsCarryStderr(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "openshell")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'Error: sandbox not found' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := NewCLI(Config{Binary: bin}).Delete(context.Background(), []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "openshell sandbox delete: sandbox not found") {
		t.Errorf("err = %v", err)
	}
}

func TestPlainErrorFlattensTheCLIReport(t *testing.T) {
	report := "Error:   × code: 'Internal error', message: \"create sandbox failed: podman API error\n  │ (500): image pull failed\"\n\n"
	want := "code: 'Internal error', message: \"create sandbox failed: podman API error (500): image pull failed\""
	if got := plainError(report); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestImageRef(t *testing.T) {
	for in, want := range map[string]string{
		"site":                   "localhost/site",
		"site:1":                 "localhost/site:1",
		"localhost/site:1":       "localhost/site:1",
		"quay.io/team/site:1":    "quay.io/team/site:1",
		"team/site":              "team/site",
		"site@sha256:abc":        "localhost/site@sha256:abc",
		"registry:5000/site:tag": "registry:5000/site:tag",
	} {
		if got := ImageRef(in); got != want {
			t.Errorf("ImageRef(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewRejectsUnknownTransports(t *testing.T) {
	if _, err := New(Config{Transport: "carrier-pigeon"}); err == nil {
		t.Error("unknown transport accepted")
	}
	if tr, err := New(Config{}); err != nil || tr == nil {
		t.Errorf("default transport: %v", err)
	}
}
