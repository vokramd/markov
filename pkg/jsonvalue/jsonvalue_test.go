package jsonvalue

import (
	"reflect"
	"testing"
)

func TestDecodeKeepsWholeNumbersAsInt(t *testing.T) {
	got, err := Decode([]byte(`{"id": 50163277, "score": 1.5, "big": 1e3, "ids": [1, 2], "nested": {"n": -7}}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := map[string]any{
		"id":     50163277,
		"score":  1.5,
		"big":    1000.0,
		"ids":    []any{1, 2},
		"nested": map[string]any{"n": -7},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode = %#v, want %#v", got, want)
	}
}

func TestDecodeScalarsAndErrors(t *testing.T) {
	if got, err := Decode([]byte(` 42 `)); err != nil || got != 42 {
		t.Fatalf("Decode(42) = %#v, %v", got, err)
	}
	if got, err := Decode([]byte(`"x"`)); err != nil || got != "x" {
		t.Fatalf(`Decode("x") = %#v, %v`, got, err)
	}
	for _, bad := range []string{``, `{`, `1 2`, `[1] x`} {
		if _, err := Decode([]byte(bad)); err == nil {
			t.Errorf("Decode(%q) succeeded, want error", bad)
		}
	}
}

func TestDecodeHugeIntegerFallsBackToFloat(t *testing.T) {
	got, err := Decode([]byte(`99999999999999999999`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if _, ok := got.(float64); !ok {
		t.Fatalf("Decode = %#v (%T), want float64", got, got)
	}
}
