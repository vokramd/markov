package executor

import (
	"context"
	"fmt"
	"github.com/vokramd/markov/pkg/jev"
)

type Jev struct{ client *jev.Client }

func NewJev(connections map[string]jev.Connection) *Jev { return &Jev{client: jev.New(connections)} }
func (j *Jev) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	name, _ := params["connection"].(string)
	state, ok := params["state"]
	if !ok || state == nil {
		return nil, fmt.Errorf("jev: state is required")
	}
	qs, err := jev.Questions(params["questions"])
	if err != nil {
		return nil, err
	}
	out, err := j.client.Execute(ctx, name, state, qs)
	if err != nil {
		return nil, err
	}
	return &Result{Output: out}, nil
}
