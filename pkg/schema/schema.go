// Package schema describes the workflow format for programs: the fields the parser accepts and
// the step types with their parameters. `markov schema` prints it as JSON so editors and agents
// do not keep hand-written copies. Fields are reflected from the parser's structs; the step-type
// parameter table is written here and checked against the step-type reference docs in a test.
package schema

import (
	"reflect"
	"strings"

	"github.com/jctanner/markov/pkg/parser"
)

// Version changes when the shape of this output changes incompatibly.
const Version = 1

// Field is one YAML key of a parser struct.
type Field struct {
	Name string `json:"name"`
	// Type is a coarse description: string, int, bool, map, list, or object.
	Type string `json:"type"`
}

// Param is one key of a step's `params`.
type Param struct {
	Name     string `json:"name"`
	Required bool   `json:"required,omitempty"`
	Doc      string `json:"doc,omitempty"`
}

// StepType describes a built-in step type.
type StepType struct {
	Name string `json:"name"`
	// Inputs says where the step's own inputs go: "params" (most types) or "step" when they are
	// step-level fields listed in StepFields (set_fact uses `vars`, gate uses `rules` and `facts`,
	// assert uses `that` and `msg`, load_artifact uses `artifacts`).
	Inputs     string   `json:"inputs"`
	StepFields []string `json:"step_fields,omitempty"`
	Params     []Param  `json:"params,omitempty"`
	Summary    string   `json:"summary"`
}

// Schema is the whole description.
type Schema struct {
	Version   int        `json:"version"`
	File      []Field    `json:"file"`
	Workflow  []Field    `json:"workflow"`
	Step      []Field    `json:"step"`
	StepTypes []StepType `json:"step_types"`
}

// fieldsOf lists the yaml-tagged fields of a struct type, skipping `yaml:"-"`.
func fieldsOf(v any) []Field {
	t := reflect.TypeOf(v)
	var out []Field
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		out = append(out, Field{Name: name, Type: kindName(f.Type)})
	}
	return out
}

func kindName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int64, reflect.Int32:
		return "int"
	case reflect.Bool:
		return "bool"
	case reflect.Map:
		return "map"
	case reflect.Slice, reflect.Array:
		return "list"
	default:
		return "object"
	}
}

func p(name, doc string) Param            { return Param{Name: name, Doc: doc} }
func req(name, doc string) Param          { return Param{Name: name, Doc: doc, Required: true} }
func params(types ...StepType) []StepType { return types }

// stepTypes is the parameter table. Keep it in step with docs/reference/step-types.md; the
// schema test fails when a parameter here is missing from the docs, or a built-in type is missing here.
func stepTypes() []StepType {
	return params(
		StepType{Name: "shell_exec", Inputs: "params", Summary: "Run a shell command.", Params: []Param{req("command", "Shell command to execute")}},
		StepType{Name: "script_exec", Inputs: "params", Summary: "Run an inline script or a script from scripts/.", Params: []Param{
			req("interpreter", "Interpreter executable, such as python3 or bash"), p("content", "Inline script body (or path)"), p("path", "Script path relative to the workflow scripts/ directory (or content)"),
			p("args", "Arguments passed to the script"), p("env", "Environment variables"), p("stdin", "Standard input; a map or list is sent as JSON"),
		}},
		StepType{Name: "write_file", Inputs: "params", Summary: "Write content to a file on the runner, creating parent directories.", Params: []Param{
			req("path", "File to write"), req("content", "Text, or a map or list written as indented JSON"),
			p("mode", "Octal file mode as a string, default \"0644\""), p("dir_mode", "Octal mode for created directories, default \"0755\""),
			p("append", "Append instead of replacing"),
		}},
		StepType{Name: "claude", Inputs: "params", Summary: "Run Claude Code with a prompt or skill, streaming its output, with limits enforced by Markov.", Params: []Param{
			p("prompt", "The prompt text (or skill)"), p("skill", "Skill or command name (or prompt)"), p("args", "Arguments appended after skill"), p("chdir", "Directory to run in"),
			p("model", ""), p("effort", ""), p("fallback_model", ""), p("permission_mode", "acceptEdits, auto, bypassPermissions, manual, dontAsk, or plan"),
			p("allowed_tools", ""), p("disallowed_tools", ""), p("append_system_prompt", ""), p("add_dirs", "Extra directories the run may access"), p("mcp_config", ""),
			p("settings", ""), p("resume", "Resume a session"), p("session_id", ""), p("bare", "Skip hooks, plugins and CLAUDE.md discovery (needs an API key)"),
			p("limits", "max_turns, max_tokens, max_duration, max_budget_usd"), p("events_limit", "Maximum events kept in the output"), p("env", ""), p("extra_args", ""), p("binary", ""),
		}},
		StepType{Name: "ansible_playbook", Inputs: "params", Summary: "Run ansible-playbook.", Params: []Param{
			req("playbook", "Playbook path(s)"), p("chdir", ""), p("inventory", "Path, inline host list, list, or inline inventory map"), p("limit", ""), p("tags", ""), p("skip_tags", ""),
			p("start_at_task", ""), p("extra_vars", "Map (written to a private temp file) or string"), p("extra_vars_files", ""), p("check", ""), p("diff", ""), p("become", ""),
			p("become_user", ""), p("become_method", ""), p("remote_user", ""), p("connection", ""), p("private_key", ""), p("vault_password_file", ""), p("vault_id", ""),
			p("forks", ""), p("timeout", "SSH timeout"), p("verbosity", "0 to 6"), p("env", ""), p("extra_args", ""), p("binary", ""),
		}},
		StepType{Name: "ansible", Inputs: "params", Summary: "Run an ad-hoc ansible module.", Params: []Param{
			req("pattern", "Host pattern"), p("module", "Module name (default command)"), p("module_args", "String or map"), p("chdir", ""), p("inventory", ""), p("limit", ""),
			p("become", ""), p("forks", ""), p("poll", ""), p("background", ""), p("env", ""), p("extra_args", ""),
		}},
		StepType{Name: "http_request", Inputs: "params", Summary: "Make an HTTP request.", Params: []Param{
			p("method", "Default GET"), p("url", "Full URL (or base_url)"), p("base_url", ""), p("path", ""), p("body", "JSON-encoded automatically"), p("headers", ""),
			p("basic_auth", "username and password"), p("ignore_status", "true or a list of status codes treated as success"), p("tls_insecure", ""), p("tls_ca_cert", ""),
		}},
		StepType{Name: "k8s_job", Inputs: "params", Summary: "Run a Kubernetes Job and wait for it.", Params: []Param{
			req("image", "Container image"), p("command", ""), p("args", ""), p("env", ""), p("secrets", ""), p("volumes", ""), p("init_containers", ""), p("resources", ""), p("affinity", ""),
			p("service_account", ""), p("backoff_limit", ""), p("ttl_seconds", ""), p("image_pull_policy", ""), p("namespace", ""), p("name_prefix", ""),
		}},
		StepType{Name: "k8s_job_wait", Inputs: "params", Summary: "Wait for an existing Kubernetes Job.", Params: []Param{
			req("job_name", "Name of the Job to watch"), p("namespace", ""), p("timeout", "Seconds to wait (0 relies on the step timeout)"), p("tail_logs", ""), p("log_bytes", ""),
		}},
		StepType{Name: "prompt", Inputs: "params", Summary: "Ask for input.", Params: []Param{
			req("message", "Text shown before the prompt"), p("choices", ""), p("default", ""), p("case_insensitive", ""),
		}},
		StepType{Name: "gate", Inputs: "step", StepFields: []string{"rules", "facts"}, Summary: "Evaluate named rules against the context and decide: continue, skip, or pause."},
		StepType{Name: "set_fact", Inputs: "step", StepFields: []string{"vars"}, Summary: "Set variables for the steps after it; values may be templates."},
		StepType{Name: "load_artifact", Inputs: "step", StepFields: []string{"artifacts"}, Summary: "Load files (YAML, markdown, tables) into the context."},
		StepType{Name: "assert", Inputs: "step", StepFields: []string{"that", "msg"}, Summary: "Fail unless every condition holds."},
		StepType{Name: "reload_workflow", Inputs: "params", Summary: "Replace the running workflow definition; sub-workflows called afterwards use the new one (needs --allow-reload).", Params: []Param{
			p("source", "Workflow file or directory to load (default: the run's own source)"), p("require", "Workflow names the new definition must contain"),
		}},
		StepType{Name: "codify", Inputs: "params", Summary: "One iteration of markov codify on a workflow of the project: move one job out of its agent steps and promote the result when its outputs still match.", Params: []Param{
			req("workflow", "The workflow to codify"), p("model", "Retro agent and judge model (default claude-haiku-5-5)"), p("keep", "Steps that must stay agent steps"),
			p("project", "Project directory to codify (default the run's source)"), p("codify_dir", "History directory (default <project>.codify)"), p("prompt_file", "Replacement retro prompt"),
		}},
		StepType{Name: "jev", Inputs: "params", Summary: "Ask a Jev decision service.", Params: []Param{}},
		StepType{Name: "llm_invoke", Inputs: "params", Summary: "Invoke a language model.", Params: []Param{}},
	)
}

// Build assembles the schema.
func Build() Schema {
	return Schema{
		Version:   Version,
		File:      fieldsOf(parser.WorkflowFile{}),
		Workflow:  fieldsOf(parser.Workflow{}),
		Step:      fieldsOf(parser.Step{}),
		StepTypes: stepTypes(),
	}
}
