package codify

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
}

// repeatCheck compares the current version's and a candidate's repeat-run records. A candidate
// may not change more on the repeat than the current version: it would be redoing work. The
// reason is empty when the candidate passes.
func (c *Config) repeatCheck(live, cand map[string][]byte) (*Repeat, string) {
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
	if r.Candidate > r.Live {
		return r, fmt.Sprintf("running the candidate again changed %v thing(s) (%s), the current version %v: a candidate may not redo more work than the current version; check what is already done before acting", r.Candidate, path, r.Live)
	}
	return r, ""
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
