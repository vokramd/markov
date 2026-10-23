package template

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/flosch/pongo2/v6"
	"github.com/jctanner/markov/pkg/jsonvalue"
)

func init() {
	// Workflows render shell commands, script arguments, JSON bodies and file paths, never
	// HTML, so values are inserted as they are.
	pongo2.SetAutoescape(false)

	pongo2.RegisterFilter(captureFilter, filterCapture)
	pongo2.RegisterFilter("csv", filterCSV)
	pongo2.RegisterFilter("seq", filterSeq)
	pongo2.RegisterFilter("flatten", filterFlatten)
	pongo2.RegisterFilter("pluck", filterPluck)
	pongo2.RegisterFilter("fromjson", filterFromJSON)
	pongo2.RegisterFilter("from_json", filterFromJSON)
	pongo2.RegisterFilter("trim", filterTrim)
	pongo2.RegisterFilter("to_json", filterToJSON)
	pongo2.RegisterFilter("tojson", filterToJSON)
}

func filterFromJSON(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	raw := in.String()
	parsed, err := jsonvalue.Decode([]byte(raw))
	if err != nil {
		return nil, &pongo2.Error{
			Sender:    "filter:fromjson",
			OrigError: err,
		}
	}
	return pongo2.AsValue(parsed), nil
}

func filterTrim(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	return pongo2.AsValue(strings.TrimSpace(in.String())), nil
}

func filterToJSON(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	data, err := json.Marshal(in.Interface())
	if err != nil {
		return nil, &pongo2.Error{
			Sender:    "filter:to_json",
			OrigError: err,
		}
	}
	return pongo2.AsSafeValue(string(data)), nil
}

// filterCSV turns comma-separated text into a list, trimming each item and dropping empty ones,
// so "" gives an empty list: `{{ "S1, S2" | csv }}` is ["S1", "S2"]. (pongo2's own `split` keeps
// empty items and spaces.)
func filterCSV(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	items := []any{}
	for _, item := range strings.Split(in.String(), ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return pongo2.AsValue(items), nil
}

// filterSeq turns a count into the list 1..n: `{{ 3 | seq }}` is [1, 2, 3]. A string count is
// parsed; an empty one gives an empty list.
func filterSeq(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	n := 0
	switch {
	case in.IsInteger(), in.IsFloat():
		n = in.Integer()
	case strings.TrimSpace(in.String()) != "":
		parsed, err := strconv.Atoi(strings.TrimSpace(in.String()))
		if err != nil {
			return nil, &pongo2.Error{Sender: "filter:seq", OrigError: fmt.Errorf("not a count: %q", in.String())}
		}
		n = parsed
	}
	out := make([]any, 0, max(n, 0))
	for i := 1; i <= n; i++ {
		out = append(out, i)
	}
	return pongo2.AsValue(out), nil
}

// filterFlatten flattens nested lists into one list: [[1, 2], [3]] gives [1, 2, 3]. With a
// number it flattens that many levels only.
func filterFlatten(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	depth := -1
	if param != nil && !param.IsNil() && param.String() != "" {
		depth = param.Integer()
	}
	return pongo2.AsValue(flatten(in.Interface(), depth)), nil
}

func flatten(value any, depth int) []any {
	out := []any{}
	v := reflect.ValueOf(value)
	if !v.IsValid() || (v.Kind() != reflect.Slice && v.Kind() != reflect.Array) {
		return out
	}
	for i := 0; i < v.Len(); i++ {
		item := v.Index(i).Interface()
		iv := reflect.ValueOf(item)
		if depth != 0 && iv.IsValid() && (iv.Kind() == reflect.Slice || iv.Kind() == reflect.Array) {
			out = append(out, flatten(item, depth-1)...)
		} else {
			out = append(out, item)
		}
	}
	return out
}

// filterPluck takes one field from each item of a list: `{{ runs | pluck:"eval.stdout" }}`.
// The field may be a dotted path; items without it give nil.
func filterPluck(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	path := strings.Split(param.String(), ".")
	out := []any{}
	v := reflect.ValueOf(in.Interface())
	if !v.IsValid() || (v.Kind() != reflect.Slice && v.Kind() != reflect.Array) {
		return nil, &pongo2.Error{Sender: "filter:pluck", OrigError: fmt.Errorf("pluck needs a list, got %T", in.Interface())}
	}
	for i := 0; i < v.Len(); i++ {
		var current any = v.Index(i).Interface()
		for _, key := range path {
			m, ok := current.(map[string]any)
			if !ok {
				current = nil
				break
			}
			current = m[key]
		}
		out = append(out, current)
	}
	return pongo2.AsValue(out), nil
}

// Native evaluation: an exact `{{ expr }}` value should keep the type the expression produces
// (a list from `split`, a map from `tiers[tier]`), not its printed form. pongo2 only prints, so
// the expression is bound with {% with %} and passed to a capture filter that stores the value
// under a per-call token.
const captureFilter = "markov_native_capture"

var (
	captured   sync.Map
	captureSeq atomic.Uint64
)

func filterCapture(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	captured.Store(param.String(), in.Interface())
	return pongo2.AsValue(""), nil
}

// evalNative evaluates a template expression and returns its value. ok is false when the
// expression is undefined (nil), so the caller keeps rendering it as text.
func (e *Engine) evalNative(expr string, ctx map[string]any) (any, bool, error) {
	token := strconv.FormatUint(captureSeq.Add(1), 10)
	defer captured.Delete(token)
	tmpl := fmt.Sprintf("{%% with markov_native_value=%s %%}{{ markov_native_value|%s:\"%s\" }}{%% endwith %%}", expr, captureFilter, token)
	if _, err := e.Render(tmpl, ctx); err != nil {
		return nil, false, err
	}
	value, ok := captured.Load(token)
	if !ok || value == nil {
		return nil, false, nil
	}
	return value, true, nil
}

// Eval evaluates an expression (without braces) to its native value; undefined gives nil.
func (e *Engine) Eval(expr string, ctx map[string]any) (any, error) {
	value, _, err := e.evalNative(expr, ctx)
	return value, err
}

type Engine struct{}

func New() *Engine {
	return &Engine{}
}

func (e *Engine) Render(tmpl string, ctx map[string]any) (string, error) {
	t, err := pongo2.FromString(tmpl)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}
	result, err := execute(t, ctx)
	if err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}
	return result, nil
}

// execute runs a parsed template, turning a panic inside the template engine into an error so
// one bad expression fails its step instead of the whole process.
func execute(t *pongo2.Template, ctx map[string]any) (result string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("template engine failed: %v", r)
		}
	}()
	return t.Execute(pongo2.Context(ctx))
}

func (e *Engine) RenderMap(params map[string]any, ctx map[string]any) (map[string]any, error) {
	result := make(map[string]any)
	for k, v := range params {
		rendered, err := e.renderValue(v, ctx)
		if err != nil {
			return nil, fmt.Errorf("rendering param %q: %w", k, err)
		}
		result[k] = rendered
	}
	return result, nil
}

func (e *Engine) EvalBool(expr string, ctx map[string]any) (bool, error) {
	wrapped := fmt.Sprintf("{%% if %s %%}true{%% endif %%}", expr)
	t, err := pongo2.FromString(wrapped)
	if err != nil {
		return false, fmt.Errorf("parsing expression %q: %w", expr, err)
	}
	result, err := execute(t, ctx)
	if err != nil {
		return false, fmt.Errorf("evaluating expression %q: %w", expr, err)
	}
	return strings.TrimSpace(result) == "true", nil
}

func (e *Engine) renderValue(v any, ctx map[string]any) (any, error) {
	switch val := v.(type) {
	case string:
		if !strings.Contains(val, "{{") && !strings.Contains(val, "{%") {
			return val, nil
		}
		if rendered, ok, err := e.renderNativeExpression(val, ctx); ok || err != nil {
			return rendered, err
		}
		if expr, ok := exactExpression(val); ok {
			if value, ok, err := e.evalNative(expr, ctx); ok || err != nil {
				return value, err
			}
		}
		return e.Render(val, ctx)
	case map[string]any:
		return e.RenderMap(val, ctx)
	case []any:
		result := make([]any, len(val))
		for i, item := range val {
			rendered, err := e.renderValue(item, ctx)
			if err != nil {
				return nil, err
			}
			result[i] = rendered
		}
		return result, nil
	default:
		return v, nil
	}
}

func (e *Engine) renderNativeExpression(tmpl string, ctx map[string]any) (any, bool, error) {
	expr, ok := exactExpression(tmpl)
	if !ok {
		return nil, false, nil
	}

	parts := strings.Split(expr, "|")
	path := strings.TrimSpace(parts[0])
	if path == "" {
		return nil, false, nil
	}
	value, ok := lookupPath(ctx, path)
	if !ok {
		return nil, false, nil
	}

	for _, rawFilter := range parts[1:] {
		filter := strings.TrimSpace(rawFilter)
		switch filter {
		case "fromjson", "from_json":
			parsed, err := parseJSONValue(value)
			if err != nil {
				return nil, true, fmt.Errorf("from_json: %w", err)
			}
			value = parsed
		case "tojson", "to_json":
			data, err := json.Marshal(value)
			if err != nil {
				return nil, true, fmt.Errorf("to_json: %w", err)
			}
			value = string(data)
		default:
			return nil, false, nil
		}
	}

	if len(parts) == 1 {
		if parsed, err := parseJSONObjectOrArrayString(value); err == nil {
			value = parsed
		}
	}
	return value, true, nil
}

func exactExpression(tmpl string) (string, bool) {
	s := strings.TrimSpace(tmpl)
	if !strings.HasPrefix(s, "{{") || !strings.HasSuffix(s, "}}") {
		return "", false
	}
	inner := strings.TrimSpace(s[2 : len(s)-2])
	if inner == "" || strings.Contains(inner, "{") || strings.Contains(inner, "}") {
		return "", false
	}
	return inner, true
}

func lookupPath(ctx map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var current any = ctx
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func parseJSONValue(value any) (any, error) {
	s, ok := value.(string)
	if !ok {
		return value, nil
	}
	return jsonvalue.Decode([]byte(strings.TrimSpace(s)))
}

func parseJSONObjectOrArrayString(value any) (any, error) {
	s, ok := value.(string)
	if !ok {
		return value, nil
	}
	s = strings.TrimSpace(s)
	if len(s) == 0 || (s[0] != '{' && s[0] != '[') {
		return value, fmt.Errorf("value is not a JSON object or array")
	}
	return parseJSONValue(s)
}
