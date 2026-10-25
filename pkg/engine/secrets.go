package engine

import (
	"reflect"
	"slices"

	"github.com/jctanner/markov/pkg/callback"
	"github.com/jctanner/markov/pkg/executor"
	"github.com/jctanner/markov/pkg/parser"
	"github.com/jctanner/markov/pkg/secret"
)

// Secrets is the masker for this engine's runs: every event and, when the CLI routes its log
// through it, every log line has these values replaced with <secret:NAME>.
func (e *Engine) Secrets() *secret.Masker {
	return &e.secrets
}

// noteVars adds the vars in vars that are secret: listed in the file's secrets, or named like a
// secret (GITHUB_TOKEN).
func (e *Engine) noteVars(file *parser.WorkflowFile, vars map[string]any) {
	for k, v := range vars {
		if slices.Contains(file.Secrets, k) || secret.LooksSecret(k) {
			e.secrets.Add(k, v)
		}
	}
}

// noteStepOutput adds the parts of a step's output its `secret` setting marks, under the step's
// name.
func (e *Engine) noteStepOutput(step *parser.Step, output map[string]any) {
	if step.Secret.All {
		e.secrets.Add(step.Name, output)
	}
	for _, path := range step.Secret.Paths {
		e.secrets.AddPath(step.Name+"."+path, output, path)
	}
}

// noteSecretParams adds the rendered values of a step type's secret params.
func (e *Engine) noteSecretParams(step *parser.Step, base string, params map[string]any) {
	for _, name := range executor.SecretParams(base) {
		if v, ok := params[name]; ok {
			e.secrets.Add(step.Name+"."+name, v)
		}
	}
}

// maskedCallback hands its callback a masked copy of every event.
type maskedCallback struct {
	cb callback.Callback
	m  *secret.Masker
}

func (c maskedCallback) OnRunStarted(ev callback.RunStartedEvent) error {
	return c.cb.OnRunStarted(maskEvent(c.m, ev))
}
func (c maskedCallback) OnRunCompleted(ev callback.RunCompletedEvent) error {
	return c.cb.OnRunCompleted(maskEvent(c.m, ev))
}
func (c maskedCallback) OnRunFailed(ev callback.RunFailedEvent) error {
	return c.cb.OnRunFailed(maskEvent(c.m, ev))
}
func (c maskedCallback) OnRunPaused(ev callback.RunPausedEvent) error {
	return c.cb.OnRunPaused(maskEvent(c.m, ev))
}
func (c maskedCallback) OnRunResumed(ev callback.RunResumedEvent) error {
	return c.cb.OnRunResumed(maskEvent(c.m, ev))
}
func (c maskedCallback) OnStepStarted(ev callback.StepStartedEvent) error {
	return c.cb.OnStepStarted(maskEvent(c.m, ev))
}
func (c maskedCallback) OnStepCompleted(ev callback.StepCompletedEvent) error {
	return c.cb.OnStepCompleted(maskEvent(c.m, ev))
}
func (c maskedCallback) OnStepFailed(ev callback.StepFailedEvent) error {
	return c.cb.OnStepFailed(maskEvent(c.m, ev))
}
func (c maskedCallback) OnStepSkipped(ev callback.StepSkippedEvent) error {
	return c.cb.OnStepSkipped(maskEvent(c.m, ev))
}
func (c maskedCallback) OnStepProgress(ev callback.StepProgressEvent) error {
	return c.cb.OnStepProgress(maskEvent(c.m, ev))
}
func (c maskedCallback) OnDebug(ev callback.DebugEvent) error {
	return c.cb.OnDebug(maskEvent(c.m, ev))
}
func (c maskedCallback) OnJobCreated(ev callback.JobCreatedEvent) error {
	return c.cb.OnJobCreated(maskEvent(c.m, ev))
}
func (c maskedCallback) OnGateEvaluated(ev callback.GateEvaluatedEvent) error {
	return c.cb.OnGateEvaluated(maskEvent(c.m, ev))
}
func (c maskedCallback) OnSubRunStarted(ev callback.SubRunStartedEvent) error {
	return c.cb.OnSubRunStarted(maskEvent(c.m, ev))
}
func (c maskedCallback) OnSubRunCompleted(ev callback.SubRunCompletedEvent) error {
	return c.cb.OnSubRunCompleted(maskEvent(c.m, ev))
}
func (c maskedCallback) OnSubRunFailed(ev callback.SubRunFailedEvent) error {
	return c.cb.OnSubRunFailed(maskEvent(c.m, ev))
}
func (c maskedCallback) OnWorkflowReloaded(ev callback.WorkflowReloadedEvent) error {
	return c.cb.OnWorkflowReloaded(maskEvent(c.m, ev))
}
func (c maskedCallback) Close() error { return c.cb.Close() }

// maskEvent is a copy of an event struct with its strings, string lists and maps masked. The
// maps are new; the run's context they came from is not changed.
func maskEvent[T any](m *secret.Masker, ev T) T {
	if m.Empty() {
		return ev
	}
	maskFields(m, reflect.ValueOf(&ev).Elem())
	return ev
}

func maskFields(m *secret.Masker, v reflect.Value) {
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if !f.CanSet() {
			continue
		}
		switch {
		case v.Type().Field(i).Anonymous && f.Kind() == reflect.Struct:
			maskFields(m, f)
		case f.Kind() == reflect.String:
			f.SetString(m.String(f.String()))
		case f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String:
			out := reflect.MakeSlice(f.Type(), f.Len(), f.Len())
			for j := 0; j < f.Len(); j++ {
				out.Index(j).SetString(m.String(f.Index(j).String()))
			}
			f.Set(out)
		case f.Type() == reflect.TypeOf(map[string]any(nil)):
			if !f.IsNil() {
				f.Set(reflect.ValueOf(m.Value(f.Interface())))
			}
		}
	}
}
