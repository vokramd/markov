package executor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/vokramd/markov/pkg/jsonvalue"
	"github.com/vokramd/markov/pkg/outputlimit"
)

// streaming says how http_request reads a streamed response: `stream: sse` or a map
// {format: sse, text: <path>, thinking: <path>, progress: {<kind>: <path>, ...}}.
type streaming struct {
	// reports maps a progress kind to a dotted path into each event's data; a non-empty string
	// found there is reported as progress of that kind ({text}), so a renderer can show it as it
	// arrives. `text` and `thinking` are shorthands for progress.text and progress.thinking.
	reports map[string]string
}

func streamingFromParams(raw any) (*streaming, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case bool:
		if v {
			return nil, fmt.Errorf("http_request: stream must be \"sse\" or a map with format: sse")
		}
		return nil, nil
	case string:
		if v != "sse" {
			return nil, fmt.Errorf("http_request: stream format %q is not supported (supported: sse)", v)
		}
		return &streaming{}, nil
	case map[string]any:
		s := &streaming{}
		for key, value := range v {
			switch key {
			case "format":
				if value != "sse" {
					return nil, fmt.Errorf("http_request: stream format %v is not supported (supported: sse)", value)
				}
			case "text", "thinking":
				path, ok := value.(string)
				if !ok || path == "" {
					return nil, fmt.Errorf("http_request: stream.%s must be a dotted path", key)
				}
				if err := s.report(key, path); err != nil {
					return nil, err
				}
			case "progress":
				kinds, ok := value.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("http_request: stream.progress must be a map of progress kind to dotted path")
				}
				for kind, raw := range kinds {
					path, ok := raw.(string)
					if !ok || path == "" {
						return nil, fmt.Errorf("http_request: stream.progress.%s must be a dotted path", kind)
					}
					if err := s.report(kind, path); err != nil {
						return nil, err
					}
				}
			default:
				return nil, fmt.Errorf("http_request: unknown stream setting %q (supported: format, text, thinking, progress)", key)
			}
		}
		return s, nil
	default:
		return nil, fmt.Errorf("http_request: stream must be \"sse\" or a map with format: sse")
	}
}

func (s *streaming) report(kind, path string) error {
	if kind == "" || kind == "sse" {
		return fmt.Errorf("http_request: %q cannot be a stream progress kind", kind)
	}
	if s.reports == nil {
		s.reports = map[string]string{}
	}
	if _, dup := s.reports[kind]; dup {
		return fmt.Errorf("http_request: stream progress kind %q is set twice", kind)
	}
	s.reports[kind] = path
	return nil
}

// reportPath reports the non-empty string at path in an event's data as progress of kind.
func reportPath(ctx context.Context, kind string, data any, path string) {
	if path == "" {
		return
	}
	if v, ok := bodyPath(data, path); ok {
		if str, ok := v.(string); ok && str != "" {
			reportProgress(ctx, kind, map[string]any{"text": str})
		}
	}
}

// isEventStream reports whether a response's Content-Type is text/event-stream.
func isEventStream(contentType string) bool {
	mediaType, _, _ := strings.Cut(contentType, ";")
	return strings.EqualFold(strings.TrimSpace(mediaType), "text/event-stream")
}

// readSSE reads server-sent events until the stream ends, reporting each one as an `sse`
// progress event (and the strings at its configured paths as their own kinds) as
// soon as it is complete. It returns every event as {event, data}; data is decoded when it is JSON. The bytes read count
// against the step's output limit.
func readSSE(ctx context.Context, r io.Reader, counter *outputlimit.Counter, s *streaming) ([]any, error) {
	events := []any{}
	reader := bufio.NewReader(r)
	name := ""
	var data []string
	dispatch := func() {
		if name == "" && data == nil {
			return
		}
		if name == "" {
			name = "message"
		}
		raw := strings.Join(data, "\n")
		var value any = raw
		if parsed, err := jsonvalue.Decode([]byte(raw)); err == nil {
			value = parsed
		}
		event := map[string]any{"event": name, "data": value}
		events = append(events, event)
		reportProgress(ctx, "sse", event)
		for _, kind := range slices.Sorted(maps.Keys(s.reports)) {
			reportPath(ctx, kind, value, s.reports[kind])
		}
		name, data = "", nil
	}
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			if lim := counter.Add("body", len(line)); lim != nil {
				return events, lim
			}
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if err == nil {
				dispatch()
			}
		case strings.HasPrefix(line, ":"):
			// a comment, often a keep-alive
		default:
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				name = value
			case "data":
				data = append(data, value)
			}
			// id and retry are for reconnecting clients; a step reads one stream once.
		}
		if err == io.EOF {
			dispatch() // a final event without its blank line
			return events, nil
		}
		if err != nil {
			return events, err
		}
	}
}
