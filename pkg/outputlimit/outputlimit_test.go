package outputlimit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"0": 0, "512": 512, "512KB": 512 << 10, "64MB": 64 << 20, "64 mb": 64 << 20, "1.5GB": 3 << 29, "2G": 2 << 30} {
		got, err := ParseSize(in)
		if err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "lots", "-1MB", "MB"} {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) succeeded", in)
		}
	}
	var s struct {
		Max Size `yaml:"max_output"`
	}
	if err := yaml.Unmarshal([]byte("max_output: 200MB"), &s); err != nil || !s.Max.Set || s.Max.Bytes != 200<<20 {
		t.Fatalf("yaml: %+v, %v", s.Max, err)
	}
	if err := yaml.Unmarshal([]byte("max_output: 0"), &s); err != nil || !s.Max.Set || s.Max.Bytes != 0 {
		t.Fatalf("yaml 0: %+v, %v", s.Max, err)
	}
}

// Measure is close to the real JSON size, escapes included, without building it.
func TestMeasureMatchesJSON(t *testing.T) {
	v := map[string]any{
		"text":  "a \"quoted\"\nline\tand ünïcode",
		"list":  []any{1, 2.5, true, nil, "x"},
		"inner": map[string]any{"n": int64(-42), "json": `{"a":[1,2]}`},
		"typed": []map[string]any{{"k": "v"}},
	}
	b, _ := json.Marshal(v)
	if got := Measure(v, 0); got != int64(len(b)) {
		t.Fatalf("Measure = %d, json.Marshal = %d (%s)", got, len(b), b)
	}
}

// Measuring stops soon after the limit, so a huge value is not walked whole.
func TestMeasureStopsEarly(t *testing.T) {
	big := make([]any, 1_000_000)
	for i := range big {
		big[i] = "0123456789"
	}
	if got := Measure(big, 1000); got <= 1000 || got > 1100 {
		t.Fatalf("Measure with stop = %d, want just over 1000", got)
	}
}

// The failure names the biggest parts: here the trial's register held inside a register.
func TestCheckReportsTheLargestParts(t *testing.T) {
	issues := []any{map[string]any{"body": strings.Repeat("x", 5000)}}
	comments := []any{map[string]any{"issue": map[string]any{"n": 1}, "issues": issues}}
	v := map[string]any{"command_inputs": map[string]any{"issue_comments": comments, "branches": "small"}}
	err := Check(v, 1000)
	e, ok := As(err)
	if !ok {
		t.Fatalf("Check = %v, want a limit error", err)
	}
	if e.Bytes < 5000 || e.Limit != 1000 || len(e.Largest) == 0 || e.Largest[0].Path != "command_inputs" {
		t.Fatalf("error = %+v", e)
	}
	var paths []string
	for _, p := range e.Largest {
		paths = append(paths, p.Path)
	}
	if !strings.Contains(strings.Join(paths, " "), "command_inputs.issue_comments[0].issues") {
		t.Errorf("largest = %v, want the nested register named", paths)
	}
	if !strings.Contains(err.Error(), "over max_output 1000 B") {
		t.Errorf("message = %q", err.Error())
	}
	if Check(v, 0) != nil || Check(v, 1<<20) != nil {
		t.Error("no limit, or a high one, must pass")
	}
}

func TestCounterWriterStopsAtTheLimit(t *testing.T) {
	c := NewCounter(10)
	var out, errs bytes.Buffer
	stopped := 0
	w1 := c.Writer(&out, "stdout", func() { stopped++ })
	w2 := c.Writer(&errs, "stderr", func() { stopped++ })
	if _, err := w1.Write([]byte("123456")); err != nil {
		t.Fatal(err)
	}
	if _, err := w2.Write([]byte("78901")); err == nil {
		t.Fatal("write past the limit succeeded")
	}
	if _, err := w1.Write([]byte("x")); err == nil {
		t.Fatal("write after the limit succeeded")
	}
	e, ok := As(c.Err())
	if !ok || e.Stream != "stderr" || e.Bytes != 11 || out.String() != "123456" || errs.Len() != 0 || stopped != 2 {
		t.Fatalf("err %+v, out %q, errs %q, stopped %d", e, out.String(), errs.String(), stopped)
	}
	if NewCounter(0).Add("x", 1<<40) != nil {
		t.Error("a zero limit is no limit")
	}
}

func TestFormat(t *testing.T) {
	for n, want := range map[int64]string{512: "512 B", 2048: "2 KB", 64 << 20: "64 MB", 562603118: "537 MB", 1289468104: "1.2 GB"} {
		if got := Format(n); got != want {
			t.Errorf("Format(%d) = %q, want %q", n, got, want)
		}
	}
}
