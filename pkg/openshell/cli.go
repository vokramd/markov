package openshell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// CLI is the transport that runs the openshell command. Gateway selection and authentication are
// the command's own (openshell gateway select, OPENSHELL_* variables) unless Config names them.
type CLI struct {
	cfg Config
}

// NewCLI returns a CLI transport.
func NewCLI(cfg Config) *CLI { return &CLI{cfg: cfg} }

func (c *CLI) binary() string {
	if c.cfg.Binary != "" {
		return c.cfg.Binary
	}
	return "openshell"
}

// command builds an openshell invocation with the configured gateway and workspace.
func (c *CLI) command(ctx context.Context, args ...string) *exec.Cmd {
	var global []string
	if c.cfg.Gateway != "" {
		global = append(global, "--gateway", c.cfg.Gateway)
	}
	if c.cfg.Workspace != "" {
		global = append(global, "--workspace", c.cfg.Workspace)
	}
	cmd := exec.CommandContext(ctx, c.binary(), append(global, args...)...)
	cmd.Env = append(os.Environ(), "OPENSHELL_COLOR=never", "NO_COLOR=1")
	return cmd
}

// run runs an invocation that does not stream, returning its stdout.
func (c *CLI) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := c.command(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), cliError(args, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func cliError(args []string, err error, stderr string) error {
	op := strings.Join(args[:min(2, len(args))], " ")
	if msg := plainError(stderr); msg != "" {
		return fmt.Errorf("openshell %s: %s", op, msg)
	}
	return fmt.Errorf("openshell %s: %w", op, err)
}

// plainError flattens the CLI's boxed, wrapped error report ("Error:   × code: …\n  │ …")
// into one line.
func plainError(stderr string) string {
	var parts []string
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "Error:")
		line = strings.TrimLeft(line, " ×│╰─▶├┬┴┼╭╮╯")
		if line = strings.TrimSpace(line); line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, " ")
}

func (c *CLI) Create(ctx context.Context, req CreateRequest) (*Sandbox, error) {
	if req.Name == "" {
		return nil, errors.New("a sandbox name is required")
	}
	args := []string{"sandbox", "create", "--name", req.Name, "--detach", "--no-tty", "-o", "json"}
	if req.Image != "" {
		args = append(args, "--from", req.Image)
	}
	if req.CPU != "" {
		args = append(args, "--cpu", req.CPU)
	}
	if req.Memory != "" {
		args = append(args, "--memory", req.Memory)
	}
	switch req.GPU {
	case "":
	case "any":
		args = append(args, "--gpu")
	default:
		args = append(args, "--gpu", req.GPU)
	}
	for _, kv := range pairs(req.Env) {
		args = append(args, "--env", kv)
	}
	for _, p := range req.Providers {
		args = append(args, "--provider", p)
	}
	if req.Policy != "" {
		args = append(args, "--policy", req.Policy)
	}
	for _, kv := range pairs(req.Labels) {
		args = append(args, "--label", kv)
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var sb struct {
		Name      string `json:"name"`
		ID        string `json:"id"`
		Phase     string `json:"phase"`
		Workspace string `json:"workspace"`
	}
	if err := json.Unmarshal(out, &sb); err != nil {
		return nil, fmt.Errorf("openshell sandbox create: reading its JSON output: %w", err)
	}
	return &Sandbox{Name: sb.Name, ID: sb.ID, Phase: sb.Phase, Workspace: sb.Workspace}, nil
}

func (c *CLI) Upload(ctx context.Context, sandbox, src, destDir string, gitIgnore bool) error {
	if _, err := os.Stat(src); err != nil {
		return err
	}
	// With a trailing slash the CLI copies a file or a directory into destDir under its own name.
	args := []string{"sandbox", "upload", sandbox, src, strings.TrimRight(destDir, "/") + "/"}
	if !gitIgnore {
		args = append(args, "--no-git-ignore")
	}
	_, err := c.run(ctx, args...)
	return err
}

func (c *CLI) Download(ctx context.Context, sandbox, src, destDir string) error {
	// The CLI writes a file to the path it is given and a directory's contents into it, so
	// destDir/<base> is the result either way.
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	_, err := c.run(ctx, "sandbox", "download", sandbox, src, filepath.Join(destDir, filepath.Base(src)))
	return err
}

func (c *CLI) Exec(ctx context.Context, req ExecRequest, stdout, stderr io.Writer) (int, error) {
	args := []string{"sandbox", "exec", "--name", req.Sandbox, "--no-tty"}
	if req.Workdir != "" {
		args = append(args, "--workdir", req.Workdir)
	}
	if req.Timeout > 0 {
		args = append(args, "--timeout", fmt.Sprint(req.Timeout))
	}
	for _, kv := range pairs(req.Env) {
		args = append(args, "--env", kv)
	}
	args = append(append(args, "--"), req.Command...)
	cmd := c.command(ctx, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = req.Stdin
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && ctx.Err() == nil {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return -1, fmt.Errorf("openshell sandbox exec: %w", err)
	}
	return 0, nil
}

func (c *CLI) Delete(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}
	_, err := c.run(ctx, append([]string{"sandbox", "delete"}, names...)...)
	return err
}

// pairs renders a map as sorted KEY=VALUE strings.
func pairs(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
