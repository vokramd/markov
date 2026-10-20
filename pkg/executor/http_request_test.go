package executor

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHTTPRequestAppliesHeadersAndBasicAuth(t *testing.T) {
	transport := &captureTransport{
		statusCode: http.StatusOK,
		body:       `{"ok":true}`,
	}
	exec := &HTTPRequest{client: &http.Client{Transport: transport}}

	result, err := exec.Execute(context.Background(), map[string]any{
		"url":    "https://example.test/api",
		"method": "POST",
		"body": map[string]any{
			"name": "markov",
		},
		"headers": map[string]any{
			"Accept":        "application/json",
			"Authorization": "Bearer ignored",
			"Content-Type":  "application/vnd.example+json",
			"X-Trace":       12345,
		},
		"basic_auth": map[string]any{
			"username": "admin",
			"password": "secret",
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Output["status_code"] != http.StatusOK {
		t.Fatalf("status_code = %v, want %d", result.Output["status_code"], http.StatusOK)
	}

	req := transport.request
	if req == nil {
		t.Fatal("transport did not receive request")
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q, want application/json", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/vnd.example+json" {
		t.Fatalf("Content-Type = %q, want custom override", got)
	}
	if got := req.Header.Get("X-Trace"); got != "12345" {
		t.Fatalf("X-Trace = %q, want 12345", got)
	}
	username, password, ok := req.BasicAuth()
	if !ok || username != "admin" || password != "secret" {
		t.Fatalf("BasicAuth = (%q, %q, %v), want admin/secret", username, password, ok)
	}
}

func TestHTTPRequestIgnoreStatusAllowsConfiguredErrorStatus(t *testing.T) {
	transport := &captureTransport{
		statusCode: http.StatusConflict,
		body:       `{"error":"already exists"}`,
	}
	exec := &HTTPRequest{client: &http.Client{Transport: transport}}

	result, err := exec.Execute(context.Background(), map[string]any{
		"url":           "https://example.test/api/resource",
		"ignore_status": []any{http.StatusNotFound, http.StatusConflict},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Output["status_code"] != http.StatusConflict {
		t.Fatalf("status_code = %v, want %d", result.Output["status_code"], http.StatusConflict)
	}
}

func TestHTTPRequestIgnoreStatusTrueAllowsAnyErrorStatus(t *testing.T) {
	transport := &captureTransport{
		statusCode: http.StatusInternalServerError,
		body:       "server error",
	}
	exec := &HTTPRequest{client: &http.Client{Transport: transport}}

	result, err := exec.Execute(context.Background(), map[string]any{
		"url":           "https://example.test/api/resource",
		"ignore_status": true,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Output["status_code"] != http.StatusInternalServerError {
		t.Fatalf("status_code = %v, want %d", result.Output["status_code"], http.StatusInternalServerError)
	}
}

func TestHTTPRequestReturnsErrorForUnhandledErrorStatus(t *testing.T) {
	transport := &captureTransport{
		statusCode: http.StatusNotFound,
		body:       "missing",
	}
	exec := &HTTPRequest{client: &http.Client{Transport: transport}}

	result, err := exec.Execute(context.Background(), map[string]any{
		"url":           "https://example.test/api/resource",
		"ignore_status": []any{http.StatusConflict},
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want status error")
	}
	if result == nil || result.Output["status_code"] != http.StatusNotFound {
		t.Fatalf("Result = %#v, want populated output with 404", result)
	}
}

func TestHTTPRequestTLSInsecureBuildsTLSClient(t *testing.T) {
	exec := NewHTTPRequest()

	client, err := exec.clientForParams(map[string]any{
		"tls_insecure": true,
	})
	if err != nil {
		t.Fatalf("clientForParams() error = %v", err)
	}
	if client == exec.client {
		t.Fatal("clientForParams() returned default client, want TLS-specific client")
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T, want *http.Transport", client.Transport)
	}
	if transport.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig = nil, want config")
	}
	if !transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify = false, want true")
	}
}

func TestHTTPRequestTLSCACertLoadsRootPool(t *testing.T) {
	certPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(certPath, testCertificatePEM(t), 0644); err != nil {
		t.Fatal(err)
	}

	config, enabled, err := tlsConfigFromParams(map[string]any{
		"tls_ca_cert": certPath,
	})
	if err != nil {
		t.Fatalf("tlsConfigFromParams() error = %v", err)
	}
	if !enabled {
		t.Fatal("enabled = false, want true")
	}
	if config.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify = true, want false")
	}
	if config.RootCAs == nil {
		t.Fatal("RootCAs = nil, want cert pool")
	}
}

func TestHTTPRequestRejectsInvalidTLSParams(t *testing.T) {
	_, _, err := tlsConfigFromParams(map[string]any{
		"tls_insecure": "true",
	})
	if err == nil {
		t.Fatal("tlsConfigFromParams() error = nil, want invalid bool error")
	}

	badCertPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(badCertPath, []byte("not a certificate"), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, err = tlsConfigFromParams(map[string]any{
		"tls_ca_cert": badCertPath,
	})
	if err == nil {
		t.Fatal("tlsConfigFromParams() error = nil, want invalid cert error")
	}
}

type captureTransport struct {
	request    *http.Request
	statusCode int
	body       string
}

func (t *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.request = req
	return &http.Response{
		StatusCode: t.statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Request:    req,
	}, nil
}

func testCertificatePEM(t *testing.T) []byte {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "markov-test-ca",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestHTTPRequestDecodesWholeNumbersAsInt(t *testing.T) {
	transport := &captureTransport{statusCode: http.StatusOK, body: `[50163277, 1.5]`}
	exec := &HTTPRequest{client: &http.Client{Transport: transport}}

	result, err := exec.Execute(context.Background(), map[string]any{"url": "https://example.test/topstories.json"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	body, ok := result.Output["body"].([]any)
	if !ok || len(body) != 2 || body[0] != 50163277 || body[1] != 1.5 {
		t.Fatalf("body = %#v, want []any{50163277, 1.5}", result.Output["body"])
	}
}
