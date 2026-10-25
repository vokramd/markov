package codify

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jctanner/markov/pkg/outputlimit"
)

// defaultRepeatKey is where a repeat-run record keeps its change count when --repeat-changes
// names only a file.
const defaultRepeatKey = "counts.total"

// repeatSpec splits --repeat-changes ("path" or "path:key") into the output file and the dotted
// key of the number in it.
func (c *Config) repeatSpec() (path, key string) {
	path, key, ok := strings.Cut(c.RepeatChanges, ":")
	if !ok || key == "" {
		key = defaultRepeatKey
	}
	return path, key
}

// repeatCount is how many changes a run's repeat-run record says the repeat made.
func repeatCount(outputs map[string][]byte, path, key string) (float64, error) {
	data, ok := outputs[path]
	if !ok {
		return 0, fmt.Errorf("no output %s", path)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return 0, fmt.Errorf("%s is not JSON: %v", path, err)
	}
	for _, k := range strings.Split(key, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return 0, fmt.Errorf("%s has no %s", path, key)
		}
		if v, ok = m[k]; !ok {
			return 0, fmt.Errorf("%s has no %s", path, key)
		}
	}
	n, ok := v.(float64)
	if !ok {
		return 0, fmt.Errorf("%s: %s is not a number", path, key)
	}
	return n, nil
}

// Repeat is what running the workflow again right after it changed, for the current version
// and a candidate: a workflow that checks state before acting finds nothing left to do.
type Repeat struct {
	File      string  `json:"file"`
	Live      float64 `json:"live"`
	Candidate float64 `json:"candidate"`
	// Kinds splits the counts by kind of change (the numbers beside the total in the record),
	// when the pass declared which kinds its work makes.
	Kinds []RepeatKind `json:"kinds,omitempty"`
}

// RepeatKind is one kind of change in a repeat-run record (comments_added, reviews_added, ...):
// how many of it each version redid, and whether this pass's work makes that kind.
type RepeatKind struct {
	Kind      string  `json:"kind"`
	Live      float64 `json:"live"`
	Candidate float64 `json:"candidate"`
	Touched   bool    `json:"touched"`
}

// repeatKinds reads the counts beside the total in a repeat-run record: the numbers in the
// object that holds key (counts.total -> every number in counts but total). A kind the record
// leaves out counted none.
func repeatKinds(outputs map[string][]byte, path, key string) map[string]float64 {
	var v any
	if json.Unmarshal(outputs[path], &v) != nil {
		return nil
	}
	parts := strings.Split(key, ".")
	for _, k := range parts[:len(parts)-1] {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]float64{}
	for k, n := range m {
		if f, ok := n.(float64); ok && k != parts[len(parts)-1] {
			out[k] = f
		}
	}
	return out
}

// repeatCheck compares the current version's and a candidate's repeat-run records. A candidate
// may not change more on the repeat than the current version: it would be redoing work. When the
// pass declared the kinds of change its work makes (touched), only those kinds are held to that,
// one by one: repeats of other kinds come from work the pass did not change, whose agent varies
// from run to run, and are left to the judge. The reason is empty when the candidate passes.
func (c *Config) repeatCheck(live, cand map[string][]byte, touched []string) (*Repeat, string) {
	if c.RepeatChanges == "" {
		return nil, ""
	}
	path, key := c.repeatSpec()
	r := &Repeat{File: path}
	var err error
	if r.Live, err = repeatCount(live, path, key); err != nil {
		return nil, "the current version's repeat-run record: " + err.Error()
	}
	if r.Candidate, err = repeatCount(cand, path, key); err != nil {
		return r, "the candidate's repeat-run record: " + err.Error()
	}
	if len(touched) == 0 {
		if r.Candidate > r.Live {
			return r, fmt.Sprintf("running the candidate again changed %v thing(s) (%s), the current version %v: a candidate may not redo more work than the current version; check what is already done before acting", r.Candidate, path, r.Live)
		}
		return r, ""
	}
	r.Kinds = compareKinds(repeatKinds(live, path, key), repeatKinds(cand, path, key), touched)
	var worse []string
	for _, k := range r.Kinds {
		if k.Touched && k.Candidate > k.Live {
			worse = append(worse, fmt.Sprintf("%s %v (current version %v)", k.Kind, k.Candidate, k.Live))
		}
	}
	if len(worse) > 0 {
		return r, fmt.Sprintf("running the candidate again redid work of the kinds this pass changed: %s (%s); a candidate may not redo more of its own work than the current version; check what is already done before acting", strings.Join(worse, ", "), path)
	}
	return r, ""
}

// compareKinds lines up two records' kinds, the touched ones first.
func compareKinds(live, cand map[string]float64, touched []string) []RepeatKind {
	is := map[string]bool{}
	for _, t := range touched {
		is[t] = true
	}
	names := map[string]bool{}
	for k := range live {
		names[k] = true
	}
	for k := range cand {
		names[k] = true
	}
	for _, t := range touched {
		names[t] = true
	}
	var out []RepeatKind
	for k := range names {
		out = append(out, RepeatKind{Kind: k, Live: live[k], Candidate: cand[k], Touched: is[k]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Touched != out[j].Touched {
			return out[i].Touched
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// declaredKinds reads the kinds of change a pass says its work makes (retro.json repeat_kinds).
func declaredKinds(report map[string]any) []string {
	list, _ := report["repeat_kinds"].([]any)
	var out []string
	for _, v := range list {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// agentCall is one agent step call, from its step_completed event.
type agentCall struct {
	Step        string // workflow.step
	Run         string // the run (a for_each item's sub-run names its item)
	PromptChars int
	InputTokens int
	CostUSD     float64
	Turns       int
}

// agentCalls lists the agent calls in a run's events.
func agentCalls(events []map[string]any) []agentCall {
	var out []agentCall
	for _, ev := range events {
		if str(ev["event_type"]) != "step_completed" || !agentTypes[str(ev["step_type"])] {
			continue
		}
		o, ok := ev["output"].(map[string]any)
		if !ok {
			continue // a for_each step's closing event; each item's call had its own
		}
		u, _ := o["usage"].(map[string]any)
		out = append(out, agentCall{
			Step:        str(ev["workflow_name"]) + "." + str(ev["step_name"]),
			Run:         str(ev["run_id"]),
			PromptChars: int(num(o["prompt_chars"])),
			InputTokens: int(num(u["input_tokens"]) + num(u["cache_creation_input_tokens"]) + num(u["cache_read_input_tokens"])),
			CostUSD:     num(o["total_cost_usd"]),
			Turns:       int(num(o["num_turns"])),
		})
	}
	return out
}

// agentCallsDoc is the evidence file on how much each agent call was given: a step whose
// prompt carries a whole list of work items, rather than one item's data, shows up as one large
// call (or calls that grow with the data).
func agentCallsDoc(calls []agentCall) string {
	var b strings.Builder
	b.WriteString("# Agent calls\n\nEvery agent step call of the run: how big its rendered prompt was (characters), its input\n" +
		"tokens (cache included), turns and cost. A call that makes one decision for one item has a\n" +
		"prompt about the size of that item's data; a call given a whole list of work items has a\n" +
		"prompt that grows with the list.\n\n")
	if len(calls) == 0 {
		b.WriteString("No agent calls.\n")
		return b.String()
	}
	type group struct {
		calls        []agentCall
		chars, input []int
		cost         float64
	}
	groups := map[string]*group{}
	var order []string
	for _, c := range calls {
		g := groups[c.Step]
		if g == nil {
			g = &group{}
			groups[c.Step] = g
			order = append(order, c.Step)
		}
		g.calls = append(g.calls, c)
		g.chars = append(g.chars, c.PromptChars)
		g.input = append(g.input, c.InputTokens)
		g.cost += c.CostUSD
	}
	b.WriteString("| step | calls | prompt chars (min / median / max) | input tokens (max) | cost |\n|---|---|---|---|---|\n")
	for _, step := range order {
		g := groups[step]
		lo, mid, hi := spread(g.chars)
		_, _, inHi := spread(g.input)
		fmt.Fprintf(&b, "| %s | %d | %d / %d / %d | %d | $%.4f |\n", step, len(g.calls), lo, mid, hi, inHi, g.cost)
	}
	b.WriteString("\n## Each call\n\n")
	const most = 60
	for i, c := range calls {
		if i == most {
			fmt.Fprintf(&b, "... and %d more\n", len(calls)-most)
			break
		}
		fmt.Fprintf(&b, "- %s (run %s): %d prompt chars, %d input tokens, %d turns, $%.4f\n", c.Step, c.Run, c.PromptChars, c.InputTokens, c.Turns, c.CostUSD)
	}
	return b.String()
}

func spread(ns []int) (lo, mid, hi int) {
	s := append([]int(nil), ns...)
	sort.Ints(s)
	return s[0], s[len(s)/2], s[len(s)-1]
}

// stepOutput is the size of one step's output, from its step_completed event (or the summary
// that stands for one too large to keep).
type stepOutput struct {
	Step  string // workflow.step
	Bytes int64
}

// limitFailure is a step that failed for producing more than its max_output.
type limitFailure struct {
	Step    string
	Bytes   int64
	Limit   int64
	Stream  string
	Largest []outputlimit.Part
}

// outputSizes reads every step's output size, and the max_output failures, from a run's events.
func outputSizes(events []map[string]any) ([]stepOutput, []limitFailure) {
	var sizes []stepOutput
	var fails []limitFailure
	for _, ev := range events {
		step := str(ev["workflow_name"]) + "." + str(ev["step_name"])
		switch str(ev["event_type"]) {
		case "step_completed":
			if n := num(ev["oversized_event_bytes"]); n > 0 {
				sizes = append(sizes, stepOutput{step, int64(n)})
			} else if o, ok := ev["output"]; ok && o != nil {
				sizes = append(sizes, stepOutput{step, outputlimit.Measure(o, 0)})
			}
		case "step_failed":
			if str(ev["error_kind"]) != "max_output" {
				continue
			}
			f := limitFailure{Step: step, Bytes: int64(num(ev["output_bytes"])), Limit: int64(num(ev["limit_bytes"])), Stream: str(ev["stream"])}
			if parts, ok := ev["largest"].([]any); ok {
				for _, p := range parts {
					if m, ok := p.(map[string]any); ok {
						f.Largest = append(f.Largest, outputlimit.Part{Path: str(m["path"]), Bytes: int64(num(m["bytes"])), Items: int(num(m["items"]))})
					}
				}
			}
			fails = append(fails, f)
		}
	}
	return sizes, fails
}

// outputSizesDoc is the evidence file on how much each step produced. A step whose output grows
// with the data (a list bundled into one value, registers nested in registers) is the next thing
// to fix, before it reaches its limit on a bigger repository; one over its limit already failed.
func outputSizesDoc(sizes []stepOutput, fails []limitFailure) string {
	var b strings.Builder
	b.WriteString("# Output sizes\n\nHow much each step produced, as JSON. Every step's output is held in the run's context and\n" +
		"written to its event and state, so a step that produces a lot costs memory everywhere, and one\n" +
		"over its max_output (" + outputlimit.Format(outputlimit.Default) + " unless set) fails. Steps over a quarter of that are marked.\n\n")
	if len(fails) > 0 {
		b.WriteString("## Over the limit\n\n")
		for _, f := range fails {
			what := "output"
			if f.Stream != "" {
				what = f.Stream
			}
			fmt.Fprintf(&b, "- **%s**: %s reached %s, over its max_output %s; the step failed.\n", f.Step, what, outputlimit.Format(f.Bytes), outputlimit.Format(f.Limit))
			for _, p := range f.Largest {
				items := ""
				if p.Items > 0 {
					items = fmt.Sprintf(", %d items", p.Items)
				}
				fmt.Fprintf(&b, "  - %s: %s%s\n", p.Path, outputlimit.Format(p.Bytes), items)
			}
		}
		b.WriteString("\n")
	}
	if len(sizes) == 0 {
		b.WriteString("No step outputs.\n")
		return b.String()
	}
	type group struct {
		sizes []int
		total int64
	}
	groups := map[string]*group{}
	var order []string
	for _, s := range sizes {
		g := groups[s.Step]
		if g == nil {
			g = &group{}
			groups[s.Step] = g
			order = append(order, s.Step)
		}
		g.sizes = append(g.sizes, int(s.Bytes))
		g.total += s.Bytes
	}
	sort.SliceStable(order, func(i, j int) bool { return groups[order[i]].total > groups[order[j]].total })
	b.WriteString("| step | runs | size (min / median / max) | total |\n|---|---|---|---|\n")
	for _, step := range order {
		g := groups[step]
		lo, mid, hi := spread(g.sizes)
		mark := ""
		if int64(hi) > outputlimit.Default/4 {
			mark = " **(over a quarter of the limit)**"
		}
		fmt.Fprintf(&b, "| %s%s | %d | %s / %s / %s | %s |\n", step, mark, len(g.sizes),
			outputlimit.Format(int64(lo)), outputlimit.Format(int64(mid)), outputlimit.Format(int64(hi)), outputlimit.Format(g.total))
	}
	return b.String()
}

// liveLimitFailure says why the current version ran out of room, or "" when it did not: a step
// over its max_output, or the run killed for its memory.
func liveLimitFailure(live *RunResult, events []map[string]any) string {
	if live.Err == nil {
		return ""
	}
	if _, fails := outputSizes(events); len(fails) > 0 {
		f := fails[0]
		return fmt.Sprintf("step %s produced %s, over its max_output %s", f.Step, outputlimit.Format(f.Bytes), outputlimit.Format(f.Limit))
	}
	if msg := live.Err.Error(); strings.Contains(msg, "out of memory") {
		return "the run " + msg
	}
	return ""
}

// largestText lists a limit failure's biggest parts in one line.
func largestText(parts []outputlimit.Part) string {
	var out []string
	for i, p := range parts {
		if i == 4 {
			break
		}
		out = append(out, p.Path+" "+outputlimit.Format(p.Bytes))
	}
	if len(out) == 0 {
		return "not measured"
	}
	return strings.Join(out, ", ")
}
