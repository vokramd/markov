package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jctanner/markov/pkg/codify"
	"github.com/jctanner/markov/pkg/executor"
)

// codifyExecutor implements the codify step type: one iteration of `markov codify` on a
// workflow of the project (plan 003 phase 6). Workflows that drive their own loop follow it with
// reload_workflow when it promoted a version.
type codifyExecutor struct {
	e *Engine
}

// codifyConfig maps the step's params onto a codify configuration.
func codifyConfig(params map[string]any, source string) (codify.Config, error) {
	cfg := codify.Config{Source: source, Model: "claude-haiku-5-5", RunTimeout: 30 * time.Minute}
	cfg.Workflow, _ = params["workflow"].(string)
	if cfg.Workflow == "" {
		return cfg, errors.New("codify: the workflow param names the workflow to codify")
	}
	if p, _ := params["project"].(string); p != "" {
		cfg.Source = p
	}
	if cfg.Source == "" {
		return cfg, errors.New("codify: no project param and the run has no source path")
	}
	if m, _ := params["model"].(string); m != "" {
		cfg.Model = m
	}
	cfg.CodifyDir, _ = params["codify_dir"].(string)
	cfg.PromptFile, _ = params["prompt_file"].(string)
	keep, err := stringList(params["keep"])
	if err != nil {
		return cfg, fmt.Errorf("codify: keep: %w", err)
	}
	cfg.Keep = keep
	if bin := os.Getenv("MARKOV_BIN"); bin != "" {
		cfg.Markov = bin
	} else if cfg.Markov, err = os.Executable(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (x *codifyExecutor) Execute(ctx context.Context, params map[string]any) (*executor.Result, error) {
	cfg, err := codifyConfig(params, x.e.SourcePath)
	if err != nil {
		return nil, err
	}
	// The pass's phases, agent streams and the target's runs go to this run's callbacks too.
	cfg.Callbacks = x.e.callbacks
	row, err := codify.Pass(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("codify: %w", err)
	}
	job, _ := row.Retro["extracted"].(string)
	out := map[string]any{
		"status":      row.Validation.Status,
		"promoted":    row.Promoted != nil,
		"reasons":     row.Validation.Reasons,
		"judged":      row.Validation.Judged,
		"iteration":   row.Iteration,
		"extracted":   job,
		"agent_steps": row.Task.AgentSteps,
		"spent_usd":   row.SpentUSD,
	}
	if row.Promoted != nil {
		out["version"] = *row.Promoted
	}
	return &executor.Result{Output: out}, nil
}
