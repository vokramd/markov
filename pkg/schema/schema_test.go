package schema

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/vokramd/markov/pkg/parser"
)

func TestEveryBuiltInStepTypeIsDescribedExactlyOnce(t *testing.T) {
	want := parser.PrimitiveNames()
	var got []string
	seen := map[string]bool{}
	for _, st := range Build().StepTypes {
		if seen[st.Name] {
			t.Errorf("step type %q listed twice", st.Name)
		}
		seen[st.Name] = true
		got = append(got, st.Name)
		if st.Summary == "" {
			t.Errorf("%s has no summary", st.Name)
		}
		if st.Inputs != "params" && st.Inputs != "step" {
			t.Errorf("%s: inputs = %q", st.Name, st.Inputs)
		}
		if (st.Inputs == "step") != (len(st.StepFields) > 0) {
			t.Errorf("%s: step_fields must be set exactly when inputs is \"step\"", st.Name)
		}
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("schema step types = %v, parser primitives = %v", got, want)
	}
}

func TestFieldsAreReflectedFromTheParserStructs(t *testing.T) {
	s := Build()
	has := func(fs []Field, name, typ string) bool {
		for _, f := range fs {
			if f.Name == name && f.Type == typ {
				return true
			}
		}
		return false
	}
	for _, c := range []struct {
		fs        []Field
		name, typ string
	}{
		{s.File, "entrypoint", "string"}, {s.File, "forks", "int"}, {s.File, "workflows", "list"}, {s.File, "step_types", "map"},
		{s.Workflow, "steps", "list"}, {s.Workflow, "rescue", "list"}, {s.Workflow, "always", "list"},
		{s.Step, "type", "string"}, {s.Step, "params", "map"}, {s.Step, "vars", "map"}, {s.Step, "that", "list"}, {s.Step, "for_each", "string"}, {s.Step, "concurrency", "int"}, {s.Step, "workflow", "string"},
	} {
		if !has(c.fs, c.name, c.typ) {
			t.Errorf("missing field %s (%s)", c.name, c.typ)
		}
	}
	for _, f := range s.File {
		if f.Name == "" || f.Name == "-" {
			t.Errorf("a field without a name: %+v", f)
		}
	}
	// A field that is not part of the YAML schema must not appear (ScriptDir is yaml:"-").
	for _, f := range s.File {
		if strings.EqualFold(f.Name, "scriptdir") {
			t.Error("ScriptDir is not part of the schema")
		}
	}
}

// The parameter table and the reference docs must agree: every parameter listed here appears in
// its type's section of docs/reference/step-types.md, and step-level fields exist on the parser's Step.
func TestParametersAreDocumentedAndStepFieldsExist(t *testing.T) {
	data, err := os.ReadFile("../../docs/reference/step-types.md")
	if err != nil {
		t.Skipf("docs not available: %v", err)
	}
	sections := map[string]string{}
	var cur string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "## ") {
			cur = strings.TrimSpace(strings.TrimPrefix(line, "## "))
		}
		sections[cur] += line + "\n"
	}
	stepFields := map[string]bool{}
	for _, f := range Build().Step {
		stepFields[f.Name] = true
	}
	tick := regexp.MustCompile("`([a-z_]+)`")
	for _, st := range Build().StepTypes {
		for _, f := range st.StepFields {
			if !stepFields[f] {
				t.Errorf("%s: step field %q is not a field of the parser's Step", st.Name, f)
			}
		}
		doc, ok := sections[st.Name]
		if !ok {
			t.Errorf("%s: no \"## %s\" section in docs/reference/step-types.md", st.Name, st.Name)
			continue
		}
		if st.Name != "llm_invoke" { // reserved: no executor, so no behavior to document
			for _, h := range []string{"\n### Failure Conditions", "\n### Example"} {
				if !strings.Contains(doc, h) {
					t.Errorf("%s: its section has no %q heading", st.Name, strings.TrimSpace(h))
				}
			}
		}
		documented := map[string]bool{}
		for _, m := range tick.FindAllStringSubmatch(doc, -1) {
			documented[m[1]] = true
		}
		for _, pr := range st.Params {
			if !documented[pr.Name] {
				t.Errorf("%s: parameter %q is not in the reference docs", st.Name, pr.Name)
			}
		}
		// Required parameters really are marked required in the docs' tables.
		for _, pr := range st.Params {
			if pr.Required && !strings.Contains(doc, "`"+pr.Name+"`") {
				t.Errorf("%s: required parameter %q is not documented", st.Name, pr.Name)
			}
		}
	}
}

// executorFiles maps each source file that reads step params to the step types it implements.
// A new executor file must be added here, or the code-vs-schema test fails.
var executorFiles = map[string][]string{
	"../executor/shell_exec.go":   {"shell_exec"},
	"../executor/script_exec.go":  {"script_exec"},
	"../executor/write_file.go":   {"write_file"},
	"../executor/claude.go":       {"claude"},
	"../executor/ansible.go":      {"ansible", "ansible_playbook"},
	"../executor/http_request.go": {"http_request"},
	"../executor/k8s_job.go":      {"k8s_job", "k8s_job_wait"},
	"../executor/openshell.go":    {"openshell_build", "openshell_create", "openshell_upload", "openshell_exec", "openshell_download", "openshell_delete", "openshell_provider"},
	"../executor/prompt.go":       {"prompt"},
	"../executor/jev.go":          {"jev"},
	"../engine/codify_step.go":    {"codify"},
	"../engine/reload.go":         {"reload_workflow"},
	"../engine/engine.go":         {"k8s_job"},
}

// Every parameter an executor reads (params["x"], or a helper called as f(params, "x")) is in the
// schema for one of the types that file implements. Names starting with _ are set by the engine.
// This catches the schema falling behind the code; the docs test above catches the docs falling
// behind the schema.
func TestParametersReadByExecutorsAreInTheSchema(t *testing.T) {
	read := regexp.MustCompile(`(?:params\["([a-z_]+)"\]|[A-Za-z]+\(params, *"([a-z_]+)")`)
	byType := map[string]map[string]bool{}
	for _, st := range Build().StepTypes {
		byType[st.Name] = map[string]bool{}
		for _, pr := range st.Params {
			byType[st.Name][pr.Name] = true
		}
	}
	for file, types := range executorFiles {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		for _, m := range read.FindAllStringSubmatch(string(src), -1) {
			name := m[1] + m[2]
			if strings.HasPrefix(name, "_") {
				continue
			}
			found := false
			for _, ty := range types {
				found = found || byType[ty][name]
			}
			if !found {
				t.Errorf("%s reads param %q, which the schema does not list for %v", file, name, types)
			}
		}
	}
	// Every executor source file is accounted for.
	for _, dir := range []string{"../executor", "../engine"} {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			path := dir + "/" + e.Name()
			if strings.HasSuffix(path, "_test.go") || !strings.HasSuffix(path, ".go") {
				continue
			}
			if _, ok := executorFiles[path]; ok {
				continue
			}
			src, _ := os.ReadFile(path)
			if read.Match(src) {
				t.Errorf("%s reads step params but is not in executorFiles", path)
			}
		}
	}
}

// The overview pages that list step types name every built-in type.
func TestOverviewPagesListEveryStepType(t *testing.T) {
	for _, page := range []string{"../../docs/concepts.md", "../../docs/README.md"} {
		data, err := os.ReadFile(page)
		if err != nil {
			t.Skipf("docs not available: %v", err)
		}
		for _, name := range parser.PrimitiveNames() {
			if !strings.Contains(string(data), "`"+name+"`") {
				t.Errorf("%s does not mention step type %s", page, name)
			}
		}
	}
}
