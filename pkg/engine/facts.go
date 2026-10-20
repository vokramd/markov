package engine

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jctanner/markov/pkg/jsonvalue"
)

// evalFacts evaluates every fact against runCtx as it was before the step and leaves runCtx
// unchanged. Facts in one step cannot see each other: vars is a Go map with no order, so letting
// each fact see the ones evaluated before it made the result depend on map iteration order.
func (e *Engine) evalFacts(vars map[string]any, runCtx map[string]any) (map[string]any, error) {
	result := make(map[string]any)

	for k, v := range vars {
		val, err := e.evalFact(v, runCtx)
		if err != nil {
			return nil, fmt.Errorf("fact %q: %w", k, err)
		}
		result[k] = val
	}

	return result, nil
}

func (e *Engine) evalFact(v any, runCtx map[string]any) (any, error) {
	switch val := v.(type) {
	case string:
		if strings.Contains(val, "{{") || strings.Contains(val, "{%") {
			if path, ok := extractFromJSONExpr(val); ok {
				raw := resolveContextPath(path, runCtx)
				if raw != nil {
					if s, ok := raw.(string); ok {
						parsed, err := jsonvalue.Decode([]byte(strings.TrimSpace(s)))
						if err != nil {
							return nil, fmt.Errorf("fromjson: %w", err)
						}
						return parsed, nil
					}
				}
			}
			// An exact {{ expr }} keeps its native value (a map, a list, a number); anything
			// rendered to text is coerced as before.
			rendered, err := e.renderTemplateValue(val, runCtx)
			if err != nil {
				return nil, fmt.Errorf("rendering template: %w", err)
			}
			if text, ok := rendered.(string); ok {
				return coerceString(text), nil
			}
			return rendered, nil
		}
		result, err := e.tmpl.EvalBool(val, runCtx)
		if err != nil {
			return nil, fmt.Errorf("evaluating expression: %w", err)
		}
		return result, nil

	case map[string]any:
		if fromPath, ok := val["from"].(string); ok {
			return e.lookupFact(fromPath, val, runCtx)
		}
		// Nested values are rendered like step params: templates are rendered, exact
		// expressions keep native values, and other strings are kept as they are.
		return e.renderTemplateValue(val, runCtx)

	case []any:
		return e.renderTemplateValue(val, runCtx)

	default:
		return v, nil
	}
}

// renderTemplateValue renders a value with the same rules as step params.
func (e *Engine) renderTemplateValue(v any, runCtx map[string]any) (any, error) {
	rendered, err := e.tmpl.RenderMap(map[string]any{"v": v}, runCtx)
	if err != nil {
		return nil, err
	}
	return rendered["v"], nil
}

func (e *Engine) lookupFact(fromPath string, spec map[string]any, runCtx map[string]any) (any, error) {
	source := resolveContextPath(fromPath, runCtx)
	if source == nil {
		if def, ok := spec["default"]; ok {
			return def, nil
		}
		return nil, nil
	}

	var rows []any
	switch v := source.(type) {
	case []any:
		rows = v
	case []map[string]any:
		rows = make([]any, len(v))
		for i, r := range v {
			rows[i] = r
		}
	default:
		return nil, fmt.Errorf("lookup: %q is not a list (got %T)", fromPath, source)
	}

	matchSpec, ok := spec["match"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("lookup: match criteria required")
	}

	matchVals := make(map[string]string)
	for k, v := range matchSpec {
		vs := fmt.Sprintf("%v", v)
		if strings.Contains(vs, "{{") {
			rendered, err := e.tmpl.Render(vs, runCtx)
			if err != nil {
				return nil, fmt.Errorf("lookup match %q: %w", k, err)
			}
			vs = rendered
		}
		matchVals[k] = vs
	}

	for _, item := range rows {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		matched := true
		for k, v := range matchVals {
			if fmt.Sprintf("%v", row[k]) != v {
				matched = false
				break
			}
		}
		if matched {
			if field, ok := spec["field"].(string); ok {
				return row[field], nil
			}
			return row, nil
		}
	}

	if def, ok := spec["default"]; ok {
		return def, nil
	}
	return nil, nil
}

func extractFromJSONExpr(tmpl string) (string, bool) {
	s := strings.TrimSpace(tmpl)
	if !strings.HasPrefix(s, "{{") || !strings.HasSuffix(s, "}}") {
		return "", false
	}
	inner := strings.TrimSpace(s[2 : len(s)-2])
	filter := ""
	switch {
	case strings.HasSuffix(inner, "| from_json"):
		filter = "| from_json"
	case strings.HasSuffix(inner, "| fromjson"):
		filter = "| fromjson"
	default:
		return "", false
	}
	path := strings.TrimSpace(inner[:len(inner)-len(filter)])
	if path == "" {
		return "", false
	}
	return path, true
}

func coerceString(s string) any {
	s = strings.TrimSpace(s)
	if s == "True" || s == "true" {
		return true
	}
	if s == "False" || s == "false" {
		return false
	}
	if s == "None" || s == "" {
		return s
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return int(i)
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	if len(s) > 0 && (s[0] == '[' || s[0] == '{') {
		if parsed, err := jsonvalue.Decode([]byte(s)); err == nil {
			return parsed
		}
	}
	return s
}

// CoerceString converts a CLI or rendered string to Markov's canonical native
// value representation. It preserves ordinary strings while recognizing
// booleans, numbers, and JSON arrays or objects.
func CoerceString(s string) any {
	return coerceString(s)
}
