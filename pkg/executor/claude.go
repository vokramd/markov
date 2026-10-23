package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jctanner/markov/pkg/openshell"
)

const (
	claudeDefaultEventsLimit = 200
	claudeMaxStringLen       = 4000
)

var claudePermissionModes = map[string]string{
	"acceptEdits":       "acceptEdits",
	"auto":              "auto",
	"bypassPermissions": "bypassPermissions",
	"bypass":            "bypassPermissions",
	"manual":            "manual",
	"dontAsk":           "dontAsk",
	"plan":              "plan",
}

// Claude runs the Claude Code CLI non-interactively and consumes its
// stream-json output live. Authentication is whatever the claude CLI finds in
// the runner environment (an OAuth login under HOME, or any key the user puts
// in env); the step never requires an API key.
//
// With `sandbox`, the CLI runs inside that OpenShell sandbox instead, through
// the same transport as the openshell_* steps; chdir, add_dirs and other paths
// are then sandbox paths, and only the step's own env is passed in.
type Claude struct {
	openshell    openshell.Config
	newTransport func(openshell.Config) (openshell.Transport, error)
}

func NewClaude() *Claude { return &Claude{newTransport: openshell.New} }

// WithOpenShell sets the workflow file's openshell settings, used by steps with `sandbox`.
func (e *Claude) WithOpenShell(cfg openshell.Config) *Claude {
	e.openshell = cfg
	return e
}

type claudeLimits struct {
	maxTurns     int
	maxTokens    int
	maxBudgetUSD float64
	maxDuration  time.Duration
}

type claudeUsage struct {
	input, output, cacheCreate, cacheRead int
}

// counted is the number of new tokens charged to max_tokens. Cache reads are
// excluded because every turn re-reads the whole cached context.
func (u claudeUsage) counted() int { return u.input + u.output + u.cacheCreate }

func (e *Claude) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	const name = "claude"

	prompt, err := claudePrompt(params)
	if err != nil {
		return nil, err
	}
	limits, err := claudeParseLimits(params)
	if err != nil {
		return nil, err
	}
	args, err := claudeArgs(params, limits)
	if err != nil {
		return nil, err
	}

	binary := "claude"
	if raw, ok := params["binary"]; ok {
		s, ok := raw.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("%s: binary must be a non-empty string", name)
		}
		binary = s
	}
	sandbox := ""
	if raw, ok := params["sandbox"]; ok {
		s, ok := raw.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("%s: sandbox must be a non-empty string", name)
		}
		sandbox = s
	}
	chdir := ""
	if raw, ok := params["chdir"]; ok {
		s, ok := raw.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("%s: chdir must be a non-empty string", name)
		}
		if info, err := os.Stat(s); sandbox == "" && (err != nil || !info.IsDir()) {
			return nil, fmt.Errorf("%s: chdir %q is not a directory", name, s)
		}
		chdir = s
	}
	stepEnv := map[string]string{}
	if raw, ok := params["env"]; ok {
		values, ok := stringMapParam(raw)
		if !ok {
			return nil, fmt.Errorf("%s: env must be a map of strings", name)
		}
		for k, v := range values {
			if k == "" || strings.Contains(k, "=") {
				return nil, fmt.Errorf("%s: env key %q is invalid", name, k)
			}
			stepEnv[k] = v
		}
	}
	eventsLimit := claudeDefaultEventsLimit
	if raw, ok := params["events_limit"]; ok {
		if eventsLimit, err = intParam(name, "events_limit", raw); err != nil {
			return nil, err
		}
	}

	started := time.Now()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if limits.maxDuration > 0 {
		var cancelTimeout context.CancelFunc
		runCtx, cancelTimeout = context.WithTimeout(runCtx, limits.maxDuration)
		defer cancelTimeout()
	}

	cfg := e.openshell
	if t, ok := params["transport"].(string); ok && t != "" {
		cfg.Transport = t
	}
	var stderr bytes.Buffer
	var stdout io.Reader
	var wait func() (int, error)
	if sandbox != "" {
		stdout, wait, err = e.startInSandbox(runCtx, cfg, openshell.ExecRequest{
			Sandbox: sandbox,
			Command: append([]string{binary}, args...),
			Workdir: chdir,
			Env:     stepEnv,
			Stdin:   strings.NewReader(prompt),
		}, &stderr)
	} else {
		stdout, wait, err = startLocal(runCtx, binary, args, chdir, stepEnv, prompt, &stderr)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	s := &claudeStream{
		ctx:         ctx,
		limits:      limits,
		eventsLimit: eventsLimit,
		turnIDs:     map[string]struct{}{},
		usageByMsg:  map[string]claudeUsage{},
		cancel:      cancel,
	}
	s.consume(stdout)
	exitCode, waitErr := wait()
	breach := s.breach
	if breach == "" && errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		breach = "max_duration"
		s.emit("limit_exceeded", map[string]any{"limit": breach, "max": limits.maxDuration.Seconds()}, true)
	}

	output := s.output(exitCode, stderr.String(), breach)
	res := &Result{Output: output}

	// Why it failed, beyond the limit or exit status: the API never answering, and in a sandbox,
	// the connections its policy denied or that failed.
	var why []string
	if u := s.unreachable(); u != "" {
		why = append(why, u)
	}
	failed := breach != "" || ctx.Err() != nil || s.result == nil || s.resultIsError() || waitErr != nil
	if sandbox != "" && failed {
		if problems := e.sandboxNetwork(cfg, sandbox, output, time.Since(started)); len(problems) > 0 {
			why = append(why, "sandbox network:\n  "+strings.Join(problems, "\n  "))
		}
	}
	detail := ""
	if len(why) > 0 {
		detail = " (" + strings.Join(why, "; ") + ")"
	}

	switch {
	case breach != "":
		return res, fmt.Errorf("%s: limit exceeded: %s%s", name, breach, detail)
	case ctx.Err() != nil:
		return res, fmt.Errorf("%s: %w", name, ctx.Err())
	case s.result == nil:
		if waitErr != nil {
			return res, fmt.Errorf("%s: %w%s\nstderr: %s", name, waitErr, detail, stderr.String())
		}
		return res, fmt.Errorf("%s: process ended without a result event%s\nstderr: %s", name, detail, stderr.String())
	case s.resultIsError():
		return res, fmt.Errorf("%s: %s", name, claudeString(s.result["result"], "run reported an error"))
	case waitErr != nil:
		return res, fmt.Errorf("%s: %w\nstderr: %s", name, waitErr, stderr.String())
	}
	return res, nil
}

// sandboxNetwork reads the sandbox's network denials and failures since the step started into
// output["sandbox_network"]. It has its own deadline: the step's context may already be over.
func (e *Claude) sandboxNetwork(cfg openshell.Config, sandbox string, output map[string]any, since time.Duration) []string {
	tr, err := e.newTransport(cfg)
	if err != nil {
		return nil
	}
	if c, ok := tr.(io.Closer); ok {
		defer c.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	problems, err := tr.NetworkProblems(ctx, sandbox, since+5*time.Second)
	if err != nil || len(problems) == 0 {
		return nil
	}
	list := make([]any, len(problems))
	for i, p := range problems {
		list[i] = p
	}
	output["sandbox_network"] = list
	return problems
}

// startLocal runs the CLI on the runner. wait returns its exit code (-1 when it did not exit
// normally) and the error from waiting.
func startLocal(ctx context.Context, binary string, args []string, chdir string, stepEnv map[string]string, prompt string, stderr io.Writer) (io.Reader, func() (int, error), error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = chdir
	cmd.Env = os.Environ()
	for k, v := range stepEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin = strings.NewReader(prompt)
	isolateProcessGroup(cmd)
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("starting %s: %w", binary, err)
	}
	return stdout, func() (int, error) {
		err := cmd.Wait()
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		return code, err
	}, nil
}

// startInSandbox runs the CLI in an OpenShell sandbox. Ending ctx ends the exec stream, which
// stops the CLI there too, so limits are enforced the same way as on the runner.
func (e *Claude) startInSandbox(ctx context.Context, cfg openshell.Config, req openshell.ExecRequest, stderr io.Writer) (io.Reader, func() (int, error), error) {
	tr, err := e.newTransport(cfg)
	if err != nil {
		return nil, nil, err
	}
	pr, pw := io.Pipe()
	type exit struct {
		code int
		err  error
	}
	done := make(chan exit, 1)
	go func() {
		code, err := tr.Exec(ctx, req, pw, stderr)
		pw.Close()
		if c, ok := tr.(io.Closer); ok {
			c.Close()
		}
		done <- exit{code, err}
	}()
	return pr, func() (int, error) {
		// Drain what consume left unread so Exec is not blocked on the pipe.
		_, _ = io.Copy(io.Discard, pr)
		x := <-done
		if x.err == nil && x.code != 0 {
			x.err = fmt.Errorf("exit status %d", x.code)
		}
		return x.code, x.err
	}, nil
}

func claudePrompt(params map[string]any) (string, error) {
	prompt, hasPrompt := params["prompt"]
	skill, hasSkill := params["skill"]
	if hasPrompt == hasSkill {
		return "", fmt.Errorf("claude: exactly one of prompt or skill is required")
	}
	if hasPrompt {
		p, ok := prompt.(string)
		if !ok || strings.TrimSpace(p) == "" {
			return "", fmt.Errorf("claude: prompt must be a non-empty string")
		}
		return p, nil
	}
	sk, ok := skill.(string)
	if !ok || strings.TrimSpace(sk) == "" {
		return "", fmt.Errorf("claude: skill must be a non-empty string")
	}
	p := "/" + strings.TrimPrefix(strings.TrimSpace(sk), "/")
	if raw, ok := params["args"]; ok {
		a, ok := raw.(string)
		if !ok {
			return "", fmt.Errorf("claude: args must be a string")
		}
		if a = strings.TrimSpace(a); a != "" {
			p += " " + a
		}
	}
	return p, nil
}

func claudeParseLimits(params map[string]any) (claudeLimits, error) {
	var l claudeLimits
	raw, ok := params["limits"]
	if !ok {
		return l, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return l, fmt.Errorf("claude: limits must be a map")
	}
	for key, v := range m {
		switch key {
		case "max_turns", "max_tokens", "max_duration":
			n, err := intParam("claude", "limits."+key, v)
			if err != nil {
				return l, err
			}
			if n < 0 {
				return l, fmt.Errorf("claude: limits.%s must not be negative", key)
			}
			switch key {
			case "max_turns":
				l.maxTurns = n
			case "max_tokens":
				l.maxTokens = n
			default:
				l.maxDuration = time.Duration(n) * time.Second
			}
		case "max_budget_usd":
			f, err := floatParam("claude", "limits.max_budget_usd", v)
			if err != nil {
				return l, err
			}
			if f < 0 {
				return l, fmt.Errorf("claude: limits.max_budget_usd must not be negative")
			}
			l.maxBudgetUSD = f
		default:
			return l, fmt.Errorf("claude: unknown limit %q", key)
		}
	}
	return l, nil
}

func claudeArgs(params map[string]any, limits claudeLimits) ([]string, error) {
	const name = "claude"
	// The prompt is written to stdin, so variadic flags cannot swallow it and
	// it never appears in the process list.
	args := []string{"-p", "--output-format", "stream-json", "--verbose"}

	if raw, ok := params["permission_mode"]; ok {
		s, _ := raw.(string)
		mode, ok := claudePermissionModes[s]
		if !ok {
			return nil, fmt.Errorf("%s: permission_mode must be one of acceptEdits, auto, bypassPermissions (or bypass), manual, dontAsk, plan", name)
		}
		args = append(args, "--permission-mode", mode)
	}
	for _, f := range []struct{ key, flag string }{
		{"model", "--model"},
		{"effort", "--effort"},
		{"fallback_model", "--fallback-model"},
		{"append_system_prompt", "--append-system-prompt"},
		{"resume", "--resume"},
		{"session_id", "--session-id"},
		{"settings", "--settings"},
	} {
		if raw, ok := params[f.key]; ok {
			s, ok := raw.(string)
			if !ok || s == "" {
				return nil, fmt.Errorf("%s: %s must be a non-empty string", name, f.key)
			}
			args = append(args, f.flag, s)
		}
	}
	if raw, ok := params["bare"]; ok {
		b, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("%s: bare must be a boolean", name)
		}
		if b {
			args = append(args, "--bare")
		}
	}
	for _, f := range []struct {
		key, flag string
		join      bool
	}{
		{"allowed_tools", "--allowedTools", true},
		{"disallowed_tools", "--disallowedTools", true},
		{"add_dirs", "--add-dir", false},
		{"mcp_config", "--mcp-config", false},
	} {
		vals, err := stringListParam(name, params, f.key)
		if err != nil {
			return nil, err
		}
		if len(vals) == 0 {
			continue
		}
		if f.join {
			args = append(args, f.flag, strings.Join(vals, ","))
			continue
		}
		for _, v := range vals {
			args = append(args, f.flag, v)
		}
	}
	// Markov enforces turns, tokens and duration itself; the CLI's own budget
	// flag is passed as a backstop.
	if limits.maxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(limits.maxBudgetUSD, 'f', -1, 64))
	}
	extra, err := stringListParam(name, params, "extra_args")
	if err != nil {
		return nil, err
	}
	return append(args, extra...), nil
}

// claudeStream folds the stream-json event stream into run state, reporting
// progress and enforcing limits as events arrive.
type claudeStream struct {
	ctx         context.Context
	limits      claudeLimits
	eventsLimit int
	cancel      context.CancelFunc

	sessionID string
	model     string
	turnIDs   map[string]struct{}
	// usageByMsg keeps the latest usage per message id: one message can be
	// streamed as several events that each repeat its usage.
	usageByMsg map[string]claudeUsage
	events     []any
	truncated  bool
	result     map[string]any
	breach     string
	// retries counts the CLI's api_retry events; lastRetry is the most recent one.
	retries   int
	lastRetry map[string]any
}

func (s *claudeStream) consume(r io.Reader) {
	reader := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var ev map[string]any
			// After a breach the process is being killed; keep draining the
			// pipe but ignore events so counts reflect the moment of breach.
			if json.Unmarshal(line, &ev) == nil && s.breach == "" {
				s.handle(ev)
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *claudeStream) handle(ev map[string]any) {
	switch ev["type"] {
	case "system":
		switch ev["subtype"] {
		case "init":
			s.sessionID, _ = ev["session_id"].(string)
			s.model, _ = ev["model"].(string)
			s.emit("init", map[string]any{"session_id": s.sessionID, "model": s.model, "cwd": ev["cwd"]}, true)
		case "api_retry":
			// The CLI could not get an answer from the API and is waiting to try again; without
			// this, a run that never reaches the API shows nothing until a limit stops it.
			s.retries++
			s.lastRetry = map[string]any{
				"attempt": ev["attempt"], "max_retries": ev["max_retries"], "retry_delay_ms": ev["retry_delay_ms"],
				"error": ev["error"], "error_status": ev["error_status"],
			}
			s.emit("api_retry", s.lastRetry, true)
		}
	case "assistant":
		s.handleAssistant(ev)
	case "user":
		s.handleUser(ev)
	case "result":
		s.result = ev
		if id, ok := ev["session_id"].(string); ok && id != "" {
			s.sessionID = id
		}
		s.emit("result", map[string]any{
			"is_error":       ev["is_error"],
			"num_turns":      ev["num_turns"],
			"total_cost_usd": ev["total_cost_usd"],
			"stop_reason":    ev["stop_reason"],
			"result":         ev["result"],
		}, true)
	}
}

func (s *claudeStream) handleAssistant(ev map[string]any) {
	msg, _ := ev["message"].(map[string]any)
	if msg == nil {
		return
	}
	id, _ := msg["id"].(string)
	if id != "" {
		// Sub-agent messages (parent_tool_use_id set) spend tokens but are not
		// turns of this run.
		if ev["parent_tool_use_id"] == nil {
			s.turnIDs[id] = struct{}{}
		}
		if u, ok := msg["usage"].(map[string]any); ok {
			s.usageByMsg[id] = claudeUsage{
				input:       jsonInt(u["input_tokens"]),
				output:      jsonInt(u["output_tokens"]),
				cacheCreate: jsonInt(u["cache_creation_input_tokens"]),
				cacheRead:   jsonInt(u["cache_read_input_tokens"]),
			}
		}
	}
	blocks, _ := msg["content"].([]any)
	for _, b := range blocks {
		block, _ := b.(map[string]any)
		switch block["type"] {
		case "text":
			s.emit("text", map[string]any{"text": block["text"]}, true)
		case "tool_use":
			s.emit("tool_use", map[string]any{"id": block["id"], "name": block["name"], "input": block["input"]}, true)
		}
	}
	turns, tokens := len(s.turnIDs), s.totalUsage().counted()
	s.emit("usage", map[string]any{"turns": turns, "tokens": tokens}, false)

	switch {
	case s.limits.maxTurns > 0 && turns > s.limits.maxTurns:
		s.exceed("max_turns", float64(s.limits.maxTurns), float64(turns))
	case s.limits.maxTokens > 0 && tokens > s.limits.maxTokens:
		s.exceed("max_tokens", float64(s.limits.maxTokens), float64(tokens))
	}
}

func (s *claudeStream) handleUser(ev map[string]any) {
	msg, _ := ev["message"].(map[string]any)
	blocks, _ := msg["content"].([]any)
	for _, b := range blocks {
		block, _ := b.(map[string]any)
		if block["type"] != "tool_result" {
			continue
		}
		s.emit("tool_result", map[string]any{
			"tool_use_id": block["tool_use_id"],
			"is_error":    block["is_error"],
			"content":     claudeToolResultText(block["content"]),
		}, true)
	}
}

func (s *claudeStream) exceed(limit string, max, observed float64) {
	if s.breach != "" {
		return
	}
	s.breach = limit
	s.emit("limit_exceeded", map[string]any{"limit": limit, "max": max, "observed": observed}, true)
	s.cancel()
}

func (s *claudeStream) totalUsage() claudeUsage {
	var t claudeUsage
	for _, u := range s.usageByMsg {
		t.input += u.input
		t.output += u.output
		t.cacheCreate += u.cacheCreate
		t.cacheRead += u.cacheRead
	}
	return t
}

// emit reports live progress and, when store is set, keeps a compact copy in
// the capped events list returned in the step output.
func (s *claudeStream) emit(kind string, data map[string]any, store bool) {
	for k, v := range data {
		data[k] = truncateValue(v)
	}
	reportProgress(s.ctx, kind, data)
	if !store {
		return
	}
	if len(s.events) >= s.eventsLimit {
		s.truncated = true
		return
	}
	event := map[string]any{"kind": kind}
	for k, v := range data {
		event[k] = v
	}
	s.events = append(s.events, event)
}

func (s *claudeStream) resultIsError() bool {
	b, _ := s.result["is_error"].(bool)
	return b
}

func (s *claudeStream) output(exitCode int, stderr, breach string) map[string]any {
	u := s.totalUsage()
	out := map[string]any{
		"session_id": s.sessionID,
		"model":      s.model,
		"exit_code":  exitCode,
		"stderr":     stderr,
		"num_turns":  len(s.turnIDs),
		"tokens":     u.counted(),
		"usage": map[string]any{
			"input_tokens":                u.input,
			"output_tokens":               u.output,
			"cache_creation_input_tokens": u.cacheCreate,
			"cache_read_input_tokens":     u.cacheRead,
		},
		"events":           s.events,
		"events_truncated": s.truncated,
		"limit_exceeded":   breach,
		"result":           "",
		"is_error":         breach != "",
		"total_cost_usd":   0.0,
	}
	if s.retries > 0 {
		out["api_retries"] = s.retries
		out["last_api_error"] = s.lastRetry
	}
	if s.result != nil {
		out["result"] = claudeString(s.result["result"], "")
		out["is_error"] = breach != "" || s.resultIsError()
		for _, k := range []string{"stop_reason", "terminal_reason", "duration_ms", "permission_denials"} {
			if v, ok := s.result[k]; ok {
				out[k] = v
			}
		}
		if v, ok := s.result["total_cost_usd"].(float64); ok {
			out["total_cost_usd"] = v
		}
		if n, ok := s.result["num_turns"]; ok {
			out["num_turns"] = jsonInt(n)
		}
	}
	return out
}

// unreachable explains a run that ended while the CLI was still retrying the API, or "".
func (s *claudeStream) unreachable() string {
	if s.retries == 0 || s.result != nil {
		return ""
	}
	status := ""
	if st := s.lastRetry["error_status"]; st != nil {
		status = fmt.Sprintf(", status %v", st)
	}
	return fmt.Sprintf("the API never answered: %d retries, last error %v%s", s.retries, s.lastRetry["error"], status)
}

func claudeToolResultText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, item := range c {
			if m, ok := item.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func claudeString(v any, fallback string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fallback
}

func jsonInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func truncateValue(v any) any {
	switch t := v.(type) {
	case string:
		if len(t) > claudeMaxStringLen {
			return t[:claudeMaxStringLen] + "…[truncated]"
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = truncateValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = truncateValue(val)
		}
		return out
	}
	return v
}

func floatParam(name, key string, raw any) (float64, error) {
	switch v := raw.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, nil
		}
	}
	return 0, fmt.Errorf("%s: %s must be a number", name, key)
}
