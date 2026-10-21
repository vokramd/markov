package codify

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// RunResult is one run of the target workflow in a fresh working directory.
type RunResult struct {
	Dir     string // the run's directory: work/ (its outputs), events.jsonl, output.log
	Err     error
	Log     string            // the end of the child's output, for failures
	Outputs map[string][]byte // files the run created in work/
	Metrics Metrics
}

// Metrics summarizes a run's events.
type Metrics struct {
	AgentSteps  int      `json:"agent_steps"`
	NativeSteps int      `json:"native_steps"`
	Tokens      int      `json:"tokens"`
	Turns       int      `json:"turns"`
	CostUSD     float64  `json:"cost_usd"`
	Seconds     float64  `json:"wall_seconds"`
	Sessions    []string `json:"sessions,omitempty"`
	Failed      bool     `json:"failed"`
}

// Coverage is the share of step executions that did not run a model.
func (m Metrics) Coverage() float64 {
	if n := m.AgentSteps + m.NativeSteps; n > 0 {
		return float64(m.NativeSteps) / float64(n)
	}
	return 0
}

// RunWorkflow runs workflow from source as a child markov process. The process starts in
// dir/work, an empty directory, so the files it writes there are its outputs; env is added to
// its environment (the recording proxy).
func RunWorkflow(ctx context.Context, markov, source, workflow, dir string, env []string, timeout time.Duration) *RunResult {
	return runWorkflow(ctx, markov, source, workflow, dir, env, timeout, nil)
}

// runWorkflow is RunWorkflow that also hands each event to onEvent as the child writes it.
func runWorkflow(ctx context.Context, markov, source, workflow, dir string, env []string, timeout time.Duration, onEvent func([]byte)) *RunResult {
	r := &RunResult{Dir: dir}
	work := filepath.Join(dir, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		r.Err = err
		return r
	}
	events := filepath.Join(dir, "events.jsonl")
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, markov, "run", source, "--workflow", workflow,
		"--state-store", filepath.Join(dir, "state.db"), "--callback", "jsonl://"+events)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), env...)
	// On a stop, ask the child to stop (it cancels its steps, which stop their own processes)
	// rather than killing it outright, which would orphan a running claude.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second
	var stop chan struct{}
	var followed sync.WaitGroup
	if onEvent != nil {
		stop = make(chan struct{})
		followed.Add(1)
		go func() {
			defer followed.Done()
			follow(events, stop, onEvent)
		}()
	}
	out, err := cmd.CombinedOutput()
	if stop != nil {
		close(stop)
		followed.Wait()
	}
	_ = os.WriteFile(filepath.Join(dir, "output.log"), out, 0o644)
	r.Err = err
	r.Log = tail(string(out), 3000)
	r.Outputs, _ = Files(work)
	r.Metrics = Summarize(LoadEvents(events))
	return r
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// LoadEvents reads a jsonl event log.
func LoadEvents(path string) []map[string]any {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 64<<20)
	for sc.Scan() {
		var ev map[string]any
		if json.Unmarshal(sc.Bytes(), &ev) == nil {
			out = append(out, ev)
		}
	}
	return out
}

// Summarize counts a run's agent and native step executions and what the agents used.
func Summarize(events []map[string]any) Metrics {
	var m Metrics
	var first, last time.Time
	for _, ev := range events {
		if ts, err := time.Parse(time.RFC3339Nano, str(ev["timestamp"])); err == nil {
			if first.IsZero() {
				first = ts
			}
			last = ts
		}
		switch str(ev["event_type"]) {
		case "run_failed":
			m.Failed = true
		case "step_completed", "step_failed":
			ty := str(ev["step_type"])
			if ty == "" || ty == "workflow" {
				continue
			}
			out, _ := ev["output"].(map[string]any)
			if agentTypes[ty] {
				m.AgentSteps++
				m.Tokens += int(num(out["tokens"]))
				m.Turns += int(num(out["num_turns"]))
				m.CostUSD += num(out["total_cost_usd"])
				if sid := str(out["session_id"]); sid != "" {
					m.Sessions = append(m.Sessions, sid)
				}
			} else {
				m.NativeSteps++
			}
		}
	}
	if !first.IsZero() {
		m.Seconds = last.Sub(first).Seconds()
	}
	return m
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}

// Transcripts copies the Claude Code transcripts of the given sessions into dir.
func Transcripts(sessions []string, dir string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	for _, sid := range sessions {
		matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", sid+".jsonl"))
		for _, m := range matches {
			if data, err := os.ReadFile(m); err == nil {
				_ = os.MkdirAll(dir, 0o755)
				_ = os.WriteFile(filepath.Join(dir, sid+".jsonl"), data, 0o644)
			}
		}
	}
}

// SameOutputs reports whether two runs produced identical files, and a reason when not.
func SameOutputs(want, got map[string][]byte) (bool, string) {
	for rel, w := range want {
		g, ok := got[rel]
		if !ok {
			return false, "missing output " + rel
		}
		if string(g) != string(w) {
			return false, "output " + rel + " differs: " + firstDiff(string(w), string(g))
		}
	}
	for rel := range got {
		if _, ok := want[rel]; !ok {
			return false, "unexpected output " + rel
		}
	}
	return true, ""
}

func firstDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return "line " + strconv.Itoa(i+1) + ": want " + quote(w) + ", got " + quote(g)
		}
	}
	return "(trailing whitespace)"
}

func quote(s string) string {
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	b, _ := json.Marshal(s)
	return string(b)
}
