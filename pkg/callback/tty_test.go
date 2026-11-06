package callback

import (
	"bytes"
	"fmt"
	"testing"
)

func TestTTYCallbackWritesStepTextAndEndsItsLine(t *testing.T) {
	var out bytes.Buffer
	tty := &TTYCallback{w: &out}
	progress := func(step, kind, text string) {
		tty.OnStepProgress(StepProgressEvent{EventHeader: EventHeader{RunID: "r"}, WorkflowName: "w", StepName: step, Kind: kind, Data: map[string]any{"text": text}})
	}
	progress("call", "text", "Hel")
	progress("call", "sse", "ignored")
	progress("call", "text", "lo")
	// another step ending leaves the open line alone
	tty.OnStepCompleted(StepCompletedEvent{EventHeader: EventHeader{RunID: "r"}, WorkflowName: "w", StepName: "other"})
	tty.OnStepCompleted(StepCompletedEvent{EventHeader: EventHeader{RunID: "r"}, WorkflowName: "w", StepName: "call"})
	// a step whose text already ends with a newline gets no extra one
	progress("next", "text", "done\n")
	tty.OnStepFailed(StepFailedEvent{EventHeader: EventHeader{RunID: "r"}, WorkflowName: "w", StepName: "next"})

	if got, want := out.String(), "Hello\ndone\n"; got != want {
		t.Fatalf("tty wrote %q, want %q", got, want)
	}
}

func TestTTYCallbackDimsThinkingAndSeparatesItFromText(t *testing.T) {
	var out bytes.Buffer
	tty := &TTYCallback{w: &out, terminal: true}
	progress := func(kind, text string) {
		tty.OnStepProgress(StepProgressEvent{EventHeader: EventHeader{RunID: "r"}, WorkflowName: "w", StepName: "call", Kind: kind, Data: map[string]any{"text": text}})
	}
	progress("thinking", "Let me ")
	progress("thinking", "see.")
	progress("text", "Hi")
	progress("text", "!")
	tty.OnStepCompleted(StepCompletedEvent{EventHeader: EventHeader{RunID: "r"}, WorkflowName: "w", StepName: "call"})

	want := ansiDim + "Let me " + ansiReset + ansiDim + "see." + ansiReset + "\n\nHi!\n"
	if got := out.String(); got != want {
		t.Fatalf("tty wrote %q, want %q", got, want)
	}

	out.Reset()
	tty.terminal = false
	progress("thinking", "plan\n")
	progress("text", "done")
	tty.OnStepCompleted(StepCompletedEvent{EventHeader: EventHeader{RunID: "r"}, WorkflowName: "w", StepName: "call"})
	if got, want := out.String(), "plan\n\ndone\n"; got != want {
		t.Fatalf("tty wrote %q, want %q", got, want)
	}

	// thinking that already ends in a blank line gets no more
	out.Reset()
	progress("thinking", "plan")
	progress("thinking", "\n\n")
	progress("text", "done")
	tty.OnStepCompleted(StepCompletedEvent{EventHeader: EventHeader{RunID: "r"}, WorkflowName: "w", StepName: "call"})
	if got, want := out.String(), "plan\n\ndone\n"; got != want {
		t.Fatalf("tty wrote %q, want %q", got, want)
	}
}

func TestParseTTYCallbackURL(t *testing.T) {
	cb, err := ParseCallbackURL("tty://stderr", nil, 0, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cb.(*TTYCallback); !ok {
		t.Fatalf("got %T, want *TTYCallback", cb)
	}
	if _, err := ParseCallbackURL("tty://printer", nil, 0, false, ""); err == nil {
		t.Fatal("unknown tty target accepted")
	}
}

func TestTTYCallbackLinesAndCountStyles(t *testing.T) {
	var out bytes.Buffer
	tty := &TTYCallback{w: &out, terminal: true, styles: map[string]string{"text": "text", "output": "lines", "tool_input": "count"}}
	progress := func(step, kind, text string) {
		tty.OnStepProgress(StepProgressEvent{EventHeader: EventHeader{RunID: "r"}, WorkflowName: "w", StepName: step, Kind: kind, Data: map[string]any{"text": text}})
	}
	end := func(step string) {
		tty.OnStepCompleted(StepCompletedEvent{EventHeader: EventHeader{RunID: "r"}, WorkflowName: "w", StepName: step})
	}
	progress("call", "text", "Sure")
	progress("call", "tool_input", `{"path":`)
	progress("call", "tool_input", `"a.txt"}`)
	end("call")
	progress("run", "output", "one")
	progress("run", "output", "two")
	progress("run", "sse", "ignored")
	end("run")

	want := "Sure" +
		"\n" + ansiDim + "  … tool input: 8 chars" + ansiReset +
		ansiClearLine + ansiDim + "  … tool input: 16 chars" + ansiReset +
		ansiClearLine +
		ansiDim + "one" + ansiReset + "\n" + ansiDim + "two" + ansiReset + "\n"
	if got := out.String(); got != want {
		t.Fatalf("tty wrote\n%q\nwant\n%q", got, want)
	}

	// without a terminal: no dimming, and no count line
	out.Reset()
	tty.terminal = false
	progress("call", "tool_input", "{}")
	progress("run", "output", "plain")
	end("run")
	if got, want := out.String(), "plain\n"; got != want {
		t.Fatalf("tty wrote %q, want %q", got, want)
	}
}

func TestNewTTYCallbackShowSetting(t *testing.T) {
	cb, err := NewTTYCallback("stderr?show=text,output:lines,tool_input:count")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"text": "text", "output": "lines", "tool_input": "count"}
	if fmt.Sprint(cb.styles) != fmt.Sprint(want) {
		t.Fatalf("styles = %v, want %v", cb.styles, want)
	}
	for _, bad := range []string{"stderr?show=text:loud", "stderr?show=:dim", "stderr?colour=1"} {
		if _, err := NewTTYCallback(bad); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}
