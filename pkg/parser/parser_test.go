package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFileSingleFileStillWorks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.yaml")
	writeFile(t, path, `
entrypoint: main
vars:
  greeting: hello
workflows:
  - name: main
    steps:
      - name: say_hello
        type: shell_exec
        params:
          command: "echo {{ greeting }}"
`)

	wf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if wf.Entrypoint != "main" {
		t.Fatalf("Entrypoint = %q, want main", wf.Entrypoint)
	}
	if len(wf.Workflows) != 1 {
		t.Fatalf("len(Workflows) = %d, want 1", len(wf.Workflows))
	}
}

func TestParseFileAllowsK8sJobWaitPrimitiveAndStepType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.yaml")
	writeFile(t, path, `
entrypoint: main
step_types:
  agent_job_wait:
    base: k8s_job_wait
    params:
      namespace: ai-pipeline
workflows:
  - name: main
    steps:
      - name: wait_direct
        type: k8s_job_wait
        params:
          job_name: existing-job
      - name: wait_custom
        type: agent_job_wait
        params:
          job_name: other-job
`)

	wf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	step := wf.Workflows[0].Steps[1]
	base, params := wf.ResolveStepType(&step)
	if base != "k8s_job_wait" {
		t.Fatalf("base = %q, want k8s_job_wait", base)
	}
	if params["namespace"] != "ai-pipeline" {
		t.Fatalf("namespace = %v, want ai-pipeline", params["namespace"])
	}
	if params["job_name"] != "other-job" {
		t.Fatalf("job_name = %v, want other-job", params["job_name"])
	}
}

func TestParseFileAllowsScriptExecAndSetsScriptsDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.yaml")
	writeFile(t, path, `
entrypoint: main
step_types:
  python_script:
    base: script_exec
    params:
      interpreter: python3
workflows:
  - name: main
    steps:
      - name: direct
        type: script_exec
        params:
          interpreter: sh
          path: hello.sh
      - name: custom
        type: python_script
        params:
          path: hello.py
`)

	wf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if got, want := wf.ScriptDir, filepath.Join(dir, "scripts"); got != want {
		t.Fatalf("ScriptDir = %q, want %q", got, want)
	}
	base, params := wf.ResolveStepType(&wf.Workflows[0].Steps[1])
	if base != "script_exec" {
		t.Fatalf("base = %q, want script_exec", base)
	}
	if got, want := params["interpreter"], "python3"; got != want {
		t.Fatalf("interpreter = %v, want %q", got, want)
	}
}

func TestParseFileAllowsPromptPrimitive(t *testing.T) {
	wf, err := Parse([]byte(`
entrypoint: main
workflows:
  - name: main
    steps:
      - name: approval
        type: prompt
        params:
          message: "Approve?"
`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got := wf.Workflows[0].Steps[0].Type; got != "prompt" {
		t.Fatalf("step type = %q, want prompt", got)
	}
}

func TestParseRejectsUnknownStructuredFields(t *testing.T) {
	tests := []struct {
		name  string
		yaml  string
		field string
	}{
		{
			name:  "top level",
			field: "unsupported_top_level",
			yaml: `
entrypoint: main
unsupported_top_level: true
workflows:
  - name: main
    steps: []`,
		},
		{
			name:  "workflow",
			field: "unsupported_workflow",
			yaml: `
entrypoint: main
workflows:
  - name: main
    unsupported_workflow: true
    steps: []`,
		},
		{
			name:  "step",
			field: "on_pause",
			yaml: `
entrypoint: main
workflows:
  - name: main
    steps:
      - name: approval
        type: shell_exec
        on_pause: ignored
        params: {command: "true"}`,
		},
		{
			name:  "rule",
			field: "unsupported_rule",
			yaml: `
entrypoint: main
rules:
  - name: continue
    when: "true"
    action: continue
    unsupported_rule: true
workflows:
  - name: main
    steps: []`,
		},
		{
			name:  "step type",
			field: "unsupported_step_type",
			yaml: `
entrypoint: main
step_types:
  local:
    base: shell_exec
    unsupported_step_type: true
workflows:
  - name: main
    steps: []`,
		},
		{
			name:  "artifact",
			field: "unsupported_artifact",
			yaml: `
entrypoint: main
workflows:
  - name: main
    steps:
      - name: collect
        type: shell_exec
        params: {command: "true"}
        artifacts:
          result:
            path: result.yaml
            unsupported_artifact: true`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("Parse() error = %v, want unknown field %q", err, tt.field)
			}
		})
	}
}

func TestParseAllowsFreeFormExecutorMaps(t *testing.T) {
	_, err := Parse([]byte(`
entrypoint: main
vars:
  application_specific_setting:
    nested: value
step_types:
  custom:
    base: shell_exec
    defaults:
      future_default: true
    job:
      future_job_setting: value
    params:
      future_parameter: value
workflows:
  - name: main
    vars:
      workflow_specific: value
    steps:
      - name: run
        type: custom
        params:
          executor_specific: value
        facts:
          rule_specific: value
`))
	if err != nil {
		t.Fatalf("Parse() error = %v, want free-form maps to remain accepted", err)
	}
}

func TestParseDirMergesDirectoryWorkflow(t *testing.T) {
	dir := makeDirectoryWorkflow(t)

	wf, err := ParseFile(dir)
	if err != nil {
		t.Fatalf("ParseFile(directory) error = %v", err)
	}

	if wf.Entrypoint != "main" {
		t.Fatalf("Entrypoint = %q, want main", wf.Entrypoint)
	}
	if wf.Namespace != "markov-test" {
		t.Fatalf("Namespace = %q, want markov-test", wf.Namespace)
	}
	if got, want := wf.ScriptDir, filepath.Join(dir, "scripts"); got != want {
		t.Fatalf("ScriptDir = %q, want %q", got, want)
	}
	if wf.Forks != 2 {
		t.Fatalf("Forks = %d, want 2", wf.Forks)
	}
	if len(wf.Secrets) != 1 || wf.Secrets[0] != "ADMIN" {
		t.Fatalf("Secrets = %v, want [ADMIN]", wf.Secrets)
	}
	if wf.Vars["greeting"] != "hello from directory" {
		t.Fatalf("Vars[greeting] = %v, want hello from directory", wf.Vars["greeting"])
	}
	if _, ok := wf.StepTypes["echo_local"]; !ok {
		t.Fatalf("step type echo_local missing")
	}
	if len(wf.Rules) != 1 || wf.Rules[0].Name != "always_continue" {
		t.Fatalf("Rules = %#v, want always_continue", wf.Rules)
	}
	if wf.GetWorkflow("main") == nil || wf.GetWorkflow("child") == nil {
		t.Fatalf("expected main and child workflows, got %#v", wf.Workflows)
	}
}

func TestParseDirRequiresCategoryFiles(t *testing.T) {
	dir := makeDirectoryWorkflow(t)
	if err := os.Remove(filepath.Join(dir, "vars.yaml")); err != nil {
		t.Fatal(err)
	}

	_, err := ParseFile(dir)
	if err == nil {
		t.Fatalf("ParseFile(directory) error = nil, want missing vars.yaml error")
	}
	if !strings.Contains(err.Error(), "vars.yaml") {
		t.Fatalf("error = %q, want vars.yaml", err)
	}
}

func TestParseDirRejectsDuplicateRules(t *testing.T) {
	dir := makeDirectoryWorkflow(t)
	writeFile(t, filepath.Join(dir, "rules.yaml"), `
- name: duplicate
  when: "true"
  action: continue
- name: duplicate
  when: "false"
  action: skip
`)

	_, err := ParseFile(dir)
	if err == nil {
		t.Fatalf("ParseFile(directory) error = nil, want duplicate rule error")
	}
	if !strings.Contains(err.Error(), `duplicate rule name "duplicate"`) {
		t.Fatalf("error = %q, want duplicate rule error", err)
	}
}

func TestParseDirRejectsUnknownFieldsWithFilePath(t *testing.T) {
	dir := makeDirectoryWorkflow(t)
	metaPath := filepath.Join(dir, "meta.yaml")
	writeFile(t, metaPath, `
entrypoint: main
namespace: markov-test
forks: 2
unknown_meta_field: true
`)

	_, err := ParseFile(dir)
	if err == nil || !strings.Contains(err.Error(), "unknown_meta_field") || !strings.Contains(err.Error(), metaPath) {
		t.Fatalf("ParseFile() error = %v, want meta path and unknown field", err)
	}
}

func TestParseDirRejectsDuplicateWorkflows(t *testing.T) {
	dir := makeDirectoryWorkflow(t)
	writeFile(t, filepath.Join(dir, "workflows", "duplicate.yaml"), `
name: main
steps:
  - name: other
    type: shell_exec
    params:
      command: "echo duplicate"
`)

	_, err := ParseFile(dir)
	if err == nil {
		t.Fatalf("ParseFile(directory) error = nil, want duplicate workflow error")
	}
	if !strings.Contains(err.Error(), `duplicate workflow name "main"`) {
		t.Fatalf("error = %q, want duplicate workflow error", err)
	}
}

func TestParseDirRejectsDuplicateStepTypeKeys(t *testing.T) {
	dir := makeDirectoryWorkflow(t)
	writeFile(t, filepath.Join(dir, "step_types.yaml"), `
echo_local:
  base: shell_exec
echo_local:
  base: http_request
`)

	_, err := ParseFile(dir)
	if err == nil {
		t.Fatalf("ParseFile(directory) error = nil, want duplicate step type key error")
	}
	if !strings.Contains(err.Error(), "step_types.yaml") {
		t.Fatalf("error = %q, want step_types.yaml parse error", err)
	}
}

func TestParseDirLoadsStepTypesDirectory(t *testing.T) {
	dir := makeDirectoryWorkflow(t)
	if err := os.Remove(filepath.Join(dir, "step_types.yaml")); err != nil {
		t.Fatal(err)
	}
	stepTypesDir := filepath.Join(dir, "step_types")
	if err := os.MkdirAll(stepTypesDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(stepTypesDir, "shell.yaml"), `
echo_local:
  base: shell_exec
`)
	writeFile(t, filepath.Join(stepTypesDir, "http.yaml"), `
get_json:
  base: http_request
  defaults:
    method: GET
`)

	wf, err := ParseFile(dir)
	if err != nil {
		t.Fatalf("ParseFile(directory) error = %v", err)
	}
	if _, ok := wf.StepTypes["echo_local"]; !ok {
		t.Fatalf("step type echo_local missing")
	}
	if _, ok := wf.StepTypes["get_json"]; !ok {
		t.Fatalf("step type get_json missing")
	}
}

func TestParseDirRejectsDuplicateStepTypesAcrossDirectoryFiles(t *testing.T) {
	dir := makeDirectoryWorkflow(t)
	if err := os.Remove(filepath.Join(dir, "step_types.yaml")); err != nil {
		t.Fatal(err)
	}
	stepTypesDir := filepath.Join(dir, "step_types")
	if err := os.MkdirAll(stepTypesDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(stepTypesDir, "a.yaml"), `
echo_local:
  base: shell_exec
`)
	writeFile(t, filepath.Join(stepTypesDir, "b.yaml"), `
echo_local:
  base: http_request
`)

	_, err := ParseFile(dir)
	if err == nil {
		t.Fatalf("ParseFile(directory) error = nil, want duplicate step type error")
	}
	if !strings.Contains(err.Error(), `duplicate step type "echo_local"`) {
		t.Fatalf("error = %q, want duplicate step type error", err)
	}
	if !strings.Contains(err.Error(), "step_types") {
		t.Fatalf("error = %q, want step_types path", err)
	}
}

func TestParseDirAllowsEmptyCategoryFiles(t *testing.T) {
	dir := makeDirectoryWorkflow(t)
	writeFile(t, filepath.Join(dir, "vars.yaml"), "")
	writeFile(t, filepath.Join(dir, "rules.yaml"), "")
	writeFile(t, filepath.Join(dir, "step_types.yaml"), "")
	writeFile(t, filepath.Join(dir, "workflows", "main.yaml"), `
name: main
steps:
  - name: say_hello
    type: shell_exec
    params:
      command: "echo hello"
`)

	wf, err := ParseFile(dir)
	if err != nil {
		t.Fatalf("ParseFile(directory) error = %v", err)
	}
	if len(wf.Vars) != 0 {
		t.Fatalf("Vars = %#v, want empty map", wf.Vars)
	}
	if len(wf.Rules) != 0 {
		t.Fatalf("Rules = %#v, want empty list", wf.Rules)
	}
	if len(wf.StepTypes) != 0 {
		t.Fatalf("StepTypes = %#v, want empty map", wf.StepTypes)
	}
}

func TestParseDirLoadsRuleFilesRelativeToDirectoryRoot(t *testing.T) {
	dir := makeDirectoryWorkflow(t)
	rulesDir := filepath.Join(dir, "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "rules.yaml"), `
- file: rules/common.yaml
`)
	writeFile(t, filepath.Join(rulesDir, "common.yaml"), `
rules:
  - name: from_external_file
    when: "true"
    action: continue
`)
	writeFile(t, filepath.Join(dir, "workflows", "main.yaml"), `
name: main
steps:
  - name: check_gate
    type: gate
    rules:
      - from_external_file
`)

	wf, err := ParseFile(dir)
	if err != nil {
		t.Fatalf("ParseFile(directory) error = %v", err)
	}
	if len(wf.Rules) != 1 || wf.Rules[0].Name != "from_external_file" {
		t.Fatalf("Rules = %#v, want external rule", wf.Rules)
	}
}

func TestResolveStepTypeMergesHeaders(t *testing.T) {
	wf := &WorkflowFile{
		StepTypes: map[string]StepType{
			"github_api": {
				Base: "http_request",
				Defaults: map[string]any{
					"headers": map[string]any{
						"User-Agent": "markov",
					},
				},
				Params: map[string]any{
					"base_url": "https://github.example/api/v3",
					"basic_auth": map[string]any{
						"username": "api-user",
						"password": "api-token",
					},
					"headers": map[string]any{
						"Accept":        "application/vnd.github+json",
						"Authorization": "token default",
					},
				},
			},
		},
	}
	step := &Step{
		Type: "github_api",
		Params: map[string]any{
			"path": "/repos/example/repo",
			"headers": map[string]any{
				"Authorization": "token step",
				"X-Request-ID":  "abc123",
			},
		},
	}

	base, params := wf.ResolveStepType(step)
	if base != "http_request" {
		t.Fatalf("base = %q, want http_request", base)
	}
	headers, ok := params["headers"].(map[string]any)
	if !ok {
		t.Fatalf("headers = %#v, want map[string]any", params["headers"])
	}
	if headers["Accept"] != "application/vnd.github+json" {
		t.Fatalf("Accept = %v, want default header", headers["Accept"])
	}
	if headers["User-Agent"] != "markov" {
		t.Fatalf("User-Agent = %v, want default header", headers["User-Agent"])
	}
	if headers["Authorization"] != "token step" {
		t.Fatalf("Authorization = %v, want step override", headers["Authorization"])
	}
	if headers["X-Request-ID"] != "abc123" {
		t.Fatalf("X-Request-ID = %v, want step header", headers["X-Request-ID"])
	}
	basicAuth, ok := params["basic_auth"].(map[string]any)
	if !ok {
		t.Fatalf("basic_auth = %#v, want map[string]any", params["basic_auth"])
	}
	if basicAuth["username"] != "api-user" || basicAuth["password"] != "api-token" {
		t.Fatalf("basic_auth = %#v, want step type values", basicAuth)
	}
}

func makeDirectoryWorkflow(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "workflows"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "meta.yaml"), `
entrypoint: main
namespace: markov-test
forks: 2
secrets: [ADMIN]
`)
	writeFile(t, filepath.Join(dir, "vars.yaml"), `
greeting: hello from directory
`)
	writeFile(t, filepath.Join(dir, "rules.yaml"), `
- name: always_continue
  when: "true"
  action: continue
`)
	writeFile(t, filepath.Join(dir, "step_types.yaml"), `
echo_local:
  base: shell_exec
`)
	writeFile(t, filepath.Join(dir, "workflows", "main.yaml"), `
name: main
steps:
  - name: say_hello
    type: echo_local
    params:
      command: "echo {{ greeting }}"
  - name: run_child
    workflow: child
`)
	writeFile(t, filepath.Join(dir, "workflows", "child.yaml"), `
name: child
steps:
  - name: child_step
    type: shell_exec
    params:
      command: "echo child"
`)
	return dir
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimPrefix(content, "\n")), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestParseDirMergesVarsDirectory(t *testing.T) {
	dir := makeDirectoryWorkflow(t)
	writeFile(t, filepath.Join(dir, "vars.yaml"), "tier: smoke\n")
	varsDir := filepath.Join(dir, "vars")
	if err := os.MkdirAll(varsDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(varsDir, "tiers.yaml"), "tiers:\n  smoke:\n    repeats: 3\n")
	writeFile(t, filepath.Join(varsDir, "variants.yaml"), "variants: {haiku: {model: m}}\n")

	wf, err := ParseFile(dir)
	if err != nil {
		t.Fatalf("ParseFile(directory) error = %v", err)
	}
	for _, name := range []string{"tier", "tiers", "variants"} {
		if _, ok := wf.Vars[name]; !ok {
			t.Fatalf("var %q missing: %#v", name, wf.Vars)
		}
	}

	writeFile(t, filepath.Join(varsDir, "more.yaml"), "tier: other\n")
	if _, err := ParseFile(dir); err == nil || !strings.Contains(err.Error(), `duplicate variable "tier"`) {
		t.Fatalf("duplicate variable error = %v", err)
	}
}

func TestParseRejectsForEachWhenWithoutForEach(t *testing.T) {
	_, err := Parse([]byte(`
entrypoint: main
workflows:
  - name: main
    steps:
      - name: a
        type: shell_exec
        description: "Long-form notes about the step."
        ignore_errors: true
        for_each_when: "x"
        params:
          command: "true"
`))
	if err == nil || !strings.Contains(err.Error(), "for_each_when requires for_each") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseValidatesWorkflowNames(t *testing.T) {
	base := `
entrypoint: main
workflows:
  - name: main
    steps:
      - name: submit
        workflow: %s
        workflow_names: %s
  - name: submit-bash
    steps:
      - name: s
        type: shell_exec
        params: {command: "true"}
`
	cases := []struct {
		workflow, names, wantErr string
	}{
		{`"submit-{{ arm }}"`, `[submit-bash]`, ""},
		{`"submit-{{ arm }}"`, `[submit-bsh]`, `workflow_names lists unknown workflow "submit-bsh"`},
		{`submit-bash`, `[submit-bash]`, "workflow_names needs a templated workflow"},
	}
	for _, tc := range cases {
		_, err := Parse([]byte(fmt.Sprintf(base, tc.workflow, tc.names)))
		if tc.wantErr == "" && err != nil {
			t.Fatalf("%s %s: %v", tc.workflow, tc.names, err)
		}
		if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Fatalf("%s %s: err = %v, want %q", tc.workflow, tc.names, err, tc.wantErr)
		}
	}
}

func TestParseSecrets(t *testing.T) {
	wf, err := Parse([]byte(`
entrypoint: main
secrets: [ADMIN, BOT]
workflows:
  - name: main
    steps:
      - name: mint
        type: http_request
        secret: [token, body.token]
      - name: facts
        type: set_fact
        secret: true
        vars: {a: b}
      - name: plain
        type: shell_exec
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := wf.Secrets; len(got) != 2 || got[0] != "ADMIN" || got[1] != "BOT" {
		t.Errorf("Secrets = %v", got)
	}
	steps := wf.Workflows[0].Steps
	if got := steps[0].Secret; got.All || len(got.Paths) != 2 || got.Paths[1] != "body.token" {
		t.Errorf("mint secret = %+v", got)
	}
	if !steps[1].Secret.All || steps[2].Secret.Set() {
		t.Errorf("facts secret = %+v, plain secret = %+v", steps[1].Secret, steps[2].Secret)
	}

	for name, step := range map[string]string{
		"not true or a list": "{name: s, type: shell_exec, secret: token}",
		"an empty path":      "{name: s, type: shell_exec, secret: [\"\"]}",
		"a malformed path":   "{name: s, type: shell_exec, secret: [body..token]}",
		"on a sub-workflow":  "{name: s, workflow: main, secret: true}",
		"on an assert step":  "{name: s, type: assert, that: [\"true\"], secret: true}",
	} {
		_, err := Parse([]byte("entrypoint: main\nworkflows:\n  - name: main\n    steps:\n      - " + step + "\n"))
		if err == nil || !strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: err = %v, want a secret error", name, err)
		}
	}
}
