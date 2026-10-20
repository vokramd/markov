// Package jsonvalue decodes JSON into workflow values. Whole numbers become
// int rather than float64, so a template prints an ID from an API response as
// 50163277, not 50163277.000000.
package jsonvalue

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Decode parses one JSON value. Numbers written without a fraction or
// exponent that fit in an int64 become int; all other numbers stay float64.
func Decode(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("invalid character after top-level value")
	}
	return Normalize(v), nil
}

// Normalize replaces json.Number values (from a decoder with UseNumber) with
// int or float64, in place for maps and slices.
func Normalize(v any) any {
	switch val := v.(type) {
	case json.Number:
		if i, err := val.Int64(); err == nil {
			return int(i)
		}
		f, _ := val.Float64()
		return f
	case map[string]any:
		for k, item := range val {
			val[k] = Normalize(item)
		}
		return val
	case []any:
		for i, item := range val {
			val[i] = Normalize(item)
		}
		return val
	default:
		return v
	}
}
