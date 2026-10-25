package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jctanner/markov/pkg/callback"
	"github.com/jctanner/markov/pkg/executor"
	"github.com/jctanner/markov/pkg/parser"
)

type countExec struct {
	mu       sync.Mutex
	commands []any
}

func (c *countExec) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commands = append(c.commands, params["command"])
	return &executor.Result{Output: map[string]any{"ok": true}}, nil
}

func (c *countExec) Commands() []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]any, len(c.commands))
	copy(out, c.commands)
	return out
}

func TestForEachKey_UsesItemField(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars: map[string]any{
			"epics": []any{
				map[string]any{"key": "EPIC-100", "title": "First"},
				map[string]any{"key": "EPIC-200", "title": "Second"},
			},
		},
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{
						Name:       "per_epic",
						ForEach:    "epics",
						ForEachKey: "key",
						As:         "epic",
						Workflow:   "process",
						Vars:       map[string]any{"epic_key": "{{ epic.key }}"},
					},
				},
			},
			{
				Name: "process",
				Steps: []parser.Step{
					{Name: "do_work", Type: "set_fact", Vars: map[string]any{"done": "yes"}},
				},
			},
		},
	}

	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	cb.mu.Lock()
	defer cb.mu.Unlock()

	var keys []string
	for _, ev := range cb.all {
		if sr, ok := ev.(callback.SubRunStartedEvent); ok {
			keys = append(keys, sr.ForEachKey)
		}
	}

	if len(keys) != 2 {
		t.Fatalf("got %d sub_run_started events, want 2", len(keys))
	}

	hasEpic100 := false
	hasEpic200 := false
	for _, k := range keys {
		if k == "EPIC-100" {
			hasEpic100 = true
		}
		if k == "EPIC-200" {
			hasEpic200 = true
		}
	}
	if !hasEpic100 || !hasEpic200 {
		t.Errorf("keys = %v, want [EPIC-100, EPIC-200]", keys)
	}
}

func TestDirectForEachTypedStepUsesDistinctState(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"items": []any{"alpha", "bravo", "charlie"}},
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{
						Name:        "process_items",
						ForEach:     "items",
						As:          "item",
						Type:        "shell_exec",
						Params:      map[string]any{"command": "echo {{ item }}"},
						Concurrency: 1,
					},
				},
			},
		},
	}

	exec := &countExec{}
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"shell_exec": exec})

	ctx := context.Background()
	runID, err := eng.Run(ctx, "main", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	commands := exec.Commands()
	if len(commands) != 3 {
		t.Fatalf("executor calls = %d, want 3: %v", len(commands), commands)
	}

	steps, err := eng.store.GetSteps(ctx, runID)
	if err != nil {
		t.Fatalf("GetSteps: %v", err)
	}

	got := make(map[string]bool)
	for _, step := range steps {
		got[step.StepName] = true
	}
	for _, want := range []string{"process_items[0]", "process_items[1]", "process_items[2]"} {
		if !got[want] {
			t.Errorf("missing state step %q in %#v", want, got)
		}
	}
}

func TestDirectForEachTypedStepLargeFanOutUsesDistinctState(t *testing.T) {
	items := make([]any, 200)
	for i := range items {
		items[i] = map[string]any{"id": i}
	}

	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"items": items},
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{
						Name:        "process_items",
						ForEach:     "items",
						ForEachKey:  "id",
						As:          "item",
						Type:        "shell_exec",
						Params:      map[string]any{"command": "echo {{ item.id }}"},
						Concurrency: 20,
					},
				},
			},
		},
	}

	exec := &countExec{}
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"shell_exec": exec})

	ctx := context.Background()
	runID, err := eng.Run(ctx, "main", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := len(exec.Commands()); got != len(items) {
		t.Fatalf("executor calls = %d, want %d", got, len(items))
	}

	steps, err := eng.store.GetSteps(ctx, runID)
	if err != nil {
		t.Fatalf("GetSteps: %v", err)
	}

	seen := make(map[string]bool, len(steps))
	for _, step := range steps {
		seen[step.StepName] = true
	}
	for i := range items {
		want := fmt.Sprintf("process_items[%d]", i)
		if !seen[want] {
			t.Fatalf("missing state step %q", want)
		}
	}
}

func TestForEachKey_FallsBackToIndex(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"items": []any{"a", "b", "c"}},
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{
						Name:     "loop",
						ForEach:  "items",
						As:       "item",
						Workflow: "process",
					},
				},
			},
			{
				Name: "process",
				Steps: []parser.Step{
					{Name: "noop", Type: "set_fact", Vars: map[string]any{"x": "1"}},
				},
			},
		},
	}

	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	cb.mu.Lock()
	defer cb.mu.Unlock()

	var keys []string
	for _, ev := range cb.all {
		if sr, ok := ev.(callback.SubRunStartedEvent); ok {
			keys = append(keys, sr.ForEachKey)
		}
	}

	for _, want := range []string{"0", "1", "2"} {
		found := false
		for _, k := range keys {
			if k == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing index key %q in %v", want, keys)
		}
	}
}

func TestForEachSort_OrdersByField(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars: map[string]any{
			"items": []any{
				map[string]any{"name": "charlie", "rank": "3"},
				map[string]any{"name": "alpha", "rank": "1"},
				map[string]any{"name": "bravo", "rank": "2"},
			},
		},
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{
						Name:        "sorted_loop",
						ForEach:     "items",
						ForEachKey:  "name",
						ForEachSort: "name",
						As:          "item",
						Workflow:    "process",
						Vars:        map[string]any{"item_name": "{{ item.name }}"},
						Concurrency: 1,
					},
				},
			},
			{
				Name: "process",
				Steps: []parser.Step{
					{Name: "noop", Type: "set_fact", Vars: map[string]any{"x": "1"}},
				},
			},
		},
	}

	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	cb.mu.Lock()
	defer cb.mu.Unlock()

	var keys []string
	for _, ev := range cb.all {
		if sr, ok := ev.(callback.SubRunStartedEvent); ok {
			keys = append(keys, sr.ForEachKey)
		}
	}

	if len(keys) != 3 {
		t.Fatalf("got %d sub_run_started events, want 3", len(keys))
	}

	expected := []string{"alpha", "bravo", "charlie"}
	for i, want := range expected {
		if keys[i] != want {
			t.Errorf("keys[%d] = %q, want %q (full: %v)", i, keys[i], want, keys)
		}
	}
}

func TestForEachKey_DuplicateKeyFails(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars: map[string]any{
			"items": []any{
				map[string]any{"id": "AAA", "val": "1"},
				map[string]any{"id": "BBB", "val": "2"},
				map[string]any{"id": "AAA", "val": "3"},
			},
		},
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{
						Name:       "loop",
						ForEach:    "items",
						ForEachKey: "id",
						As:         "item",
						Workflow:   "process",
					},
				},
			},
			{
				Name: "process",
				Steps: []parser.Step{
					{Name: "noop", Type: "set_fact", Vars: map[string]any{"x": "1"}},
				},
			},
		},
	}

	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", nil)
	if err == nil {
		t.Fatal("expected error for duplicate for_each_key")
	}

	if got := err.Error(); !contains(got, "duplicate for_each_key") {
		t.Errorf("error = %q, want it to contain 'duplicate for_each_key'", got)
	}
}

func TestForEachKey_MissingFieldFails(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars: map[string]any{
			"items": []any{
				map[string]any{"id": "AAA"},
				map[string]any{"name": "BBB"},
			},
		},
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{
						Name:       "loop",
						ForEach:    "items",
						ForEachKey: "id",
						As:         "item",
						Workflow:   "process",
					},
				},
			},
			{
				Name: "process",
				Steps: []parser.Step{
					{Name: "noop", Type: "set_fact", Vars: map[string]any{"x": "1"}},
				},
			},
		},
	}

	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", nil)
	if err == nil {
		t.Fatal("expected error for missing for_each_key field")
	}

	if got := err.Error(); !contains(got, "for_each_key") && !contains(got, "not found") {
		t.Errorf("error = %q, want it to mention missing for_each_key", got)
	}
}

func TestForEachSort_MissingFieldFails(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars: map[string]any{
			"items": []any{
				map[string]any{"name": "alpha"},
				map[string]any{"title": "bravo"},
			},
		},
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{
						Name:        "loop",
						ForEach:     "items",
						ForEachSort: "name",
						As:          "item",
						Workflow:    "process",
					},
				},
			},
			{
				Name: "process",
				Steps: []parser.Step{
					{Name: "noop", Type: "set_fact", Vars: map[string]any{"x": "1"}},
				},
			},
		},
	}

	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", nil)
	if err == nil {
		t.Fatal("expected error for missing for_each_sort field")
	}

	if got := err.Error(); !contains(got, "for_each_sort") {
		t.Errorf("error = %q, want it to mention for_each_sort", got)
	}
}

func TestForEachSort_WithoutKey_DeterministicOrder(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars: map[string]any{
			"items": []any{
				map[string]any{"name": "zeta"},
				map[string]any{"name": "alpha"},
				map[string]any{"name": "mu"},
			},
		},
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{
						Name:        "loop",
						ForEach:     "items",
						ForEachSort: "name",
						As:          "item",
						Workflow:    "process",
						Concurrency: 1,
					},
				},
			},
			{
				Name: "process",
				Steps: []parser.Step{
					{Name: "noop", Type: "set_fact", Vars: map[string]any{"x": "1"}},
				},
			},
		},
	}

	eng, cb := newTestEngine(t, wfFile, map[string]executor.Executor{})

	ctx := context.Background()
	_, err := eng.Run(ctx, "main", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	cb.mu.Lock()
	defer cb.mu.Unlock()

	var keys []string
	for _, ev := range cb.all {
		if sr, ok := ev.(callback.SubRunStartedEvent); ok {
			keys = append(keys, sr.ForEachKey)
		}
	}

	// Without for_each_key, keys are indices — but they should be in sorted order
	expected := []string{"0", "1", "2"}
	if len(keys) != 3 {
		t.Fatalf("got %d keys, want 3: %v", len(keys), keys)
	}
	for i, want := range expected {
		if keys[i] != want {
			t.Errorf("keys[%d] = %q, want %q", i, keys[i], want)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// slowEcho returns its command as output, finishing later items first: item N sleeps (5-N)*10ms.
type slowEcho struct{}

func (slowEcho) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	n, _ := params["command"].(int)
	time.Sleep(time.Duration(5-n) * 10 * time.Millisecond)
	return &executor.Result{Output: map[string]any{"value": n}}, nil
}

// captureExec records the params it was given.
type captureExec struct {
	mu     sync.Mutex
	params map[string]any
}

func (c *captureExec) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.params = params
	return &executor.Result{Output: map[string]any{}}, nil
}

func registeredValues(t *testing.T, got any, path ...string) []any {
	t.Helper()
	list, ok := got.([]map[string]any)
	if !ok {
		t.Fatalf("registered = %#v (%T), want []map[string]any", got, got)
	}
	var values []any
	for _, item := range list {
		var v any = item
		for _, key := range path {
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("no %q in %#v", key, v)
			}
			v = m[key]
		}
		values = append(values, v)
	}
	return values
}

func TestDirectForEachRegisterCollectsOutputsInInputOrder(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"items": []any{1, 2, 3, 4}},
		Workflows: []parser.Workflow{{
			Name: "main",
			Steps: []parser.Step{
				{
					Name: "echo", ForEach: "items", As: "item", Type: "slow", Concurrency: 4,
					Params: map[string]any{"command": "{{ item }}"}, Register: "echoed",
				},
				{Name: "capture", Type: "capture", Params: map[string]any{"got": "{{ echoed }}"}},
			},
		}},
	}
	capture := &captureExec{}
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"slow": slowEcho{}, "capture": capture})
	if _, err := eng.Run(context.Background(), "main", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := registeredValues(t, capture.params["got"], "echoed", "value")
	if fmt.Sprint(got) != "[1 2 3 4]" {
		t.Fatalf("registered outputs = %v, want [1 2 3 4] (input order, with each step's output)", got)
	}
}

func TestSubWorkflowForEachRegisterKeepsInputOrder(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"items": []any{1, 2, 3, 4}},
		Workflows: []parser.Workflow{
			{
				Name: "main",
				Steps: []parser.Step{
					{
						Name: "each", ForEach: "items", As: "item", Workflow: "one", Concurrency: 4,
						Vars: map[string]any{"n": "{{ item }}"}, Register: "results",
					},
					{Name: "capture", Type: "capture", Params: map[string]any{"got": "{{ results }}"}},
				},
			},
			{
				Name: "one",
				Steps: []parser.Step{
					{Name: "echo", Type: "slow", Params: map[string]any{"command": "{{ n }}"}, Register: "out"},
				},
			},
		},
	}
	capture := &captureExec{}
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"slow": slowEcho{}, "capture": capture})
	if _, err := eng.Run(context.Background(), "main", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := registeredValues(t, capture.params["got"], "out", "value")
	if fmt.Sprint(got) != "[1 2 3 4]" {
		t.Fatalf("registered outputs = %v, want [1 2 3 4]", got)
	}
}

// A typed for_each register keeps each iteration's item and output, not the context every
// iteration starts with: loops that each collect over an earlier loop's register stay the size of
// their data (they used to nest every earlier register in every element).
func TestDirectForEachRegisterKeepsOnlyTheIteration(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"items": []any{1, 2, 3, 4}, "big": strings.Repeat("x", 10000)},
		Workflows: []parser.Workflow{{
			Name: "main",
			Steps: []parser.Step{
				{Name: "first", ForEach: "items", As: "item", Type: "slow", Params: map[string]any{"command": "{{ item }}"}, Register: "a"},
				{Name: "second", ForEach: "items", As: "item", Type: "slow", Params: map[string]any{"command": "{{ item }}"}, Register: "b"},
				{Name: "third", ForEach: "items", As: "item", Type: "slow", Params: map[string]any{"command": "{{ item }}"}, Register: "c"},
				{Name: "capture", Type: "capture", Params: map[string]any{"got": "{{ c }}"}},
			},
		}},
	}
	capture := &captureExec{}
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"slow": slowEcho{}, "capture": capture})
	if _, err := eng.Run(context.Background(), "main", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	list, ok := capture.params["got"].([]map[string]any)
	if !ok || len(list) != 4 {
		t.Fatalf("registered = %#v, want 4 elements", capture.params["got"])
	}
	for i, el := range list {
		if len(el) != 2 || el["item"] != i+1 {
			t.Fatalf("element %d = %#v, want only item and c", i, el)
		}
		if out, _ := el["c"].(map[string]any); out["value"] != i+1 {
			t.Fatalf("element %d output = %#v", i, el["c"])
		}
	}
	data, _ := json.Marshal(list)
	if len(data) > 500 {
		t.Fatalf("register is %d bytes as JSON, want about the size of its data: %s", len(data), data)
	}
}

// What an iteration sets besides the register (set_fact facts here) is kept with it.
func TestDirectForEachRegisterKeepsWhatTheIterationSet(t *testing.T) {
	wfFile := &parser.WorkflowFile{
		Entrypoint: "main",
		Vars:       map[string]any{"items": []any{"a", "b"}, "shared": "parent"},
		Workflows: []parser.Workflow{{
			Name: "main",
			Steps: []parser.Step{
				{Name: "facts", ForEach: "items", As: "item", Type: "set_fact", Vars: map[string]any{"seen": "{{ item }}!"}, Register: "out"},
				{Name: "capture", Type: "capture", Params: map[string]any{"got": "{{ out }}"}},
			},
		}},
	}
	capture := &captureExec{}
	eng, _ := newTestEngine(t, wfFile, map[string]executor.Executor{"capture": capture})
	if _, err := eng.Run(context.Background(), "main", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := registeredValues(t, capture.params["got"], "seen")
	if fmt.Sprint(got) != "[a! b!]" {
		t.Fatalf("facts = %v, want [a! b!]", got)
	}
	for _, el := range capture.params["got"].([]map[string]any) {
		if _, ok := el["shared"]; ok {
			t.Fatalf("element %#v holds the parent's var", el)
		}
	}
}
