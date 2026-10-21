package engine

import (
	"strings"
	"testing"
)

func TestCodifyStepParams(t *testing.T) {
	t.Setenv("MARKOV_BIN", "/bin/markov")
	cfg, err := codifyConfig(map[string]any{"workflow": "codify-me", "keep": []any{"judge"}, "model": "m"}, "/src")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workflow != "codify-me" || cfg.Source != "/src" || cfg.Model != "m" || strings.Join(cfg.Keep, ",") != "judge" || cfg.Markov != "/bin/markov" {
		t.Fatalf("cfg = %+v", cfg)
	}
	cfg, _ = codifyConfig(map[string]any{"workflow": "w", "project": "/elsewhere"}, "/snapshot")
	if cfg.Source != "/elsewhere" || cfg.Model != "claude-haiku-5-5" {
		t.Fatalf("project param and default model: %+v", cfg)
	}
	if _, err := codifyConfig(map[string]any{}, "/src"); err == nil || !strings.Contains(err.Error(), "workflow") {
		t.Fatalf("missing workflow: %v", err)
	}
}
