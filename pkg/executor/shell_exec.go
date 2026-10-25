package executor

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/jctanner/markov/pkg/outputlimit"
)

type ShellExec struct{}

func NewShellExec() *ShellExec {
	return &ShellExec{}
}

func (e *ShellExec) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	command, ok := params["command"].(string)
	if !ok || command == "" {
		return nil, fmt.Errorf("shell_exec: command is required")
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", command)

	stdout, stderr, err := runCapped(ctx, cmd)
	if _, over := outputlimit.As(err); over {
		return nil, limitErr("shell_exec", err)
	}
	output := map[string]any{
		"stdout":    stdout,
		"stderr":    stderr,
		"exit_code": cmd.ProcessState.ExitCode(),
	}

	if err != nil {
		return &Result{Output: output}, fmt.Errorf("shell_exec: %w\nstderr: %s", err, stderr)
	}

	return &Result{Output: output}, nil
}
