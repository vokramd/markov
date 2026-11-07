package parser

import (
	"fmt"
	"github.com/vokramd/markov/pkg/jev"
	"os"
	"path/filepath"
)

func readOptionalYAML(path string, out any) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return readYAML(path, out)
}

func readJevDefinitions(path string, wf *WorkflowFile) error {
	if err := readOptionalYAML(filepath.Join(path, "connections.yaml"), &wf.Connections); err != nil {
		return err
	}
	return readOptionalYAML(filepath.Join(path, "decisions.yaml"), &wf.Decisions)
}

func validateJev(wf *WorkflowFile) error {
	for name, c := range wf.Connections {
		if name == "" {
			return fmt.Errorf("connection name cannot be empty")
		}
		if err := c.Validate(); err != nil {
			return fmt.Errorf("connection %q: %w", name, err)
		}
	}
	for name, d := range wf.Decisions {
		if name == "" {
			return fmt.Errorf("decision name cannot be empty")
		}
		if _, ok := wf.Connections[d.Connection]; !ok {
			return fmt.Errorf("decision %q: unknown connection %q", name, d.Connection)
		}
		if err := d.Question().Validate(); err != nil {
			return fmt.Errorf("decision %q: %w", name, err)
		}
	}
	return nil
}

func validateJevStep(wf *WorkflowFile, s Step) error {
	base, params := wf.ResolveStepType(&s)
	if base == "jev" {
		for k := range params {
			if k != "connection" && k != "state" && k != "questions" {
				return fmt.Errorf("jev: unknown param %q", k)
			}
		}
		conn, _ := params["connection"].(string)
		if _, ok := wf.Connections[conn]; !ok {
			return fmt.Errorf("jev: unknown connection %q", conn)
		}
		if v, ok := params["state"]; !ok || v == nil {
			return fmt.Errorf("jev: state is required")
		}
		if _, err := jev.Questions(params["questions"]); err != nil {
			return err
		}
	}
	if base == "gate" {
		for name, raw := range s.Facts {
			spec, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			decision, exists := spec["decision"]
			if !exists {
				continue
			}
			dn, ok := decision.(string)
			if !ok {
				return fmt.Errorf("fact %q: decision must be a name", name)
			}
			d, ok := wf.Decisions[dn]
			if !ok {
				return fmt.Errorf("fact %q: unknown decision %q", name, dn)
			}
			for k := range spec {
				if k != "decision" && k != "state" && k != "select" && k != "refresh" {
					return fmt.Errorf("fact %q: unknown decision field %q", name, k)
				}
			}
			if v, ok := spec["state"]; !ok || v == nil {
				return fmt.Errorf("fact %q: state is required", name)
			}
			if sel, ok := spec["select"]; ok {
				if sel != d.Type && !(d.Type != "noul" && (sel == "confidence" || sel == "probabilities")) {
					return fmt.Errorf("fact %q: invalid select %v for %s", name, sel, d.Type)
				}
			}
		}
	}
	return nil
}
