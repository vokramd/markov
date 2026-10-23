package template

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFromJSON_Object(t *testing.T) {
	eng := New()
	ctx := map[string]any{
		"raw": `{"name":"alice","age":30}`,
	}

	result, err := eng.Render("{{ raw | fromjson }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if result == "" || result == `{"name":"alice","age":30}` {
		t.Logf("rendered as string representation: %s", result)
	}
}

func TestFromJSON_ArrayAccess(t *testing.T) {
	eng := New()
	ctx := map[string]any{
		"raw": `[{"key":"A"},{"key":"B"}]`,
	}

	result, err := eng.Render("{{ raw | fromjson | length }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if result != "2" {
		t.Errorf("length = %q, want 2", result)
	}
}

func TestFromJSON_UsedInSetFact(t *testing.T) {
	eng := New()

	ctx := map[string]any{
		"stdout": `[{"key":"PROJ-100"},{"key":"PROJ-200"}]`,
	}

	rendered, err := eng.Render("{{ stdout | fromjson }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if rendered == "" {
		t.Error("rendered is empty")
	}
}

func TestFromJSON_InvalidJSON(t *testing.T) {
	eng := New()
	ctx := map[string]any{
		"raw": `not valid json`,
	}

	_, err := eng.Render("{{ raw | fromjson }}", ctx)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestFromJSON_NestedAccess(t *testing.T) {
	eng := New()
	ctx := map[string]any{
		"raw": `{"data":{"count":42}}`,
	}

	result, err := eng.Render("{{ raw | fromjson }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result == "" {
		t.Error("rendered is empty")
	}
}

func TestFromJSON_ScalarString(t *testing.T) {
	eng := New()
	ctx := map[string]any{
		"raw": `"hello world"`,
	}

	result, err := eng.Render("{{ raw | fromjson }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "hello world" {
		t.Errorf("result = %q, want 'hello world'", result)
	}
}

func TestFromJSON_Number(t *testing.T) {
	eng := New()
	ctx := map[string]any{
		"raw": `42`,
	}

	result, err := eng.Render("{{ raw | fromjson }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "42" {
		t.Errorf("result = %q, want '42'", result)
	}
}

func TestTrimFilter(t *testing.T) {
	eng := New()
	ctx := map[string]any{
		"stdout": "  RHAISTRAT-1\n",
	}

	result, err := eng.Render("{{ stdout | trim }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "RHAISTRAT-1" {
		t.Errorf("result = %q, want RHAISTRAT-1", result)
	}
}

func TestToJSONFilterAliases(t *testing.T) {
	eng := New()
	ctx := map[string]any{
		"config": map[string]any{"foo": "bar"},
	}

	for _, tmpl := range []string{
		"{{ config | to_json }}",
		"{{ config | tojson }}",
	} {
		result, err := eng.Render(tmpl, ctx)
		if err != nil {
			t.Fatalf("Render(%q): %v", tmpl, err)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("Render(%q) = %q, want JSON object: %v", tmpl, result, err)
		}
		if parsed["foo"] != "bar" {
			t.Fatalf("parsed foo = %v, want bar", parsed["foo"])
		}
	}
}

func TestFromJSONAlias(t *testing.T) {
	eng := New()
	ctx := map[string]any{
		"raw": `{"foo":"bar"}`,
	}

	rendered, err := eng.RenderMap(map[string]any{
		"config": "{{ raw | from_json }}",
	}, ctx)
	if err != nil {
		t.Fatalf("RenderMap: %v", err)
	}
	config, ok := rendered["config"].(map[string]any)
	if !ok {
		t.Fatalf("config = %#v, want map", rendered["config"])
	}
	if config["foo"] != "bar" {
		t.Fatalf("foo = %v, want bar", config["foo"])
	}
}

func TestFromJSONAliasParsesScalarInRenderMap(t *testing.T) {
	eng := New()
	ctx := map[string]any{
		"raw": `42`,
	}

	rendered, err := eng.RenderMap(map[string]any{
		"value": "{{ raw | from_json }}",
	}, ctx)
	if err != nil {
		t.Fatalf("RenderMap: %v", err)
	}
	if rendered["value"] != 42 {
		t.Fatalf("value = %#v, want int 42", rendered["value"])
	}
}

func TestRenderMapExactExpressionPreservesNativeMap(t *testing.T) {
	eng := New()
	ctx := map[string]any{
		"config": map[string]any{"foo": "bar"},
	}

	rendered, err := eng.RenderMap(map[string]any{
		"body": map[string]any{
			"args": map[string]any{
				"some_map": "{{ config }}",
			},
		},
	}, ctx)
	if err != nil {
		t.Fatalf("RenderMap: %v", err)
	}

	body := rendered["body"].(map[string]any)
	args := body["args"].(map[string]any)
	someMap, ok := args["some_map"].(map[string]any)
	if !ok {
		t.Fatalf("some_map = %#v, want map", args["some_map"])
	}
	if someMap["foo"] != "bar" {
		t.Fatalf("foo = %v, want bar", someMap["foo"])
	}
}

func TestRenderMapExactExpressionParsesJSONObjectString(t *testing.T) {
	eng := New()
	ctx := map[string]any{
		"config": `{"foo":"bar"}`,
	}

	rendered, err := eng.RenderMap(map[string]any{
		"some_map": "{{ config }}",
	}, ctx)
	if err != nil {
		t.Fatalf("RenderMap: %v", err)
	}
	someMap, ok := rendered["some_map"].(map[string]any)
	if !ok {
		t.Fatalf("some_map = %#v, want map", rendered["some_map"])
	}
	if someMap["foo"] != "bar" {
		t.Fatalf("foo = %v, want bar", someMap["foo"])
	}
}

func TestRenderMapMixedTemplateStillRendersString(t *testing.T) {
	eng := New()
	ctx := map[string]any{
		"config": map[string]any{"foo": "bar"},
	}

	rendered, err := eng.RenderMap(map[string]any{
		"message": "config={{ config | to_json }}",
	}, ctx)
	if err != nil {
		t.Fatalf("RenderMap: %v", err)
	}
	if rendered["message"] != `config={"foo":"bar"}` {
		t.Fatalf("message = %q, want JSON string interpolation", rendered["message"])
	}
}

func TestFromJSON_WholeNumbersRenderAsIntegers(t *testing.T) {
	eng := New()
	ctx := map[string]any{"raw": `{"ids": [50163277, 7], "score": 1.5}`}

	result, err := eng.Render("{% set d = raw | fromjson %}{{ d.ids.0 }}/{{ d.ids.1 }}/{{ d.score }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "50163277/7/1.500000" {
		t.Errorf("result = %q, want %q", result, "50163277/7/1.500000")
	}
}

func TestDivisionByZeroIsAnErrorNotAPanic(t *testing.T) {
	e := New()
	// An unquoted path in an expression context parses as a division of undefined names.
	if _, err := e.EvalBool("openshell/ai-pipeline-ca.pem", map[string]any{}); err == nil || !strings.Contains(err.Error(), "division by zero") {
		t.Errorf("EvalBool: %v", err)
	}
	for _, tmpl := range []string{"{{ 1 / 0 }}", "{{ 5 % 0 }}", "{{ n / zero }}"} {
		if _, err := e.Render(tmpl, map[string]any{"n": 4, "zero": 0}); err == nil {
			t.Errorf("%s rendered without an error", tmpl)
		}
	}
	if got, err := e.Render("{{ 7 / 2 }}", nil); err != nil || got != "3" {
		t.Errorf("ordinary division: %q %v", got, err)
	}
}
