package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/vokramd/markov/pkg/parser"
	"github.com/vokramd/markov/pkg/state"
)

// Sub-run ownership. Sub-run IDs are concatenations of names (<run>-<step>, <run>-<step>-<key>),
// so two different calls can produce the same ID: a step "a-b", and a call "a" whose workflow has
// a step "b". Each sub-run row records the call that made it, and a call that finds a row made by
// another refuses to use it instead of entering that run's steps.

// subRunOwner is the call that made a sub-run. Its JSON (fields in this order) is what the
// state store keeps in runs.owner, and what repeat iteration IDs hash.
type subRunOwner struct {
	Kind       string `json:"kind"`   // call, for_each or repeat
	Parent     string `json:"parent"` // the calling run
	Workflow   string `json:"workflow"`
	Step       string `json:"step"`
	Key        string `json:"key,omitempty"` // a for_each item's key
	Generation int    `json:"generation"`    // repeat: which start of the loop (0, 1, ...)
	Index      int    `json:"index"`         // repeat: which iteration
}

func (o subRunOwner) encode() string {
	b, _ := json.Marshal(o)
	return string(b)
}

// hash is a short digest of the owner, for IDs that must not collide with concatenated ones.
func (o subRunOwner) hash() string {
	sum := sha256.Sum256([]byte(o.encode()))
	return hex.EncodeToString(sum[:6])
}

func (o subRunOwner) describe() string {
	switch o.Kind {
	case "for_each":
		return fmt.Sprintf("for_each step %q (key %q) of workflow %q", o.Step, o.Key, o.Workflow)
	case "repeat":
		return fmt.Sprintf("repeat step %q (generation %d, iteration %d) of workflow %q", o.Step, o.Generation, o.Index, o.Workflow)
	default:
		return fmt.Sprintf("step %q of workflow %q", o.Step, o.Workflow)
	}
}

// claimSubRun checks whether subRunID may be used for owner, running workflow name. It reports
// whether a row already exists (a resumed sub-run). A row made by another call, or one that ran
// another workflow, is an error; so is a store that fails to answer.
func (e *Engine) claimSubRun(ctx context.Context, subRunID string, owner subRunOwner, step parser.Step, name string) (bool, error) {
	prev, err := e.store.GetRun(ctx, subRunID)
	if errors.Is(err, state.ErrRunNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("looking up sub-run %s: %w", subRunID, err)
	}
	mine := owner.encode()
	switch {
	case prev.Owner == mine:
	case prev.Owner == "" && owner.Kind != "repeat":
		// Written before owners were recorded: ordinary calls and fan-outs, as now.
	default:
		was := "a call recorded without an owner"
		var o subRunOwner
		if json.Unmarshal([]byte(prev.Owner), &o) == nil && prev.Owner != "" {
			was = o.describe()
		}
		return false, fmt.Errorf("sub-run ID %s is already used by %s, so %s can't use it; rename one of the steps", subRunID, was, owner.describe())
	}
	if prev.Entrypoint != "" && prev.Entrypoint != name {
		return false, fmt.Errorf("sub-run %s ran workflow %q, but %q now resolves to %q; it can't be resumed in place (start a new run, or rewind to before step %q)",
			subRunID, prev.Entrypoint, step.Workflow, name, step.Name)
	}
	return true, nil
}

// startSubRun records a sub-run as running: a new row, or the existing one claimSubRun found.
func (e *Engine) startSubRun(ctx context.Context, run *state.Run, exists bool) error {
	if exists {
		if err := e.store.UpdateRun(ctx, run); err != nil {
			return fmt.Errorf("recording sub-run %s: %w", run.RunID, err)
		}
		return nil
	}
	if err := e.store.CreateRun(ctx, run); err != nil {
		return fmt.Errorf("recording sub-run %s: %w", run.RunID, err)
	}
	return nil
}
