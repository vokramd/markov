package codify

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"regexp"
)

// maxEventLine is the longest event-log line codify keeps. A step's output is capped
// (max_output, 64 MB by default), but a log written before that cap, or a run with it raised,
// can hold a line of gigabytes; holding one such line took codify's memory with it.
const maxEventLine = 16 << 20

// eventPrefix is how much of an oversized line is kept to say what it was.
const eventPrefix = 4096

// lineSplitter cuts an event log into lines as bytes arrive, without holding a line longer than
// max: such a line is replaced by a short summary event, built from its start (event type, step,
// run) and its length.
type lineSplitter struct {
	max  int
	fn   func([]byte)
	buf  []byte
	over bool  // the current line is past max: only its prefix is kept
	n    int64 // the current line's length so far
}

func newLineSplitter(max int, fn func([]byte)) *lineSplitter {
	return &lineSplitter{max: max, fn: fn}
}

func (s *lineSplitter) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		chunk := p
		if i >= 0 {
			chunk = p[:i]
		}
		s.n += int64(len(chunk))
		if !s.over {
			if len(s.buf)+len(chunk) > s.max {
				s.over = true
				keep := eventPrefix - len(s.buf)
				if keep > 0 {
					s.buf = append(s.buf, chunk[:min(keep, len(chunk))]...)
				} else {
					s.buf = s.buf[:eventPrefix]
				}
			} else {
				s.buf = append(s.buf, chunk...)
			}
		}
		if i < 0 {
			break
		}
		s.flush()
		p = p[i+1:]
	}
	return total, nil
}

// Close hands over a last line that had no newline.
func (s *lineSplitter) Close() {
	if len(s.buf) > 0 || s.over {
		s.flush()
	}
}

func (s *lineSplitter) flush() {
	line := s.buf
	if s.over {
		line = summarizeLine(s.buf, s.n)
	}
	if len(line) > 0 {
		s.fn(line)
	}
	s.buf, s.over, s.n = s.buf[:0:0], false, 0
}

var headerField = regexp.MustCompile(`"(timestamp|run_id|event_type|workflow_name|step_name|step_type|resolved_type)":"([^"]*)"`)

// summarizeLine is the event that stands for a line too long to keep: the header fields found in
// its start, and how long it was.
func summarizeLine(prefix []byte, n int64) []byte {
	ev := map[string]any{"oversized_event_bytes": n, "note": "event too large to keep; codify read only its start"}
	for _, m := range headerField.FindAllSubmatch(prefix, -1) {
		if _, seen := ev[string(m[1])]; !seen {
			ev[string(m[1])] = string(m[2])
		}
	}
	b, _ := json.Marshal(ev)
	return b
}

// eachEventLine reads an event log a line at a time, oversized lines summarized.
func eachEventLine(r io.Reader, fn func([]byte)) error {
	s := newLineSplitter(maxEventLine, fn)
	_, err := io.CopyBuffer(s, r, make([]byte, 1<<20))
	s.Close()
	return err
}

// copyEventLog copies an event log with its oversized lines summarized, for evidence an agent
// reads.
func copyEventLog(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	var werr error
	err = eachEventLine(in, func(line []byte) {
		if werr == nil {
			_, werr = out.Write(append(line, '\n'))
		}
	})
	if cerr := out.Close(); werr == nil {
		werr = cerr
	}
	if err != nil {
		return err
	}
	return werr
}
