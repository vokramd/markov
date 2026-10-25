package executor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/jctanner/markov/pkg/jsonvalue"
)

type HTTPRequest struct {
	client *http.Client
}

func NewHTTPRequest() *HTTPRequest {
	return &HTTPRequest{client: &http.Client{}}
}

func (e *HTTPRequest) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	method, _ := params["method"].(string)
	if method == "" {
		method = "GET"
	}

	url, ok := params["url"].(string)
	if !ok || url == "" {
		baseURL, _ := params["base_url"].(string)
		path, _ := params["path"].(string)
		if baseURL == "" {
			return nil, fmt.Errorf("http_request: url or base_url is required")
		}
		url = baseURL + path
	}

	pages, err := pagingFromParams(params["paginate"])
	if err != nil {
		return nil, err
	}
	client, err := e.clientForParams(params)
	if err != nil {
		return nil, err
	}
	if pages == nil {
		output, _, err := e.send(ctx, client, method, url, params)
		return resultOrNil(output), err
	}

	// Follow the pages, collecting their items into one list.
	var items []any
	for page := 1; ; page++ {
		output, header, err := e.send(ctx, client, method, url, params)
		if err != nil {
			if output == nil {
				return nil, fmt.Errorf("%w (page %d)", err, page)
			}
			return &Result{Output: output}, fmt.Errorf("%w (page %d)", err, page)
		}
		output["pages"] = page
		output["truncated"] = false
		if code, _ := output["status_code"].(int); code >= 400 {
			// An error status ignore_status allows ends the list.
			if items == nil {
				items = []any{}
			}
			output["body"] = items
			return &Result{Output: output}, nil
		}
		pageItems, err := pages.pageItems(output["body"], page)
		if err != nil {
			return &Result{Output: output}, err
		}
		items = append(items, pageItems...)
		next, err := pages.nextURL(url, header, output["body"], pageItems)
		if err != nil {
			return &Result{Output: output}, err
		}
		if next != "" && page >= pages.maxPages {
			output["truncated"] = true
		}
		if next == "" || page >= pages.maxPages {
			if items == nil {
				items = []any{}
			}
			output["body"] = items
			return &Result{Output: output}, nil
		}
		url = next
	}
}

func resultOrNil(output map[string]any) *Result {
	if output == nil {
		return nil
	}
	return &Result{Output: output}
}

// send makes one request. The output is set whenever a response arrived, also when its status
// is an error that ignore_status does not allow.
func (e *HTTPRequest) send(ctx context.Context, client *http.Client, method, url string, params map[string]any) (map[string]any, http.Header, error) {
	var bodyReader io.Reader
	if body, ok := params["body"]; ok {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, nil, fmt.Errorf("http_request: encoding body: %w", err)
		}
		bodyReader = strings.NewReader(string(encoded))
	}

	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), url, bodyReader)
	if err != nil {
		return nil, nil, fmt.Errorf("http_request: creating request: %w", err)
	}

	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if err := applyHeaders(req, params["headers"]); err != nil {
		return nil, nil, err
	}
	if err := applyBasicAuth(req, params["basic_auth"]); err != nil {
		return nil, nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("http_request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("http_request: reading response: %w", err)
	}

	output := map[string]any{
		"status_code": resp.StatusCode,
		"headers":     headerOutput(resp.Header),
		"body":        string(respBody),
	}

	if parsed, err := jsonvalue.Decode(respBody); err == nil {
		output["body"] = parsed
	}

	ignoreStatus, err := shouldIgnoreStatus(resp.StatusCode, params["ignore_status"])
	if err != nil {
		return output, resp.Header, err
	}
	if resp.StatusCode >= 400 && !ignoreStatus {
		return output, resp.Header, fmt.Errorf("http_request: status %d", resp.StatusCode)
	}

	return output, resp.Header, nil
}

func (e *HTTPRequest) clientForParams(params map[string]any) (*http.Client, error) {
	tlsConfig, enabled, err := tlsConfigFromParams(params)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return e.client, nil
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	return &http.Client{
		Transport:     transport,
		CheckRedirect: e.client.CheckRedirect,
		Jar:           e.client.Jar,
		Timeout:       e.client.Timeout,
	}, nil
}

func tlsConfigFromParams(params map[string]any) (*tls.Config, bool, error) {
	insecure, err := boolParam(params["tls_insecure"], "tls_insecure")
	if err != nil {
		return nil, false, err
	}
	caCertPath, _ := params["tls_ca_cert"].(string)
	if !insecure && caCertPath == "" {
		return nil, false, nil
	}

	config := &tls.Config{InsecureSkipVerify: insecure}
	if caCertPath == "" {
		return config, true, nil
	}

	certPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, false, fmt.Errorf("http_request: reading tls_ca_cert %q: %w", caCertPath, err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if ok := roots.AppendCertsFromPEM(certPEM); !ok {
		return nil, false, fmt.Errorf("http_request: tls_ca_cert %q contains no PEM certificates", caCertPath)
	}
	config.RootCAs = roots
	return config, true, nil
}

func applyHeaders(req *http.Request, raw any) error {
	if raw == nil {
		return nil
	}
	for key, value := range stringMap(raw) {
		if key == "" {
			return fmt.Errorf("http_request: headers contains empty key")
		}
		req.Header.Set(key, value)
	}
	return nil
}

func applyBasicAuth(req *http.Request, raw any) error {
	if raw == nil {
		return nil
	}
	auth := stringMap(raw)
	username, ok := auth["username"]
	if !ok {
		return fmt.Errorf("http_request: basic_auth.username is required")
	}
	password, ok := auth["password"]
	if !ok {
		return fmt.Errorf("http_request: basic_auth.password is required")
	}
	req.SetBasicAuth(username, password)
	return nil
}

func shouldIgnoreStatus(statusCode int, raw any) (bool, error) {
	switch v := raw.(type) {
	case nil:
		return false, nil
	case bool:
		return v && statusCode >= 400, nil
	case []any:
		for _, item := range v {
			code, err := statusCodeValue(item)
			if err != nil {
				return false, err
			}
			if code == statusCode {
				return true, nil
			}
		}
		return false, nil
	case []int:
		for _, code := range v {
			if code == statusCode {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("http_request: ignore_status must be a bool or list of status codes")
	}
}

func statusCodeValue(raw any) (int, error) {
	switch v := raw.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		if v != float64(int(v)) {
			return 0, fmt.Errorf("http_request: ignore_status contains non-integer status code %v", v)
		}
		return int(v), nil
	case string:
		code, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("http_request: ignore_status contains invalid status code %q", v)
		}
		return code, nil
	default:
		return 0, fmt.Errorf("http_request: ignore_status contains invalid status code %v", raw)
	}
}

func boolParam(raw any, name string) (bool, error) {
	switch v := raw.(type) {
	case nil:
		return false, nil
	case bool:
		return v, nil
	default:
		return false, fmt.Errorf("http_request: %s must be a bool", name)
	}
}

func stringMap(raw any) map[string]string {
	switch v := raw.(type) {
	case map[string]string:
		return v
	case map[string]any:
		out := make(map[string]string, len(v))
		for key, value := range v {
			out[key] = fmt.Sprint(value)
		}
		return out
	default:
		return map[string]string{}
	}
}
