package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jctanner/markov/pkg/callback"
	"github.com/jctanner/markov/pkg/parser"
)

// The debugger pauses a run in memory at breakpoints and accepts commands while paused. See
// docs/reference/debugging.md and ADR-0005.

const (
	sectionSteps  = "steps"
	sectionRescue = "rescue"
	sectionAlways = "always"

	phaseBefore = "before"
	phaseAfter  = "after"

	// maxVarsBytes caps the variables sent with a pause so a huge context cannot flood a callback.
	maxVarsBytes = 256 * 1024
	// maxControlLine is the longest control command accepted (a set_breakpoints with many entries).
	maxControlLine = 4 << 20
)

// Breakpoint is a structured step selector. Workflow and Step are required; the rest narrow or
// extend the match. Names are never parsed out of a string, so any characters are safe.
type Breakpoint struct {
	Workflow string `json:"workflow"`
	Step     string `json:"step"`
	// Section is steps (default), rescue, or always.
	Section string `json:"section,omitempty"`
	// Iteration limits the breakpoint to one for_each key. Without it the breakpoint applies to the
	// step as a whole (a for_each step pauses once, before the loop starts).
	Iteration string `json:"iteration,omitempty"`
	// Phase is before (default, the step is about to run) or after (it just completed).
	Phase string `json:"phase,omitempty"`
	// When is a condition, in the same expression syntax as a step's `when`, evaluated against the
	// variables the step sees. Conditions must be side-effect free (Jev functions are unavailable).
	When string `json:"when,omitempty"`
	// Skip ignores the first N hits.
	Skip int `json:"skip,omitempty"`
	// Log turns the breakpoint into a logpoint: the rendered template is reported and the run continues.
	Log string `json:"log,omitempty"`

	hits int
}

// DebugCommand is one JSON line on the control channel.
type DebugCommand struct {
	Cmd         string       `json:"cmd"`
	Breakpoints []Breakpoint `json:"breakpoints,omitempty"`
	Workflow    string       `json:"workflow,omitempty"`
	Step        string       `json:"step,omitempty"`
	Section     string       `json:"section,omitempty"`
	Iteration   string       `json:"iteration,omitempty"`
	ID          string       `json:"id,omitempty"`
	Expression  string       `json:"expression,omitempty"`
	// Source is the workflow source a `reload` command loads instead of the step's own, or the one
	// a `request_reload` queues (the run's own when empty).
	Source string `json:"source,omitempty"`
}

// debugPoint says where the run is.
type debugPoint struct {
	runID     string
	workflow  string
	step      string
	stepType  string
	section   string
	iteration string
	phase     string
}

type pausedState struct {
	resume chan DebugCommand
	vars   map[string]any
	// reload marks a reload_workflow step waiting for `reload` or `reject_reload`.
	reload bool
}

// Debugger holds the breakpoints and the pause state for one engine.
type Debugger struct {
	mu       sync.Mutex
	file     *parser.WorkflowFile
	bps      []*Breakpoint
	stepMode bool
	until    *Breakpoint
	paused   *pausedState
	closed   bool
	runID    string
	cancel   context.CancelFunc
	engine   *Engine

	// queue lets one goroutine at a time hold a pause; others wait their turn.
	queue sync.Mutex
}

// NewDebugger creates a debugger for a parsed workflow file.
func NewDebugger(file *parser.WorkflowFile) *Debugger {
	return &Debugger{file: file}
}

// SetCancel gives the debugger a way to stop the run for a `terminate` command.
func (d *Debugger) SetCancel(cancel context.CancelFunc) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cancel = cancel
}

// SetStepMode makes the run pause before every step until a `continue` command.
func (d *Debugger) SetStepMode(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stepMode = on
}

// SetDebugger attaches a debugger; call before Run or Resume.
func (e *Engine) SetDebugger(d *Debugger) {
	e.debugger = d
	if d != nil {
		d.mu.Lock()
		d.engine = e
		d.mu.Unlock()
	}
}

// ---- resolving breakpoints against the workflow file ----

func (d *Debugger) currentFile() *parser.WorkflowFile {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.file
}

// rebind switches the debugger to a reloaded workflow file and resolves the breakpoints again:
// those whose step still exists keep their hit counts, the rest are dropped and reported.
func (d *Debugger) rebind(file *parser.WorkflowFile) {
	d.mu.Lock()
	d.file = file
	old := d.bps
	d.mu.Unlock()

	var list []*Breakpoint
	resolved := []Breakpoint{}
	var rejected []map[string]any
	for _, bp := range old {
		r, err := d.resolve(*bp)
		if err != nil {
			rejected = append(rejected, map[string]any{"breakpoint": *bp, "error": err.Error()})
			continue
		}
		r.hits = bp.hits
		c := r
		list = append(list, &c)
		resolved = append(resolved, r)
	}
	d.mu.Lock()
	d.bps = list
	d.mu.Unlock()
	data := map[string]any{"resolved": resolved, "reason": "workflow_reloaded"}
	if len(rejected) > 0 {
		data["unresolved"] = rejected
	}
	d.emit("", "", "", "breakpoints_set", data)
}

func normalizeSection(s string) (string, error) {
	switch s {
	case "", sectionSteps:
		return sectionSteps, nil
	case sectionRescue, sectionAlways:
		return s, nil
	}
	return "", fmt.Errorf("unknown section %q (use steps, rescue, or always)", s)
}

func normalizePhase(p string) (string, error) {
	switch p {
	case "", phaseBefore:
		return phaseBefore, nil
	case phaseAfter:
		return phaseAfter, nil
	}
	return "", fmt.Errorf("unknown phase %q (use before or after)", p)
}

func sectionList(wf *parser.Workflow, section string) []parser.Step {
	switch section {
	case sectionRescue:
		return wf.Rescue
	case sectionAlways:
		return wf.Always
	}
	return wf.Steps
}

func stepNames(steps []parser.Step) []string {
	names := make([]string, 0, len(steps))
	for _, s := range steps {
		names = append(names, s.Name)
	}
	return names
}

// resolve validates one breakpoint against the file and fills in defaults.
func (d *Debugger) resolve(bp Breakpoint) (Breakpoint, error) {
	var err error
	if bp.Section, err = normalizeSection(bp.Section); err != nil {
		return bp, err
	}
	if bp.Phase, err = normalizePhase(bp.Phase); err != nil {
		return bp, err
	}
	if bp.Workflow == "" || bp.Step == "" {
		return bp, errors.New("a breakpoint needs a workflow and a step")
	}
	if bp.Skip < 0 {
		return bp, errors.New("skip cannot be negative")
	}
	file := d.currentFile()
	wf := file.GetWorkflow(bp.Workflow)
	if wf == nil {
		names := make([]string, 0, len(file.Workflows))
		for _, w := range file.Workflows {
			names = append(names, w.Name)
		}
		return bp, fmt.Errorf("unknown workflow %q (workflows: %s)", bp.Workflow, quoteList(names))
	}
	steps := sectionList(wf, bp.Section)
	for _, s := range steps {
		if s.Name == bp.Step {
			return bp, nil
		}
	}
	return bp, fmt.Errorf("workflow %q has no step %q in %s (steps: %s)", bp.Workflow, bp.Step, bp.Section, quoteList(stepNames(steps)))
}

func quoteList(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}

// ResolveShorthand turns "workflow.step" into a Breakpoint without splitting on a delimiter:
// every real workflow name is tried as a prefix and the remainder must be one of its steps.
// No match, or more than one, is an error (the structured form is unambiguous).
func (d *Debugger) ResolveShorthand(s string) (Breakpoint, error) {
	file := d.currentFile()
	var found []Breakpoint
	for _, w := range file.Workflows {
		prefix := w.Name + "."
		if !strings.HasPrefix(s, prefix) {
			continue
		}
		rest := strings.TrimPrefix(s, prefix)
		for _, section := range []string{sectionSteps, sectionRescue, sectionAlways} {
			for _, st := range sectionList(&w, section) {
				if st.Name == rest {
					found = append(found, Breakpoint{Workflow: w.Name, Step: rest, Section: section})
				}
			}
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		var all []string
		for _, w := range file.Workflows {
			for _, section := range []string{sectionSteps, sectionRescue, sectionAlways} {
				for _, st := range sectionList(&w, section) {
					all = append(all, w.Name+"."+st.Name)
				}
			}
		}
		sort.Strings(all)
		return Breakpoint{}, fmt.Errorf("--break %q matches no workflow.step (known: %s)", s, quoteList(all))
	}
	var opts []string
	for _, b := range found {
		opts = append(opts, fmt.Sprintf(`{"workflow":%q,"step":%q}`, b.Workflow, b.Step))
	}
	return Breakpoint{}, fmt.Errorf("--break %q is ambiguous; use --breakpoint with one of: %s", s, strings.Join(opts, " "))
}

// SetBreakpoints replaces the breakpoint list. It returns the accepted (normalized) breakpoints
// and a description of each rejected one.
func (d *Debugger) SetBreakpoints(in []Breakpoint) (resolved []Breakpoint, rejected []map[string]any) {
	var list []*Breakpoint
	for _, bp := range in {
		r, err := d.resolve(bp)
		if err != nil {
			rejected = append(rejected, map[string]any{"breakpoint": bp, "error": err.Error()})
			continue
		}
		r.hits = 0
		c := r
		list = append(list, &c)
		resolved = append(resolved, r)
	}
	d.mu.Lock()
	d.bps = list
	d.mu.Unlock()
	return resolved, rejected
}

// ---- events ----

func (d *Debugger) emit(runID, workflow, step, kind string, data map[string]any) {
	d.mu.Lock()
	e := d.engine
	if runID == "" {
		runID = d.runID
	}
	d.mu.Unlock()
	if e == nil {
		return
	}
	e.fireEvent(func(cb callback.Callback) error {
		return cb.OnDebug(callback.DebugEvent{
			EventHeader:  callback.EventHeader{Timestamp: time.Now(), RunID: runID, EventType: "debug"},
			WorkflowName: workflow,
			StepName:     step,
			Kind:         kind,
			Data:         data,
		})
	})
}

func (d *Debugger) noteRun(runID string) {
	d.mu.Lock()
	d.runID = runID
	d.mu.Unlock()
}

// ---- matching ----

// splitStateName recovers the section and for_each iteration from a step's state name. The step
// name is known, so this is unambiguous even when names contain brackets or slashes.
func splitStateName(stateName, stepName string) (section, iteration string) {
	section = sectionSteps
	rest := stateName
	for _, sec := range []string{sectionRescue, sectionAlways} {
		if strings.HasPrefix(rest, sec+"/") {
			section = sec
			rest = strings.TrimPrefix(rest, sec+"/")
			break
		}
	}
	if strings.HasPrefix(rest, stepName+"[") && strings.HasSuffix(rest, "]") {
		iteration = rest[len(stepName)+1 : len(rest)-1]
	}
	return section, iteration
}

func (b *Breakpoint) matches(p debugPoint) bool {
	return b.Workflow == p.workflow && b.Step == p.step && b.Section == p.section && b.Phase == p.phase && b.Iteration == p.iteration
}

func snapshotVars(m map[string]any) (out map[string]any) {
	defer func() {
		if r := recover(); r != nil {
			out = map[string]any{"_error": fmt.Sprintf("variables unavailable: %v", r)}
		}
	}()
	b, err := json.Marshal(m)
	if err != nil {
		return map[string]any{"_error": "variables unavailable: " + err.Error()}
	}
	if len(b) > maxVarsBytes {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return map[string]any{"_truncated": true, "_size_bytes": len(b), "_keys": keys}
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		return map[string]any{"_error": err.Error()}
	}
	return v
}

// match decides whether the run should pause at p. It evaluates conditions, counts hits, and
// reports logpoints (which never pause). reason is empty when the run should carry on.
func (d *Debugger) match(e *Engine, p debugPoint, vars map[string]any) (reason string, data map[string]any, logs []map[string]any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return "", nil, nil
	}
	if d.until != nil && d.until.matches(p) {
		d.until = nil
		return "until", nil, logs
	}
	for _, bp := range d.bps {
		if !bp.matches(p) {
			continue
		}
		if bp.When != "" {
			ok, err := e.tmpl.EvalBool(bp.When, vars)
			if err != nil {
				// A condition that cannot be evaluated pauses and says why, rather than silently never firing.
				return "condition_error", map[string]any{"condition": bp.When, "error": err.Error()}, logs
			}
			if !ok {
				continue
			}
		}
		bp.hits++
		if bp.hits <= bp.Skip {
			continue
		}
		if bp.Log != "" {
			msg, err := e.tmpl.Render(bp.Log, vars)
			ld := map[string]any{"message": msg, "phase": p.phase, "section": p.section, "iteration": p.iteration}
			if err != nil {
				ld["error"] = err.Error()
			}
			logs = append(logs, ld)
			continue
		}
		return "breakpoint", map[string]any{"hits": bp.hits}, logs
	}
	if d.stepMode && p.phase == phaseBefore {
		return "step", nil, logs
	}
	return "", nil, logs
}

var errTerminated = errors.New("run terminated from the debugger")

// pause blocks the calling step until a command arrives.
func (d *Debugger) pause(ctx context.Context, p debugPoint, vars, output map[string]any, reason string, extra map[string]any) error {
	d.queue.Lock()
	defer d.queue.Unlock()

	st := &pausedState{resume: make(chan DebugCommand, 1), vars: vars}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	if reason == "step" && !d.stepMode {
		// Another pause finished with `continue` while this step waited its turn: honour it.
		d.mu.Unlock()
		return nil
	}
	d.paused = st
	d.stepMode = false
	d.mu.Unlock()

	data := map[string]any{
		"reason":    reason,
		"phase":     p.phase,
		"section":   p.section,
		"iteration": p.iteration,
		"step_type": p.stepType,
		"variables": snapshotVars(vars),
	}
	if output != nil {
		data["output"] = output
	}
	for k, v := range extra {
		data[k] = v
	}
	label := p.workflow + "." + p.step
	if p.iteration != "" {
		label += "[" + p.iteration + "]"
	}
	log.Printf("[run:%s] paused %s %s (%s); send continue, step, until or terminate on the control channel", p.runID, p.phase, label, reason)
	d.emit(p.runID, p.workflow, p.step, "paused", data)

	var cmd DebugCommand
	select {
	case cmd = <-st.resume:
	case <-ctx.Done():
		d.mu.Lock()
		d.paused = nil
		d.mu.Unlock()
		return ctx.Err()
	}

	d.mu.Lock()
	d.paused = nil
	switch cmd.Cmd {
	case "continue":
		d.stepMode = false
	case "step":
		d.stepMode = true
	case "until":
		bp := Breakpoint{Workflow: cmd.Workflow, Step: cmd.Step, Section: cmd.Section, Iteration: cmd.Iteration, Phase: phaseBefore}
		d.until = &bp
		d.stepMode = false
	}
	d.mu.Unlock()

	switch cmd.Cmd {
	case "terminate":
		d.emit(p.runID, p.workflow, p.step, "resumed", map[string]any{"command": "terminate"})
		return errTerminated
	case "__abort":
		return errors.New("debugger control channel closed while the run was paused")
	}
	d.emit(p.runID, p.workflow, p.step, "resumed", map[string]any{"command": cmd.Cmd})
	return nil
}

// controlOpen reports whether a client is attached to the control channel.
func (d *Debugger) controlOpen() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.closed
}

// awaitReload holds a reload_workflow step until the control channel answers `reload` (optionally
// with another source) or `reject_reload`. data describes the pending reload.
func (d *Debugger) awaitReload(ctx context.Context, p debugPoint, data map[string]any) (DebugCommand, error) {
	d.queue.Lock()
	defer d.queue.Unlock()

	st := &pausedState{resume: make(chan DebugCommand, 1), reload: true}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return DebugCommand{Cmd: "reload"}, nil
	}
	d.paused = st
	d.mu.Unlock()

	log.Printf("[run:%s] reload pending at %s.%s; send reload or reject_reload on the control channel", p.runID, p.workflow, p.step)
	d.emit(p.runID, p.workflow, p.step, "reload_pending", data)

	var cmd DebugCommand
	select {
	case cmd = <-st.resume:
	case <-ctx.Done():
		d.mu.Lock()
		d.paused = nil
		d.mu.Unlock()
		return cmd, ctx.Err()
	}
	d.mu.Lock()
	d.paused = nil
	d.mu.Unlock()

	switch cmd.Cmd {
	case "terminate":
		d.emit(p.runID, p.workflow, p.step, "resumed", map[string]any{"command": "terminate"})
		return cmd, errTerminated
	case "__abort":
		return cmd, errors.New("debugger control channel closed while a reload was pending")
	}
	d.emit(p.runID, p.workflow, p.step, "resumed", map[string]any{"command": cmd.Cmd})
	return cmd, nil
}

// debugPause is the engine's hook: call at a step boundary. It returns an error only when the
// run should stop (terminate, or the control channel closing while paused).
func (e *Engine) debugPause(ctx context.Context, runID, workflowName string, step parser.Step, stateName, phase string, runCtx, output map[string]any) error {
	d := e.debugger
	if d == nil {
		return nil
	}
	section, iteration := splitStateName(stateName, step.Name)
	p := debugPoint{runID: runID, workflow: workflowName, step: step.Name, stepType: step.Type, section: section, iteration: iteration, phase: phase}
	reason, extra, logs := d.match(e, p, runCtx)
	for _, l := range logs {
		d.emit(runID, workflowName, step.Name, "logpoint", l)
	}
	if reason == "" {
		return nil
	}
	return d.pause(ctx, p, runCtx, output, reason, extra)
}

// ---- control channel ----

// Serve reads JSON commands, one per line, until the reader closes. Closing while a step is paused
// aborts the run (it stays resumable); closing while running just switches debugging off.
func (d *Debugger) Serve(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxControlLine)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		d.Handle(line)
	}
	d.closeControl()
}

func (d *Debugger) closeControl() {
	d.mu.Lock()
	if !d.closed && (len(d.bps) > 0 || d.stepMode) {
		log.Printf("debugger control channel closed; breakpoints are disabled")
	}
	d.closed = true
	d.bps = nil
	d.stepMode = false
	d.until = nil
	p := d.paused
	d.mu.Unlock()
	if p != nil {
		select {
		case p.resume <- DebugCommand{Cmd: "__abort"}:
		default:
		}
	}
}

// Handle runs one control command.
func (d *Debugger) Handle(line string) {
	var cmd DebugCommand
	if err := json.Unmarshal([]byte(line), &cmd); err != nil {
		d.emit("", "", "", "error", map[string]any{"error": "invalid control command: " + err.Error(), "line": line})
		return
	}
	switch cmd.Cmd {
	case "set_breakpoints":
		resolved, rejected := d.SetBreakpoints(cmd.Breakpoints)
		data := map[string]any{"resolved": resolved}
		if len(rejected) > 0 {
			data["unresolved"] = rejected
		}
		d.emit("", "", "", "breakpoints_set", data)
	case "continue", "step", "until", "terminate", "reload", "reject_reload":
		if cmd.Cmd == "until" {
			if _, err := d.resolve(Breakpoint{Workflow: cmd.Workflow, Step: cmd.Step, Section: cmd.Section}); err != nil {
				d.emit("", "", "", "error", map[string]any{"error": err.Error(), "command": cmd.Cmd})
				return
			}
		}
		d.dispatch(cmd)
	case "request_reload":
		d.mu.Lock()
		e := d.engine
		d.mu.Unlock()
		if e == nil {
			d.emit("", "", "", "error", map[string]any{"error": "no run to reload", "command": cmd.Cmd})
			return
		}
		kind, data := e.requestReload(cmd.Source)
		d.emit("", "", "", kind, data)
	case "evaluate":
		d.evaluate(cmd)
	default:
		d.emit("", "", "", "error", map[string]any{"error": fmt.Sprintf("unknown command %q", cmd.Cmd)})
	}
}

// dispatch hands a resume-type command to the paused step, or applies it to a running one.
func (d *Debugger) dispatch(cmd DebugCommand) {
	d.mu.Lock()
	p := d.paused
	cancel := d.cancel
	isReload := cmd.Cmd == "reload" || cmd.Cmd == "reject_reload"
	waiting := p != nil && p.reload
	if cmd.Cmd != "terminate" && isReload != waiting {
		d.mu.Unlock()
		msg := "no reload is pending"
		if waiting {
			msg = "a reload is pending: send reload, reject_reload or terminate"
		}
		d.emit("", "", "", "error", map[string]any{"error": msg, "command": cmd.Cmd})
		return
	}
	if p == nil {
		switch cmd.Cmd {
		case "continue":
			d.stepMode = false
		case "step":
			d.stepMode = true
		case "until":
			bp := Breakpoint{Workflow: cmd.Workflow, Step: cmd.Step, Section: cmd.Section, Iteration: cmd.Iteration, Phase: phaseBefore}
			if bp.Section == "" {
				bp.Section = sectionSteps
			}
			d.until = &bp
		}
	}
	d.mu.Unlock()
	if p == nil {
		if cmd.Cmd == "terminate" && cancel != nil {
			cancel()
		}
		return
	}
	if cmd.Section == "" {
		cmd.Section = sectionSteps
	}
	select {
	case p.resume <- cmd:
	default: // a command is already pending for this pause
	}
}

func (d *Debugger) evaluate(cmd DebugCommand) {
	d.mu.Lock()
	p := d.paused
	e := d.engine
	d.mu.Unlock()
	data := map[string]any{"id": cmd.ID, "expression": cmd.Expression}
	switch {
	case p == nil || e == nil:
		data["error"] = "the run is not paused"
	default:
		expr := cmd.Expression
		if !strings.Contains(expr, "{{") {
			expr = "{{ " + expr + " }}"
		}
		out, err := safeRender(e, expr, p.vars)
		if err != nil {
			data["error"] = err.Error()
		} else {
			data["result"] = out
		}
	}
	d.emit("", "", "", "evaluated", data)
}

func safeRender(e *Engine, expr string, vars map[string]any) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("evaluating: %v", r)
		}
	}()
	return e.tmpl.Render(expr, vars)
}
