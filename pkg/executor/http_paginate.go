package executor

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// defaultMaxPages bounds a paginated request when the workflow does not.
const defaultMaxPages = 10

// paging is how an http_request follows a paginated list (its paginate param). Exactly one way
// of finding the next page is set: next, cursor or pageParam.
type paging struct {
	// next is "link" (the Link header's rel="next", as GitHub sends) or a body path to the next
	// page's URL ("links.next", "_links.next.href").
	next string
	// cursor is a body path to the next page's cursor, sent as the query parameter cursorParam.
	cursor      string
	cursorParam string
	// pageParam is a page-number query parameter, counted up until a page has no items.
	pageParam string
	// items is a body path to each page's items; empty means the body is the list.
	items    string
	maxPages int
}

// pagingFromParams reads the paginate param: true (follow Link headers) or a map. Nil when the
// request is not paginated.
func pagingFromParams(raw any) (*paging, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case bool:
		if !v {
			return nil, nil
		}
		return &paging{next: "link", maxPages: defaultMaxPages}, nil
	case map[string]any:
		p := &paging{maxPages: defaultMaxPages}
		for key, value := range v {
			switch key {
			case "next", "cursor", "cursor_param", "page_param", "items":
				s, ok := value.(string)
				if !ok {
					return nil, fmt.Errorf("http_request: paginate.%s must be a string", key)
				}
				switch key {
				case "next":
					p.next = s
				case "cursor":
					p.cursor = s
				case "cursor_param":
					p.cursorParam = s
				case "page_param":
					p.pageParam = s
				case "items":
					p.items = s
				}
			case "max_pages":
				n, err := statusCodeValue(value)
				if err != nil || n < 1 {
					return nil, fmt.Errorf("http_request: paginate.max_pages must be a whole number of at least 1")
				}
				p.maxPages = n
			default:
				return nil, fmt.Errorf("http_request: paginate has no %q setting (next, cursor, cursor_param, page_param, items, max_pages)", key)
			}
		}
		ways := 0
		for _, s := range []string{p.next, p.cursor, p.pageParam} {
			if s != "" {
				ways++
			}
		}
		switch {
		case ways > 1:
			return nil, fmt.Errorf("http_request: paginate takes one of next, cursor or page_param")
		case ways == 0:
			p.next = "link"
		}
		if (p.cursor == "") != (p.cursorParam == "") {
			return nil, fmt.Errorf("http_request: paginate.cursor and paginate.cursor_param go together")
		}
		return p, nil
	default:
		return nil, fmt.Errorf("http_request: paginate must be true or a map")
	}
}

// pageItems is the list of items in one page's parsed body.
func (p *paging) pageItems(body any, page int) ([]any, error) {
	v := body
	if p.items != "" {
		found, ok := bodyPath(body, p.items)
		if !ok {
			return nil, fmt.Errorf("http_request: page %d has no %q in its body", page, p.items)
		}
		v = found
	}
	if v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		where := "the body"
		if p.items != "" {
			where = p.items
		}
		return nil, fmt.Errorf("http_request: page %d: %s is not a list (set paginate.items to where the list is)", page, where)
	}
	return items, nil
}

// nextURL is the URL of the page after the one fetched from current, or "" when it was the last.
func (p *paging) nextURL(current string, header http.Header, body any, items []any) (string, error) {
	switch {
	case p.next == "link":
		return resolveURL(current, linkNext(header.Values("Link")))
	case p.next != "":
		v, _ := bodyPath(body, p.next)
		s, _ := v.(string)
		return resolveURL(current, s)
	case p.cursor != "":
		v, _ := bodyPath(body, p.cursor)
		if v == nil || v == "" || v == false {
			return "", nil
		}
		return withQuery(current, p.cursorParam, fmt.Sprint(v))
	default:
		if len(items) == 0 {
			return "", nil
		}
		u, err := url.Parse(current)
		if err != nil {
			return "", err
		}
		page := 1
		if s := u.Query().Get(p.pageParam); s != "" {
			if page, err = strconv.Atoi(s); err != nil {
				return "", fmt.Errorf("http_request: %s=%q is not a page number", p.pageParam, s)
			}
		}
		return withQuery(current, p.pageParam, strconv.Itoa(page+1))
	}
}

// linkNext is the rel="next" target of Link header values, or "".
func linkNext(values []string) string {
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			target, params, ok := strings.Cut(strings.TrimSpace(part), ";")
			if !ok || !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
				continue
			}
			for _, param := range strings.Split(params, ";") {
				name, rel, _ := strings.Cut(strings.TrimSpace(param), "=")
				if strings.EqualFold(name, "rel") && fieldsContain(strings.Trim(rel, `"`), "next") {
					return strings.TrimSuffix(strings.TrimPrefix(target, "<"), ">")
				}
			}
		}
	}
	return ""
}

func fieldsContain(s, want string) bool {
	for _, f := range strings.Fields(s) {
		if strings.EqualFold(f, want) {
			return true
		}
	}
	return false
}

// resolveURL resolves ref (absolute or relative) against base; "" stays "".
func resolveURL(base, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("http_request: next page URL %q: %w", ref, err)
	}
	return b.ResolveReference(r).String(), nil
}

// withQuery is raw with the query parameter key set to value, other parameters kept.
func withQuery(raw, key, value string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// bodyPath looks up a dotted path ("data.items", "links.0.href") in a parsed JSON body.
func bodyPath(v any, path string) (any, bool) {
	for _, key := range strings.Split(path, ".") {
		switch node := v.(type) {
		case map[string]any:
			next, ok := node[key]
			if !ok {
				return nil, false
			}
			v = next
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			v = node[i]
		default:
			return nil, false
		}
	}
	return v, true
}

// headerOutput is a response's headers for the step output: canonical names, repeated values
// joined with ", ".
func headerOutput(h http.Header) map[string]any {
	out := make(map[string]any, len(h))
	for name, values := range h {
		out[name] = strings.Join(values, ", ")
	}
	return out
}
