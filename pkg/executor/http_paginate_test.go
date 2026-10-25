package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// pagedServer serves the numbers 1..total, per items a page, in the style each test asks for.
func pagedServer(t *testing.T, total, per int, style string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if s := r.URL.Query().Get("page"); s != "" {
			page, _ = strconv.Atoi(s)
		}
		if s := r.URL.Query().Get("cursor"); s != "" {
			page, _ = strconv.Atoi(strings.TrimPrefix(s, "c"))
		}
		var items []int
		for n := (page-1)*per + 1; n <= total && n <= page*per; n++ {
			items = append(items, n)
		}
		if items == nil {
			items = []int{}
		}
		more := page*per < total
		var body any = items
		switch style {
		case "link":
			if more {
				// GitHub keeps the request's other query parameters in its links.
				q := r.URL.Query()
				q.Set("page", strconv.Itoa(page+1))
				w.Header().Set("Link", fmt.Sprintf(`<%s%s?%s>; rel="next", <%s/x?page=99>; rel="last"`, srv.URL, r.URL.Path, q.Encode(), srv.URL))
			}
		case "next":
			next := ""
			if more {
				next = fmt.Sprintf("/list?page=%d", page+1) // relative
			}
			body = map[string]any{"data": map[string]any{"values": items}, "links": map[string]any{"next": next}}
		case "cursor":
			var cursor any
			if more {
				cursor = fmt.Sprintf("c%d", page+1)
			}
			body = map[string]any{"items": items, "meta": map[string]any{"next_cursor": cursor}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func numbers(t *testing.T, body any) []int {
	t.Helper()
	list, ok := body.([]any)
	if !ok {
		t.Fatalf("body is %T, want a list", body)
	}
	var out []int
	for _, v := range list {
		out = append(out, v.(int))
	}
	return out
}

func wantRun(t *testing.T, got []int, from, to int) {
	t.Helper()
	if len(got) != to-from+1 || (len(got) > 0 && (got[0] != from || got[len(got)-1] != to)) {
		t.Fatalf("items = %v, want %d..%d", got, from, to)
	}
}

func TestHTTPRequestFollowsPages(t *testing.T) {
	cases := []struct {
		name     string
		style    string
		paginate any
		query    string
	}{
		{"Link headers (paginate: true)", "link", true, "?state=all&per_page=100"},
		{"next URL in the body", "next", map[string]any{"next": "links.next", "items": "data.values"}, ""},
		{"cursor in the body", "cursor", map[string]any{"cursor": "meta.next_cursor", "cursor_param": "cursor", "items": "items"}, ""},
		{"page numbers until an empty page", "plain", map[string]any{"page_param": "page"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := pagedServer(t, 250, 100, c.style)
			result, err := NewHTTPRequest().Execute(context.Background(), map[string]any{"url": srv.URL + "/list" + c.query, "paginate": c.paginate})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			wantRun(t, numbers(t, result.Output["body"]), 1, 250)
			if result.Output["truncated"] != false {
				t.Fatalf("truncated = %v", result.Output["truncated"])
			}
			if _, ok := result.Output["headers"].(map[string]any); !ok {
				t.Fatalf("headers = %T", result.Output["headers"])
			}
		})
	}
}

func TestHTTPRequestPaginationStopsAtMaxPages(t *testing.T) {
	srv := pagedServer(t, 250, 100, "link")
	result, err := NewHTTPRequest().Execute(context.Background(), map[string]any{"url": srv.URL + "/list", "paginate": map[string]any{"max_pages": 2}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	wantRun(t, numbers(t, result.Output["body"]), 1, 200)
	if result.Output["pages"] != 2 || result.Output["truncated"] != true {
		t.Fatalf("pages = %v, truncated = %v; want 2, true", result.Output["pages"], result.Output["truncated"])
	}

	// Exactly enough pages is not truncated.
	srv = pagedServer(t, 200, 100, "link")
	result, err = NewHTTPRequest().Execute(context.Background(), map[string]any{"url": srv.URL + "/list", "paginate": map[string]any{"max_pages": 2}})
	if err != nil || result.Output["truncated"] != false {
		t.Fatalf("err = %v, truncated = %v", err, result.Output["truncated"])
	}
}

func TestHTTPRequestPaginationErrors(t *testing.T) {
	srv := pagedServer(t, 5, 100, "next")
	_, err := NewHTTPRequest().Execute(context.Background(), map[string]any{"url": srv.URL + "/list", "paginate": map[string]any{"next": "links.next"}})
	if err == nil || !strings.Contains(err.Error(), "not a list") {
		t.Fatalf("a body that is not a list: err = %v", err)
	}
	for _, bad := range []any{
		"yes",
		map[string]any{"next": "link", "page_param": "page"},
		map[string]any{"cursor": "meta.next"},
		map[string]any{"max_pages": 0},
		map[string]any{"per_page": 100},
	} {
		if _, err := NewHTTPRequest().Execute(context.Background(), map[string]any{"url": srv.URL, "paginate": bad}); err == nil {
			t.Errorf("paginate %v was accepted", bad)
		}
	}
}

func TestLinkNext(t *testing.T) {
	for header, want := range map[string]string{
		`<https://a/x?page=2>; rel="next", <https://a/x?page=5>; rel="last"`: "https://a/x?page=2",
		`<https://a/x?page=1>; rel="prev first"`:                             "",
		`<https://a/x?page=3>; REL=next`:                                     "https://a/x?page=3",
		``:                                                                   "",
	} {
		if got := linkNext([]string{header}); got != want {
			t.Errorf("linkNext(%q) = %q, want %q", header, got, want)
		}
	}
}
