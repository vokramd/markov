package openshell

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestE2E runs the same checks on every transport against a live gateway. It needs the
// openshell CLI with an active gateway and podman: MARKOV_OPENSHELL_E2E=1 go test ./pkg/openshell
func TestE2E(t *testing.T) {
	if os.Getenv("MARKOV_OPENSHELL_E2E") != "1" {
		t.Skip("set MARKOV_OPENSHELL_E2E=1 to run against a live OpenShell gateway")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Containerfile"), []byte("FROM docker.io/library/python:3.12-slim\nRUN echo baked > /etc/markov-e2e\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	img, err := Build(ctx, BuildRequest{Tag: "markov-e2e:1", Context: dir}, &log)
	if err != nil {
		t.Fatalf("build: %v\n%s", err, log.String())
	}
	if img.Ref != "localhost/markov-e2e:1" || img.ID == "" {
		t.Fatalf("image %+v", img)
	}

	for _, transport := range []string{"cli"} {
		t.Run(transport, func(t *testing.T) {
			tr, err := New(Config{Transport: transport})
			if err != nil {
				t.Fatal(err)
			}
			conformance(ctx, t, tr, img.Ref)
		})
	}
}

func conformance(ctx context.Context, t *testing.T, tr Transport, image string) {
	name := fmt.Sprintf("mk-e2e-%d", time.Now().UnixNano()%1e9)
	sb, err := tr.Create(ctx, CreateRequest{Name: name, Image: image, Env: map[string]string{"FROM_CREATE": "yes"}, Labels: map[string]string{"markov-run": "e2e"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() {
		if err := tr.Delete(context.Background(), []string{name}); err != nil {
			t.Errorf("delete: %v", err)
		}
	}()
	if sb.Name != name || sb.Phase != "Ready" || sb.ID == "" {
		t.Fatalf("sandbox %+v", sb)
	}

	run := func(req ExecRequest) (int, string, string) {
		t.Helper()
		req.Sandbox = name
		var out, errOut bytes.Buffer
		code, err := tr.Exec(ctx, req, &out, &errOut)
		if err != nil {
			t.Fatalf("exec %v: %v", req.Command, err)
		}
		return code, out.String(), errOut.String()
	}

	t.Run("exec", func(t *testing.T) {
		code, out, errOut := run(ExecRequest{Command: []string{"sh", "-c", "cat /etc/markov-e2e; echo to-stderr >&2; exit 3"}})
		if code != 3 || out != "baked\n" || errOut != "to-stderr\n" {
			t.Errorf("code=%d out=%q err=%q", code, out, errOut)
		}
		code, out, _ = run(ExecRequest{Command: []string{"sh", "-c", "pwd; echo $K; cat"}, Workdir: "/tmp", Env: map[string]string{"K": "v"}, Stdin: strings.NewReader("from-stdin")})
		if code != 0 || out != "/tmp\nv\nfrom-stdin" {
			t.Errorf("code=%d out=%q", code, out)
		}
		if code, _, _ = run(ExecRequest{Command: []string{"sleep", "30"}, Timeout: 1}); code != TimeoutExitCode {
			t.Errorf("timeout exit code %d", code)
		}
		if code, out, _ = run(ExecRequest{Command: []string{"printf", "%s", "two words"}}); code != 0 || out != "two words" {
			t.Errorf("argv not preserved: code=%d out=%q", code, out)
		}
	})

	t.Run("copy", func(t *testing.T) {
		local := t.TempDir()
		tree := filepath.Join(local, "tree")
		for rel, body := range map[string]string{"a.txt": "a\n", "sub/b.txt": "b\n", ".hidden": "h\n"} {
			p := filepath.Join(tree, rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		single := filepath.Join(local, "single.txt")
		if err := os.WriteFile(single, []byte("one\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := tr.Upload(ctx, name, tree, "/sandbox/in", false); err != nil {
			t.Fatalf("upload dir: %v", err)
		}
		if err := tr.Upload(ctx, name, single, "/sandbox/in/deeper", false); err != nil {
			t.Fatalf("upload file: %v", err)
		}
		_, out, _ := run(ExecRequest{Command: []string{"sh", "-c", "cd /sandbox/in && find . -type f | sort"}})
		if want := "./deeper/single.txt\n./tree/.hidden\n./tree/a.txt\n./tree/sub/b.txt\n"; out != want {
			t.Errorf("uploaded files\n%s\nwant\n%s", out, want)
		}

		back := filepath.Join(local, "back")
		if err := tr.Download(ctx, name, "/sandbox/in/tree", back); err != nil {
			t.Fatalf("download dir: %v", err)
		}
		if err := tr.Download(ctx, name, "/sandbox/in/deeper/single.txt", back); err != nil {
			t.Fatalf("download file: %v", err)
		}
		for rel, body := range map[string]string{"tree/a.txt": "a\n", "tree/sub/b.txt": "b\n", "tree/.hidden": "h\n", "single.txt": "one\n"} {
			got, err := os.ReadFile(filepath.Join(back, rel))
			if err != nil || string(got) != body {
				t.Errorf("%s: %q %v", rel, got, err)
			}
		}
		if err := tr.Download(ctx, name, "/sandbox/nope", back); err == nil {
			t.Error("downloading a missing path succeeded")
		}
	})

	t.Run("errors", func(t *testing.T) {
		var out bytes.Buffer
		if code, err := tr.Exec(ctx, ExecRequest{Sandbox: name + "-missing", Command: []string{"true"}}, &out, io.Discard); err == nil && code == 0 {
			t.Error("exec in a missing sandbox succeeded")
		}
		if err := tr.Delete(ctx, []string{name + "-missing"}); err != nil {
			t.Errorf("deleting a missing sandbox: %v", err)
		}
	})
}
