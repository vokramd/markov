package parser

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/vokramd/markov/pkg/template"
)

// Issue is something `markov validate` reports about a workflow's variables.
type Issue struct {
	Error    bool // an error; otherwise a warning
	Workflow string
	Step     string // empty for the workflow itself
	Name     string // the variable
	Msg      string
}

func (i Issue) String() string {
	level := "warning"
	if i.Error {
		level = "error"
	}
	where := fmt.Sprintf("workflow %q", i.Workflow)
	if i.Step != "" {
		where += fmt.Sprintf(", step %q", i.Step)
	}
	return level + ": " + where + ": " + i.Msg
}

// Lint reports names a workflow reads that it cannot see, and names a call passes that the
// called workflow does not declare. A workflow sees the globals (file-level vars), the markov_*
// built-ins, its own vars (parameters), and what its own steps register, set or loop over.
// Reading is found best effort, from templates and expressions; a computed key is not followed.
//
// A name a called workflow cannot see, or one it is passed without declaring it, is an error: a
// workflow says what it takes. In a workflow nothing calls (an entry workflow) an undeclared name
// is a warning, since --var may set it.
func Lint(wf *WorkflowFile) []Issue {
	called := map[string]bool{}
	passed := map[string]map[string]bool{} // workflow -> names some call passes it
	for _, w := range wf.Workflows {
		for _, s := range allSteps(&w) {
			if s.Workflow != "" && !IsTemplated(s.Workflow) {
				called[s.Workflow] = true
				if passed[s.Workflow] == nil {
					passed[s.Workflow] = map[string]bool{}
				}
				for _, n := range passedNames(s) {
					passed[s.Workflow][n] = true
				}
			}
			for _, n := range s.WorkflowNames {
				called[n] = true
			}
		}
	}
	var issues []Issue
	for i := range wf.Workflows {
		w := &wf.Workflows[i]
		visible := visibleNames(wf, w)
		// A step name with a hyphen (rfe-analysis) reads as a subtraction; read it whole.
		var pairs []string
		for name := range visible {
			if strings.Contains(name, "-") {
				pairs = append(pairs, name, strings.ReplaceAll(name, "-", "_"))
				visible[strings.ReplaceAll(name, "-", "_")] = true
			}
		}
		rep := strings.NewReplacer(pairs...)
		for _, s := range allSteps(w) {
			for _, name := range stepReads(wf, s, rep) {
				if visible[name] || strings.HasPrefix(name, "markov_") || (name == "repeat" && s.Repeat != nil) {
					continue
				}
				isErr := called[w.Name]
				msg := fmt.Sprintf("reads %q, which this workflow cannot see: declare it in the workflow's vars: (and have callers pass it), or make it a file-level var", name)
				switch {
				case !called[w.Name]:
					msg = fmt.Sprintf("reads %q, which is not declared: it must come from --var, or declare it in the workflow's vars:", name)
				case passed[w.Name][name]:
					// Its callers pass it, so it runs, but the workflow does not say it takes it.
					msg = fmt.Sprintf("reads %q, which its callers pass but it does not declare in its vars:", name)
				}
				issues = append(issues, Issue{Error: isErr, Workflow: w.Name, Step: s.Name, Name: name, Msg: msg})
			}
			issues = append(issues, lintTemplates(wf, w, s)...)
			if s.Workflow == "" || IsTemplated(s.Workflow) {
				continue
			}
			child := wf.GetWorkflow(s.Workflow)
			if child == nil {
				continue
			}
			issues = append(issues, lintRepeatUntil(wf, w, s, child)...)
			for _, name := range passedNames(s) {
				if _, ok := child.Vars[name]; ok || isGlobalName(wf, name) {
					continue
				}
				issues = append(issues, Issue{Error: true, Workflow: w.Name, Step: s.Name, Name: name, Msg: fmt.Sprintf("passes %q, which workflow %q does not declare in its vars:", name, child.Name)})
			}
		}
	}
	return issues
}

// lintRepeatUntil warns about names a repeat step's until reads that the iteration's results
// can't hold: until sees what the called workflow was passed and what its steps set, plus
// repeat. Outputs can be conditional, so this is a hint; the runtime check is the real one.
func lintRepeatUntil(wf *WorkflowFile, w *Workflow, s Step, child *Workflow) []Issue {
	if s.Repeat == nil || s.Repeat.Until == "" {
		return nil
	}
	visible := visibleNames(wf, child)
	for _, n := range passedNames(s) {
		visible[n] = true
	}
	visible["repeat"] = true
	var pairs []string
	for name := range visible {
		if strings.Contains(name, "-") {
			pairs = append(pairs, name, strings.ReplaceAll(name, "-", "_"))
			visible[strings.ReplaceAll(name, "-", "_")] = true
		}
	}
	rep := strings.NewReplacer(pairs...)
	names := map[string]bool{}
	if IsTemplated(s.Repeat.Until) {
		collectTemplateNames(s.Repeat.Until, names, rep)
	} else {
		collectExprNamesExcept(rep.Replace(s.Repeat.Until), names, nil)
	}
	delete(names, "")
	var issues []Issue
	for _, name := range sortedKeys(names) {
		if visible[name] || isGlobalName(wf, name) {
			continue
		}
		issues = append(issues, Issue{Workflow: w.Name, Step: s.Name, Name: name,
			Msg: fmt.Sprintf("repeat until reads %q, which workflow %q's results don't hold: until sees what it is passed and what its steps set, plus repeat", name, child.Name)})
	}
	return issues
}

// missingCallParams is a call's required parameters (declared with no default) that it does not
// give: not a global, not passed, not in its vars:, not its loop variable.
func missingCallParams(wf *WorkflowFile, s Step, child *Workflow) []string {
	given := map[string]bool{}
	for _, n := range passedNames(s) {
		given[n] = true
	}
	if s.ForEach != "" {
		given[s.As] = true
	}
	var missing []string
	for name, v := range child.Vars {
		if v == nil && !given[name] && !isGlobalName(wf, name) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

func passedNames(s Step) []string {
	names := append([]string(nil), s.Pass...)
	for k := range s.Vars {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func isGlobalName(wf *WorkflowFile, name string) bool {
	if strings.HasPrefix(name, "markov_") {
		return true
	}
	_, ok := wf.Vars[name]
	return ok
}

func allSteps(w *Workflow) []Step {
	steps := append([]Step(nil), w.Steps...)
	steps = append(steps, w.Rescue...)
	return append(steps, w.Always...)
}

// visibleNames is every name a workflow's steps may read.
func visibleNames(wf *WorkflowFile, w *Workflow) map[string]bool {
	v := map[string]bool{"result": true, "jev": true} // failed_when's view of the output; jev's functions
	for k := range wf.Vars {
		v[k] = true
	}
	for k := range w.Vars {
		v[k] = true
	}
	for _, s := range allSteps(w) {
		if s.Register != "" {
			v[s.Register] = true
		}
		if s.As != "" {
			v[s.As] = true
		}
		if s.Type == "set_fact" {
			for k := range s.Vars {
				v[k] = true
			}
		}
		if s.Type == "load_artifact" || len(s.Artifacts) > 0 {
			v[s.Name] = true // the loaded artifacts, under the step's name
		}
		for k := range s.Facts {
			v[k] = true // a gate's resolved facts
		}
		for _, rn := range s.Rules {
			if r := wf.GetRule(rn); r != nil {
				for k := range r.SetFact {
					v[k] = true
				}
			}
		}
	}
	return v
}

// lintTemplates reports a step's templates and expressions that don't parse: a syntax error, or
// a filter or tag that doesn't exist. Each would otherwise fail only when the step runs.
func lintTemplates(wf *WorkflowFile, w *Workflow, s Step) []Issue {
	var issues []Issue
	seen := map[string]bool{}
	report := func(text string, err error) {
		if err == nil || seen[text] {
			return
		}
		seen[text] = true
		issues = append(issues, Issue{Error: true, Workflow: w.Name, Step: s.Name, Msg: fmt.Sprintf("%q does not parse: %v", text, err)})
	}
	expr := func(e string) {
		if IsTemplated(e) {
			report(e, template.Check(e))
		} else {
			report(e, template.CheckExpr(e))
		}
	}
	walkStepText(wf, s, func(t string) {
		if IsTemplated(t) {
			report(t, template.Check(t))
		}
	}, expr)
	if s.Repeat != nil && s.Repeat.Until != "" {
		expr(s.Repeat.Until)
	}
	if s.Type == "set_fact" {
		names := make([]string, 0, len(s.Vars))
		for name := range s.Vars {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if str, ok := s.Vars[name].(string); ok && looksLikeAValue(str) {
				issues = append(issues, Issue{Workflow: w.Name, Step: s.Name, Name: name, Msg: fmt.Sprintf(
					"%s: %q is a boolean expression (a plain set_fact string), so it stores true or false; to store the value, write \"{{ %s }}\"",
					name, str, strings.TrimSpace(str))})
			}
		}
	}
	return issues
}

// dataFilter matches an expression that ends in a filter producing data rather than a truth value.
var dataFilter = regexp.MustCompile(`\|\s*(pluck|flatten|fromjson|from_json|to_json|tojson|csv|seq|join|split|trim|upper|lower|title|capfirst|slice|sort|reverse|make_list|urlencode)(\s*:\s*('[^']*'|"[^"]*"|[^|\s]+))?\s*$`)

// booleanOps are what make an expression a test rather than a value.
var booleanOps = regexp.MustCompile(`==|!=|<|>|\b(and|or|not|in|is)\b`)

// looksLikeAValue is true for a plain set_fact string that computes a value but would be stored as
// a boolean: `kids | pluck:'leaves' | flatten` rather than `"{{ kids | pluck:'leaves' | flatten }}"`.
func looksLikeAValue(expr string) bool {
	if IsTemplated(expr) {
		return false
	}
	return dataFilter.MatchString(expr) && !booleanOps.MatchString(expr)
}

// walkStepText calls tmpl with each template string a step holds and expr with each
// expression (when, that and the like, and set_fact strings), as the engine reads them.
// Fact sources ({from: path}) are paths, not templates, and are skipped.
func walkStepText(wf *WorkflowFile, s Step, tmpl func(string), expr func(string)) {
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case string:
			tmpl(t)
		case map[string]any:
			for _, x := range t {
				walk(x)
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	if s.Type != "" {
		_, params := wf.ResolveStepType(&s)
		walk(params)
	}
	for _, e := range []string{s.When, s.ForEach, s.ForEachWhen, s.FailedWhen} {
		if e != "" {
			expr(e)
		}
	}
	for _, e := range s.That {
		expr(e)
	}
	walk(s.Msg)
	walk(s.Workflow)
	for _, v := range s.Vars {
		if str, ok := v.(string); ok && s.Type == "set_fact" {
			expr(str)
		} else {
			walk(v)
		}
	}
	for _, v := range s.Facts {
		if m, ok := v.(map[string]any); ok {
			if _, ok := m["from"].(string); ok {
				continue
			}
		}
		walk(v)
	}
	for _, a := range s.Artifacts {
		walk(a.Path)
	}
	if s.Repeat != nil {
		walk(s.Repeat.Max) // until reads the iteration's results, not the step's scope
	}
}

// stepReads is the names a step reads, from its templates and expressions, sorted.
func stepReads(wf *WorkflowFile, s Step, rep *strings.Replacer) []string {
	names := map[string]bool{}
	walkStepText(wf, s, func(t string) {
		collectTemplateNames(t, names, rep)
	}, func(e string) {
		if IsTemplated(e) {
			collectTemplateNames(e, names, rep)
		} else {
			collectExprNamesExcept(rep.Replace(e), names, nil)
		}
	})
	for _, n := range s.Pass {
		names[n] = true
	}
	for _, v := range s.Facts {
		if m, ok := v.(map[string]any); ok {
			if from, ok := m["from"].(string); ok {
				names[rootName(from)] = true
			}
		}
	}
	delete(names, "")
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func rootName(path string) string {
	name, _, _ := strings.Cut(path, ".")
	name, _, _ = strings.Cut(name, "[")
	return strings.TrimSpace(name)
}

var (
	blockRe  = regexp.MustCompile(`(?s)\{\{(.*?)\}\}|\{%-?(.*?)-?%\}`)
	identRe  = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	stringRe = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'`)
	forTagRe = regexp.MustCompile(`^\s*for\s+(.+?)\s+in\s+(.*)$`)
	setTagRe = regexp.MustCompile(`^\s*set\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$`)
)

// exprWords are the words of the expression language that are not variables.
var exprWords = map[string]bool{
	"and": true, "or": true, "not": true, "in": true, "is": true, "if": true, "else": true,
	"true": true, "false": true, "none": true, "True": true, "False": true, "None": true,
	"forloop": true, "loop": true,
}

// collectTemplateNames adds the names read by every template string in v (a string, or maps
// and lists of them). Names a template sets itself ({% for x in %}, {% set x = %}) are left out.
func collectTemplateNames(v any, names map[string]bool, rep *strings.Replacer) {
	switch t := v.(type) {
	case string:
		t = rep.Replace(t)
		local := map[string]bool{}
		for _, m := range blockRe.FindAllStringSubmatch(t, -1) {
			if m[1] != "" || strings.HasPrefix(m[0], "{{") {
				collectExprNamesExcept(m[1], names, local)
				continue
			}
			tag := m[2]
			if f := forTagRe.FindStringSubmatch(tag); f != nil {
				for _, n := range strings.Split(f[1], ",") {
					local[strings.TrimSpace(n)] = true
				}
				collectExprNamesExcept(f[2], names, local)
				continue
			}
			if s := setTagRe.FindStringSubmatch(tag); s != nil {
				local[s[1]] = true
				collectExprNamesExcept(s[2], names, local)
				continue
			}
			word := identRe.FindString(tag)
			switch word {
			case "if", "elif", "with", "filter":
				collectExprNamesExcept(strings.TrimPrefix(strings.TrimSpace(tag), word), names, local)
			}
		}
	case map[string]any:
		for _, x := range t {
			collectTemplateNames(x, names, rep)
		}
	case []any:
		for _, x := range t {
			collectTemplateNames(x, names, rep)
		}
	}
}

// collectExprNamesExcept adds the variable names an expression reads, except local ones.
func collectExprNamesExcept(expr string, names, local map[string]bool) {
	expr = stringRe.ReplaceAllString(expr, `""`)
	for _, loc := range identRe.FindAllStringIndex(expr, -1) {
		word := expr[loc[0]:loc[1]]
		before := strings.TrimRight(expr[:loc[0]], " \t\n")
		after := strings.TrimLeft(expr[loc[1]:], " \t\n")
		switch {
		case exprWords[word], local[word]:
		case loc[0] > 0 && isIdentChar(expr[loc[0]-1]): // the tail of a number such as 1e5
		case strings.HasSuffix(before, "."): // an attribute
		case strings.HasSuffix(before, "|"): // a filter
		case strings.HasPrefix(after, "=") && !strings.HasPrefix(after, "=="): // a keyword argument
		case word[0] >= '0' && word[0] <= '9':
		default:
			names[word] = true
		}
	}
}

func isIdentChar(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
