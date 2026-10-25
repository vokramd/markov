// Package outputlimit caps how much a step may produce. A step's output is held in the run's
// context and written to its event, its state row and any template that renders it, so one
// runaway value can take the whole process (and the machine) down; a step over its limit fails
// instead, with a breakdown of what was big.
package outputlimit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Default is the limit for a step that sets none: far above what ordinary steps produce, far
// below what hurts.
const Default int64 = 64 << 20

// Size is a max_output setting: a number of bytes, or a size with a unit (512KB, 64MB, 2GB; units
// are powers of 1024). 0 means no limit. Set is false when nothing was given.
type Size struct {
	Bytes int64
	Set   bool
}

// ParseSize reads a size: plain bytes or a number with KB, MB or GB.
func ParseSize(s string) (int64, error) {
	t := strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"KB", 1 << 10}, {"MB", 1 << 20}, {"GB", 1 << 30}, {"K", 1 << 10}, {"M", 1 << 20}, {"G", 1 << 30}, {"B", 1}} {
		if strings.HasSuffix(t, u.suffix) {
			t, mult = strings.TrimSpace(strings.TrimSuffix(t, u.suffix)), u.mult
			break
		}
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil || f < 0 || math.IsInf(f, 0) {
		return 0, fmt.Errorf("max_output: %q is not a size (bytes, or a number with KB, MB or GB)", s)
	}
	return int64(f * float64(mult)), nil
}

func (s *Size) UnmarshalYAML(node *yaml.Node) error {
	var raw string
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("line %d: max_output must be a size such as 64MB", node.Line)
	}
	n, err := ParseSize(raw)
	if err != nil {
		return fmt.Errorf("line %d: %w", node.Line, err)
	}
	*s = Size{Bytes: n, Set: true}
	return nil
}

func (s Size) MarshalYAML() (any, error) {
	if !s.Set {
		return nil, nil
	}
	return Format(s.Bytes), nil
}

// Format writes a byte count the way a person reads it: 64 MB, 1.3 GB, 512 B.
func Format(n int64) string {
	switch {
	case n >= 1<<30:
		return trimFloat(float64(n)/(1<<30)) + " GB"
	case n >= 1<<20:
		return trimFloat(float64(n)/(1<<20)) + " MB"
	case n >= 1<<10:
		return trimFloat(float64(n)/(1<<10)) + " KB"
	}
	return strconv.FormatInt(n, 10) + " B"
}

func trimFloat(f float64) string {
	if f >= 100 {
		return strconv.FormatFloat(f, 'f', 0, 64)
	}
	return strings.TrimSuffix(strings.TrimRight(strconv.FormatFloat(f, 'f', 1, 64), "0"), ".")
}

// Part is one of the largest values inside an output: its path, its size as JSON, and for a
// list, how many items it has.
type Part struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Items int    `json:"items,omitempty"`
}

// Error is a step that produced more than its limit allows.
type Error struct {
	Bytes   int64  // how much it produced (as JSON, or read from a stream), at least
	Limit   int64  // its limit
	Stream  string // for a streaming executor, what was cut off (stdout, body, ...)
	Largest []Part // the biggest values inside the output, when it was measured whole
}

func (e *Error) Error() string {
	what := "output is " + Format(e.Bytes)
	if e.Stream != "" {
		what = e.Stream + " reached " + Format(e.Bytes)
	}
	msg := what + ", over max_output " + Format(e.Limit)
	if len(e.Largest) > 0 {
		var parts []string
		for i, p := range e.Largest {
			if i == 3 {
				break
			}
			parts = append(parts, p.Path+" "+Format(p.Bytes))
		}
		msg += " (largest: " + strings.Join(parts, ", ") + ")"
	}
	return msg + "; give the step a larger max_output, or have it produce less (fan out, filter, or write to a file)"
}

// As returns the limit error inside err, if there is one.
func As(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// Check measures v and returns an *Error with its largest parts when it is over limit (0: no
// limit). Measuring stops once the limit is passed, so checking a huge value costs little.
func Check(v any, limit int64) error {
	if limit <= 0 {
		return nil
	}
	if n := Measure(v, limit); n > limit {
		return &Error{Bytes: Measure(v, 0), Limit: limit, Largest: Largest(v, 8)}
	}
	return nil
}

// Measure is the size of v as JSON, close enough to compare with a limit, without building the
// JSON. With stop > 0 it stops counting once past stop and returns a value above it.
func Measure(v any, stop int64) int64 {
	m := measurer{stop: stop}
	m.walk(reflect.ValueOf(v))
	return m.n
}

type measurer struct {
	n, stop int64
}

func (m *measurer) done() bool { return m.stop > 0 && m.n > m.stop }

func (m *measurer) walk(v reflect.Value) {
	if m.done() {
		return
	}
	if !v.IsValid() {
		m.n += 4 // null
		return
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			m.n += 4
			return
		}
		m.walk(v.Elem())
	case reflect.String:
		m.n += stringSize(v.String())
	case reflect.Bool:
		if v.Bool() {
			m.n += 4
		} else {
			m.n += 5
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		m.n += int64(len(strconv.FormatInt(v.Int(), 10)))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		m.n += int64(len(strconv.FormatUint(v.Uint(), 10)))
	case reflect.Float32, reflect.Float64:
		m.n += int64(len(strconv.FormatFloat(v.Float(), 'g', -1, 64)))
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			m.n += 2 + (int64(v.Len())+2)/3*4 // []byte is base64
			return
		}
		m.n += 2
		for i := 0; i < v.Len() && !m.done(); i++ {
			if i > 0 {
				m.n++
			}
			m.walk(v.Index(i))
		}
	case reflect.Map:
		m.n += 2
		iter := v.MapRange()
		first := true
		for iter.Next() && !m.done() {
			if !first {
				m.n++
			}
			first = false
			m.n += stringSize(fmt.Sprint(iter.Key().Interface())) + 1
			m.walk(iter.Value())
		}
	case reflect.Struct:
		// Structs are rare in outputs; marshal them rather than mirror encoding/json's rules.
		if b, err := json.Marshal(v.Interface()); err == nil {
			m.n += int64(len(b))
		}
	default:
		m.n += 4
	}
}

// stringSize is a string's size as JSON: quotes and escapes counted, which matters for text
// that holds JSON (a rendered register is mostly backslashes and quotes).
func stringSize(s string) int64 {
	n := int64(2)
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"' || c == '\\' || c == '\n' || c == '\r' || c == '\t':
			n += 2
			i++
		case c < 0x20:
			n += 6
			i++
		case c < utf8.RuneSelf:
			n++
			i++
		default:
			_, size := utf8.DecodeRuneInString(s[i:])
			n += int64(size)
			i += size
		}
	}
	return n
}

// Largest lists the biggest values a few levels into v, largest first, so a failure shows where
// the bulk is (command_inputs.pull_reviews[0].issue_comments).
func Largest(v any, n int) []Part {
	var parts []Part
	var walk func(path string, v reflect.Value, depth int)
	walk = func(path string, v reflect.Value, depth int) {
		for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) && !v.IsNil() {
			v = v.Elem()
		}
		if !v.IsValid() || depth > 4 {
			return
		}
		switch v.Kind() {
		case reflect.Map:
			iter := v.MapRange()
			for iter.Next() {
				p := fmt.Sprint(iter.Key().Interface())
				if path != "" {
					p = path + "." + p
				}
				add(&parts, p, iter.Value())
				walk(p, iter.Value(), depth+1)
			}
		case reflect.Slice, reflect.Array:
			if v.Len() == 0 || v.Type().Elem().Kind() == reflect.Uint8 {
				return
			}
			// The first item stands for the rest: lists of records are alike.
			p := path + "[0]"
			add(&parts, p, v.Index(0))
			walk(p, v.Index(0), depth+1)
		}
	}
	walk("", reflect.ValueOf(v), 0)
	sort.SliceStable(parts, func(i, j int) bool { return parts[i].Bytes > parts[j].Bytes })
	if len(parts) > n {
		parts = parts[:n]
	}
	return parts
}

func add(parts *[]Part, path string, v reflect.Value) {
	p := Part{Path: path, Bytes: Measure(v.Interface(), 0)}
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) && !v.IsNil() {
		v = v.Elem()
	}
	if v.IsValid() && (v.Kind() == reflect.Slice || v.Kind() == reflect.Array) {
		p.Items = v.Len()
	}
	if p.Bytes >= 1024 {
		*parts = append(*parts, p)
	}
}

type limitKey struct{}

// WithLimit gives a step's executor its output limit (0: none).
func WithLimit(ctx context.Context, limit int64) context.Context {
	return context.WithValue(ctx, limitKey{}, limit)
}

// Limit is the output limit an executor runs under; 0 means none.
func Limit(ctx context.Context) int64 {
	n, _ := ctx.Value(limitKey{}).(int64)
	return n
}

// Counter counts bytes read from or written to streams that together make one output (stdout
// and stderr, or every page of a listing), and reports the first time the total passes the
// limit. It is safe for concurrent use.
type Counter struct {
	limit int64
	mu    sync.Mutex
	n     int64
	err   *Error
}

// NewCounter counts against limit; 0 means no limit.
func NewCounter(limit int64) *Counter { return &Counter{limit: limit} }

// Add counts n bytes of stream and returns an *Error once the total is over the limit.
func (c *Counter) Add(stream string, n int) error {
	if c == nil || c.limit <= 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n += int64(n)
	if c.n > c.limit && c.err == nil {
		c.err = &Error{Bytes: c.n, Limit: c.limit, Stream: stream}
	}
	if c.err != nil {
		return c.err
	}
	return nil
}

// Err is the limit error, once the limit was passed.
func (c *Counter) Err() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		return nil
	}
	return c.err
}

// Writer wraps w so writes count against c as stream; a write past the limit is cut short and
// returns the limit error, and onOver (if set) runs once, to stop what is producing the output.
func (c *Counter) Writer(w writer, stream string, onOver func()) writer {
	return &countingWriter{w: w, c: c, stream: stream, onOver: onOver}
}

type writer interface {
	Write(p []byte) (int, error)
}

type countingWriter struct {
	w      writer
	c      *Counter
	stream string
	onOver func()
	over   bool
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	if cw.over {
		return 0, cw.c.Err()
	}
	if err := cw.c.Add(cw.stream, len(p)); err != nil {
		cw.over = true
		if cw.onOver != nil {
			cw.onOver()
		}
		return 0, err
	}
	return cw.w.Write(p)
}
