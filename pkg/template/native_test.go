package template

import (
	"reflect"
	"testing"
)

func TestRenderDoesNotHTMLEscape(t *testing.T) {
	out, err := New().Render(`{{ v }}`, map[string]any{"v": `{"a": "<b>&"}`})
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"a": "<b>&"}` {
		t.Fatalf("got %q", out)
	}
}

func TestCSVFilter(t *testing.T) {
	ctx := map[string]any{"csv": " S1, S2 ,,", "empty": ""}
	cases := map[string]any{
		`{{ csv | csv }}`:   []any{"S1", "S2"},
		`{{ empty | csv }}`: []any{},
	}
	for tmpl, want := range cases {
		got, err := New().RenderMap(map[string]any{"v": tmpl}, ctx)
		if err != nil {
			t.Fatalf("%s: %v", tmpl, err)
		}
		if !reflect.DeepEqual(got["v"], want) {
			t.Fatalf("%s: got %#v, want %#v", tmpl, got["v"], want)
		}
	}
}

func TestSeqFilter(t *testing.T) {
	ctx := map[string]any{"n": 3, "s": "2", "empty": ""}
	cases := map[string]any{
		`{{ n | seq }}`:     []any{1, 2, 3},
		`{{ s | seq }}`:     []any{1, 2},
		`{{ empty | seq }}`: []any{},
	}
	for tmpl, want := range cases {
		got, err := New().RenderMap(map[string]any{"v": tmpl}, ctx)
		if err != nil {
			t.Fatalf("%s: %v", tmpl, err)
		}
		if !reflect.DeepEqual(got["v"], want) {
			t.Fatalf("%s: got %#v, want %#v", tmpl, got["v"], want)
		}
	}
	if _, err := New().RenderMap(map[string]any{"v": `{{ "x" | seq }}`}, nil); err == nil {
		t.Fatal("seq of a non-number should fail")
	}
}

func TestExactExpressionKeepsNativeValue(t *testing.T) {
	tiers := map[string]any{"smoke": map[string]any{"repeats": 3}}
	ctx := map[string]any{"tiers": tiers, "tier": "smoke", "variants": "", "own": []any{"haiku"}, "n": 2}
	cases := map[string]any{
		`{{ tiers[tier] }}`:                      tiers["smoke"],
		`{{ variants | csv | default:own }}`:     []any{"haiku"},
		`{{ "opus,haiku" | csv | default:own }}`: []any{"opus", "haiku"},
		`{{ n + 1 }}`:                            3,
		`{{ missing }}`:                          "",
		`{{ tiers.smoke.repeats }}`:              3,
	}
	for tmpl, want := range cases {
		got, err := New().RenderMap(map[string]any{"v": tmpl}, ctx)
		if err != nil {
			t.Fatalf("%s: %v", tmpl, err)
		}
		if !reflect.DeepEqual(got["v"], want) {
			t.Fatalf("%s: got %#v (%T), want %#v", tmpl, got["v"], got["v"], want)
		}
	}
}

func TestEval(t *testing.T) {
	got, err := New().Eval(`items | csv`, map[string]any{"items": "a,b"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []any{"a", "b"}) {
		t.Fatalf("got %#v", got)
	}
	if got, _ := New().Eval(`nothing`, nil); got != nil {
		t.Fatalf("undefined: got %#v", got)
	}
}

func TestChainedSubscripts(t *testing.T) {
	ctx := map[string]any{
		"tiers": map[string]any{"smoke": map[string]any{"tests": []any{"S1", "S2"}}},
		"tier":  "smoke",
		"grid":  []any{[]any{1, 2}, []any{3, 4}},
	}
	cases := map[string]string{
		`{{ tiers[tier].tests.1 }}`:     "S2",
		`{{ tiers[tier]["tests"][0] }}`: "S1",
		`{{ grid[1][0] }}`:              "3",
	}
	for tmpl, want := range cases {
		got, err := New().Render(tmpl, ctx)
		if err != nil || got != want {
			t.Fatalf("%s: got %q, %v; want %q", tmpl, got, err, want)
		}
	}
}

func TestFlattenAndPluck(t *testing.T) {
	rounds := []any{
		map[string]any{"tests_done": []any{
			map[string]any{"variants_done": []any{map[string]any{"eval": map[string]any{"stdout": "a"}}}},
			map[string]any{"variants_done": []any{map[string]any{"eval": map[string]any{"stdout": "b"}}}},
		}},
		map[string]any{"tests_done": []map[string]any{
			{"variants_done": []any{map[string]any{"eval": map[string]any{"stdout": "c"}}}},
		}},
	}
	ctx := map[string]any{"rounds": rounds, "nested": []any{1, []any{2, []any{3}}}}
	cases := map[string]any{
		`{{ rounds | pluck:"tests_done" | flatten | pluck:"variants_done" | flatten | pluck:"eval.stdout" }}`: []any{"a", "b", "c"},
		`{{ nested | flatten }}`:   []any{1, 2, 3},
		`{{ nested | flatten:1 }}`: []any{1, 2, []any{3}},
	}
	for tmpl, want := range cases {
		got, err := New().RenderMap(map[string]any{"v": tmpl}, ctx)
		if err != nil {
			t.Fatalf("%s: %v", tmpl, err)
		}
		if !reflect.DeepEqual(got["v"], want) {
			t.Fatalf("%s: got %#v, want %#v", tmpl, got["v"], want)
		}
	}
}

// An exact {{ expr }} with text around it, such as the newline a YAML block scalar ends with, keeps
// that text when the value is a string: it is part of what was written. A structured value (a
// list, a map) can't carry it, so it stays native.
func TestExactExpressionKeepsSurroundingWhitespaceForStrings(t *testing.T) {
	ctx := map[string]any{"s": "a", "obj": map[string]any{"k": 1}, "list": []any{1, 2}, "n": 3}
	cases := map[string]any{
		"{{ s }}\n":             "a\n",
		"  {{ s }}":             "  a",
		"{{ obj | to_json }}\n": `{"k":1}` + "\n",
		"{{ s }}":               "a",
		"{{ list }}\n":          []any{1, 2},
		"{{ obj }}\n":           map[string]any{"k": 1},
		"{{ n }}\n":             3,
	}
	for tmpl, want := range cases {
		got, err := New().RenderMap(map[string]any{"v": tmpl}, ctx)
		if err != nil {
			t.Fatalf("%q: %v", tmpl, err)
		}
		if !reflect.DeepEqual(got["v"], want) {
			t.Errorf("%q: got %#v, want %#v", tmpl, got["v"], want)
		}
	}
}
