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
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vokramd/markov/pkg/openshell"
)

const codexDefaultEventsLimit = 200

var codexSandboxModes = map[string]bool{"read-only": true, "workspace-write": true, "danger-full-access": true}

// Codex runs the OpenAI Codex CLI non-interactively (`codex exec --json`) and consumes its JSONL
// event stream live. Authentication is whatever the codex CLI finds in the runner environment (a
// ChatGPT login under CODEX_HOME, or a key the user puts in env); the step never requires one.
//
// With `sandbox`, the CLI runs inside that OpenShell sandbox instead, as the claude step does.
type Codex struct {
	openshell    openshell.Config
	newTransport func(openshell.Config) (openshell.Transport, error)
}

func NewCodex() *Codex { return &Codex{newTransport: openshell.New} }

// WithOpenShell sets the workflow file's openshell settings, used by steps with `sandbox`.
func (e *Codex) WithOpenShell(cfg openshell.Config) *Codex {
	e.openshell = cfg
	return e
}

type codexLimits struct {
	maxToolCalls int
	maxTokens    int
	maxDuration  time.Duration
}

type codexUsage struct {
	input, cached, cacheWrite, output, reasoning int
}

// counted is the number of new tokens charged to max_tokens: input that was not a cache read, plus
// output (which includes reasoning).
func (u codexUsage) counted() int { return u.input - u.cached + u.output }

func (e *Codex) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	const name = "codex"

	raw, ok := params["prompt"]
	prompt, _ := raw.(string)
	if !ok || strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("%s: prompt must be a non-empty string", name)
	}
	limits, err := codexParseLimits(params)
	if err != nil {
		return nil, err
	}
	binary := "codex"
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
	eventsLimit := codexDefaultEventsLimit
	if raw, ok := params["events_limit"]; ok {
		if eventsLimit, err = intParam(name, "events_limit", raw); err != nil {
			return nil, err
		}
	}

	// The CLI reads a JSON Schema for its final answer from a file, so it is written to one.
	schemaFile := ""
	if raw, ok := params["json_schema"]; ok && raw != nil {
		if sandbox != "" {
			return nil, fmt.Errorf("%s: json_schema is not supported with sandbox", name)
		}
		schema, ok := raw.(string)
		if !ok {
			b, err := json.Marshal(raw)
			if err != nil {
				return nil, fmt.Errorf("%s: json_schema: %w", name, err)
			}
			schema = string(b)
		}
		if !json.Valid([]byte(schema)) {
			return nil, fmt.Errorf("%s: json_schema must be a JSON Schema (a map, or a string holding JSON)", name)
		}
		f, err := os.CreateTemp("", "markov-codex-schema-*.json")
		if err != nil {
			return nil, fmt.Errorf("%s: json_schema: %w", name, err)
		}
		defer os.Remove(f.Name())
		_, werr := f.WriteString(schema)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return nil, fmt.Errorf("%s: json_schema: %w", name, werr)
		}
		schemaFile = f.Name()
	}
	args, err := codexArgs(params, schemaFile)
	if err != nil {
		return nil, err
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
		stdout, wait, err = startInSandbox(runCtx, e.newTransport, cfg, openshell.ExecRequest{
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

	s := &codexStream{ctx: ctx, limits: limits, eventsLimit: eventsLimit, cancel: cancel, counted: map[string]bool{}}
	s.consume(stdout)
	exitCode, waitErr := wait()
	breach := s.breach
	if breach == "" && errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		breach = "max_duration"
		s.emit("limit_exceeded", map[string]any{"limit": breach, "max": limits.maxDuration.Seconds()}, true)
	}

	output := s.output(exitCode, stderr.String(), breach)
	if m, ok := params["model"].(string); ok {
		output["model"] = m
	}
	output["prompt_chars"] = utf8.RuneCountInString(prompt)
	res := &Result{Output: output}

	var badAnswer error
	if schemaFile != "" && s.completed && breach == "" {
		var answer any
		if err := json.Unmarshal([]byte(s.lastMessage), &answer); err != nil {
			badAnswer = fmt.Errorf("%s: the answer does not follow json_schema: %w", name, err)
		} else {
			output["structured_output"] = answer
		}
	}

	var why []string
	if s.lastError != "" && !s.completed {
		why = append(why, "last error: "+s.lastError)
	}
	if r := s.stillRunning(); r != "" {
		why = append(why, r)
	}
	failed := breach != "" || ctx.Err() != nil || !s.completed || s.failure != "" || waitErr != nil
	if sandbox != "" && failed {
		if problems := sandboxNetwork(e.newTransport, cfg, sandbox, output, time.Since(started)); len(problems) > 0 {
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
	case s.failure != "":
		return res, fmt.Errorf("%s: %s", name, s.failure)
	case !s.completed:
		if waitErr != nil {
			return res, fmt.Errorf("%s: %w%s\nstderr: %s", name, waitErr, detail, stderr.String())
		}
		return res, fmt.Errorf("%s: process ended without completing its turn%s\nstderr: %s", name, detail, stderr.String())
	case waitErr != nil:
		return res, fmt.Errorf("%s: %w\nstderr: %s", name, waitErr, stderr.String())
	case badAnswer != nil:
		output["is_error"] = true
		return res, badAnswer
	}
	return res, nil
}

func codexParseLimits(params map[string]any) (codexLimits, error) {
	var l codexLimits
	raw, ok := params["limits"]
	if !ok {
		return l, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return l, fmt.Errorf("codex: limits must be a map")
	}
	for key, v := range m {
		switch key {
		case "max_tool_calls", "max_tokens", "max_duration":
			n, err := intParam("codex", "limits."+key, v)
			if err != nil {
				return l, err
			}
			if n < 0 {
				return l, fmt.Errorf("codex: limits.%s must not be negative", key)
			}
			switch key {
			case "max_tool_calls":
				l.maxToolCalls = n
			case "max_tokens":
				l.maxTokens = n
			default:
				l.maxDuration = time.Duration(n) * time.Second
			}
		default:
			return l, fmt.Errorf("codex: unknown limit %q", key)
		}
	}
	return l, nil
}

// codexArgs builds `exec [options] -` or, with resume, `exec resume [options] <id> -`; the prompt
// is written to stdin, so it never appears in the process list.
func codexArgs(params map[string]any, schemaFile string) ([]string, error) {
	const name = "codex"
	resume := ""
	if raw, ok := params["resume"]; ok {
		s, ok := raw.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("%s: resume must be a non-empty string", name)
		}
		resume = s
	}
	args := []string{"exec"}
	if resume != "" {
		args = append(args, "resume")
	}
	args = append(args, "--json")

	// Codex otherwise refuses to start outside a Git repository, which workflow directories often are not.
	check := false
	if raw, ok := params["git_repo_check"]; ok {
		if check, ok = raw.(bool); !ok {
			return nil, fmt.Errorf("%s: git_repo_check must be a boolean", name)
		}
	}
	if !check {
		args = append(args, "--skip-git-repo-check")
	}
	if raw, ok := params["model"]; ok {
		s, ok := raw.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("%s: model must be a non-empty string", name)
		}
		args = append(args, "--model", s)
	}
	if raw, ok := params["profile"]; ok {
		s, ok := raw.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("%s: profile must be a non-empty string", name)
		}
		if resume != "" {
			return nil, fmt.Errorf("%s: profile cannot be used with resume", name)
		}
		args = append(args, "--profile", s)
	}
	// Settings that are only config keys, given as -c key=<TOML value>; a JSON string is a TOML string.
	var config []string
	if raw, ok := params["sandbox_mode"]; ok {
		s, _ := raw.(string)
		if !codexSandboxModes[s] {
			return nil, fmt.Errorf("%s: sandbox_mode must be one of read-only, workspace-write, danger-full-access", name)
		}
		config = append(config, "sandbox_mode="+tomlString(s))
	}
	if raw, ok := params["effort"]; ok {
		s, ok := raw.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("%s: effort must be a non-empty string", name)
		}
		config = append(config, "model_reasoning_effort="+tomlString(s))
	}
	if raw, ok := params["config"]; ok {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: config must be a map", name)
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v, err := tomlValue(m[k])
			if err != nil {
				return nil, fmt.Errorf("%s: config.%s: %w", name, k, err)
			}
			config = append(config, k+"="+v)
		}
	}
	for _, c := range config {
		args = append(args, "-c", c)
	}
	for _, f := range []struct{ key, flag string }{
		{"dangerously_bypass_approvals_and_sandbox", "--dangerously-bypass-approvals-and-sandbox"},
		{"ephemeral", "--ephemeral"},
		{"ignore_user_config", "--ignore-user-config"},
		{"ignore_rules", "--ignore-rules"},
	} {
		if raw, ok := params[f.key]; ok {
			b, ok := raw.(bool)
			if !ok {
				return nil, fmt.Errorf("%s: %s must be a boolean", name, f.key)
			}
			if b {
				args = append(args, f.flag)
			}
		}
	}
	for _, f := range []struct{ key, flag string }{
		{"add_dirs", "--add-dir"},
		{"images", "--image"},
		{"enable", "--enable"},
		{"disable", "--disable"},
	} {
		vals, err := stringListParam(name, params, f.key)
		if err != nil {
			return nil, err
		}
		if len(vals) > 0 && resume != "" && f.key == "add_dirs" {
			return nil, fmt.Errorf("%s: add_dirs cannot be used with resume", name)
		}
		for _, v := range vals {
			args = append(args, f.flag, v)
		}
	}
	if schemaFile != "" {
		args = append(args, "--output-schema", schemaFile)
	}
	extra, err := stringListParam(name, params, "extra_args")
	if err != nil {
		return nil, err
	}
	args = append(args, extra...)
	if resume != "" {
		args = append(args, resume)
	}
	return append(args, "-"), nil
}

func tomlString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// tomlValue writes a config value as TOML: JSON scalars and lists of them are valid TOML too.
func tomlValue(v any) (string, error) {
	switch t := v.(type) {
	case map[string]any:
		return "", fmt.Errorf("maps are not supported; use a dotted key instead")
	case []any:
		for _, item := range t {
			if _, ok := item.(map[string]any); ok {
				return "", fmt.Errorf("lists of maps are not supported")
			}
		}
	case nil:
		return "", fmt.Errorf("a value is required")
	}
	b, err := json.Marshal(v)
	return string(b), err
}

// codexStream folds the exec JSONL event stream into run state, reporting progress and enforcing
// limits as events arrive.
type codexStream struct {
	ctx         context.Context
	limits      codexLimits
	eventsLimit int
	cancel      context.CancelFunc

	threadID    string
	turns       int
	usage       codexUsage
	toolCalls   int
	counted     map[string]bool
	lastMessage string
	// completed is a turn.completed; failure is a turn.failed's message.
	completed bool
	failure   string
	// errors are the stream's error events, which may be transient (a reconnect) or the reason
	// the turn failed.
	errors    []any
	lastError string
	openTools []map[string]any
	events    []any
	truncated bool
	breach    string
}

func (s *codexStream) consume(r io.Reader) {
	reader := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var ev map[string]any
			if json.Unmarshal(line, &ev) == nil && s.breach == "" {
				s.handle(ev)
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *codexStream) handle(ev map[string]any) {
	switch ev["type"] {
	case "thread.started":
		s.threadID, _ = ev["thread_id"].(string)
		s.emit("init", map[string]any{"session_id": s.threadID}, true)
	case "turn.completed":
		s.completed = true
		s.turns++
		if u, ok := ev["usage"].(map[string]any); ok {
			s.usage.input += jsonInt(u["input_tokens"])
			s.usage.cached += jsonInt(u["cached_input_tokens"])
			s.usage.cacheWrite += jsonInt(u["cache_write_input_tokens"])
			s.usage.output += jsonInt(u["output_tokens"])
			s.usage.reasoning += jsonInt(u["reasoning_output_tokens"])
		}
		tokens := s.usage.counted()
		s.emit("usage", map[string]any{"turns": s.turns, "tokens": tokens, "tool_calls": s.toolCalls}, false)
		if s.limits.maxTokens > 0 && tokens > s.limits.maxTokens {
			s.exceed("max_tokens", float64(s.limits.maxTokens), float64(tokens))
			return
		}
		s.emit("result", map[string]any{"is_error": false, "num_turns": s.turns, "result": s.lastMessage}, true)
	case "turn.failed":
		msg := "the turn failed"
		if e, ok := ev["error"].(map[string]any); ok {
			msg = claudeString(e["message"], msg)
		}
		s.failure = msg
		s.emit("result", map[string]any{"is_error": true, "num_turns": s.turns, "result": msg}, true)
	case "error":
		s.recordError(ev["message"])
	case "item.started":
		item, _ := ev["item"].(map[string]any)
		s.startTool(item)
	case "item.completed":
		item, _ := ev["item"].(map[string]any)
		s.completeItem(item)
	}
}

func (s *codexStream) recordError(v any) {
	msg := claudeString(v, "error")
	s.lastError = msg
	if len(s.errors) < 20 {
		s.errors = append(s.errors, truncateValue(msg))
	}
	s.emit("error", map[string]any{"message": msg}, true)
}

// codexTool names a tool-like item and its input, or "" for items that are not tool calls.
func codexTool(item map[string]any) (string, map[string]any) {
	switch item["type"] {
	case "command_execution":
		return "command_execution", map[string]any{"command": item["command"]}
	case "file_change":
		return "file_change", map[string]any{"changes": item["changes"]}
	case "mcp_tool_call":
		return fmt.Sprintf("mcp:%v.%v", item["server"], item["tool"]), map[string]any{"arguments": item["arguments"]}
	case "web_search":
		return "web_search", map[string]any{"query": item["query"]}
	}
	return "", nil
}

func (s *codexStream) startTool(item map[string]any) {
	name, input := codexTool(item)
	id, _ := item["id"].(string)
	if name == "" || s.counted[id] {
		return
	}
	s.counted[id] = true
	s.toolCalls++
	s.openTools = append(s.openTools, map[string]any{"id": id, "name": name, "input": input})
	s.emit("tool_use", map[string]any{"id": id, "name": name, "input": input}, true)
	if s.limits.maxToolCalls > 0 && s.toolCalls > s.limits.maxToolCalls {
		s.exceed("max_tool_calls", float64(s.limits.maxToolCalls), float64(s.toolCalls))
	}
}

func (s *codexStream) completeItem(item map[string]any) {
	id, _ := item["id"].(string)
	switch item["type"] {
	case "agent_message":
		text, _ := item["text"].(string)
		s.lastMessage = text
		s.emit("text", map[string]any{"text": text}, true)
		return
	case "reasoning":
		s.emit("reasoning", map[string]any{"text": item["text"]}, true)
		return
	case "todo_list":
		s.emit("todo", map[string]any{"items": item["items"]}, true)
		return
	case "error":
		s.recordError(item["message"])
		return
	}
	// A tool call that arrives completed without having started is still counted, once.
	if name, _ := codexTool(item); name == "" {
		return
	}
	s.startTool(item)
	if s.breach != "" {
		return
	}
	for i, t := range s.openTools {
		if t["id"] == id {
			s.openTools = append(s.openTools[:i], s.openTools[i+1:]...)
			break
		}
	}
	status, _ := item["status"].(string)
	isError := status == "failed" || status == "declined"
	content := ""
	switch item["type"] {
	case "command_execution":
		content, _ = item["aggregated_output"].(string)
		if code, ok := item["exit_code"].(float64); ok && code != 0 {
			isError = true
		}
	case "mcp_tool_call":
		if e, ok := item["error"].(map[string]any); ok {
			content = claudeString(e["message"], "")
		} else if r, ok := item["result"]; ok && r != nil {
			b, _ := json.Marshal(r)
			content = string(b)
		}
	}
	data := map[string]any{"tool_use_id": id, "is_error": isError, "content": content}
	if code, ok := item["exit_code"]; ok && code != nil {
		data["exit_code"] = jsonInt(code)
	}
	s.emit("tool_result", data, true)
}

func (s *codexStream) exceed(limit string, max, observed float64) {
	if s.breach != "" {
		return
	}
	s.breach = limit
	s.emit("limit_exceeded", map[string]any{"limit": limit, "max": max, "observed": observed}, true)
	s.cancel()
}

// emit reports live progress and, when store is set, keeps a compact copy in the capped events
// list returned in the step output.
func (s *codexStream) emit(kind string, data map[string]any, store bool) {
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

func (s *codexStream) output(exitCode int, stderr, breach string) map[string]any {
	out := map[string]any{
		"session_id": s.threadID,
		"model":      "",
		"exit_code":  exitCode,
		"stderr":     stderr,
		"num_turns":  s.turns,
		"tool_calls": s.toolCalls,
		"tokens":     s.usage.counted(),
		"usage": map[string]any{
			"input_tokens":             s.usage.input,
			"cached_input_tokens":      s.usage.cached,
			"cache_write_input_tokens": s.usage.cacheWrite,
			"output_tokens":            s.usage.output,
			"reasoning_output_tokens":  s.usage.reasoning,
		},
		"events":           s.events,
		"events_truncated": s.truncated,
		"limit_exceeded":   breach,
		"result":           s.lastMessage,
		"is_error":         breach != "" || s.failure != "" || !s.completed,
	}
	if len(s.errors) > 0 {
		out["errors"] = s.errors
	}
	return out
}

// stillRunning names the tool calls a run that ended without completing its turn was waiting on.
func (s *codexStream) stillRunning() string {
	if s.completed || s.failure != "" || len(s.openTools) == 0 {
		return ""
	}
	var calls []string
	for _, t := range s.openTools {
		call := fmt.Sprint(t["name"])
		if in, ok := t["input"].(map[string]any); ok {
			if v, ok := in["command"].(string); ok && v != "" {
				if r := []rune(strings.Join(strings.Fields(v), " ")); len(r) > 120 {
					v = string(r[:120]) + "…"
				} else {
					v = string(r)
				}
				call += " `" + v + "`"
			}
		}
		calls = append(calls, call)
	}
	return "still waiting on a tool call: " + strings.Join(calls, ", ")
}
