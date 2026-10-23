package openshell

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
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

	for _, transport := range []string{"cli", "sdk"} {
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

	t.Run("large, executable and symlinked files", func(t *testing.T) {
		local := t.TempDir()
		tree := filepath.Join(local, "big")
		if err := os.MkdirAll(tree, 0o755); err != nil {
			t.Fatal(err)
		}
		// 3 MiB: several stdin chunks on the sdk transport, past the gateway's 1 MiB message size.
		blob := bytes.Repeat([]byte("0123456789abcdef"), 3<<16)
		if err := os.WriteFile(filepath.Join(tree, "blob"), blob, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tree, "run.sh"), []byte("#!/bin/sh\necho ran\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("run.sh", filepath.Join(tree, "link")); err != nil {
			t.Fatal(err)
		}
		if err := tr.Upload(ctx, name, tree, "/sandbox/x", false); err != nil {
			t.Fatalf("upload: %v", err)
		}
		code, out, _ := run(ExecRequest{Command: []string{"sh", "-c", "cd /sandbox/x/big && wc -c < blob && ./run.sh && readlink link"}})
		if want := fmt.Sprintf("%d\nran\nrun.sh\n", len(blob)); code != 0 || out != want {
			t.Errorf("in the sandbox: code=%d out=%q want %q", code, out, want)
		}
		back := filepath.Join(local, "back")
		if err := tr.Download(ctx, name, "/sandbox/x/big", back); err != nil {
			t.Fatalf("download: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(back, "big", "blob"))
		if err != nil || !bytes.Equal(got, blob) {
			t.Errorf("blob came back with %d bytes (%v), want %d", len(got), err, len(blob))
		}
		if info, err := os.Stat(filepath.Join(back, "big", "run.sh")); err != nil || info.Mode()&0o100 == 0 {
			t.Errorf("run.sh lost its executable bit: %v %v", info, err)
		}
	})

	t.Run("git ignore", func(t *testing.T) {
		repo := filepath.Join(t.TempDir(), "repo")
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		for rel, body := range map[string]string{".gitignore": "*.log\n", "keep.txt": "k\n", "drop.log": "d\n"} {
			if err := os.WriteFile(filepath.Join(repo, rel), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
		if err := tr.Upload(ctx, name, repo, "/sandbox/gi", true); err != nil {
			t.Fatalf("upload: %v", err)
		}
		if err := tr.Upload(ctx, name, repo, "/sandbox/all", false); err != nil {
			t.Fatalf("upload: %v", err)
		}
		_, out, _ := run(ExecRequest{Command: []string{"sh", "-c", "ls -A /sandbox/gi/repo | grep -v '^.git$' | tr '\\n' ' '; echo; ls -A /sandbox/all/repo | grep -v '^.git$' | tr '\\n' ' '"}})
		if want := ".gitignore keep.txt \n.gitignore drop.log keep.txt "; out != want {
			t.Errorf("git_ignore uploads\n%q\nwant\n%q", out, want)
		}
	})

	// gone waits for no process in the sandbox to have marker in its command line. The sandbox
	// has no ps, so it reads /proc; the marker is written as a bracket pattern so the check does
	// not find itself.
	gone := func(t *testing.T, marker string) {
		t.Helper()
		pattern := marker[:len(marker)-1] + "[" + marker[len(marker)-1:] + "]"
		var out string
		for i := 0; i < 10; i++ {
			_, out, _ = run(ExecRequest{Command: []string{"sh", "-c", "cat /proc/[0-9]*/cmdline 2>/dev/null | tr '\\0' ' ' | grep -c '" + pattern + "' || true"}})
			if strings.TrimSpace(out) == "0" {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Errorf("%q still runs in the sandbox", marker)
	}

	t.Run("cancel stops the command", func(t *testing.T) {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		start := time.Now()
		_, _ = tr.Exec(cctx, ExecRequest{Sandbox: name, Command: []string{"sh", "-c", "sleep 417 | cat"}}, io.Discard, io.Discard)
		if took := time.Since(start); took > 15*time.Second {
			t.Errorf("exec returned %s after its context ended", took)
		}
		gone(t, "sleep 417")
	})

	t.Run("timeout stops the command", func(t *testing.T) {
		if code, _, _ := run(ExecRequest{Command: []string{"sh", "-c", "sleep 418 | cat"}, Timeout: 1}); code != TimeoutExitCode {
			t.Errorf("exit code %d", code)
		}
		gone(t, "sleep 418")
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
