package parser

import "github.com/jctanner/markov/pkg/jev"

type WorkflowFile struct {
	Connections map[string]jev.Connection `yaml:"connections"`
	Decisions   map[string]jev.Decision   `yaml:"decisions"`
	Entrypoint  string                    `yaml:"entrypoint"`
	Namespace   string                    `yaml:"namespace"`
	Forks       int                       `yaml:"forks"`
	Vars        map[string]any            `yaml:"vars"`
	Rules       []Rule                    `yaml:"rules"`
	StepTypes   map[string]StepType       `yaml:"step_types"`
	Workflows   []Workflow                `yaml:"workflows"`
	OpenShell   *OpenShell                `yaml:"openshell"`
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
	Base        string         `yaml:"base"`
	Description string         `yaml:"description"`
	Defaults    map[string]any `yaml:"defaults"`
	Job         map[string]any `yaml:"job"`
	Params      map[string]any `yaml:"params"`
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
	WorkflowNames []string            `yaml:"workflow_names"`
	Vars          map[string]any      `yaml:"vars"`
	Artifacts     map[string]Artifact `yaml:"artifacts"`
	That          []string            `yaml:"that"`
	Msg           string              `yaml:"msg"`
	Rules         []string            `yaml:"rules"`
	Facts         map[string]any      `yaml:"facts"`
	// IgnoreErrors records a failure in the step's output (failed: true, error) and carries on.
	IgnoreErrors bool `yaml:"ignore_errors"`
	// FailedWhen, when set, alone decides whether the step failed. It sees the step's output as
	// `result` (and under the register name); an executor error is in result.error.
	FailedWhen string `yaml:"failed_when"`
}

type Artifact struct {
	Path     string `yaml:"path"`
	Format   string `yaml:"format"`
	Source   string `yaml:"source"`
	Optional bool   `yaml:"optional"`
}
