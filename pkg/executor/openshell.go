package executor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jctanner/markov/pkg/openshell"
)

// OpenShell runs one of the openshell_* step types. Every step but openshell_build reaches the
// gateway through a transport chosen by the step's `transport` param, else the workflow file's
// `openshell:` settings.
type OpenShell struct {
	op  string
	cfg openshell.Config
	// newTransport is replaced in tests.
	newTransport func(openshell.Config) (openshell.Transport, error)
}

// NewOpenShellSteps returns the openshell_* executors, keyed by step type.
func NewOpenShellSteps(cfg openshell.Config) map[string]Executor {
	out := map[string]Executor{}
	for _, op := range []string{"build", "create", "upload", "exec", "download", "delete"} {
		out["openshell_"+op] = &OpenShell{op: op, cfg: cfg, newTransport: openshell.New}
	}
	return out
}

func (e *OpenShell) name() string { return "openshell_" + e.op }

func (e *OpenShell) errf(format string, a ...any) error {
	return fmt.Errorf("%s: %s", e.name(), fmt.Sprintf(format, a...))
}

func (e *OpenShell) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	if e.op == "build" {
		return e.build(ctx, params)
	}
	cfg := e.cfg
	if t, ok := params["transport"].(string); ok && t != "" {
		cfg.Transport = t
	}
	tr, err := e.newTransport(cfg)
	if err != nil {
		return nil, e.errf("%v", err)
	}
	switch e.op {
	case "create":
		return e.create(ctx, tr, params)
	case "upload":
		return e.copy(ctx, tr, params, true)
	case "download":
		return e.copy(ctx, tr, params, false)
	case "exec":
		return e.exec(ctx, tr, params)
	case "delete":
		return e.delete(ctx, tr, params)
	}
	return nil, e.errf("unknown operation")
}

func (e *OpenShell) build(ctx context.Context, params map[string]any) (*Result, error) {
	req := openshell.BuildRequest{
		Tag:           str(params, "tag"),
		Context:       str(params, "context"),
		Containerfile: str(params, "containerfile"),
		Target:        str(params, "target"),
		Pull:          truthy(params["pull"]),
		NoCache:       truthy(params["no_cache"]),
		Push:          truthy(params["push"]),
		Binary:        str(params, "binary"),
	}
	if req.Tag == "" {
		return nil, e.errf("tag is required")
	}
	if raw, ok := params["build_args"]; ok {
		m, ok := stringMapParam(raw)
		if !ok {
			return nil, e.errf("build_args must be a map of strings")
		}
		req.BuildArgs = m
	}
	out := newProgressLines(ctx, "stdout")
	img, err := openshell.Build(ctx, req, out)
	out.Flush()
	if err != nil {
		return nil, e.errf("%v", err)
	}
	return &Result{Output: map[string]any{"image": img.Ref, "id": img.ID}}, nil
}

func (e *OpenShell) create(ctx context.Context, tr openshell.Transport, params map[string]any) (*Result, error) {
	req := openshell.CreateRequest{
		Name:   str(params, "name"),
		Image:  str(params, "image"),
		CPU:    scalar(params["cpu"]),
		Memory: scalar(params["memory"]),
		Policy: str(params, "policy"),
	}
	if req.Name == "" {
		req.Name = defaultSandboxName(StepFrom(ctx))
	}
	switch g := params["gpu"].(type) {
	case nil:
	case bool:
		if g {
			req.GPU = "any"
		}
	default:
		req.GPU = scalar(g)
	}
	var err error
	if req.Env, err = e.stringMap(params, "env"); err != nil {
		return nil, err
	}
	if req.Labels, err = e.stringMap(params, "labels"); err != nil {
		return nil, err
	}
	req.Labels = withStepLabels(req.Labels, StepFrom(ctx))
	if req.Providers, err = stringListParam(e.name(), params, "providers"); err != nil {
		return nil, err
	}
	sb, err := tr.Create(ctx, req)
	if err != nil {
		return nil, e.errf("%v", err)
	}
	return &Result{Output: map[string]any{
		"name": sb.Name, "id": sb.ID, "phase": sb.Phase, "workspace": sb.Workspace, "image": req.Image,
	}}, nil
}

// copy uploads or downloads src into the directory dest, keeping src's base name.
func (e *OpenShell) copy(ctx context.Context, tr openshell.Transport, params map[string]any, upload bool) (*Result, error) {
	sandbox, src := str(params, "sandbox"), str(params, "src")
	if sandbox == "" || src == "" {
		return nil, e.errf("sandbox and src are required")
	}
	dest := str(params, "dest")
	var result string
	var err error
	if upload {
		if dest == "" {
			dest = "/sandbox"
		}
		result = path.Join(dest, filepath.Base(filepath.Clean(src)))
		err = tr.Upload(ctx, sandbox, src, dest, truthy(params["git_ignore"]))
	} else {
		if dest == "" {
			dest = "."
		}
		result = filepath.Join(dest, path.Base(path.Clean(src)))
		err = tr.Download(ctx, sandbox, src, dest)
	}
	if err != nil {
		return nil, e.errf("%v", err)
	}
	return &Result{Output: map[string]any{"sandbox": sandbox, "path": result}}, nil
}

func (e *OpenShell) exec(ctx context.Context, tr openshell.Transport, params map[string]any) (*Result, error) {
	req := openshell.ExecRequest{Sandbox: str(params, "sandbox"), Workdir: str(params, "workdir")}
	if req.Sandbox == "" {
		return nil, e.errf("sandbox is required")
	}
	switch c := params["command"].(type) {
	case string:
		if c == "" {
			return nil, e.errf("command is required")
		}
		req.Command = []string{"sh", "-c", c}
	case []any, []string:
		argv, err := stringListParam(e.name(), params, "command")
		if err != nil {
			return nil, err
		}
		req.Command = argv
	default:
		return nil, e.errf("command must be a string (run by sh -c) or a list (argv)")
	}
	if len(req.Command) == 0 {
		return nil, e.errf("command is required")
	}
	var err error
	if req.Env, err = e.stringMap(params, "env"); err != nil {
		return nil, err
	}
	if raw, ok := params["timeout"]; ok {
		if req.Timeout, err = intParam(e.name(), "timeout", raw); err != nil {
			return nil, err
		}
	}
	if raw, ok := params["stdin"]; ok && raw != nil {
		text, err := stdinText(raw)
		if err != nil {
			return nil, err
		}
		req.Stdin = strings.NewReader(text)
	}

	var stdout, stderr bytes.Buffer
	outLines, errLines := newProgressLines(ctx, "stdout"), newProgressLines(ctx, "stderr")
	code, err := tr.Exec(ctx, req, io.MultiWriter(&stdout, outLines), io.MultiWriter(&stderr, errLines))
	outLines.Flush()
	errLines.Flush()
	if err != nil {
		return nil, e.errf("%v", err)
	}
	timedOut := req.Timeout > 0 && code == openshell.TimeoutExitCode
	output := map[string]any{
		"stdout":    stdout.String(),
		"stderr":    stderr.String(),
		"exit_code": code,
		"timed_out": timedOut,
	}
	switch {
	case timedOut:
		return &Result{Output: output}, e.errf("command timed out after %ds", req.Timeout)
	case code != 0:
		return &Result{Output: output}, e.errf("command exited with status %d\nstderr: %s", code, stderr.String())
	}
	return &Result{Output: output}, nil
}

func (e *OpenShell) delete(ctx context.Context, tr openshell.Transport, params map[string]any) (*Result, error) {
	names, err := stringListParam(e.name(), params, "sandbox")
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, e.errf("sandbox is required")
	}
	if err := tr.Delete(ctx, names); err != nil {
		return nil, e.errf("%v", err)
	}
	deleted := make([]any, len(names))
	for i, n := range names {
		deleted[i] = n
	}
	return &Result{Output: map[string]any{"deleted": deleted}}, nil
}

func (e *OpenShell) stringMap(params map[string]any, key string) (map[string]string, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return nil, nil
	}
	m, ok := stringMapParam(raw)
	if !ok {
		return nil, e.errf("%s must be a map of strings", key)
	}
	return m, nil
}

func str(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return s
}

func scalar(v any) string {
	s, _ := scalarString(v)
	return s
}

func truthy(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return b == "true" || b == "yes" || b == "1"
	}
	return false
}

// defaultSandboxName is unique per call, so a create inside for_each gets a sandbox per item.
// OpenShell names are DNS labels of at most 19 characters: mk-<step, up to 9>-<6 hex>. The run
// is recorded in labels instead (see withStepLabels).
func defaultSandboxName(info StepContext) string {
	step := dnsLabel(info.Step, 9)
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	if step == "" {
		return "mk-" + hex.EncodeToString(suffix)
	}
	return "mk-" + step + "-" + hex.EncodeToString(suffix)
}

// dnsLabel lowercases s and keeps letters and digits, joining the rest with single dashes,
// cut to max characters.
func dnsLabel(s string, max int) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		} else {
			dash = true
		}
	}
	out := b.String()
	if len(out) > max {
		out = out[:max]
	}
	return strings.Trim(out, "-")
}

// withStepLabels adds markov-run, markov-workflow and markov-step labels so a sandbox can be
// traced to its run (openshell sandbox list --selector markov-run=<id>). Labels the step sets
// win.
func withStepLabels(labels map[string]string, info StepContext) map[string]string {
	out := map[string]string{}
	for k, v := range map[string]string{"markov-run": info.RunID, "markov-workflow": info.Workflow, "markov-step": info.Step} {
		if v := labelValue(v); v != "" {
			out[k] = v
		}
	}
	for k, v := range labels {
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// labelValue fits s to a Kubernetes-style label value: at most 63 of [A-Za-z0-9._-], starting
// and ending with a letter or digit.
func labelValue(s string) string {
	b := []byte(s)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			b[i] = '-'
		}
	}
	if len(b) > 63 {
		b = b[:63]
	}
	return strings.Trim(string(b), "-_.")
}

// progressLines reports each complete line written to it as an "output" progress event.
type progressLines struct {
	ctx    context.Context
	stream string
	mu     sync.Mutex
	buf    []byte
}

func newProgressLines(ctx context.Context, stream string) *progressLines {
	return &progressLines{ctx: ctx, stream: stream}
}

func (p *progressLines) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buf = append(p.buf, b...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			break
		}
		p.emit(string(p.buf[:i]))
		p.buf = p.buf[i+1:]
	}
	return len(b), nil
}

// Flush reports a final line that had no newline.
func (p *progressLines) Flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.buf) > 0 {
		p.emit(string(p.buf))
		p.buf = nil
	}
}

func (p *progressLines) emit(line string) {
	reportProgress(p.ctx, "output", map[string]any{"stream": p.stream, "text": strings.TrimRight(line, "\r")})
}
