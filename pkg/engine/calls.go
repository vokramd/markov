package engine

import (
	"context"
	"fmt"
	"strings"
)

// Sub-workflow calls nest, and a workflow may call itself (directly, or through a templated
// name), so a recursive walk is a workflow that calls itself with `when` as its base case. The
// call chain travels in the context; entering a call deeper than max_call_depth fails, naming
// the chain, instead of recursing until memory runs out.

type callChainKey struct{}

// callFrame is one sub-workflow call: the calling workflow and step, and the workflow called.
type callFrame struct {
	caller, step, callee string
}

func callChain(ctx context.Context) []callFrame {
	frames, _ := ctx.Value(callChainKey{}).([]callFrame)
	return frames
}

// enterCall returns ctx with the call from at to callee added to its chain, or an error when
// that call would nest deeper than max.
func enterCall(ctx context.Context, at stepInfo, callee string, max int) (context.Context, error) {
	outer := callChain(ctx)
	frames := make([]callFrame, len(outer), len(outer)+1)
	copy(frames, outer)
	frames = append(frames, callFrame{caller: at.workflow, step: at.step, callee: callee})
	if len(frames) > max {
		return nil, fmt.Errorf("sub-workflow calls nest deeper than max_call_depth %d: %s; check the base case of a workflow that calls itself, or raise max_call_depth",
			max, describeChain(frames))
	}
	return context.WithValue(ctx, callChainKey{}, frames), nil
}

// describeChain writes a call chain as "main.walk → walk", collapsing a run of the same call
// into one with a count: "walk.recurse → walk (×64)".
func describeChain(frames []callFrame) string {
	var parts []string
	for i := 0; i < len(frames); {
		f := frames[i]
		n := 1
		for i+n < len(frames) && frames[i+n] == f {
			n++
		}
		part := f.caller + "." + f.step + " → " + f.callee
		if n > 1 {
			part += fmt.Sprintf(" (×%d)", n)
		}
		parts = append(parts, part)
		i += n
	}
	return strings.Join(parts, ", ")
}
