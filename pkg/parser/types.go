package parser

import (
	"fmt"
	"strings"

	"github.com/jctanner/markov/pkg/jev"
	"github.com/jctanner/markov/pkg/outputlimit"
	"gopkg.in/yaml.v3"
)

type WorkflowFile struct {
	Connections map[string]jev.Connection `yaml:"connections"`
	Decisions   map[string]jev.Decision   `yaml:"decisions"`
	Entrypoint  string                    `yaml:"entrypoint"`
	Namespace   string                    `yaml:"namespace"`
	Forks       int                       `yaml:"forks"`
	Vars        map[string]any            `yaml:"vars"`
	// Secrets names vars whose values are masked in events and logs.
	Secrets []string `yaml:"secrets"`
	// MaxOutput is the output limit of every step that sets none (and whose step type sets none).
	MaxOutput outputlimit.Size    `yaml:"max_output"`
	Rules     []Rule              `yaml:"rules"`
	StepTypes map[string]StepType `yaml:"step_types"`
	Workflows []Workflow          `yaml:"workflows"`
	OpenShell *OpenShell          `yaml:"openshell"`
	// ScriptDir is the directory used as the base for script_exec path values.
	// It is set by the parser and is not part of the workflow YAML schema.
	ScriptDir string `yaml:"-"`
}

// OpenShell configures the openshell_* steps: how they reach the OpenShell gateway.
type OpenShell struct {
	// Transport is "cli" (the openshell command, the default) or "sdk" (the Go SDK).
	Transport string `yaml:"transport"`
	// Workspace is the OpenShell workspace sandboxes live in (default "default").
	Workspace string `yaml:"workspace"`
	// Gateway is the gateway name for the CLI, or its host:port address for the SDK.
	Gateway string `yaml:"gateway"`
	// Binary is the openshell command the CLI transport runs.
	Binary string `yaml:"binary"`
	// TokenEnv names the environment variable holding a bearer token (SDK only).
	TokenEnv string `yaml:"token_env"`
	// CAFile is the CA certificate for the gateway's TLS (SDK only).
	CAFile string `yaml:"ca_file"`
	// CertFile and KeyFile are a client certificate for an mTLS gateway given by address (SDK
	// only; a gateway given by name brings its own).
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

type Rule struct {
	Name        string         `yaml:"name"`
	File        string         `yaml:"file"`
	Description string         `yaml:"description"`
	Salience    int            `yaml:"salience"`
	When        string         `yaml:"when"`
	Action      string         `yaml:"action"`
	SetFact     map[string]any `yaml:"set_fact"`
}

type StepType struct {
	Base        string `yaml:"base"`
	Description string `yaml:"description"`
	// MaxOutput is the output limit of the steps of this type that set none.
	MaxOutput outputlimit.Size `yaml:"max_output"`
	Defaults  map[string]any   `yaml:"defaults"`
	Job       map[string]any   `yaml:"job"`
	Params    map[string]any   `yaml:"params"`
}

type Workflow struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Vars        map[string]any `yaml:"vars"`
	Steps       []Step         `yaml:"steps"`
	Rescue      []Step         `yaml:"rescue"`
	Always      []Step         `yaml:"always"`
}

type Step struct {
	Name string `yaml:"name"`
	// Description is free text for readers and tools; it does not change what the step does.
	Description string         `yaml:"description"`
	Type        string         `yaml:"type"`
	Params      map[string]any `yaml:"params"`
	When        string         `yaml:"when"`
	Register    string         `yaml:"register"`
	Timeout     int            `yaml:"timeout"`
	ForEach     string         `yaml:"for_each"`
	ForEachKey  string         `yaml:"for_each_key"`
	ForEachSort string         `yaml:"for_each_sort"`
	// ForEachWhen is evaluated per item, with the item bound to `as`; items where it is false
	// are skipped. `when` is evaluated once, before the loop.
	ForEachWhen string `yaml:"for_each_when"`
	As          string `yaml:"as"`
	Concurrency int    `yaml:"concurrency"`
	Workflow    string `yaml:"workflow"`
	// WorkflowNames lists the workflows a templated Workflow may resolve to: each must exist,
	// and a run that resolves to another name fails.
	WorkflowNames []string       `yaml:"workflow_names"`
	Vars          map[string]any `yaml:"vars"`
	// Pass hands the called workflow these of the caller's vars by name, as they are (not
	// rendered). Only on a step that calls a workflow.
	Pass      []string            `yaml:"pass"`
	Artifacts map[string]Artifact `yaml:"artifacts"`
	That      []string            `yaml:"that"`
	Msg       string              `yaml:"msg"`
	Rules     []string            `yaml:"rules"`
	Facts     map[string]any      `yaml:"facts"`
	// IgnoreErrors records a failure in the step's output (failed: true, error) and carries on.
	IgnoreErrors bool `yaml:"ignore_errors"`
	// FailedWhen, when set, alone decides whether the step failed. It sees the step's output as
	// `result` (and under the register name); an executor error is in result.error.
	FailedWhen string `yaml:"failed_when"`
	// Secret marks the step's output, or parts of it, as secret: masked in events and logs.
	Secret SecretOutput `yaml:"secret"`
	// MaxOutput is how much the step may produce (0: no limit); over it, the step fails.
	MaxOutput outputlimit.Size `yaml:"max_output"`
}

// SecretOutput is a step's `secret` setting: true (every string in the output) or a list of
// dotted paths into it ("token", "body.token").
type SecretOutput struct {
	All   bool
	Paths []string
}

// Set reports whether the step marks anything secret.
func (s SecretOutput) Set() bool { return s.All || len(s.Paths) > 0 }

func (s *SecretOutput) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!bool" {
		return node.Decode(&s.All)
	}
	if node.Kind == yaml.SequenceNode {
		if err := node.Decode(&s.Paths); err != nil {
			return fmt.Errorf("secret: %w", err)
		}
		for _, p := range s.Paths {
			if p == "" || strings.HasPrefix(p, ".") || strings.HasSuffix(p, ".") || strings.Contains(p, "..") {
				return fmt.Errorf("secret: %q is not a dotted path into the step's output", p)
			}
		}
		return nil
	}
	return fmt.Errorf("line %d: secret must be true or a list of output paths", node.Line)
}

func (s SecretOutput) MarshalYAML() (any, error) {
	if len(s.Paths) > 0 {
		return s.Paths, nil
	}
	if s.All {
		return true, nil
	}
	return nil, nil
}

type Artifact struct {
	Path     string `yaml:"path"`
	Format   string `yaml:"format"`
	Source   string `yaml:"source"`
	Optional bool   `yaml:"optional"`
}
