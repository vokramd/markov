package codify

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/jctanner/markov/pkg/parser"
	"gopkg.in/yaml.v3"
)

// agentTypes are the step types that run a model.
var agentTypes = map[string]bool{"claude": true, "llm_invoke": true}

// Project is a directory workflow and the target being codified in it.
type Project struct {
	Dir    string
	Target string
	File   *parser.WorkflowFile
	// files maps each workflow name to the file (relative, forward slashes) that defines it.
	files map[string]string
	// Reach is the target and every workflow it calls, directly or through others.
	Reach []string
	// Fixed are reached workflows the candidate may not change: the parts of a cycle that set
	// the stage and measure the result (a reset, a snapshot, the cycle itself).
	Fixed []string
}

// ignored are files that are never part of a project's source.
func ignored(rel string, d fs.DirEntry) bool {
	base := filepath.Base(rel)
	if d != nil && d.IsDir() {
		return strings.HasPrefix(base, ".") && rel != "."
	}
	return strings.HasSuffix(base, ".db") || strings.Contains(base, ".db-") || strings.HasPrefix(base, ".") || strings.HasSuffix(base, ".pyc")
}

// LoadProject parses the directory workflow in dir and works out what codifying target may change.
// fixed names workflows the target reaches that must not change.
func LoadProject(dir, target string, fixed ...string) (*Project, error) {
	if info, err := os.Stat(filepath.Join(dir, "meta.yaml")); err != nil || info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory workflow (codify needs meta.yaml and workflows/); single-file workflows are not supported yet", dir)
	}
	file, err := parser.ParseDir(dir)
	if err != nil {
		return nil, err
	}
	if file.GetWorkflow(target) == nil {
		var names []string
		for _, w := range file.Workflows {
			names = append(names, w.Name)
		}
		return nil, fmt.Errorf("no workflow %q (workflows: %s)", target, strings.Join(names, ", "))
	}
	p := &Project{Dir: dir, Target: target, File: file, files: map[string]string{}}
	matches, _ := filepath.Glob(filepath.Join(dir, "workflows", "*.yaml"))
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var head struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(data, &head) == nil && head.Name != "" {
			p.files[head.Name] = "workflows/" + filepath.Base(m)
		}
	}
	p.Reach = reach(file, target)
	for _, name := range fixed {
		if !slices.Contains(p.Reach, name) {
			return nil, fmt.Errorf("fixed workflow %q is not %s or a workflow it calls (it reaches: %s)", name, target, strings.Join(p.Reach, ", "))
		}
		if !slices.Contains(p.Fixed, name) {
			p.Fixed = append(p.Fixed, name)
		}
	}
	sort.Strings(p.Fixed)
	if len(p.changeable()) == 0 {
		return nil, fmt.Errorf("every workflow %s reaches is fixed; nothing is left to codify", target)
	}
	return p, nil
}

// FixedFiles returns those of rels that define a fixed workflow.
func (p *Project) FixedFiles(rels []string) []string {
	var out []string
	for _, rel := range rels {
		for _, name := range p.Fixed {
			if p.files[name] == rel {
				out = append(out, rel)
			}
		}
	}
	return out
}

// changeable is the reached workflows that are not fixed.
func (p *Project) changeable() []string {
	var out []string
	for _, name := range p.Reach {
		if !slices.Contains(p.Fixed, name) {
			out = append(out, name)
		}
	}
	return out
}

// reach lists target and the workflows it calls (templated calls: their workflow_names).
func reach(file *parser.WorkflowFile, target string) []string {
	seen := map[string]bool{}
	queue := []string{target}
	var out []string
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		wf := file.GetWorkflow(name)
		if wf == nil {
			continue
		}
		out = append(out, name)
		for _, steps := range [][]parser.Step{wf.Steps, wf.Rescue, wf.Always} {
			for _, st := range steps {
				switch {
				case st.Workflow != "" && !parser.IsTemplated(st.Workflow):
					queue = append(queue, st.Workflow)
				case len(st.WorkflowNames) > 0:
					queue = append(queue, st.WorkflowNames...)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// HelperPrefix is the name helper workflows of the target must start with.
func (p *Project) HelperPrefix() string { return p.Target + "_" }

// ScriptsDir is where the target's scripts go (relative).
func (p *Project) ScriptsDir() string { return "scripts/" + p.Target + "/" }

// Editable reports whether a candidate may change (add, edit or remove) a project file: a file
// defining a workflow the target reaches (unless it is fixed), a helper workflow file named for
// the target, or anything under the target's scripts directory.
func (p *Project) Editable(rel string) bool {
	for _, name := range p.changeable() {
		if p.files[name] == rel {
			return true
		}
	}
	if strings.HasPrefix(rel, "workflows/"+p.HelperPrefix()) && strings.HasSuffix(rel, ".yaml") {
		return true
	}
	return strings.HasPrefix(rel, p.ScriptsDir())
}

// EditableFiles lists the editable files of the project as described above, for the prompt.
func (p *Project) EditableFiles() []string {
	var out []string
	for _, name := range p.changeable() {
		if f := p.files[name]; f != "" {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// AgentSteps lists "workflow.step" for every model-backed step the target reaches outside the
// fixed workflows.
func (p *Project) AgentSteps() []string {
	var out []string
	for _, name := range p.changeable() {
		wf := p.File.GetWorkflow(name)
		for _, steps := range [][]parser.Step{wf.Steps, wf.Rescue, wf.Always} {
			for _, st := range steps {
				if st.Type == "" {
					continue
				}
				if base, _ := p.File.ResolveStepType(&st); agentTypes[base] {
					out = append(out, name+"."+st.Name)
				}
			}
		}
	}
	return out
}

// Files reads a directory tree (source files only), by forward-slash relative path.
func Files(dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if ignored(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = data
		return nil
	})
	return out, err
}

// Changes lists the files that differ between two trees.
func Changes(before, after map[string][]byte) []string {
	var out []string
	for rel, b := range before {
		if a, ok := after[rel]; !ok || !bytes.Equal(a, b) {
			out = append(out, rel)
		}
	}
	for rel := range after {
		if _, ok := before[rel]; !ok {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// CopyTree copies the project's source files into dest (replacing it).
func CopyTree(src, dest string) error {
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	files, err := Files(src)
	if err != nil {
		return err
	}
	for rel, data := range files {
		if err := writeFile(filepath.Join(dest, filepath.FromSlash(rel)), data, src, rel); err != nil {
			return err
		}
	}
	return os.MkdirAll(dest, 0o755)
}

// writeFile writes data, keeping the source file's mode (scripts stay executable).
func writeFile(path string, data []byte, srcDir, rel string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if srcDir != "" {
		if info, err := os.Stat(filepath.Join(srcDir, filepath.FromSlash(rel))); err == nil {
			mode = info.Mode().Perm()
		}
	}
	return os.WriteFile(path, data, mode)
}

// Install makes the project's editable files match the candidate's: copies changed and new ones,
// removes the ones the candidate dropped. It returns what changed.
func (p *Project) Install(candidate string) ([]string, error) {
	cur, err := Files(p.Dir)
	if err != nil {
		return nil, err
	}
	next, err := Files(candidate)
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, rel := range Changes(cur, next) {
		if !p.Editable(rel) {
			continue
		}
		dst := filepath.Join(p.Dir, filepath.FromSlash(rel))
		if data, ok := next[rel]; ok {
			if err := writeFile(dst, data, candidate, rel); err != nil {
				return changed, err
			}
		} else if err := os.Remove(dst); err != nil {
			return changed, err
		}
		changed = append(changed, rel)
	}
	return changed, nil
}

// Archive copies the project's editable files into dir (one version).
func (p *Project) Archive(dir string) error {
	files, err := Files(p.Dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for rel, data := range files {
		if p.Editable(rel) {
			if err := writeFile(filepath.Join(dir, filepath.FromSlash(rel)), data, p.Dir, rel); err != nil {
				return err
			}
		}
	}
	return nil
}
