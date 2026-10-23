// Package openshell talks to an OpenShell gateway for the openshell_* steps. A Transport does
// the work; the CLI transport runs the openshell command.
package openshell

import (
	"context"
	"fmt"
	"io"
)

// Config says how to reach the gateway. Empty fields take OpenShell's own defaults.
type Config struct {
	// Transport is "cli" or "sdk"; empty means cli.
	Transport string
	Workspace string
	// Gateway is a gateway name (cli) or host:port (sdk).
	Gateway  string
	Binary   string
	TokenEnv string
	CAFile   string
}

// Sandbox is a sandbox as the gateway reports it.
type Sandbox struct {
	Name      string
	ID        string
	Phase     string
	Workspace string
}

// CreateRequest describes a sandbox to create. Create returns once the sandbox is ready.
type CreateRequest struct {
	Name      string
	Image     string
	CPU       string
	Memory    string
	GPU       string
	Env       map[string]string
	Providers []string
	Policy    string
	Labels    map[string]string
}

// ExecRequest is a command to run in a sandbox.
type ExecRequest struct {
	Sandbox string
	Command []string
	Workdir string
	Env     map[string]string
	// Timeout is in seconds; 0 means none.
	Timeout int
	Stdin   io.Reader
}

// Transport performs sandbox operations against a gateway.
//
// Upload and Download copy src into the directory destDir, keeping src's base name, whether src
// is a file or a directory: uploading ./site to /sandbox gives /sandbox/site.
type Transport interface {
	Create(ctx context.Context, req CreateRequest) (*Sandbox, error)
	Upload(ctx context.Context, sandbox, src, destDir string, gitIgnore bool) error
	Download(ctx context.Context, sandbox, src, destDir string) error
	// Exec runs the command, writing its output as it arrives, and returns its exit code. An
	// error means the command could not be run at all.
	Exec(ctx context.Context, req ExecRequest, stdout, stderr io.Writer) (int, error)
	// Delete deletes sandboxes; deleting one that does not exist succeeds.
	Delete(ctx context.Context, names []string) error
}

// TimeoutExitCode is the exit code of a command stopped by ExecRequest.Timeout.
const TimeoutExitCode = 124

// New returns the transport cfg asks for.
func New(cfg Config) (Transport, error) {
	switch cfg.Transport {
	case "", "cli":
		return NewCLI(cfg), nil
	case "sdk":
		return nil, fmt.Errorf("the sdk transport is not implemented yet; use transport: cli")
	default:
		return nil, fmt.Errorf("unknown transport %q (use cli or sdk)", cfg.Transport)
	}
}
