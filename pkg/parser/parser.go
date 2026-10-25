package parser

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jctanner/markov/pkg/outputlimit"
	"gopkg.in/yaml.v3"
)

func ParseFile(path string) (*WorkflowFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat workflow path: %w", err)
	}
	if info.IsDir() {
		return ParseDir(path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading workflow file: %w", err)
	}
	return parse(data, filepath.Join(filepath.Dir(path), "scripts"))
}

func ParseDir(path string) (*WorkflowFile, error) {
	var wf WorkflowFile
	wf.ScriptDir = filepath.Join(path, "scripts")

	metaPath := filepath.Join(path, "meta.yaml")
	var meta struct {
		Entrypoint string           `yaml:"entrypoint"`
		Namespace  string           `yaml:"namespace"`
		Forks      int              `yaml:"forks"`
		OpenShell  *OpenShell       `yaml:"openshell"`
		Secrets    []string         `yaml:"secrets"`
		MaxOutput  outputlimit.Size `yaml:"max_output"`
	}
	if err := readYAML(metaPath, &meta); err != nil {
		return nil, err
	}
	wf.Entrypoint = meta.Entrypoint
	wf.Namespace = meta.Namespace
	wf.Forks = meta.Forks
	wf.OpenShell = meta.OpenShell
	wf.Secrets = meta.Secrets
	wf.MaxOutput = meta.MaxOutput

	vars, err := readVars(path)
	if err != nil {
		return nil, err
	}
	wf.Vars = vars

	var rules []Rule
	if err := readYAML(filepath.Join(path, "rules.yaml"), &rules); err != nil {
		return nil, err
	}
	wf.Rules = rules

	stepTypes, err := readStepTypes(path)
	if err != nil {
		return nil, err
	}
	wf.StepTypes = stepTypes
	if err := readJevDefinitions(path, &wf); err != nil {
		return nil, err
	}

	workflowDir := filepath.Join(path, "workflows")
	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		return nil, fmt.Errorf("reading workflows directory %q: %w", workflowDir, err)
	}
	var workflowFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) == ".yaml" {
			workflowFiles = append(workflowFiles, filepath.Join(workflowDir, entry.Name()))
		}
	}
	sort.Strings(workflowFiles)
	if len(workflowFiles) == 0 {
		return nil, fmt.Errorf("directory workflow %q: no workflow files found in workflows/*.yaml", path)
	}

	for _, workflowPath := range workflowFiles {
		var workflow Workflow
		if err := readYAML(workflowPath, &workflow); err != nil {
			return nil, err
		}
		wf.Workflows = append(wf.Workflows, workflow)
	}

	if err := loadRuleFiles(&wf, path); err != nil {
		return nil, err
	}
	if err := validate(&wf); err != nil {
		return nil, err
	}
	return &wf, nil
}

func Parse(data []byte) (*WorkflowFile, error) {
	return parse(data, "scripts")
}

func parse(data []byte, scriptDir string) (*WorkflowFile, error) {
	var wf WorkflowFile
	if err := decodeStrict(data, &wf); err != nil {
		return nil, fmt.Errorf("parsing workflow YAML: %w", err)
	}
	wf.ScriptDir = scriptDir
	if err := loadRuleFiles(&wf, "."); err != nil {
		return nil, err
	}
	if err := validate(&wf); err != nil {
		return nil, err
	}
	return &wf, nil
}

// IsTemplated reports whether a value contains template syntax.
func IsTemplated(s string) bool {
	return strings.Contains(s, "{{") || strings.Contains(s, "{%")
}

func readYAML(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %q: %w", path, err)
	}
	if len(data) == 0 {
		return nil
	}
	if err := decodeStrict(data, out); err != nil {
		return fmt.Errorf("parsing %q: %w", path, err)
	}
	return nil
}

// decodeStrict rejects unknown fields on typed schema objects. Free-form maps
// such as params, vars, facts, and artifact data remain intentionally open.
func decodeStrict(data []byte, out any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	return decoder.Decode(out)
}

// readVars reads vars.yaml and then every vars/*.yaml file in name order. A variable may be
// defined only once across them.
func readVars(path string) (map[string]any, error) {
	vars := map[string]any{}
	if err := readYAML(filepath.Join(path, "vars.yaml"), &vars); err != nil {
		return nil, err
	}
	if vars == nil {
		vars = map[string]any{}
	}
	dirPath := filepath.Join(path, "vars")
	entries, err := os.ReadDir(dirPath)
	if os.IsNotExist(err) {
		return vars, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading vars directory %q: %w", dirPath, err)
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".yaml" {
			files = append(files, filepath.Join(dirPath, entry.Name()))
		}
	}
	sort.Strings(files)
	for _, varsPath := range files {
		var current map[string]any
		if err := readYAML(varsPath, &current); err != nil {
			return nil, err
		}
		for name, value := range current {
			if _, ok := vars[name]; ok {
				return nil, fmt.Errorf("duplicate variable %q in %q", name, varsPath)
			}
			vars[name] = value
		}
	}
	return vars, nil
}

func readStepTypes(path string) (map[string]StepType, error) {
	stepTypes := map[string]StepType{}
	filePath := filepath.Join(path, "step_types.yaml")
	dirPath := filepath.Join(path, "step_types")

	fileInfo, fileErr := os.Stat(filePath)
	dirInfo, dirErr := os.Stat(dirPath)

	if fileErr != nil && !os.IsNotExist(fileErr) {
		return nil, fmt.Errorf("reading %q: %w", filePath, fileErr)
	}
	if dirErr != nil && !os.IsNotExist(dirErr) {
		return nil, fmt.Errorf("reading %q: %w", dirPath, dirErr)
	}
	if os.IsNotExist(fileErr) && os.IsNotExist(dirErr) {
		return nil, fmt.Errorf("reading %q or %q: step types category not found", filePath, dirPath)
	}

	if fileErr == nil {
		if fileInfo.IsDir() {
			return nil, fmt.Errorf("reading %q: expected file, got directory", filePath)
		}
		if err := mergeStepTypesFile(filePath, stepTypes); err != nil {
			return nil, err
		}
	}

	if dirErr == nil {
		if !dirInfo.IsDir() {
			return nil, fmt.Errorf("reading %q: expected directory, got file", dirPath)
		}
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			return nil, fmt.Errorf("reading step_types directory %q: %w", dirPath, err)
		}
		var stepTypeFiles []string
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if filepath.Ext(entry.Name()) == ".yaml" {
				stepTypeFiles = append(stepTypeFiles, filepath.Join(dirPath, entry.Name()))
			}
		}
		sort.Strings(stepTypeFiles)
		for _, stepTypePath := range stepTypeFiles {
			if err := mergeStepTypesFile(stepTypePath, stepTypes); err != nil {
				return nil, err
			}
		}
	}

	return stepTypes, nil
}

func mergeStepTypesFile(path string, stepTypes map[string]StepType) error {
	var current map[string]StepType
	if err := readYAML(path, &current); err != nil {
		return err
	}
	for name, stepType := range current {
		if _, ok := stepTypes[name]; ok {
			return fmt.Errorf("duplicate step type %q in %q", name, path)
		}
		stepTypes[name] = stepType
	}
	return nil
}

func validate(wf *WorkflowFile) error {
	if err := validateJev(wf); err != nil {
		return err
	}
	if wf.Forks <= 0 {
		wf.Forks = 5
	}
	if wf.OpenShell != nil {
		switch wf.OpenShell.Transport {
		case "", "cli", "sdk":
		default:
			return fmt.Errorf("openshell: transport must be cli or sdk, not %q", wf.OpenShell.Transport)
		}
	}
	if wf.Vars == nil {
		wf.Vars = map[string]any{}
	}
	if wf.StepTypes == nil {
		wf.StepTypes = map[string]StepType{}
	}
	if len(wf.Workflows) == 0 {
		return fmt.Errorf("no workflows defined")
	}
	if wf.Entrypoint == "" {
		return fmt.Errorf("entrypoint is required")
	}
	if !hasWorkflow(wf, wf.Entrypoint) {
		return fmt.Errorf("entrypoint %q not found in workflows", wf.Entrypoint)
	}

	ruleNames := make(map[string]bool)
	for _, r := range wf.Rules {
		if r.Name == "" {
			return fmt.Errorf("rule missing name")
		}
		if ruleNames[r.Name] {
			return fmt.Errorf("duplicate rule name %q", r.Name)
		}
		ruleNames[r.Name] = true
	}

	names := make(map[string]bool)
	for _, w := range wf.Workflows {
		if w.Name == "" {
			return fmt.Errorf("workflow missing name")
		}
		if names[w.Name] {
			return fmt.Errorf("duplicate workflow name %q", w.Name)
		}
		names[w.Name] = true
		if err := validateWorkflowSteps(wf, &w); err != nil {
			return err
		}
	}

	return nil
}

func validateWorkflowSteps(wf *WorkflowFile, workflow *Workflow) error {
	stepNames := make(map[string]bool)
	for _, section := range []struct {
		name  string
		steps []Step
	}{
		{name: "steps", steps: workflow.Steps},
		{name: "rescue", steps: workflow.Rescue},
		{name: "always", steps: workflow.Always},
	} {
		if err := validateSteps(wf, workflow.Name, section.name, section.steps, stepNames); err != nil {
			return err
		}
	}
	return nil
}

func validateSteps(wf *WorkflowFile, workflowName, section string, steps []Step, stepNames map[string]bool) error {
	for _, s := range steps {
		if strings.HasPrefix(s.Name, "__jev__/") {
			return fmt.Errorf("step name prefix __jev__/ is reserved")
		}
		if s.Name == "" {
			return fmt.Errorf("workflow %q, %s: step missing name", workflowName, section)
		}
		if stepNames[s.Name] {
			return fmt.Errorf("workflow %q: duplicate step name %q across steps, rescue, and always", workflowName, s.Name)
		}
		stepNames[s.Name] = true

		if len(s.WorkflowNames) > 0 {
			if !IsTemplated(s.Workflow) {
				return fmt.Errorf("workflow %q, step %q: workflow_names needs a templated workflow (one with {{ }}) to choose between them", workflowName, s.Name)
			}
			for _, name := range s.WorkflowNames {
				if !hasWorkflow(wf, name) {
					return fmt.Errorf("workflow %q, step %q: workflow_names lists unknown workflow %q", workflowName, s.Name, name)
				}
			}
		}
		if s.Workflow != "" {
			// A templated name is resolved when the step runs.
			if !IsTemplated(s.Workflow) && !hasWorkflow(wf, s.Workflow) {
				return fmt.Errorf("workflow %q, step %q: references unknown workflow %q", workflowName, s.Name, s.Workflow)
			}
			if child := wf.GetWorkflow(s.Workflow); child != nil && !IsTemplated(s.Workflow) {
				if missing := missingCallParams(wf, s, child); len(missing) > 0 {
					return fmt.Errorf("workflow %q, step %q: workflow %q needs %s (declared with no default); pass it with pass: or vars:", workflowName, s.Name, s.Workflow, strings.Join(missing, ", "))
				}
			}
		} else if s.Type == "" {
			return fmt.Errorf("workflow %q, step %q: must have type or workflow", workflowName, s.Name)
		}

		if s.FailedWhen != "" && s.Workflow != "" {
			return fmt.Errorf("workflow %q, step %q: failed_when applies to step types, not sub-workflows", workflowName, s.Name)
		}
		if s.Secret.Set() && (s.Workflow != "" || s.Type == "gate" || s.Type == "assert") {
			return fmt.Errorf("workflow %q, step %q: secret marks a step's own output; sub-workflow, gate and assert steps have none (mark the steps inside the sub-workflow)", workflowName, s.Name)
		}
		if len(s.Pass) > 0 {
			if s.Workflow == "" {
				return fmt.Errorf("workflow %q, step %q: pass hands vars to a called workflow; this step calls none", workflowName, s.Name)
			}
			for _, name := range s.Pass {
				if name == "" || strings.ContainsAny(name, ".[] ") || IsTemplated(name) {
					return fmt.Errorf("workflow %q, step %q: pass: %q is not a var name (use vars: to pick out or rename)", workflowName, s.Name, name)
				}
			}
		}
		if err := checkGlobalWrites(wf, workflowName, s); err != nil {
			return err
		}
		if s.ForEachWhen != "" && s.ForEach == "" {
			return fmt.Errorf("workflow %q, step %q: for_each_when requires for_each", workflowName, s.Name)
		}
		if s.ForEach != "" && s.As == "" {
			return fmt.Errorf("workflow %q, step %q: for_each requires as", workflowName, s.Name)
		}

		if s.Type != "" {
			if err := resolveType(wf, s.Type); err != nil {
				return fmt.Errorf("workflow %q, step %q: %w", workflowName, s.Name, err)
			}
		}

		if err := validateJevStep(wf, s); err != nil {
			return fmt.Errorf("workflow %q, step %q: %w", workflowName, s.Name, err)
		}
		if s.Type == "gate" {
			if len(s.Rules) == 0 {
				return fmt.Errorf("workflow %q, step %q: gate must reference at least one rule", workflowName, s.Name)
			}
			for _, rn := range s.Rules {
				if wf.GetRule(rn) == nil {
					return fmt.Errorf("workflow %q, step %q: references unknown rule %q", workflowName, s.Name, rn)
				}
			}
		}
	}
	return nil
}

// checkGlobalWrites rejects a step that would change a global (a file-level var) or a built-in:
// they are set when the run starts and are the same everywhere. A call may still shadow one for
// the workflow it calls, through pass: or vars:, and a workflow that declares the name in its own
// vars: has a parameter of that name, which is its own to change.
func checkGlobalWrites(wf *WorkflowFile, workflowName string, s Step) error {
	var own map[string]any
	if w := wf.GetWorkflow(workflowName); w != nil {
		own = w.Vars
	}
	global := func(name string) bool {
		if strings.HasPrefix(name, "markov_") {
			return true
		}
		if _, ok := own[name]; ok {
			return false
		}
		_, ok := wf.Vars[name]
		return ok
	}
	fail := func(what, name string) error {
		return fmt.Errorf("workflow %q, step %q: %s %q is a global (a file-level var), which no step may change; use another name, or pass a different value to a called workflow", workflowName, s.Name, what, name)
	}
	if s.Register != "" && global(s.Register) {
		return fail("register", s.Register)
	}
	if s.As != "" && global(s.As) {
		return fail("as", s.As)
	}
	if s.Type == "set_fact" {
		for name := range s.Vars {
			if global(name) {
				return fail("set_fact", name)
			}
		}
	}
	return nil
}

var primitives = map[string]bool{
	"jev":                true,
	"k8s_job":            true,
	"k8s_job_wait":       true,
	"http_request":       true,
	"llm_invoke":         true,
	"shell_exec":         true,
	"script_exec":        true,
	"write_file":         true,
	"claude":             true,
	"ansible":            true,
	"ansible_playbook":   true,
	"prompt":             true,
	"gate":               true,
	"load_artifact":      true,
	"set_fact":           true,
	"assert":             true,
	"reload_workflow":    true,
	"codify":             true,
	"openshell_build":    true,
	"openshell_create":   true,
	"openshell_upload":   true,
	"openshell_exec":     true,
	"openshell_download": true,
	"openshell_delete":   true,
	"openshell_provider": true,
}

// PrimitiveNames lists the built-in step types, sorted.
func PrimitiveNames() []string {
	names := make([]string, 0, len(primitives))
	for n := range primitives {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func resolveType(wf *WorkflowFile, typeName string) error {
	if primitives[typeName] {
		return nil
	}
	if _, ok := wf.StepTypes[typeName]; ok {
		return nil
	}
	return fmt.Errorf("unknown type %q (not a primitive or step_type)", typeName)
}

func hasWorkflow(wf *WorkflowFile, name string) bool {
	for _, w := range wf.Workflows {
		if w.Name == name {
			return true
		}
	}
	return false
}

func (wf *WorkflowFile) GetWorkflow(name string) *Workflow {
	for i := range wf.Workflows {
		if wf.Workflows[i].Name == name {
			return &wf.Workflows[i]
		}
	}
	return nil
}

func (wf *WorkflowFile) GetRule(name string) *Rule {
	for i := range wf.Rules {
		if wf.Rules[i].Name == name {
			return &wf.Rules[i]
		}
	}
	return nil
}

func loadRuleFiles(wf *WorkflowFile, baseDir string) error {
	var expanded []Rule
	for _, r := range wf.Rules {
		if r.File != "" {
			rulePath := r.File
			if !filepath.IsAbs(rulePath) {
				rulePath = filepath.Join(baseDir, rulePath)
			}
			data, err := os.ReadFile(rulePath)
			if err != nil {
				return fmt.Errorf("loading rule file %q: %w", r.File, err)
			}
			var rf struct {
				Rules []Rule `yaml:"rules"`
			}
			if err := decodeStrict(data, &rf); err != nil {
				return fmt.Errorf("parsing rule file %q: %w", r.File, err)
			}
			expanded = append(expanded, rf.Rules...)
		} else {
			expanded = append(expanded, r)
		}
	}
	wf.Rules = expanded
	return nil
}

func (wf *WorkflowFile) ResolveStepType(step *Step) (base string, mergedParams map[string]any) {
	if primitives[step.Type] {
		return step.Type, step.Params
	}
	st, ok := wf.StepTypes[step.Type]
	if !ok {
		return step.Type, step.Params
	}

	merged := make(map[string]any)
	for k, v := range st.Job {
		merged[k] = v
	}
	for k, v := range st.Defaults {
		merged[k] = v
	}
	for k, v := range st.Params {
		merged[k] = v
	}
	for k, v := range step.Params {
		merged[k] = v
	}
	mergeHeaderParams(merged, st.Job, st.Defaults, st.Params, step.Params)

	return st.Base, merged
}

func mergeHeaderParams(merged map[string]any, layers ...map[string]any) {
	headers := map[string]any{}
	for _, layer := range layers {
		layerHeaders, ok := mapAny(layer["headers"])
		if !ok {
			continue
		}
		for key, value := range layerHeaders {
			headers[key] = value
		}
	}
	if len(headers) > 0 {
		merged["headers"] = headers
	}
}

func mapAny(raw any) (map[string]any, bool) {
	switch v := raw.(type) {
	case map[string]any:
		return v, true
	case map[string]string:
		out := make(map[string]any, len(v))
		for key, value := range v {
			out[key] = value
		}
		return out, true
	default:
		return nil, false
	}
}
