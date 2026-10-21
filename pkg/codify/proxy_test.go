package codify

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
)

// upstreamTrusting lets the proxy reach an httptest TLS server in tests.
func (p *Proxy) upstreamTrusting(srv *httptest.Server) {
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	p.upstream.Transport = &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: pool}}
}

// clientVia is an HTTP client that goes through the proxy and trusts its CA.
func clientVia(t *testing.T, p *Proxy) *http.Client {
	t.Helper()
	pem, err := os.ReadFile(p.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	u, _ := url.Parse(p.URL())
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u), TLSClientConfig: &tls.Config{RootCAs: pool}}}
}

func get(t *testing.T, c *http.Client, method, u string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, u, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, u, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func newServer(hits *int64) *httptest.Server {
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(hits, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"path":%q,"hit":%d}`, r.URL.Path, n)
	}))
}

func TestProxyRecordsThenReplaysWithoutTheNetwork(t *testing.T) {
	var hits int64
	srv := newServer(&hits)
	rec, err := OpenRecording(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := StartProxy(t.TempDir(), rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.upstreamTrusting(srv)
	c := clientVia(t, p)

	p.SetMode(Record)
	status, body := get(t, c, "GET", srv.URL+"/topstories.json")
	if status != 200 || !strings.Contains(body, `"hit":1`) {
		t.Fatalf("record: %d %s", status, body)
	}
	srv.Close() // the network is gone

	p.SetMode(Replay)
	status, body = get(t, c, "GET", srv.URL+"/topstories.json")
	if status != 200 || !strings.Contains(body, `"hit":1`) {
		t.Fatalf("replay: %d %s, want the recorded response", status, body)
	}
	if status, _ := get(t, c, "GET", srv.URL+"/item/1.json"); status != http.StatusBadGateway {
		t.Fatalf("replay miss: status %d, want 502", status)
	}
	if status, _ := get(t, c, "POST", srv.URL+"/submit"); status != http.StatusForbidden {
		t.Fatalf("replay POST: status %d, want 403", status)
	}
	if m := p.Misses(); len(m) != 1 || !strings.HasSuffix(m[0], "/item/1.json") {
		t.Fatalf("misses = %v", m)
	}
	if r := p.Refused(); len(r) != 1 || !strings.HasPrefix(r[0], "POST ") {
		t.Fatalf("refused = %v", r)
	}

	// A reopened recording replays the same.
	again, _ := OpenRecording(rec.dir)
	if ex := again.Exchanges(); len(ex) != 1 || ex[0].Status != 200 || ex[0].Header.Get("Content-Type") != "application/json" {
		t.Fatalf("reloaded = %#v", ex)
	}
}

func TestProxyFillAddsWhatIsMissingAndKeepsWhatIsThere(t *testing.T) {
	var hits int64
	srv := newServer(&hits)
	defer srv.Close()
	rec, _ := OpenRecording(t.TempDir())
	p, err := StartProxy(t.TempDir(), rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.upstreamTrusting(srv)
	c := clientVia(t, p)
	p.SetMode(Record)
	get(t, c, "GET", srv.URL+"/a")
	p.SetMode(Fill)
	_, a := get(t, c, "GET", srv.URL+"/a")
	_, b := get(t, c, "GET", srv.URL+"/b")
	if !strings.Contains(a, `"hit":1`) || !strings.Contains(b, `"hit":2`) || hits != 2 {
		t.Fatalf("fill: a=%s b=%s hits=%d", a, b, hits)
	}
}

func TestProxyRecordsPlainHTTP(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		io.WriteString(w, "plain "+r.URL.Path)
	}))
	rec, _ := OpenRecording(t.TempDir())
	p, err := StartProxy(t.TempDir(), rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	c := clientVia(t, p)
	p.SetMode(Record)
	get(t, c, "GET", srv.URL+"/x")
	srv.Close()
	p.SetMode(Replay)
	if status, body := get(t, c, "GET", srv.URL+"/x"); status != 200 || body != "plain /x" {
		t.Fatalf("replay: %d %q", status, body)
	}
}

func TestPassthroughHostsAreNeverRecorded(t *testing.T) {
	var hits int64
	srv := newServer(&hits)
	defer srv.Close()
	rec, _ := OpenRecording(t.TempDir())
	p, err := StartProxy(t.TempDir(), rec, []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	u, _ := url.Parse(p.URL())
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	// Tunnelled: the client sees the server's own certificate, not the proxy's.
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u), TLSClientConfig: &tls.Config{RootCAs: pool}}}
	p.SetMode(Replay)
	if status, _ := get(t, c, "GET", srv.URL+"/api"); status != 200 {
		t.Fatalf("passthrough in replay mode: %d", status)
	}
	if len(rec.Exchanges()) != 0 || len(p.Misses()) != 0 {
		t.Fatalf("passthrough was recorded: %v %v", rec.Exchanges(), p.Misses())
	}
}

// curl, configured only through the environment the proxy hands out, records and replays HTTPS.
func TestCurlThroughTheProxyEnvironment(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	var hits int64
	srv := newServer(&hits)
	rec, _ := OpenRecording(t.TempDir())
	p, err := StartProxy(t.TempDir(), rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.upstreamTrusting(srv)
	curl := func() (string, error) {
		// httptest serves 127.0.0.1, which NO_PROXY would exempt; name it so it goes through.
		target := strings.Replace(srv.URL, "127.0.0.1", "codify.test", 1) + "/data"
		port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
		cmd := exec.Command("curl", "-sS", "--fail", "--resolve", "codify.test:"+port+":127.0.0.1", target)
		cmd.Env = append(os.Environ(), p.Env()...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	// The proxy itself reaches codify.test: send it to the httptest server, whose certificate
	// names example.com.
	tr := p.upstream.Transport.(*http.Transport)
	tr.TLSClientConfig.ServerName = "example.com"
	tr.DialContext = (&netDialer{addr: srv.Listener.Addr().String()}).DialContext
	p.SetMode(Record)
	out, err := curl()
	if err != nil || !strings.Contains(out, `"hit":1`) {
		t.Fatalf("curl record: %v %s", err, out)
	}
	srv.Close()
	p.SetMode(Replay)
	out, err = curl()
	if err != nil || !strings.Contains(out, `"hit":1`) {
		t.Fatalf("curl replay: %v %s", err, out)
	}
}

// netDialer sends every connection to one address (tests map a made-up host to httptest).
type netDialer struct{ addr string }

func (d *netDialer) DialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, d.addr)
}

func TestRecordingKeepsTheFirstResponseOfARepeatedRequest(t *testing.T) {
	var hits int64
	srv := newServer(&hits)
	defer srv.Close()
	rec, _ := OpenRecording(t.TempDir())
	p, err := StartProxy(t.TempDir(), rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.upstreamTrusting(srv)
	c := clientVia(t, p)
	p.SetMode(Record)
	_, first := get(t, c, "GET", srv.URL+"/item/1.json")
	_, again := get(t, c, "GET", srv.URL+"/item/1.json")
	if first != again || hits != 1 {
		t.Fatalf("first %s, again %s, hits %d: a repeat must see the same snapshot", first, again, hits)
	}
}
