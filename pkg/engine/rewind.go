package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/jctanner/markov/pkg/callback"
	"github.com/jctanner/markov/pkg/parser"
	"github.com/jctanner/markov/pkg/state"
)

// Definition hashes and rewinding a resumed run. A completed step is skipped on resume because
// its row says `completed`; the hash saved with the row records which definition produced it, so
// resume can report a completed step that was edited since, and --rewind can re-run from there.
// See docs/reference/state-store.md and plan 001.

// RewindTarget names a step of the resumed run's entrypoint workflow to re-run from.
type RewindTarget struct {
	Workflow string `json:"workflow"`
	Step     string `json:"step"`
}

// ChangedStep is a completed step whose definition differs from the one that ran.
type ChangedStep struct {
	RunID     string
	Workflow  string
	StateName string
	Stored    string
	Current   string
}

// definitionHash fingerprints everything that defines what a step does: its parsed fields and
// the custom step type it resolves to. The description is documentation, so editing it does not
// count as a change.
func (e *Engine) definitionHash(step parser.Step) string {
	return definitionHashIn(e.current(), step)
}

func definitionHashIn(file *parser.WorkflowFile, step parser.Step) string {
	step.Description = ""
	payload := struct {
		Step parser.Step
		Type *parser.StepType
	}{Step: step}
	if st, ok := file.StepTypes[step.Type]; ok {
		payload.Type = &st
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func sectionOfStepList(wf *parser.Workflow, section string) []parser.Step {
	switch section {
	case sectionRescue:
		return wf.Rescue
	case sectionAlways:
		return wf.Always
	}
	return wf.Steps
}

// findStep resolves a state name (`name`, `name[key]`, `rescue/name`, ...) to the step that
// produced it. The longest matching step name wins, so a step called `a` and one called `a[b]`
// do not confuse each other.
func (e *Engine) findStep(workflow, stateName string) (step parser.Step, section string, ok bool) {
	return findStepIn(e.current(), workflow, stateName)
}

func findStepIn(file *parser.WorkflowFile, workflow, stateName string) (step parser.Step, section string, ok bool) {
	wf := file.GetWorkflow(workflow)
	if wf == nil || strings.HasPrefix(stateName, "__jev__/") {
		return parser.Step{}, "", false
	}
	best := -1
	for _, sec := range []string{sectionSteps, sectionRescue, sectionAlways} {
		rest := stateName
		if sec != sectionSteps {
			if !strings.HasPrefix(stateName, sec+"/") {
				continue
			}
			rest = strings.TrimPrefix(stateName, sec+"/")
		}
		for _, st := range sectionOfStepList(wf, sec) {
			match := rest == st.Name || (strings.HasPrefix(rest, st.Name+"[") && strings.HasSuffix(rest, "]"))
			if match && len(st.Name) > best {
				best, step, section, ok = len(st.Name), st, sec, true
			}
		}
	}
	return step, section, ok
}

// hashingStore stamps every saved step row with the hash of its definition, as the run that saved
// it saw that definition (the context carries the workflow file it is pinned to).
type hashingStore struct {
	state.Store
	e *Engine
}

func (h *hashingStore) SaveStep(ctx context.Context, s *state.StepResult) error {
	if s.DefinitionHash == "" {
		file := h.e.fileFor(ctx)
		if st, _, ok := findStepIn(file, s.WorkflowName, s.StepName); ok {
			s.DefinitionHash = definitionHashIn(file, st)
		}
	}
	return h.Store.SaveStep(ctx, s)
}

// changedSteps lists completed steps of a run, and of its sub-runs, whose definition changed.
// Rows saved before hashes existed have no hash and are never reported.
func (e *Engine) changedSteps(ctx context.Context, runID string) ([]ChangedStep, error) {
	steps, err := e.store.GetSteps(ctx, runID)
	if err != nil {
		return nil, err
	}
	var out []ChangedStep
	for _, s := range steps {
		if s.Status != state.StepCompleted || s.DefinitionHash == "" {
			continue
		}
		st, _, ok := e.findStep(s.WorkflowName, s.StepName)
		if !ok {
			continue
		}
		if cur := e.definitionHash(st); cur != s.DefinitionHash {
			out = append(out, ChangedStep{RunID: runID, Workflow: s.WorkflowName, StateName: s.StepName, Stored: s.DefinitionHash, Current: cur})
		}
	}
	children, err := e.store.GetChildRuns(ctx, runID)
	if err != nil {
		return nil, err
	}
	for _, c := range children {
		sub, err := e.changedSteps(ctx, c.RunID)
		if err != nil {
			return nil, err
		}
		out = append(out, sub...)
	}
	return out, nil
}

func baseStepName(e *Engine, c ChangedStep) string {
	if st, _, ok := e.findStep(c.Workflow, c.StateName); ok {
		return st.Name
	}
	return c.StateName
}

// rootTarget maps a changed step anywhere in the run tree to the step of the entrypoint
// workflow that has to re-run for it to run again: itself, or the call that leads to it.
func (e *Engine) rootTarget(ctx context.Context, root *state.Run, c ChangedStep) (RewindTarget, error) {
	if c.RunID == root.RunID {
		return RewindTarget{Workflow: root.Entrypoint, Step: baseStepName(e, c)}, nil
	}
	r, err := e.store.GetRun(ctx, c.RunID)
	if err != nil {
		return RewindTarget{}, err
	}
	for r.ParentRunID != root.RunID {
		if r.ParentRunID == "" {
			return RewindTarget{}, fmt.Errorf("run %q is not part of run %q", c.RunID, root.RunID)
		}
		if r, err = e.store.GetRun(ctx, r.ParentRunID); err != nil {
			return RewindTarget{}, err
		}
	}
	return RewindTarget{Workflow: root.Entrypoint, Step: r.ParentStep}, nil
}

// prepareResume reports completed steps whose definition changed and applies any rewind. It
// returns what changed (for the resumed event) and whether the run was rewound.
func (e *Engine) prepareResume(ctx context.Context, run *state.Run) ([]ChangedStep, bool, error) {
	changed, err := e.changedSteps(ctx, run.RunID)
	if err != nil {
		return nil, false, err
	}
	targets := append([]RewindTarget(nil), e.Rewind...)
	if e.RewindChanged {
		for _, c := range changed {
			t, err := e.rootTarget(ctx, run, c)
			if err != nil {
				return nil, false, err
			}
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		for _, c := range changed {
			log.Printf("[run:%s] step %q in workflow %q was completed with a different definition and will be reused as it ran; use --rewind-changed to run it again", run.RunID, c.StateName, c.Workflow)
		}
		return changed, false, nil
	}
	wf := e.current().GetWorkflow(run.Entrypoint)
	if wf == nil {
		return nil, false, fmt.Errorf("workflow %q not found", run.Entrypoint)
	}
	from := len(wf.Steps)
	for _, t := range targets {
		i, err := e.rewindIndex(wf, t)
		if err != nil {
			return nil, false, err
		}
		if i < from {
			from = i
		}
	}
	n, err := e.rewindFrom(ctx, run, wf, from)
	if err != nil {
		return nil, false, err
	}
	log.Printf("[run:%s] rewinding to step %q: %d step record(s) cleared, they will run again", run.RunID, wf.Steps[from].Name, n)
	return changed, true, nil
}

// rewindIndex finds the target step's position among the entrypoint workflow's steps.
func (e *Engine) rewindIndex(wf *parser.Workflow, t RewindTarget) (int, error) {
	if t.Workflow != wf.Name {
		var callers []string
		for _, s := range wf.Steps {
			if s.Workflow == t.Workflow {
				callers = append(callers, fmt.Sprintf("%q", s.Name))
			}
		}
		hint := fmt.Sprintf("rewind to a step of the entrypoint workflow %q", wf.Name)
		if len(callers) > 0 {
			hint += ", for example the step that calls it: " + strings.Join(callers, ", ")
		}
		return 0, fmt.Errorf("cannot rewind to step %q of workflow %q: it runs inside the run's sub-workflows; %s", t.Step, t.Workflow, hint)
	}
	for i, s := range wf.Steps {
		if s.Name == t.Step {
			return i, nil
		}
	}
	for _, sec := range []string{sectionRescue, sectionAlways} {
		for _, s := range sectionOfStepList(wf, sec) {
			if s.Name == t.Step {
				return 0, fmt.Errorf("step %q is a %s step; rescue and always steps run every time and cannot be a rewind target", t.Step, sec)
			}
		}
	}
	names := stepNames(wf.Steps)
	return 0, fmt.Errorf("workflow %q has no step %q (steps: %s)", wf.Name, t.Step, quoteList(names))
}

// rewindFrom clears the step records of wf.Steps[from:] (including for_each iterations) and,
// for steps that call sub-workflows, every record of the sub-runs they started.
func (e *Engine) rewindFrom(ctx context.Context, run *state.Run, wf *parser.Workflow, from int) (int, error) {
	rewound := map[string]bool{}
	for _, s := range wf.Steps[from:] {
		rewound[s.Name] = true
	}
	rows, err := e.store.GetSteps(ctx, run.RunID)
	if err != nil {
		return 0, err
	}
	var names []string
	for _, r := range rows {
		if st, sec, ok := e.findStep(run.Entrypoint, r.StepName); ok && sec == sectionSteps && rewound[st.Name] {
			names = append(names, r.StepName)
		}
	}
	sort.Strings(names)
	if err := e.store.DeleteSteps(ctx, run.RunID, names); err != nil {
		return 0, err
	}
	children, err := e.store.GetChildRuns(ctx, run.RunID)
	if err != nil {
		return 0, err
	}
	cleared := len(names)
	for _, c := range children {
		if rewound[c.ParentStep] {
			n, err := e.clearRun(ctx, c.RunID)
			if err != nil {
				return 0, err
			}
			cleared += n
		}
	}
	return cleared, nil
}

// clearRun deletes every step record of a run and of the runs below it.
func (e *Engine) clearRun(ctx context.Context, runID string) (int, error) {
	rows, err := e.store.GetSteps(ctx, runID)
	if err != nil {
		return 0, err
	}
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.StepName)
	}
	if err := e.store.DeleteSteps(ctx, runID, names); err != nil {
		return 0, err
	}
	n := len(names)
	children, err := e.store.GetChildRuns(ctx, runID)
	if err != nil {
		return 0, err
	}
	for _, c := range children {
		m, err := e.clearRun(ctx, c.RunID)
		if err != nil {
			return 0, err
		}
		n += m
	}
	return n, nil
}

func changedStepEvents(e *Engine, changed []ChangedStep) []callback.ChangedStepInfo {
	var out []callback.ChangedStepInfo
	for _, c := range changed {
		out = append(out, callback.ChangedStepInfo{RunID: c.RunID, Workflow: c.Workflow, Step: baseStepName(e, c)})
	}
	return out
}
