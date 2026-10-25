// Package codify implements `markov codify`: agentic codification of any workflow (plan 003
// phase 6). Each iteration runs the target workflow live through a recording proxy, lets a
// retro agent move one job out of its agent steps in a copy of the project, runs the copy
// against the recording, and promotes it when its outputs match: exactly, or, while an agent
// produced them, by a judge comparing content. Only the proposal and the judging use a model;
// everything else here is deterministic.
package codify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jctanner/markov/docs"
	"github.com/jctanner/markov/pkg/callback"
	"github.com/jctanner/markov/pkg/schema"
)

// Config is one `markov codify` invocation.
type Config struct {
	Source        string // the project directory
	Workflow      string // the workflow to codify
	CodifyDir     string // history; default <Source>.codify
	Model         string // retro agent and judge
	MaxIterations int
	BudgetUSD     float64 // stop once the run's model spend reaches it (0: no limit)
	Keep          []string
	// Fixed are workflows the target reaches that the candidate may not change.
	Fixed []string
	// Mode is "strict" (the default: a candidate must produce the same outputs, agent-written
	// content compared by meaning) or "loose" (agent work is decomposed into atomic agent steps
	// joined by native ones; a candidate must take the same actions on the same items, and
	// values agents decide only need to be valid).
	Mode string
	// LiveHosts are host suffixes the target's runs reach for real, live run and candidates
	// alike, instead of replaying a recording: only for services the target resets itself.
	LiveHosts []string
	// RepeatChanges names the output ("path", or "path:key" for a dotted key; default key
	// counts.total) in which the target records how many things a second run, right after the
	// first, changed. A candidate may not change more on that repeat than the current version.
	RepeatChanges string
	PromptFile    string
	Markov        string // the markov binary for child runs
	ClaudeBin     string // the claude CLI (tests)
	RunTimeout    time.Duration
	Passthrough   []string
	Out           io.Writer
	// Callbacks receive codify's events: its phases as steps of the virtual "codify" workflow
	// (the retro agent's and judge's claude streams as step_progress), and every event of the
	// target workflow's runs.
	Callbacks []callback.Callback
	em        *emitter
	// proxyHook adjusts the proxy after it starts (tests route made-up hosts to test servers).
	proxyHook func(*Proxy)
}

// Row is one metrics.jsonl line (the format the IDE's Codification tab reads).
type Row struct {
	Iteration  string         `json:"iteration"`
	Time       string         `json:"time"`
	Mode       string         `json:"mode,omitempty"`
	Task       Metrics        `json:"task"`
	Retro      map[string]any `json:"retro"`
	Validation Validation     `json:"validation"`
	Promoted   *string        `json:"promoted_version"`
	SpentUSD   float64        `json:"spent_usd"`
}

// Validation is how a candidate was judged.
type Validation struct {
	Status       string   `json:"status"` // passed, failed, no_change, live_failed
	Reasons      []string `json:"reasons"`
	ChangedFiles []string `json:"changed_files,omitempty"`
	Judged       bool     `json:"judged"`
	JudgeReason  string   `json:"judge_reason,omitempty"`
	Candidate    *Metrics `json:"candidate,omitempty"`
	// Repeat is what a second run changed, current version and candidate (with --repeat-changes).
	Repeat *Repeat `json:"repeat,omitempty"`
}

func (c *Config) defaults() error {
	abs, err := filepath.Abs(c.Source)
	if err != nil {
		return err
	}
	c.Source = filepath.Clean(abs)
	if c.CodifyDir == "" {
		c.CodifyDir = c.Source + ".codify"
	}
	if c.CodifyDir, err = filepath.Abs(c.CodifyDir); err != nil {
		return err
	}
	if strings.HasPrefix(c.CodifyDir+"/", c.Source+"/") {
		return fmt.Errorf("the codify directory %s must be outside the project", c.CodifyDir)
	}
	if c.MaxIterations <= 0 {
		c.MaxIterations = 8
	}
	if c.RunTimeout <= 0 {
		c.RunTimeout = 30 * time.Minute
	}
	if c.Passthrough == nil {
		c.Passthrough = DefaultPassthrough
	}
	// Tunnelled, not forwarded: a forwarded HTTPS request is re-signed with codify's CA, which a
	// step that pins its own CA rejects.
	c.Passthrough = append(slices.Clone(c.Passthrough), c.LiveHosts...)
	if c.Out == nil {
		c.Out = io.Discard
	}
	switch c.Mode {
	case "":
		c.Mode = "strict"
	case "strict", "loose":
	default:
		return fmt.Errorf("mode must be strict or loose, not %q", c.Mode)
	}
	return nil
}

// Loose reports whether the run decomposes agent work rather than keeping outputs identical.
func (c *Config) Loose() bool { return c.Mode == "loose" }

func (c *Config) printf(format string, args ...any) { fmt.Fprintf(c.Out, format, args...) }

// progress reports a phase of an iteration as it starts or ends; an iteration takes minutes.
func (c *Config) progress(it, format string, args ...any) {
	c.printf("%s: %s\n", filepath.Base(it), fmt.Sprintf(format, args...))
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func fileList(outputs map[string][]byte) string {
	names := make([]string, 0, len(outputs))
	for n := range outputs {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "no files"
	}
	return strings.Join(names, ", ")
}

// writeDocs puts the reference docs and the step-type schema where the retro agent can read them.
func writeDocs(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	err := fs.WalkDir(docs.Reference, "reference", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := docs.Reference.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, filepath.Base(path)), data, 0o644)
	})
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(schema.Build(), "", "  ")
	return os.WriteFile(filepath.Join(dir, "schema.json"), data, 0o644)
}

// setup checks the target, writes the docs and starts the recording proxy.
func (c *Config) setup() (agents []string, docsDir string, proxy *Proxy, err error) {
	if err := c.defaults(); err != nil {
		return nil, "", nil, err
	}
	p, err := LoadProject(c.Source, c.Workflow, c.Fixed...)
	if err != nil {
		return nil, "", nil, err
	}
	docsDir = filepath.Join(c.CodifyDir, "docs")
	if err := writeDocs(docsDir); err != nil {
		return nil, "", nil, err
	}
	rec, err := OpenRecording(filepath.Join(c.CodifyDir, "proxy", "recording"))
	if err != nil {
		return nil, "", nil, err
	}
	proxy, err = StartProxy(filepath.Join(c.CodifyDir, "proxy"), rec, c.Passthrough)
	if err != nil {
		return nil, "", nil, err
	}
	if c.proxyHook != nil {
		c.proxyHook(proxy)
	}
	c.em = newEmitter(c.Callbacks)
	return p.AgentSteps(), docsDir, proxy, nil
}

// Pass runs one codification iteration (the codify step type): it records it in metrics.jsonl
// and returns its row, and promotes a passing candidate into the project.
func Pass(ctx context.Context, c Config) (Row, error) {
	_, docsDir, proxy, err := c.setup()
	if err != nil {
		return Row{}, err
	}
	defer proxy.Close()
	row, cost, _, err := c.iterate(ctx, proxy, docsDir)
	row.SpentUSD = cost
	if err != nil {
		return row, err
	}
	return row, appendRow(filepath.Join(c.CodifyDir, "metrics.jsonl"), row)
}

// Run codifies the workflow until nothing is left, it is out of iterations or budget, or
// candidates keep failing.
func Run(ctx context.Context, c Config) (rows []Row, err error) {
	agents, docsDir, proxy, err := c.setup()
	if err != nil {
		return nil, err
	}
	defer proxy.Close()
	start := time.Now()
	c.em.runStarted(c.Workflow, map[string]any{"codify": c.Workflow, "codify_dir": c.CodifyDir, "model": c.Model, "max_iterations": c.MaxIterations, "budget_usd": c.BudgetUSD})
	defer func() { c.em.runFinished(c.Workflow, start, err) }()
	if len(agents) == 0 {
		c.printf("%s has no agent steps (claude, llm_invoke) in reach: nothing to codify\n", c.Workflow)
		return nil, nil
	}

	spent, failures := 0.0, 0
	for i := 0; i < c.MaxIterations; i++ {
		row, cost, stop, err := c.iterate(ctx, proxy, docsDir)
		spent += cost
		row.SpentUSD = spent
		if ctx.Err() != nil {
			// Stopped (the IDE's Stop, Ctrl-C). A candidate is installed only after it passed; if the
			// stop came before that, the iteration is recorded as stopped and the project is as it was.
			if row.Iteration != "" {
				if row.Promoted == nil {
					row.Validation = Validation{Status: "stopped", Reasons: []string{"stopped before the iteration finished"}, ChangedFiles: row.Validation.ChangedFiles}
				}
				rows = append(rows, row)
				_ = appendRow(filepath.Join(c.CodifyDir, "metrics.jsonl"), row)
				c.printRow(row)
			}
			c.printf("stopped\n")
			return rows, nil
		}
		if err != nil {
			return rows, err
		}
		rows = append(rows, row)
		if err := appendRow(filepath.Join(c.CodifyDir, "metrics.jsonl"), row); err != nil {
			return rows, err
		}
		c.printRow(row)
		if row.Validation.Status == "passed" || row.Validation.Status == "no_change" {
			failures = 0
		} else {
			failures++
		}
		switch {
		case stop != "":
			c.printf("stopping: %s\n", stop)
			return rows, nil
		case failures >= 3:
			c.printf("stopping: three candidates in a row were rejected\n")
			return rows, nil
		case c.BudgetUSD > 0 && spent >= c.BudgetUSD:
			c.printf("stopping: spent $%.3f of the $%.2f budget\n", spent, c.BudgetUSD)
			return rows, nil
		}
	}
	c.printf("stopping: %d iterations done\n", c.MaxIterations)
	return rows, nil
}

func (c *Config) printRow(r Row) {
	v := r.Validation
	result := v.Status
	if r.Promoted != nil {
		result = "promoted " + *r.Promoted
	}
	if v.Judged {
		result += " (judged)"
	}
	if len(v.Reasons) > 0 {
		result += ": " + firstLine(v.Reasons[0])
	}
	job, _ := r.Retro["extracted"].(string)
	c.printf("%s  agent %d/%d steps  %d tokens  $%.4f  |  retro $%.3f %q  |  %s  |  spent $%.3f\n",
		r.Iteration, r.Task.AgentSteps, r.Task.AgentSteps+r.Task.NativeSteps, r.Task.Tokens, r.Task.CostUSD,
		num(r.Retro["cost_usd"]), job, result, r.SpentUSD)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func appendRow(path string, r Row) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(r)
	_, err = f.Write(append(b, '\n'))
	return err
}

// nextIteration creates codifyDir/iter-NNNN.
func nextIteration(dir string) (string, error) {
	matches, _ := filepath.Glob(filepath.Join(dir, "iter-*"))
	path := filepath.Join(dir, fmt.Sprintf("iter-%04d", len(matches)+1))
	return path, os.MkdirAll(path, 0o755)
}

func versions(dir string) []string {
	matches, _ := filepath.Glob(filepath.Join(dir, "versions", "v[0-9][0-9][0-9][0-9]"))
	sort.Strings(matches)
	return matches
}

// iterate is one pass. It returns the row, the model spend, and a reason to stop.
func (c *Config) iterate(ctx context.Context, proxy *Proxy, docsDir string) (Row, float64, string, error) {
	row := Row{Time: time.Now().Format(time.RFC3339), Mode: c.Mode, Retro: map[string]any{}}
	p, err := LoadProject(c.Source, c.Workflow, c.Fixed...)
	if err != nil {
		return row, 0, "", err
	}
	it, err := nextIteration(c.CodifyDir)
	if err != nil {
		return row, 0, "", err
	}
	row.Iteration = filepath.Base(it)

	// 1. The current version, live, recording what it fetches.
	rec, err := OpenRecording(filepath.Join(it, "recording"))
	if err != nil {
		return row, 0, "", err
	}
	proxy.SetRecording(rec)
	proxy.SetMode(Record)
	c.progress(it, "running %s (live, recording its requests)…", c.Workflow)
	ph := c.em.begin("live", "", map[string]any{"workflow": c.Workflow, "iteration": row.Iteration})
	live := runWorkflow(ctx, c.Markov, c.Source, c.Workflow, filepath.Join(it, "live"), proxy.Env(), c.RunTimeout, func(raw []byte) { c.em.forward(raw, "live") })
	row.Task = live.Metrics
	ph.end(map[string]any{"agent_steps": live.Metrics.AgentSteps, "native_steps": live.Metrics.NativeSteps, "tokens": live.Metrics.Tokens, "cost_usd": live.Metrics.CostUSD, "outputs": fileList(live.Outputs), "requests": len(rec.Exchanges())}, errText(live.Err))
	c.progress(it, "live run done in %.0fs: agent %d/%d steps, %d tokens, $%.4f; wrote %s; %d requests recorded",
		live.Metrics.Seconds, live.Metrics.AgentSteps, live.Metrics.AgentSteps+live.Metrics.NativeSteps, live.Metrics.Tokens, live.Metrics.CostUSD,
		fileList(live.Outputs), len(rec.Exchanges()))
	spent := live.Metrics.CostUSD
	if live.Err != nil {
		row.Validation = Validation{Status: "live_failed", Reasons: []string{"the current version failed: " + firstLine(live.Err.Error()) + "\n" + live.Log}}
		return row, spent, "the current version of " + c.Workflow + " fails; fix it first (see " + filepath.Join(it, "live", "output.log") + ")", nil
	}
	if len(live.Outputs) == 0 {
		// Candidates are judged by their outputs; with none, any change would "match".
		row.Validation = Validation{Status: "live_failed", Reasons: []string{"the current version wrote no files in its working directory"}}
		return row, spent, c.Workflow + " wrote no files in the directory it runs in, so there is nothing to keep equal; make it write its result there (relative paths; claude steps need tools such as Write, and permission_mode acceptEdits)", nil
	}
	if c.RepeatChanges != "" {
		path, key := c.repeatSpec()
		if _, err := repeatCount(live.Outputs, path, key); err != nil {
			row.Validation = Validation{Status: "live_failed", Reasons: []string{"--repeat-changes: " + err.Error()}}
			return row, spent, "--repeat-changes names " + c.RepeatChanges + ", but the current version's outputs do not have it (" + err.Error() + ")", nil
		}
	}
	agents := p.AgentSteps()
	if len(agents) == 0 {
		row.Validation = Validation{Status: "no_change", Reasons: []string{"no agent steps left"}}
		return row, spent, "no agent steps left in " + c.Workflow, nil
	}

	// 2. Evidence for the retro agent.
	evidence := filepath.Join(it, "evidence")
	if err := c.collectEvidence(live, rec, evidence, it); err != nil {
		return row, spent, "", err
	}

	// 3. The retro agent edits a copy of the project.
	candidate := filepath.Join(c.CodifyDir, "candidate")
	if err := CopyTree(c.Source, candidate); err != nil {
		return row, spent, "", err
	}
	retroJSON := filepath.Join(it, "retro.json")
	in := RetroInputs{
		Target: c.Workflow, Evidence: evidence, Docs: docsDir, Markov: c.Markov, RetroJSON: retroJSON,
		EditableFiles: p.EditableFiles(), HelperPrefix: p.HelperPrefix(), ScriptsDir: p.ScriptsDir(),
		AgentSteps: agents, Keep: c.Keep, Fixed: p.Fixed, Judged: true, Loose: c.Loose(),
	}
	in.RepeatChanges, _ = c.repeatSpec()
	model := c.Model
	if model == "" {
		model = "the default model"
	}
	c.progress(it, "retro agent (%s) proposing one change to %s…", model, c.Workflow)
	ph = c.em.begin("retro", "claude", map[string]any{"model": c.Model, "workflow": c.Workflow, "iteration": row.Iteration})
	retro := c.retro(ctx, in, candidate)
	ph.end(map[string]any{"result": retro.Report, "total_cost_usd": retro.CostUSD, "tokens": retro.Tokens}, retro.Failed)
	spent += retro.CostUSD
	if job, _ := retro.Report["extracted"].(string); job != "" {
		c.progress(it, "retro done ($%.3f): %s", retro.CostUSD, job)
	} else {
		c.progress(it, "retro done ($%.3f)", retro.CostUSD)
	}
	for k, v := range retro.Report {
		row.Retro[k] = v
	}
	row.Retro["cost_usd"] = retro.CostUSD
	row.Retro["tokens"] = retro.Tokens
	if retro.Failed != "" {
		row.Retro["failed"] = retro.Failed
	}

	// 4. Deterministic checks, then the candidate against the recording.
	v, cand, judgeCost := c.validate(ctx, p, candidate, retro, live, proxy, it)
	// Loose mode is not done while one agent step still does several judgment jobs: the retro
	// agent's own job list says so. Count the pass as failed so the run goes on.
	if c.Loose() && v.Status == "no_change" {
		if jobs := judgmentJobsWithAgent(retro.Report); len(jobs) >= 2 {
			v.Status = "failed"
			v.Reasons = []string{fmt.Sprintf("no change, but the agent still does %d judgment jobs (%s); in loose mode split one into an atomic agent step", len(jobs), strings.Join(jobs, "; "))}
		}
	}
	spent += judgeCost
	row.Validation = v
	if cand != nil {
		row.Validation.Candidate = &cand.Metrics
		spent += cand.Metrics.CostUSD
	}
	writeJSON(filepath.Join(it, "validation.json"), row.Validation)
	switch v.Status {
	case "no_change":
		return row, spent, "the retro agent found nothing more to codify", nil
	case "passed":
		ph := c.em.begin("promote", "", map[string]any{"workflow": c.Workflow})
		version, err := c.promote(p, candidate)
		if err != nil {
			ph.end(nil, err.Error())
			return row, spent, "", err
		}
		ph.end(map[string]any{"version": version, "files": row.Validation.ChangedFiles}, "")
		row.Promoted = &version
	}
	return row, spent, "", nil
}

func (c *Config) validate(ctx context.Context, p *Project, candidate string, retro Retro, live *RunResult, proxy *Proxy, it string) (Validation, *RunResult, float64) {
	fail := func(reasons ...string) Validation { return Validation{Status: "failed", Reasons: reasons} }
	if retro.Failed != "" && retro.Report == nil {
		return fail("the retro agent failed: " + retro.Failed), nil, 0
	}
	before, err := Files(c.Source)
	if err != nil {
		return fail(err.Error()), nil, 0
	}
	after, err := Files(candidate)
	if err != nil {
		return fail(err.Error()), nil, 0
	}
	changed := Changes(before, after)
	var protected, scripts []string
	for _, rel := range changed {
		if !p.Editable(rel) {
			protected = append(protected, rel)
		} else if strings.HasPrefix(rel, p.ScriptsDir()) {
			if _, ok := after[rel]; ok {
				scripts = append(scripts, rel)
			}
		}
	}
	v := Validation{ChangedFiles: changed}
	switch {
	case len(protected) > 0:
		reason := "files outside " + c.Workflow + " changed: " + strings.Join(protected, ", ")
		if fixed := p.FixedFiles(protected); len(fixed) > 0 {
			reason = "fixed workflows changed (they set up and measure each run): " + strings.Join(fixed, ", ") + "; " + reason
		}
		v.Status, v.Reasons = "failed", []string{reason}
		return v, nil, 0
	case len(changed) == 0:
		v.Status = "no_change"
		return v, nil, 0
	case len(scripts) > 0 && retro.Report["script_justification"] == nil:
		v.Status, v.Reasons = "failed", []string{"scripts added or changed without a script_justification: " + strings.Join(scripts, ", ")}
		return v, nil, 0
	}
	if out, err := exec.CommandContext(ctx, c.Markov, "validate", candidate).CombinedOutput(); err != nil {
		v.Status, v.Reasons = "failed", []string{"markov validate: " + errorLines(string(out))}
		return v, nil, 0
	}
	if len(c.Keep) > 0 {
		cp, err := LoadProject(candidate, c.Workflow, c.Fixed...)
		if err != nil {
			v.Status, v.Reasons = "failed", []string{err.Error()}
			return v, nil, 0
		}
		if missing := missingKept(c.Keep, cp.AgentSteps()); len(missing) > 0 {
			v.Status, v.Reasons = "failed", []string{"steps to keep as agents were codified: " + strings.Join(missing, ", ")}
			return v, nil, 0
		}
	}

	proxy.SetMode(Replay)
	c.progress(it, "running the candidate against the recording (changed: %s)…", strings.Join(changed, ", "))
	ph := c.em.begin("candidate", "", map[string]any{"workflow": c.Workflow, "changed": changed})
	cand := runWorkflow(ctx, c.Markov, candidate, c.Workflow, filepath.Join(it, "candidate"), proxy.Env(), c.RunTimeout, func(raw []byte) { c.em.forward(raw, "candidate") })
	ph.end(map[string]any{"agent_steps": cand.Metrics.AgentSteps, "native_steps": cand.Metrics.NativeSteps, "tokens": cand.Metrics.Tokens, "cost_usd": cand.Metrics.CostUSD, "outputs": fileList(cand.Outputs), "misses": proxy.Misses()}, errText(cand.Err))
	if misses := proxy.Misses(); len(misses) > 0 {
		v.Reasons = append(v.Reasons, "requests not in the recording: "+strings.Join(misses, ", "))
	}
	if refused := proxy.Refused(); len(refused) > 0 {
		v.Reasons = append(v.Reasons, "side effects refused in replay: "+strings.Join(refused, ", "))
	}
	if cand.Err != nil {
		v.Status = "failed"
		v.Reasons = append(v.Reasons, "the candidate failed: "+firstLine(cand.Err.Error())+"\n"+errorLines(cand.Log))
		return v, cand, 0
	}
	// Loose mode: every atomic decision must have answered in its schema.
	if c.Loose() {
		if misses := StructuredAnswerMisses(LoadEvents(filepath.Join(cand.Dir, "events.jsonl"))); len(misses) > 0 {
			v.Status = "failed"
			v.Reasons = append(v.Reasons, "agent steps given a json_schema returned no structured answer: "+strings.Join(misses, ", "))
			return v, cand, 0
		}
	}
	repeat, reason := c.repeatCheck(live.Outputs, cand.Outputs)
	v.Repeat = repeat
	if reason != "" {
		v.Status = "failed"
		v.Reasons = append(v.Reasons, reason)
		return v, cand, 0
	}
	same, why := SameOutputs(live.Outputs, cand.Outputs)
	if same {
		v.Status = "passed"
		return v, cand, 0
	}
	if len(live.Outputs) == 0 || len(cand.Outputs) == 0 {
		v.Status, v.Reasons = "failed", append(v.Reasons, why)
		return v, cand, 0
	}
	// The outputs differ and the current version's came from an agent: compare content.
	c.progress(it, "outputs differ (%s); judging their content…", why)
	v.Judged = true
	ph = c.em.begin("judge", "claude", map[string]any{"model": c.Model, "difference": why})
	var candidatePrompts []string
	if cp, err := LoadProject(candidate, c.Workflow, c.Fixed...); err == nil {
		candidatePrompts = agentPrompts(cp.File, cp.Reach)
	}
	verdict := c.judge(ctx, c.Workflow, agentPrompts(p.File, p.Reach), candidatePrompts, filepath.Join(live.Dir, "work"), filepath.Join(cand.Dir, "work"), filepath.Join(it, "judge", "verdict.json"), repeat)
	v.JudgeReason = verdict.Reason
	ph.end(map[string]any{"equivalent": verdict.Equivalent, "reason": verdict.Reason, "total_cost_usd": verdict.CostUSD, "tokens": verdict.Tokens}, verdict.Failed)
	switch {
	case verdict.Failed != "":
		v.Status, v.Reasons = "failed", append(v.Reasons, "the judge failed: "+verdict.Failed)
	case verdict.Equivalent:
		v.Status = "passed"
	default:
		v.Status, v.Reasons = "failed", append(v.Reasons, "the judge found a difference: "+verdict.Reason)
	}
	return v, cand, verdict.CostUSD
}

// missingKept lists kept steps ("step" or "workflow.step") that are no longer agent steps.
func missingKept(keep, agents []string) []string {
	var out []string
	for _, k := range keep {
		found := false
		for _, a := range agents {
			if a == k || strings.HasSuffix(a, "."+k) {
				found = true
			}
		}
		if !found {
			out = append(out, k)
		}
	}
	return out
}

// errorLines keeps the error lines of a failed markov command, not its usage text.
func errorLines(out string) string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "Error:") || strings.Contains(l, " failed") || strings.Contains(l, "failed:") {
			keep = append(keep, l)
		}
	}
	if len(keep) == 0 {
		return tail(strings.TrimSpace(out), 1500)
	}
	return tail(strings.Join(keep, "\n"), 1500)
}

func (c *Config) promote(p *Project, candidate string) (string, error) {
	vdir := filepath.Join(c.CodifyDir, "versions")
	if len(versions(c.CodifyDir)) == 0 {
		if err := p.Archive(filepath.Join(vdir, "v0000")); err != nil {
			return "", err
		}
	}
	if _, err := p.Install(candidate); err != nil {
		return "", err
	}
	name := fmt.Sprintf("v%04d", len(versions(c.CodifyDir)))
	return name, p.Archive(filepath.Join(vdir, name))
}

func (c *Config) collectEvidence(live *RunResult, rec *Recording, dir, it string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if data, err := os.ReadFile(filepath.Join(live.Dir, "events.jsonl")); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "events.jsonl"), data, 0o644)
	}
	Transcripts(live.Metrics.Sessions, filepath.Join(dir, "transcripts"))
	for rel, data := range live.Outputs {
		if err := writeFile(filepath.Join(dir, "outputs", filepath.FromSlash(rel)), data, "", ""); err != nil {
			return err
		}
	}
	writeJSON(filepath.Join(dir, "metrics.json"), live.Metrics)
	_ = os.WriteFile(filepath.Join(dir, "agent_calls.md"), []byte(agentCallsDoc(agentCalls(LoadEvents(filepath.Join(live.Dir, "events.jsonl"))))), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "requests.md"), []byte(requestsDoc(rec)), 0o644)
	// Every earlier pass: what it tried, in which mode, and why it was rejected.
	if rows, err := readRows(filepath.Join(c.CodifyDir, "metrics.jsonl")); err == nil && len(rows) > 0 {
		_ = os.WriteFile(filepath.Join(dir, "history.md"), []byte(historyDoc(rows)), 0o644)
	}
	// What the previous pass tried and how it was judged.
	if n := filepath.Base(it); strings.HasPrefix(n, "iter-") {
		var k int
		fmt.Sscanf(n, "iter-%d", &k)
		prev := filepath.Join(filepath.Dir(it), fmt.Sprintf("iter-%04d", k-1))
		for src, dst := range map[string]string{"retro.json": "previous_retro.json", "validation.json": "previous_validation.json"} {
			if data, err := os.ReadFile(filepath.Join(prev, src)); err == nil {
				_ = os.WriteFile(filepath.Join(dir, dst), data, 0o644)
			}
		}
	}
	return nil
}

// requestsDoc lists what the live run fetched, with the start of each response.
func requestsDoc(rec *Recording) string {
	var b strings.Builder
	b.WriteString("# Requests the run made\n\nThe candidate runs against this recording; use these URLs exactly.\n\n")
	ex := rec.Exchanges()
	if len(ex) == 0 {
		b.WriteString("(none)\n")
	}
	for _, e := range ex {
		fmt.Fprintf(&b, "## %s %s\n\n%d, %d bytes, %s\n\n", e.Method, e.URL, e.Status, e.Size, e.Header.Get("Content-Type"))
		if _, body, ok := rec.get(e.Method, e.URL); ok {
			s := string(body)
			if len(s) > 600 {
				s = s[:600] + "…"
			}
			fmt.Fprintf(&b, "```\n%s\n```\n\n", s)
		}
	}
	return b.String()
}

func writeJSON(path string, v any) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	b, _ := json.MarshalIndent(v, "", "  ")
	_ = os.WriteFile(path, append(b, '\n'), 0o644)
}

// judgmentJobsWithAgent lists the jobs a retro report marks as needing judgment and still done
// by the broad agent step.
func judgmentJobsWithAgent(report map[string]any) []string {
	jobs, _ := report["jobs"].([]any)
	var out []string
	for _, j := range jobs {
		m, _ := j.(map[string]any)
		if m["judgment"] == true && m["where"] == "agent" {
			name, _ := m["job"].(string)
			out = append(out, name)
		}
	}
	return out
}

// readRows reads metrics.jsonl.
func readRows(path string) ([]Row, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []Row
	for _, line := range strings.Split(string(data), "\n") {
		var r Row
		if strings.TrimSpace(line) != "" && json.Unmarshal([]byte(line), &r) == nil {
			rows = append(rows, r)
		}
	}
	return rows, nil
}

// historyDoc summarizes every earlier pass for the retro agent: the move, the mode it was judged
// in, and the outcome with its reasons. A strict-mode rejection over agent-decided values says
// nothing about the same move in loose mode.
func historyDoc(rows []Row) string {
	var b strings.Builder
	b.WriteString("# Earlier passes\n\nOldest first. `mode` is how the candidate was judged (strict: same outputs; loose: same actions on the same items, agent-decided values only valid). Rows written before modes existed were strict.\n\n")
	for _, r := range rows {
		mode := r.Mode
		if mode == "" {
			mode = "strict"
		}
		extracted, _ := r.Retro["extracted"].(string)
		move, _ := r.Retro["move"].(string)
		fmt.Fprintf(&b, "## %s (%s)\n\n- tried: %s", r.Iteration, mode, orNone(extracted))
		if move != "" {
			fmt.Fprintf(&b, " (move: %s)", move)
		}
		fmt.Fprintf(&b, "\n- outcome: %s", r.Validation.Status)
		if r.Promoted != nil {
			fmt.Fprintf(&b, ", promoted %s", *r.Promoted)
		}
		b.WriteString("\n")
		if rp := r.Validation.Repeat; rp != nil {
			fmt.Fprintf(&b, "- repeat run changed (%s): current %v, candidate %v\n", rp.File, rp.Live, rp.Candidate)
		}
		for _, reason := range r.Validation.Reasons {
			if len(reason) > 700 {
				reason = reason[:700] + "…"
			}
			fmt.Fprintf(&b, "- reason: %s\n", strings.ReplaceAll(reason, "\n", " "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func orNone(s string) string {
	if s == "" {
		return "(nothing)"
	}
	return s
}
