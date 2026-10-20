package callback

import "time"

type EventHeader struct {
	Timestamp time.Time `json:"timestamp"`
	RunID     string    `json:"run_id"`
	EventType string    `json:"event_type"`
}

type RunStartedEvent struct {
	EventHeader
	WorkflowName string         `json:"workflow_name"`
	WorkflowFile string         `json:"workflow_file"`
	SourceDigest string         `json:"source_digest,omitempty"`
	Vars         map[string]any `json:"vars"`
	Forks        int            `json:"forks"`
	Namespace    string         `json:"namespace"`
}

type RunCompletedEvent struct {
	EventHeader
	WorkflowName string  `json:"workflow_name"`
	Duration     float64 `json:"duration_seconds"`
}

type RunFailedEvent struct {
	EventHeader
	WorkflowName string  `json:"workflow_name"`
	Error        string  `json:"error"`
	Duration     float64 `json:"duration_seconds"`
}

// RunPausedEvent records the gate receipt that stopped a workflow. A paused
// run is neither completed nor failed and can be resumed with new input.
type RunPausedEvent struct {
	EventHeader
	WorkflowName string         `json:"workflow_name"`
	StepName     string         `json:"step_name"`
	FiredRules   []string       `json:"fired_rules"`
	Facts        map[string]any `json:"facts,omitempty"`
	Duration     float64        `json:"duration_seconds"`
}

// ChangedStepInfo names a completed step whose definition changed since it ran.
type ChangedStepInfo struct {
	RunID    string `json:"run_id"`
	Workflow string `json:"workflow"`
	Step     string `json:"step"`
}

type RunResumedEvent struct {
	EventHeader
	WorkflowName         string `json:"workflow_name"`
	CompletedSteps       int    `json:"completed_steps"`
	RemainingSteps       int    `json:"remaining_steps"`
	SourceIntegrityMode  string `json:"source_integrity_mode,omitempty"`
	ExpectedSourceDigest string `json:"expected_source_digest,omitempty"`
	ObservedSourceDigest string `json:"observed_source_digest,omitempty"`
	SourceDrifted        bool   `json:"source_drifted"`
	// ChangedSteps lists completed steps whose definition changed since they ran.
	ChangedSteps []ChangedStepInfo `json:"changed_steps,omitempty"`
	// Rewound is true when the run was rewound (--rewind or --rewind-changed) before resuming.
	Rewound bool `json:"rewound,omitempty"`
}

type StepStartedEvent struct {
	EventHeader
	WorkflowName string         `json:"workflow_name"`
	StepName     string         `json:"step_name"`
	StepType     string         `json:"step_type"`
	ResolvedType string         `json:"resolved_type"`
	Params       map[string]any `json:"params,omitempty"`
}

type StepCompletedEvent struct {
	EventHeader
	WorkflowName string         `json:"workflow_name"`
	StepName     string         `json:"step_name"`
	StepType     string         `json:"step_type"`
	Output       map[string]any `json:"output,omitempty"`
	Duration     float64        `json:"duration_seconds"`
}

type StepFailedEvent struct {
	EventHeader
	WorkflowName string  `json:"workflow_name"`
	StepName     string  `json:"step_name"`
	StepType     string  `json:"step_type"`
	Error        string  `json:"error"`
	Duration     float64 `json:"duration_seconds"`
}

type StepSkippedEvent struct {
	EventHeader
	WorkflowName string `json:"workflow_name"`
	StepName     string `json:"step_name"`
	Reason       string `json:"reason"`
}

type JobCreatedEvent struct {
	EventHeader
	WorkflowName string `json:"workflow_name"`
	StepName     string `json:"step_name"`
	StepType     string `json:"step_type"`
	ForkID       string `json:"fork_id,omitempty"`
	JobName      string `json:"job_name"`
	Namespace    string `json:"namespace"`
	PodSelector  string `json:"pod_selector"`
}

// StepProgressEvent reports live progress from inside a running step, such as
// the assistant messages and tool calls of a claude step. Progress events are
// informational and are not persisted in the state store.
type StepProgressEvent struct {
	EventHeader
	WorkflowName string         `json:"workflow_name"`
	StepName     string         `json:"step_name"`
	StepType     string         `json:"step_type"`
	Kind         string         `json:"kind"`
	Data         map[string]any `json:"data,omitempty"`
}

// DebugEvent reports debugger activity: breakpoints accepted, a run paused or resumed at a
// step, a logpoint, or the result of an `evaluate` command. Kinds and their data are described
// in docs/reference/debugging.md. Debug events are informational and are not persisted.
type DebugEvent struct {
	EventHeader
	WorkflowName string         `json:"workflow_name,omitempty"`
	StepName     string         `json:"step_name,omitempty"`
	Kind         string         `json:"kind"`
	Data         map[string]any `json:"data,omitempty"`
}

type GateEvaluatedEvent struct {
	EventHeader
	WorkflowName string         `json:"workflow_name"`
	StepName     string         `json:"step_name"`
	Action       string         `json:"action"`
	FiredRules   []string       `json:"fired_rules"`
	Facts        map[string]any `json:"facts,omitempty"`
}

type SubRunStartedEvent struct {
	EventHeader
	ParentRunID  string `json:"parent_run_id"`
	ParentStep   string `json:"parent_step"`
	WorkflowName string `json:"workflow_name"`
	ForEachKey   string `json:"for_each_key,omitempty"`
}

type SubRunCompletedEvent struct {
	EventHeader
	ParentRunID  string  `json:"parent_run_id"`
	ParentStep   string  `json:"parent_step"`
	WorkflowName string  `json:"workflow_name"`
	ForEachKey   string  `json:"for_each_key,omitempty"`
	Duration     float64 `json:"duration_seconds"`
}

type SubRunFailedEvent struct {
	EventHeader
	ParentRunID  string  `json:"parent_run_id"`
	ParentStep   string  `json:"parent_step"`
	WorkflowName string  `json:"workflow_name"`
	ForEachKey   string  `json:"for_each_key,omitempty"`
	Error        string  `json:"error"`
	Duration     float64 `json:"duration_seconds"`
}

// WorkflowReloadedEvent reports that a reload_workflow step replaced the workflow definition.
// Steps already running keep their definition; sub-workflows called from now on use the new one.
type WorkflowReloadedEvent struct {
	EventHeader
	WorkflowName string         `json:"workflow_name"`
	StepName     string         `json:"step_name"`
	Source       string         `json:"source"`
	OldDigest    string         `json:"old_digest,omitempty"`
	NewDigest    string         `json:"new_digest,omitempty"`
	Reload       int            `json:"reload"`
	Changes      map[string]any `json:"changes,omitempty"`
}

type Callback interface {
	OnRunStarted(event RunStartedEvent) error
	OnRunCompleted(event RunCompletedEvent) error
	OnRunFailed(event RunFailedEvent) error
	OnRunPaused(event RunPausedEvent) error
	OnRunResumed(event RunResumedEvent) error

	OnStepStarted(event StepStartedEvent) error
	OnStepCompleted(event StepCompletedEvent) error
	OnStepFailed(event StepFailedEvent) error
	OnStepSkipped(event StepSkippedEvent) error

	OnStepProgress(event StepProgressEvent) error
	OnDebug(event DebugEvent) error

	OnJobCreated(event JobCreatedEvent) error
	OnGateEvaluated(event GateEvaluatedEvent) error

	OnSubRunStarted(event SubRunStartedEvent) error
	OnSubRunCompleted(event SubRunCompletedEvent) error
	OnSubRunFailed(event SubRunFailedEvent) error

	OnWorkflowReloaded(event WorkflowReloadedEvent) error

	Close() error
}
