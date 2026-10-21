package codify

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The recording proxy sits between a workflow run and the network. In record mode it forwards
// requests and saves the responses; in replay mode it answers from the recording only, so every
// version of a workflow sees exactly the same inputs. HTTPS is intercepted with a certificate
// authority of its own, which the run is told to trust. Hosts the agents themselves need (the
// Claude API) are tunnelled untouched, never recorded.

// Mode says how the proxy answers.
type Mode int

const (
	// Record forwards requests and saves the responses (the live run). A request repeated within
	// the run gets the response it got the first time, so the run sees one consistent snapshot
	// of data that changes while it runs (a score updated between two fetches), and that
	// snapshot is what candidates replay.
	Record Mode = iota
	// Fill answers from the recording and records what is missing (the current version's runs).
	Fill
	// Replay answers from the recording only; anything else fails (candidates).
	Replay
)

// maxBody bounds a recorded response body.
const maxBody = 64 << 20

// DefaultPassthrough are host suffixes tunnelled without interception or recording: the
// services the claude CLI itself talks to.
var DefaultPassthrough = []string{"anthropic.com", "claude.ai", "claude.com", "statsig.com", "sentry.io", "datadoghq.com"}

// Exchange is one recorded response.
type Exchange struct {
	Method string      `json:"method"`
	URL    string      `json:"url"`
	Status int         `json:"status"`
	Header http.Header `json:"header"`
	Body   string      `json:"body"` // file name under the recording's bodies/
	Size   int         `json:"size"`
}

// Recording is what a live run fetched, on disk under one directory.
type Recording struct {
	dir string
	mu  sync.Mutex
	ex  map[string]*Exchange
}

func recordingKey(method, url string) string { return method + " " + url }

// OpenRecording loads (or starts) the recording in dir.
func OpenRecording(dir string) (*Recording, error) {
	if err := os.MkdirAll(filepath.Join(dir, "bodies"), 0o755); err != nil {
		return nil, err
	}
	r := &Recording{dir: dir, ex: map[string]*Exchange{}}
	data, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	var list []*Exchange
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("reading recording: %w", err)
	}
	for _, e := range list {
		r.ex[recordingKey(e.Method, e.URL)] = e
	}
	return r, nil
}

// Exchanges lists the recording, sorted by URL.
func (r *Recording) Exchanges() []*Exchange {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Exchange, 0, len(r.ex))
	for _, e := range r.ex {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].URL < out[j].URL || (out[i].URL == out[j].URL && out[i].Method < out[j].Method)
	})
	return out
}

func (r *Recording) get(method, url string) (*Exchange, []byte, bool) {
	r.mu.Lock()
	e, ok := r.ex[recordingKey(method, url)]
	r.mu.Unlock()
	if !ok {
		return nil, nil, false
	}
	body, err := os.ReadFile(filepath.Join(r.dir, "bodies", e.Body))
	if err != nil {
		return nil, nil, false
	}
	return e, body, true
}

func (r *Recording) put(method, url string, status int, header http.Header, body []byte) error {
	sum := sha256.Sum256([]byte(recordingKey(method, url)))
	name := hex.EncodeToString(sum[:12])
	if err := os.WriteFile(filepath.Join(r.dir, "bodies", name), body, 0o644); err != nil {
		return err
	}
	keep := http.Header{}
	for _, k := range []string{"Content-Type", "Content-Encoding", "Location"} {
		if v := header.Values(k); len(v) > 0 {
			keep[k] = v
		}
	}
	r.mu.Lock()
	r.ex[recordingKey(method, url)] = &Exchange{Method: method, URL: url, Status: status, Header: keep, Body: name, Size: len(body)}
	r.mu.Unlock()
	return r.save()
}

func (r *Recording) save() error {
	data, err := json.MarshalIndent(r.Exchanges(), "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(r.dir, "index.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(r.dir, "index.json"))
}

// Proxy is a running recording proxy.
type Proxy struct {
	CAFile     string // the proxy's CA certificate (PEM)
	BundleFile string // system roots plus the CA, for tools that replace their trust store
	ca         *x509.Certificate
	caKey      *ecdsa.PrivateKey
	rec        *Recording
	ln         net.Listener
	upstream   *http.Client
	pass       []string

	mu       sync.Mutex
	mode     Mode
	certs    map[string]*tls.Certificate
	misses   []string
	refused  []string
	tunneled map[string]bool
}

// StartProxy starts a proxy on 127.0.0.1 with the CA kept in caDir (created on first use).
func StartProxy(caDir string, rec *Recording, passthrough []string) (*Proxy, error) {
	ca, key, caFile, err := loadOrCreateCA(caDir)
	if err != nil {
		return nil, err
	}
	bundle := filepath.Join(caDir, "bundle.pem")
	if err := writeBundle(bundle, caFile); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &Proxy{
		CAFile: caFile, BundleFile: bundle, ca: ca, caKey: key, rec: rec, ln: ln, pass: passthrough,
		// The proxy's own requests never go through a proxy.
		upstream: &http.Client{Transport: &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 15 * time.Second}, Timeout: 2 * time.Minute,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		certs: map[string]*tls.Certificate{}, tunneled: map[string]bool{},
	}
	go (&http.Server{Handler: p, ReadHeaderTimeout: 30 * time.Second}).Serve(ln)
	return p, nil
}

// URL is the proxy's address for HTTP_PROXY and HTTPS_PROXY.
func (p *Proxy) URL() string { return "http://" + p.ln.Addr().String() }

// Close stops the proxy.
func (p *Proxy) Close() error { return p.ln.Close() }

// SetRecording points the proxy at another recording (each codify iteration has its own).
func (p *Proxy) SetRecording(r *Recording) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rec = r
}

// SetMode switches between recording and replaying, and clears the miss lists.
func (p *Proxy) SetMode(m Mode) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mode = m
	p.misses, p.refused = nil, nil
}

// Misses are the requests replay could not answer since the last SetMode.
func (p *Proxy) Misses() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.misses...)
}

// Refused are requests other than GET and HEAD that replay refused (side effects).
func (p *Proxy) Refused() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.refused...)
}

// Env is what a child process needs to go through the proxy and trust its certificates.
func (p *Proxy) Env() []string {
	u := p.URL()
	noProxy := "localhost,127.0.0.1,::1"
	return []string{
		"HTTP_PROXY=" + u, "HTTPS_PROXY=" + u, "http_proxy=" + u, "https_proxy=" + u,
		"NO_PROXY=" + noProxy, "no_proxy=" + noProxy,
		"SSL_CERT_FILE=" + p.BundleFile, "CURL_CA_BUNDLE=" + p.BundleFile, "REQUESTS_CA_BUNDLE=" + p.BundleFile,
		"NODE_EXTRA_CA_CERTS=" + p.CAFile,
	}
}

func (p *Proxy) passthrough(host string) bool {
	h := strings.ToLower(host)
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	for _, s := range p.pass {
		if h == s || strings.HasSuffix(h, "."+s) {
			return true
		}
	}
	return false
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.connect(w, r)
		return
	}
	if !r.URL.IsAbs() {
		http.Error(w, "markov codify proxy: absolute URL required", http.StatusBadRequest)
		return
	}
	resp := p.handle(r)
	defer resp.Body.Close()
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (p *Proxy) connect(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	host := r.Host
	if p.passthrough(host) {
		p.mu.Lock()
		p.tunneled[host] = true
		p.mu.Unlock()
		up, err := net.DialTimeout("tcp", host, 15*time.Second)
		if err != nil {
			conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
			return
		}
		defer up.Close()
		conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		done := make(chan struct{}, 2)
		go func() { io.Copy(up, conn); done <- struct{}{} }()
		go func() { io.Copy(conn, up); done <- struct{}{} }()
		<-done
		return
	}
	conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	tlsConn := tls.Server(conn, &tls.Config{GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		name := hello.ServerName
		if name == "" {
			name, _, _ = net.SplitHostPort(host)
		}
		return p.certFor(name)
	}, NextProtos: []string{"http/1.1"}})
	if err := tlsConn.Handshake(); err != nil {
		return
	}
	br := bufio.NewReader(tlsConn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		req.URL.Scheme = "https"
		req.URL.Host = req.Host
		if req.URL.Host == "" {
			req.URL.Host = host
		}
		resp := p.handle(req)
		resp.ProtoMajor, resp.ProtoMinor = 1, 1
		err = resp.Write(tlsConn)
		resp.Body.Close()
		if err != nil || req.Close {
			return
		}
	}
}

// canonicalURL drops the default port so http://h:80/x and http://h/x are one entry.
func canonicalURL(r *http.Request) string {
	u := *r.URL
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		u.Host = u.Hostname()
	}
	u.Fragment = ""
	return u.String()
}

func (p *Proxy) handle(r *http.Request) *http.Response {
	p.mu.Lock()
	mode, rec := p.mode, p.rec
	p.mu.Unlock()
	url := canonicalURL(r)
	method := r.Method
	if p.passthrough(r.URL.Host) {
		return p.forward(r)
	}
	if e, body, ok := rec.get(method, url); ok {
		return recorded(e, body)
	}
	if mode != Record {
		if method != http.MethodGet && method != http.MethodHead {
			p.mu.Lock()
			p.refused = append(p.refused, method+" "+url)
			p.mu.Unlock()
			return failure(http.StatusForbidden, fmt.Sprintf("markov codify: %s %s is not replayed (only GET and HEAD are)", method, url))
		}
		if mode == Replay {
			p.mu.Lock()
			p.misses = append(p.misses, method+" "+url)
			p.mu.Unlock()
			return failure(http.StatusBadGateway, fmt.Sprintf("markov codify: %s %s is not in the recording", method, url))
		}
	}
	out, err := http.NewRequest(method, url, r.Body)
	if err != nil {
		return failure(http.StatusBadGateway, err.Error())
	}
	for k, v := range r.Header {
		if !strings.EqualFold(k, "Proxy-Connection") && !strings.EqualFold(k, "Accept-Encoding") {
			out.Header[k] = v
		}
	}
	resp, err := p.upstream.Do(out)
	if err != nil {
		return failure(http.StatusBadGateway, err.Error())
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return failure(http.StatusBadGateway, err.Error())
	}
	if err := rec.put(method, url, resp.StatusCode, resp.Header, body); err != nil {
		return failure(http.StatusInternalServerError, "recording: "+err.Error())
	}
	e, _, _ := rec.get(method, url)
	return recorded(e, body)
}

// forward passes a passthrough request on without recording it.
func (p *Proxy) forward(r *http.Request) *http.Response {
	out, err := http.NewRequest(r.Method, r.URL.String(), r.Body)
	if err != nil {
		return failure(http.StatusBadGateway, err.Error())
	}
	out.Header = r.Header.Clone()
	resp, err := p.upstream.Do(out)
	if err != nil {
		return failure(http.StatusBadGateway, err.Error())
	}
	return resp
}

func recorded(e *Exchange, body []byte) *http.Response {
	h := e.Header.Clone()
	if h == nil {
		h = http.Header{}
	}
	h.Set("Content-Length", fmt.Sprint(len(body)))
	return &http.Response{StatusCode: e.Status, Status: fmt.Sprintf("%d %s", e.Status, http.StatusText(e.Status)), Header: h,
		Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), ProtoMajor: 1, ProtoMinor: 1}
}

func failure(status int, msg string) *http.Response {
	body := []byte(msg + "\n")
	h := http.Header{"Content-Type": {"text/plain; charset=utf-8"}, "Content-Length": {fmt.Sprint(len(body))}, "X-Markov-Codify": {"replay"}}
	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Header: h,
		Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), ProtoMajor: 1, ProtoMinor: 1}
}

func (p *Proxy) certFor(name string) (*tls.Certificate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.certs[name]; ok {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(name); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{name}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.ca, &key.PublicKey, p.caKey)
	if err != nil {
		return nil, err
	}
	c := &tls.Certificate{Certificate: [][]byte{der, p.ca.Raw}, PrivateKey: key}
	p.certs[name] = c
	return c, nil
}

func loadOrCreateCA(dir string) (*x509.Certificate, *ecdsa.PrivateKey, string, error) {
	certFile, keyFile := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca-key.pem")
	if cp, err := os.ReadFile(certFile); err == nil {
		if kp, err := os.ReadFile(keyFile); err == nil {
			cb, _ := pem.Decode(cp)
			kb, _ := pem.Decode(kp)
			if cb != nil && kb != nil {
				cert, err1 := x509.ParseCertificate(cb.Bytes)
				key, err2 := x509.ParseECPrivateKey(kb.Bytes)
				if err1 == nil && err2 == nil && time.Now().Before(cert.NotAfter.Add(-24*time.Hour)) {
					return cert, key, certFile, nil
				}
			}
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, "", err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "markov codify recording proxy"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, "", err
	}
	cert, _ := x509.ParseCertificate(der)
	kder, _ := x509.MarshalECPrivateKey(key)
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600); err != nil {
		return nil, nil, "", err
	}
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return nil, nil, "", err
	}
	return cert, key, certFile, nil
}

// systemRoots are the usual locations of the system CA bundle.
var systemRoots = []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/ssl/ca-bundle.pem", "/etc/ssl/cert.pem"}

// writeBundle writes the system roots followed by the proxy's CA, so tools that replace their
// trust store with SSL_CERT_FILE still trust the hosts the proxy does not intercept.
func writeBundle(path, caFile string) error {
	var buf bytes.Buffer
	for _, f := range systemRoots {
		if data, err := os.ReadFile(f); err == nil {
			buf.Write(data)
			buf.WriteByte('\n')
			break
		}
	}
	ca, err := os.ReadFile(caFile)
	if err != nil {
		return err
	}
	buf.Write(ca)
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
