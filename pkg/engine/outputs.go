package engine

import (
	"context"

	"github.com/jctanner/markov/pkg/callback"
	"github.com/jctanner/markov/pkg/outputlimit"
	"github.com/jctanner/markov/pkg/parser"
)

// outputLimit is how much a step may produce, most specific first: the step's max_output, its
// step type's, the file's, the engine's (--max-output), then outputlimit.Default. 0 is no limit.
func (e *Engine) outputLimit(ctx context.Context, step *parser.Step) int64 {
	if step.MaxOutput.Set {
		return step.MaxOutput.Bytes
	}
	file := e.fileFor(ctx)
	if st, ok := file.StepTypes[step.Type]; ok && st.MaxOutput.Set {
		return st.MaxOutput.Bytes
	}
	if file.MaxOutput.Set {
		return file.MaxOutput.Bytes
	}
	if e.MaxOutput.Set {
		return e.MaxOutput.Bytes
	}
	return outputlimit.Default
}

// checkOutput fails a step whose output (its register, facts or loaded artifacts) is over its
// limit, before the value reaches the context, the state store or an event.
func (e *Engine) checkOutput(ctx context.Context, step *parser.Step, v any) error {
	return outputlimit.Check(v, e.outputLimit(ctx, step))
}

// withLimitFields adds a limit failure's sizes to its step_failed event, so a reader (codify's
// retro) sees what was big without parsing the message.
func withLimitFields(ev callback.StepFailedEvent, err error) callback.StepFailedEvent {
	if lim, ok := outputlimit.As(err); ok {
		ev.ErrorKind = "max_output"
		ev.OutputBytes = lim.Bytes
		ev.LimitBytes = lim.Limit
		ev.Stream = lim.Stream
		ev.Largest = lim.Largest
		ev.Output = nil
	}
	return ev
}
