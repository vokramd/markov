package executor

import "context"

type Result struct {
	Output map[string]any
	Error  error
}

type Executor interface {
	Execute(ctx context.Context, params map[string]any) (*Result, error)
}

// ProgressFunc receives live progress from a running executor. kind names the
// progress event (for example "text" or "tool_use") and data carries its fields.
type ProgressFunc func(kind string, data map[string]any)

type progressKey struct{}

// WithProgress returns a context whose executors report progress to fn.
// Passing progress through the context, rather than storing it on the
// executor, keeps concurrent for_each iterations independent.
func WithProgress(ctx context.Context, fn ProgressFunc) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}

func reportProgress(ctx context.Context, kind string, data map[string]any) {
	if fn, ok := ctx.Value(progressKey{}).(ProgressFunc); ok && fn != nil {
		fn(kind, data)
	}
}

// StepContext identifies the step an executor is running.
type StepContext struct {
	RunID    string
	Workflow string
	Step     string
}

type stepKey struct{}

// WithStep returns a context that tells executors which step they run.
func WithStep(ctx context.Context, info StepContext) context.Context {
	return context.WithValue(ctx, stepKey{}, info)
}

// StepFrom returns the step set by WithStep (zero when there is none).
func StepFrom(ctx context.Context) StepContext {
	info, _ := ctx.Value(stepKey{}).(StepContext)
	return info
}
