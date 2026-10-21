package codify

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/jctanner/markov/pkg/executor"
	"github.com/jctanner/markov/pkg/parser"
)

//go:embed retro.md
var defaultRetroPrompt string

//go:embed judge.md
var judgePrompt string

// RetroInputs fill the retro prompt template. A replacement prompt (--prompt-file) gets the
// same fields.
type RetroInputs struct {
	Target        string
	Evidence      string
	Docs          string
	Markov        string
	RetroJSON     string
	EditableFiles []string
	HelperPrefix  string
	ScriptsDir    string
	AgentSteps    []string
	Keep          []string
	Judged        bool
}

func render(text string, data any) (string, error) {
	t, err := template.New("prompt").Funcs(template.FuncMap{"join": strings.Join}).Parse(text)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// AgentResult is what an agent call cost and said.
type AgentResult struct {
	CostUSD float64
	Tokens  int
	Failed  string // why the call failed, if it did
}

// runAgent runs the claude CLI non-interactively through Markov's claude executor.
func (c *Config) runAgent(ctx context.Context, step, prompt, dir string, addDirs, tools []string) AgentResult {
	if c.em != nil {
		ctx = executor.WithProgress(ctx, c.em.progress(step))
	}
	params := map[string]any{
		"prompt": prompt, "chdir": dir, "permission_mode": "acceptEdits",
		"allowed_tools": toAny(tools), "add_dirs": toAny(addDirs),
		"limits": map[string]any{"max_turns": 60, "max_tokens": 2_000_000, "max_duration": 1800},
	}
	if c.Model != "" {
		params["model"] = c.Model
	}
	if c.ClaudeBin != "" {
		params["binary"] = c.ClaudeBin
	}
	res, err := executor.NewClaude().Execute(ctx, params)
	var out AgentResult
	if res != nil {
		out.CostUSD = num(res.Output["total_cost_usd"])
		out.Tokens = int(num(res.Output["tokens"]))
	}
	if err != nil {
		out.Failed = err.Error()
	}
	return out
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// Retro is one retrospective pass: the agent edits the candidate and writes retro.json.
type Retro struct {
	AgentResult
	Report map[string]any
}

func (c *Config) retro(ctx context.Context, in RetroInputs, candidate string) Retro {
	text := defaultRetroPrompt
	if c.PromptFile != "" {
		data, err := os.ReadFile(c.PromptFile)
		if err != nil {
			return Retro{AgentResult: AgentResult{Failed: err.Error()}}
		}
		text = string(data)
	}
	prompt, err := render(text, in)
	if err != nil {
		return Retro{AgentResult: AgentResult{Failed: "retro prompt: " + err.Error()}}
	}
	tools := []string{"Read", "Grep", "Glob", "Edit", "Write", fmt.Sprintf("Bash(%s validate:*)", c.Markov)}
	r := Retro{AgentResult: c.runAgent(ctx, "retro", prompt, candidate, []string{filepath.Dir(in.RetroJSON), in.Docs}, tools)}
	r.Report = readJSON(in.RetroJSON)
	return r
}

// Verdict is the judge's answer.
type Verdict struct {
	AgentResult
	Equivalent bool   `json:"equivalent"`
	Reason     string `json:"reason"`
}

// judge compares two runs' outputs by content, for outputs an agent produces.
func (c *Config) judge(ctx context.Context, target string, prompts []string, reference, candidate, verdictFile string) Verdict {
	prompt, err := render(judgePrompt, map[string]any{"Target": target, "Reference": reference, "Candidate": candidate, "Prompts": prompts, "Verdict": verdictFile})
	if err != nil {
		return Verdict{AgentResult: AgentResult{Failed: err.Error()}}
	}
	dir := filepath.Dir(verdictFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Verdict{AgentResult: AgentResult{Failed: err.Error()}}
	}
	v := Verdict{AgentResult: c.runAgent(ctx, "judge", prompt, dir, []string{reference, candidate}, []string{"Read", "Grep", "Glob", "Write"})}
	report := readJSON(verdictFile)
	v.Equivalent, _ = report["equivalent"].(bool)
	v.Reason, _ = report["reason"].(string)
	if report == nil && v.Failed == "" {
		v.Failed = "the judge wrote no verdict"
	}
	return v
}

func readJSON(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return m
}

// agentPrompts lists the prompt (or skill) of every agent step the target reaches, for the judge.
func agentPrompts(file *parser.WorkflowFile, reach []string) []string {
	var out []string
	for _, name := range reach {
		wf := file.GetWorkflow(name)
		for _, steps := range [][]parser.Step{wf.Steps, wf.Rescue, wf.Always} {
			for _, st := range steps {
				base, params := file.ResolveStepType(&st)
				if !agentTypes[base] {
					continue
				}
				p := fmt.Sprint(params["prompt"])
				if params["prompt"] == nil {
					p = fmt.Sprintf("/%v %v", params["skill"], params["args"])
				}
				out = append(out, fmt.Sprintf("%s.%s: %s", name, st.Name, strings.TrimSpace(p)))
			}
		}
	}
	return out
}
