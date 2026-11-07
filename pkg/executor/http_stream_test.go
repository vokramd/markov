package executor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vokramd/markov/pkg/outputlimit"
)

type progressLog struct {
	mu     sync.Mutex
	events []string
	got    chan string
}

func newProgressLog() *progressLog { return &progressLog{got: make(chan string, 100)} }

func (p *progressLog) fn(kind string, data map[string]any) {
	entry := kind
	switch kind {
	case "sse":
		entry += ":" + fmt.Sprint(data["event"])
	case "text", "thinking", "tool_input":
		entry += ":" + fmt.Sprint(data["text"])
	}
	p.mu.Lock()
	p.events = append(p.events, entry)
	p.mu.Unlock()
	p.got <- entry
}

func sseServer(t *testing.T, handler func(w http.ResponseWriter, flush func())) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("Accept = %q, want text/event-stream", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		handler(w, func() { w.(http.Flusher).Flush() })
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPRequestStreamSSEReturnsEventsAndReportsProgress(t *testing.T) {
	srv := sseServer(t, func(w http.ResponseWriter, flush func()) {
		fmt.Fprint(w, ": keep-alive\n\n")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\"}\n\n")
		fmt.Fprint(w, "event: delta\ndata: {\"delta\":{\"thinking\":\"hmm\"}}\n\n")
		fmt.Fprint(w, "event: delta\ndata: {\"delta\":{\"text\":\"Hel\"}}\n\n")
		fmt.Fprint(w, "event: delta\r\ndata: {\"delta\":{\"text\":\"lo\"}}\r\n\r\n")
		fmt.Fprint(w, "event: delta\ndata: {\"delta\":{\"partial_json\":\"{\\\"a\\\"\"}}\n\n")
		fmt.Fprint(w, "data: line one\ndata: line two\n\n")
		fmt.Fprint(w, "event: done\ndata: [DONE]") // no trailing blank line
	})
	progress := newProgressLog()
	ctx := WithProgress(context.Background(), progress.fn)

	result, err := NewHTTPRequest().Execute(ctx, map[string]any{
		"method": "POST",
		"url":    srv.URL,
		"body":   map[string]any{"stream": true},
		"stream": map[string]any{"format": "sse", "text": "delta.text", "thinking": "delta.thinking", "progress": map[string]any{"tool_input": "delta.partial_json"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []any{
		map[string]any{"event": "message_start", "data": map[string]any{"type": "message_start"}},
		map[string]any{"event": "delta", "data": map[string]any{"delta": map[string]any{"thinking": "hmm"}}},
		map[string]any{"event": "delta", "data": map[string]any{"delta": map[string]any{"text": "Hel"}}},
		map[string]any{"event": "delta", "data": map[string]any{"delta": map[string]any{"text": "lo"}}},
		map[string]any{"event": "delta", "data": map[string]any{"delta": map[string]any{"partial_json": `{"a"`}}},
		map[string]any{"event": "message", "data": "line one\nline two"},
		map[string]any{"event": "done", "data": "[DONE]"},
	}
	if !reflect.DeepEqual(result.Output["body"], want) {
		t.Fatalf("body = %#v\nwant %#v", result.Output["body"], want)
	}
	if result.Output["status_code"] != 200 {
		t.Fatalf("status_code = %v", result.Output["status_code"])
	}
	wantProgress := []string{"sse:message_start", "sse:delta", "thinking:hmm", "sse:delta", "text:Hel", "sse:delta", "text:lo", "sse:delta", `tool_input:{"a"`, "sse:message", "sse:done"}
	if !reflect.DeepEqual(progress.events, wantProgress) {
		t.Fatalf("progress = %v\nwant %v", progress.events, wantProgress)
	}
}

// Each event is reported while the response is still open, not after it ends.
func TestHTTPRequestStreamSSEReportsEventsAsTheyArrive(t *testing.T) {
	release := make(chan struct{})
	srv := sseServer(t, func(w http.ResponseWriter, flush func()) {
		fmt.Fprint(w, "data: {\"t\":\"first\"}\n\n")
		flush()
		<-release
		fmt.Fprint(w, "data: {\"t\":\"second\"}\n\n")
	})
	progress := newProgressLog()
	ctx := WithProgress(context.Background(), progress.fn)

	done := make(chan error, 1)
	go func() {
		_, err := NewHTTPRequest().Execute(ctx, map[string]any{"url": srv.URL, "stream": map[string]any{"text": "t"}})
		done <- err
	}()
	for _, want := range []string{"sse:message", "text:first"} {
		select {
		case got := <-progress.got:
			if got != want {
				t.Fatalf("progress = %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no %q progress before the stream ended", want)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// An error answered with ordinary JSON, not a stream, is read and parsed as usual.
func TestHTTPRequestStreamFallsBackToWholeBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"type":"rate_limit_error"}}`)
	}))
	defer srv.Close()

	result, err := NewHTTPRequest().Execute(context.Background(), map[string]any{
		"url": srv.URL, "stream": "sse", "ignore_status": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"error": map[string]any{"type": "rate_limit_error"}}
	if !reflect.DeepEqual(result.Output["body"], want) || result.Output["status_code"] != 429 {
		t.Fatalf("output = %#v", result.Output)
	}
}

func TestHTTPRequestStreamErrorStatusFails(t *testing.T) {
	srv := sseServer(t, func(w http.ResponseWriter, flush func()) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "data: oops\n\n")
	})
	result, err := NewHTTPRequest().Execute(context.Background(), map[string]any{"url": srv.URL, "stream": "sse"})
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("err = %v, want status 500", err)
	}
	if result == nil || len(result.Output["body"].([]any)) != 1 {
		t.Fatalf("output should keep the events: %#v", result)
	}
}

func TestHTTPRequestStreamCountsAgainstOutputLimit(t *testing.T) {
	srv := sseServer(t, func(w http.ResponseWriter, flush func()) {
		for i := 0; i < 100; i++ {
			fmt.Fprintf(w, "data: %s\n\n", strings.Repeat("x", 100))
		}
	})
	ctx := outputlimit.WithLimit(context.Background(), 1000)
	_, err := NewHTTPRequest().Execute(ctx, map[string]any{"url": srv.URL, "stream": "sse"})
	if _, over := outputlimit.As(err); !over {
		t.Fatalf("err = %v, want an output limit error", err)
	}
}

func TestHTTPRequestStreamRejectsBadSettings(t *testing.T) {
	for name, params := range map[string]map[string]any{
		"format":       {"stream": "lines"},
		"true":         {"stream": true},
		"unknown key":  {"stream": map[string]any{"text": "a", "every": 1}},
		"empty text":   {"stream": map[string]any{"text": ""}},
		"bad thinking": {"stream": map[string]any{"thinking": 3}},
		"progress sse": {"stream": map[string]any{"progress": map[string]any{"sse": "a"}}},
		"text twice":   {"stream": map[string]any{"text": "a", "progress": map[string]any{"text": "b"}}},
		"bad progress": {"stream": map[string]any{"progress": "a"}},
		"map format":   {"stream": map[string]any{"format": "ndjson"}},
		"and paginate": {"stream": "sse", "paginate": true},
	} {
		params["url"] = "http://example.test"
		if _, err := NewHTTPRequest().Execute(context.Background(), params); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
