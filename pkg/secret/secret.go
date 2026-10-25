// Package secret masks secret values in what Markov writes out: events and log lines. Values
// are masked wherever they appear, so a secret is hidden inside a rendered URL, shell output or
// an error message as well as in the var that held it.
package secret

import (
	"encoding/json"
	"io"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// MinLength is the shortest value that is masked; shorter ones would mask ordinary text.
const MinLength = 8

// secretName matches var names that are secret without being declared.
var secretName = regexp.MustCompile(`(?i)(token|secret|password|passwd|api_?key|private_?key|credentials?)$`)

// LooksSecret reports whether a var name says it holds a secret (GITHUB_TOKEN, db_password).
func LooksSecret(name string) bool {
	return secretName.MatchString(name)
}

// Masker holds secret values and replaces each with <secret:NAME>. It is safe for concurrent
// use, and the zero value is ready (and masks nothing).
type Masker struct {
	mu       sync.RWMutex
	values   map[string]string // value -> name
	replacer *strings.Replacer
}

// Add marks every string in v (a string, or nested maps and lists) as a secret named name.
func (m *Masker) Add(name string, v any) {
	var found []string
	collect(v, &found)
	if len(found) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.values == nil {
		m.values = map[string]string{}
	}
	added := false
	for _, s := range found {
		for _, form := range []string{s, strings.TrimSpace(s), url.QueryEscape(s)} {
			if len(form) < MinLength {
				continue
			}
			if _, ok := m.values[form]; !ok {
				m.values[form] = name
				added = true
			}
		}
	}
	if added {
		m.rebuild()
	}
}

// AddPath marks the value at a dotted path in v ("token", "body.token") as a secret named name.
// A path that is not there adds nothing.
func (m *Masker) AddPath(name string, v any, path string) {
	for _, key := range strings.Split(path, ".") {
		node, ok := v.(map[string]any)
		if !ok {
			return
		}
		if v, ok = node[key]; !ok {
			return
		}
	}
	m.Add(name, v)
}

func collect(v any, out *[]string) {
	switch v := v.(type) {
	case string:
		if len(strings.TrimSpace(v)) >= MinLength {
			*out = append(*out, v)
		}
	case map[string]any:
		for _, item := range v {
			collect(item, out)
		}
	case []any:
		for _, item := range v {
			collect(item, out)
		}
	case []string:
		for _, item := range v {
			collect(item, out)
		}
	case map[string]string:
		for _, item := range v {
			collect(item, out)
		}
	}
}

// rebuild makes the replacer, longest values first so a secret containing another is masked
// whole. Called with mu held.
func (m *Masker) rebuild() {
	values := make([]string, 0, len(m.values))
	for v := range m.values {
		values = append(values, v)
	}
	sort.Slice(values, func(i, j int) bool {
		if len(values[i]) != len(values[j]) {
			return len(values[i]) > len(values[j])
		}
		return values[i] < values[j]
	})
	pairs := make([]string, 0, 2*len(values))
	for _, v := range values {
		pairs = append(pairs, v, "<secret:"+m.values[v]+">")
	}
	m.replacer = strings.NewReplacer(pairs...)
}

// Empty reports whether there is nothing to mask.
func (m *Masker) Empty() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.replacer == nil
}

// String masks s.
func (m *Masker) String(s string) string {
	m.mu.RLock()
	r := m.replacer
	m.mu.RUnlock()
	if r == nil {
		return s
	}
	return r.Replace(s)
}

// Value is a masked copy of v: strings, and the strings and keys inside maps and lists. Maps and
// lists ([]any, map[string]any) are always copied, even with nothing to mask, so the copy can be
// kept while v changes. Typed values (a []string, a struct) come back as their JSON shape when
// there is something to mask, and as they are when there is not.
func (m *Masker) Value(v any) any {
	return m.value(v)
}

func (m *Masker) value(v any) any {
	switch v := v.(type) {
	case string:
		return m.String(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[m.String(k)] = m.value(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = m.value(item)
		}
		return out
	case nil, bool, int, int64, float64:
		return v
	default:
		if m.Empty() {
			return v // typed values keep their type when there is nothing to mask
		}
		return m.reflected(v)
	}
}

// reflected masks the typed values executors return ([]string, map[string]string, structs) as
// their JSON shape: lists become []any and maps and structs map[string]any. A value with no
// strings in it comes back as it was.
func (m *Masker) reflected(v any) any {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		return m.String(rv.String())
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
			return m.String(string(rv.Bytes()))
		}
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = m.value(rv.Index(i).Interface())
		}
		return out
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return v
		}
		out := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			out[m.String(iter.Key().String())] = m.value(iter.Value().Interface())
		}
		return out
	case reflect.Struct, reflect.Pointer, reflect.Interface:
		data, err := json.Marshal(v)
		if err != nil {
			return "<unmaskable value>"
		}
		var plain any
		if err := json.Unmarshal(data, &plain); err != nil {
			return "<unmaskable value>"
		}
		return m.value(plain)
	default:
		return v
	}
}

// Writer is w with every write masked. Each write is masked on its own, which suits the log
// package (one write per line).
func (m *Masker) Writer(w io.Writer) io.Writer {
	return writer{m: m, w: w}
}

type writer struct {
	m *Masker
	w io.Writer
}

func (w writer) Write(p []byte) (int, error) {
	if w.m.Empty() {
		return w.w.Write(p)
	}
	if _, err := io.WriteString(w.w, w.m.String(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}
