package callback

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
)

// TTYCallback shows the live progress of running steps in a terminal. It renders the
// step_progress kinds it is told to show, each in a style:
//
//	text   written verbatim, so streamed pieces join up (a reply's text)
//	dim    the same, dimmed on a terminal (a model's thinking)
//	lines  each event is one line, written dimmed with a newline (a command's output)
//	count  a status line counting the characters received so far, redrawn in place and
//	       cleared when anything else is written or the step ends; terminal only
//
// The default is text:text, thinking:dim, output:lines. When the kind changes within a step, a
// blank line separates the two. When a step that wrote text ends, a newline finishes its last
// line. Every other event is ignored.
type TTYCallback struct {
	w      io.Writer
	closer io.Closer
	// terminal says whether the output is a terminal: dimming and count lines need one.
	terminal bool
	styles   map[string]string
	mu       sync.Mutex
	// open is the step (run, workflow, step) whose text does not yet end with a newline.
	open string
	// last is the step and kind of the last piece written, and how many newlines (up to 2) it
	// ended with.
	lastStep, lastKind string
	lastNewlines       int
	// status is the step and kind whose count line is on screen, and the counts so far.
	status string
	counts map[string]int
}

const (
	ansiDim       = "\033[2m"
	ansiReset     = "\033[0m"
	ansiClearLine = "\r\033[K"
)

var ttyStyles = []string{"text", "dim", "lines", "count"}

func defaultTTYStyles() map[string]string {
	return map[string]string{"text": "text", "thinking": "dim", "output": "lines"}
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// NewTTYCallback parses what follows tty://: a target ("" or "tty" for the controlling terminal,
// /dev/tty; "stdout"; "stderr"), optionally followed by ?show=kind:style,kind:style,... A kind
// without a style is shown as text.
func NewTTYCallback(spec string) (*TTYCallback, error) {
	target, query, _ := strings.Cut(spec, "?")
	styles := defaultTTYStyles()
	if query != "" {
		values, err := url.ParseQuery(query)
		if err != nil {
			return nil, fmt.Errorf("tty callback: %w", err)
		}
		for key := range values {
			if key != "show" {
				return nil, fmt.Errorf("tty callback: unknown setting %q (supported: show)", key)
			}
		}
		styles, err = parseTTYShow(values.Get("show"))
		if err != nil {
			return nil, err
		}
	}
	var t *TTYCallback
	switch target {
	case "", "tty":
		f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
		if err != nil {
			return nil, fmt.Errorf("tty callback: opening /dev/tty: %w", err)
		}
		t = &TTYCallback{w: f, closer: f, terminal: true}
	case "stdout":
		t = &TTYCallback{w: os.Stdout, terminal: isTerminal(os.Stdout)}
	case "stderr":
		t = &TTYCallback{w: os.Stderr, terminal: isTerminal(os.Stderr)}
	default:
		return nil, fmt.Errorf("tty callback: unknown target %q (supported: tty://, tty://stdout, tty://stderr)", target)
	}
	t.styles = styles
	return t, nil
}

func parseTTYShow(show string) (map[string]string, error) {
	styles := map[string]string{}
	for _, item := range strings.Split(show, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		kind, style, hasStyle := strings.Cut(item, ":")
		if !hasStyle {
			style = "text"
		}
		if kind == "" || !slices.Contains(ttyStyles, style) {
			return nil, fmt.Errorf("tty callback: bad show item %q (want kind:style, style one of %s)", item, strings.Join(ttyStyles, ", "))
		}
		styles[kind] = style
	}
	return styles, nil
}

func stepKey(runID, workflow, step string) string {
	return runID + "\x00" + workflow + "\x00" + step
}

func (t *TTYCallback) style(kind string) string {
	if t.styles == nil {
		return defaultTTYStyles()[kind]
	}
	return t.styles[kind]
}

func (t *TTYCallback) OnStepProgress(event StepProgressEvent) error {
	style := t.style(event.Kind)
	if style == "" {
		return nil
	}
	text, _ := event.Data["text"].(string)
	if text == "" && style != "lines" {
		return nil
	}
	key := stepKey(event.RunID, event.WorkflowName, event.StepName)
	t.mu.Lock()
	defer t.mu.Unlock()
	if style == "count" {
		return t.drawCount(key, event.Kind, len([]rune(text)))
	}

	out := strings.Builder{}
	if t.status != "" {
		out.WriteString(ansiClearLine)
		t.status = ""
	}
	if key == t.lastStep && event.Kind != t.lastKind {
		out.WriteString(strings.Repeat("\n", 2-t.lastNewlines))
	} else if style == "lines" && t.open != "" {
		out.WriteString("\n") // a line starts on a line of its own
	}
	written := text
	if style == "lines" {
		written = strings.TrimRight(text, "\n") + "\n"
	}
	if t.terminal && (style == "dim" || style == "lines") {
		out.WriteString(ansiDim + strings.TrimSuffix(written, "\n") + ansiReset)
		if strings.HasSuffix(written, "\n") {
			out.WriteString("\n")
		}
	} else {
		out.WriteString(written)
	}
	if _, err := io.WriteString(t.w, out.String()); err != nil {
		return err
	}
	t.lastStep, t.lastKind = key, event.Kind
	if trimmed := strings.TrimRight(written, "\n"); trimmed == "" {
		t.lastNewlines = min(2, t.lastNewlines+len(written))
	} else {
		t.lastNewlines = min(2, len(written)-len(trimmed))
	}
	if strings.HasSuffix(written, "\n") {
		t.open = ""
	} else {
		t.open = key
	}
	return nil
}

// drawCount redraws the status line of a count-style kind. It needs a terminal to redraw in
// place, and a line of its own.
func (t *TTYCallback) drawCount(key, kind string, n int) error {
	if !t.terminal {
		return nil
	}
	if t.counts == nil {
		t.counts = map[string]int{}
	}
	id := key + "\x00" + kind
	t.counts[id] += n
	prefix := ansiClearLine
	if t.status == "" && t.open != "" {
		prefix = "\n" // leave the open line alone
		t.open, t.lastNewlines = "", 1
	}
	t.status = id
	label := strings.ReplaceAll(kind, "_", " ")
	_, err := fmt.Fprintf(t.w, "%s%s  … %s: %s chars%s", prefix, ansiDim, label, groupDigits(t.counts[id]), ansiReset)
	return err
}

func groupDigits(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// endStep clears a step's count line and finishes the last line of a step that wrote text.
func (t *TTYCallback) endStep(runID, workflow, step string) error {
	key := stepKey(runID, workflow, step)
	t.mu.Lock()
	defer t.mu.Unlock()
	out := ""
	if t.status != "" && strings.HasPrefix(t.status, key+"\x00") {
		out = ansiClearLine
		t.status = ""
	}
	for id := range t.counts {
		if strings.HasPrefix(id, key+"\x00") {
			delete(t.counts, id)
		}
	}
	if t.open != "" && t.open == key {
		out += "\n"
	}
	if t.lastStep == key {
		t.open, t.lastStep, t.lastKind, t.lastNewlines = "", "", "", 0
	}
	if out == "" {
		return nil
	}
	_, err := io.WriteString(t.w, out)
	return err
}

func (t *TTYCallback) OnStepCompleted(event StepCompletedEvent) error {
	return t.endStep(event.RunID, event.WorkflowName, event.StepName)
}

func (t *TTYCallback) OnStepFailed(event StepFailedEvent) error {
	return t.endStep(event.RunID, event.WorkflowName, event.StepName)
}

func (t *TTYCallback) OnRunStarted(RunStartedEvent) error             { return nil }
func (t *TTYCallback) OnRunCompleted(RunCompletedEvent) error         { return nil }
func (t *TTYCallback) OnRunFailed(RunFailedEvent) error               { return nil }
func (t *TTYCallback) OnRunPaused(RunPausedEvent) error               { return nil }
func (t *TTYCallback) OnRunResumed(RunResumedEvent) error             { return nil }
func (t *TTYCallback) OnStepStarted(StepStartedEvent) error           { return nil }
func (t *TTYCallback) OnStepSkipped(StepSkippedEvent) error           { return nil }
func (t *TTYCallback) OnDebug(DebugEvent) error                       { return nil }
func (t *TTYCallback) OnJobCreated(JobCreatedEvent) error             { return nil }
func (t *TTYCallback) OnGateEvaluated(GateEvaluatedEvent) error       { return nil }
func (t *TTYCallback) OnSubRunStarted(SubRunStartedEvent) error       { return nil }
func (t *TTYCallback) OnSubRunCompleted(SubRunCompletedEvent) error   { return nil }
func (t *TTYCallback) OnSubRunFailed(SubRunFailedEvent) error         { return nil }
func (t *TTYCallback) OnWorkflowReloaded(WorkflowReloadedEvent) error { return nil }

func (t *TTYCallback) Close() error {
	if t.closer == nil {
		return nil
	}
	return t.closer.Close()
}
