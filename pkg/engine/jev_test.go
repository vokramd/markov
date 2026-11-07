package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vokramd/markov/pkg/parser"
	"github.com/vokramd/markov/pkg/state"
)

func TestJevStepsConditionsGateAndResume(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Questions map[string]any `json:"questions"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		answers := map[string]any{}
		for name := range body.Questions {
			answers[name] = map[string]any{"type": "noul", "noul": 0.8}
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "test", "usage": map[string]int{"input_tokens": 1, "output_tokens": 1}, "answers": answers})
	}))
	defer srv.Close()
	source := fmt.Sprintf(`
entrypoint: main
connections:
  local: {type: jev, base_url: %s}
decisions:
  risk: {connection: local, type: noul, instructions: "Is there risk?"}
vars:
  approved: false
  change: {description: Change login}
rules:
  - name: review
    when: "risk >= 0.7 and approved != true"
    action: pause
workflows:
  - name: main
    steps:
      - name: direct
        type: jev
        register: triage
        params:
          connection: local
          state: "{{ change }}"
          questions:
            risk: {type: noul, instructions: "Is there risk?"}
      - name: conditional
        type: assert
        when: "jev.noul('risk', change) < 0.1"
        that: ["false"]
      - name: gate
        type: gate
        facts:
          risk: {decision: risk, state: "{{ change }}", select: noul}
          risk_again: {decision: risk, state: "{{ change }}"}
        rules: [review]
      - name: done
        type: assert
        that: ["triage.answers.risk.noul == 0.8", "risk == 0.8", "risk_again == 0.8"]
`, srv.URL)
	wf, err := parser.Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	e, _ := newTestEngine(t, wf, nil)
	ctx := context.Background()
	id, err := e.Run(ctx, "", nil)
	if !IsPaused(err) {
		t.Fatalf("expected pause, got %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("got %d calls, want direct + condition + gate (shared)", calls.Load())
	}
	// Recreate the engine to prove this is durable, not just an in-memory cache.
	e = New(wf, e.store, nil)
	if err := e.ResumeWithVars(ctx, id, map[string]any{"approved": true}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("resume repeated inference: %d calls", calls.Load())
	}
	run, _ := e.store.GetRun(ctx, id)
	if run.Status != state.RunCompleted {
		t.Fatalf("status %v", run.Status)
	}
	steps, _ := e.store.GetSteps(ctx, id)
	count := 0
	for _, s := range steps {
		if strings.HasPrefix(s.StepName, "__jev__/") {
			count++
			if s.Status != state.StepCompleted {
				t.Fatal("decision not persisted")
			}
		}
	}
	if count != 2 {
		t.Fatalf("decision receipts %d", count)
	}
	// Changed state and explicit refresh each create a new receipt.
	_, err = e.decision(ctx, id, "main", "gate", "risk", "different state", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.decision(ctx, id, "main", "gate", "risk", "different state", "v2")
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 5 {
		t.Fatalf("input/refresh invalidation failed: %d", calls.Load())
	}
}

func TestJevConditionFailureIsNotFalse(t *testing.T) {
	wf, err := parser.Parse([]byte(`
entrypoint: main
workflows:
  - name: main
    steps:
      - name: bad
        type: assert
        when: "jev.noul('missing', 'state') > 0.7"
        that: ["true"]
`))
	if err != nil {
		t.Fatal(err)
	}
	e, _ := newTestEngine(t, wf, nil)
	id, err := e.Run(context.Background(), "", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown Jev decision") {
		t.Fatalf("error %v", err)
	}
	step, _ := e.store.GetStep(context.Background(), id, "main", "bad")
	if step == nil || step.Status != state.StepFailed {
		t.Fatal("condition failure not persisted")
	}
}
