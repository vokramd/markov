package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/jctanner/markov/pkg/outputlimit"
	"gopkg.in/yaml.v3"
)

// AnsiblePlaybook runs ansible-playbook. The command is executed directly
// (no shell) so parameter values cannot inject shell syntax.
type AnsiblePlaybook struct{}

func NewAnsiblePlaybook() *AnsiblePlaybook { return &AnsiblePlaybook{} }

// Ansible runs an ad-hoc ansible module against a host pattern.
type Ansible struct{}

func NewAnsible() *Ansible { return &Ansible{} }

func (e *AnsiblePlaybook) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	const name = "ansible_playbook"
	playbooks, err := stringListParam(name, params, "playbook")
	if err != nil {
		return nil, err
	}
	if len(playbooks) == 0 {
		return nil, fmt.Errorf("%s: playbook is required", name)
	}

	b := newAnsibleBuilder(name, "ansible-playbook")
	defer b.cleanup()
	if err := b.common(params); err != nil {
		return nil, err
	}
	if err := b.playbookFlags(params); err != nil {
		return nil, err
	}
	b.args = append(b.args, playbooks...)
	return b.run(ctx, params, true)
}

func (e *Ansible) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	const name = "ansible"
	pattern, ok := params["pattern"].(string)
	if !ok || pattern == "" {
		return nil, fmt.Errorf("%s: pattern is required", name)
	}

	b := newAnsibleBuilder(name, "ansible")
	defer b.cleanup()
	b.args = append(b.args, pattern)
	if err := b.common(params); err != nil {
		return nil, err
	}
	module := "command"
	if raw, ok := params["module"]; ok {
		m, ok := raw.(string)
		if !ok || m == "" {
			return nil, fmt.Errorf("%s: module must be a non-empty string", name)
		}
		module = m
	}
	b.args = append(b.args, "-m", module)
	if raw, ok := params["module_args"]; ok {
		switch v := raw.(type) {
		case string:
			b.args = append(b.args, "-a", v)
		case map[string]any, map[string]string:
			data, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("%s: encoding module_args: %w", name, err)
			}
			b.args = append(b.args, "-a", string(data))
		default:
			return nil, fmt.Errorf("%s: module_args must be a string or map", name)
		}
	}
	if raw, ok := params["poll"]; ok {
		n, err := intParam(name, "poll", raw)
		if err != nil {
			return nil, err
		}
		b.args = append(b.args, "-P", strconv.Itoa(n))
	}
	if raw, ok := params["background"]; ok {
		n, err := intParam(name, "background", raw)
		if err != nil {
			return nil, err
		}
		b.args = append(b.args, "-B", strconv.Itoa(n))
	}
	return b.run(ctx, params, false)
}

type ansibleBuilder struct {
	name    string
	binary  string
	args    []string
	tmp     []string
	chdir   string
	envList []string
}

func newAnsibleBuilder(name, binary string) *ansibleBuilder {
	return &ansibleBuilder{name: name, binary: binary}
}

func (b *ansibleBuilder) cleanup() {
	for _, p := range b.tmp {
		_ = os.Remove(p)
	}
}

func (b *ansibleBuilder) errf(format string, a ...any) error {
	return fmt.Errorf(b.name+": "+format, a...)
}

// common handles flags shared by ansible and ansible-playbook.
func (b *ansibleBuilder) common(params map[string]any) error {
	if raw, ok := params["binary"]; ok {
		s, ok := raw.(string)
		if !ok || s == "" {
			return b.errf("binary must be a non-empty string")
		}
		b.binary = s
	}
	if raw, ok := params["chdir"]; ok {
		s, ok := raw.(string)
		if !ok || s == "" {
			return b.errf("chdir must be a non-empty string")
		}
		info, err := os.Stat(s)
		if err != nil || !info.IsDir() {
			return b.errf("chdir %q is not a directory", s)
		}
		b.chdir = s
	}

	if raw, ok := params["inventory"]; ok {
		if err := b.inventory(raw); err != nil {
			return err
		}
	}
	if raw, ok := params["extra_vars"]; ok {
		if err := b.extraVars(raw); err != nil {
			return err
		}
	}
	files, err := stringListParam(b.name, params, "extra_vars_files")
	if err != nil {
		return err
	}
	for _, f := range files {
		b.args = append(b.args, "-e", "@"+f)
	}

	limit, err := stringListParam(b.name, params, "limit")
	if err != nil {
		return err
	}
	if len(limit) > 0 {
		b.args = append(b.args, "--limit", strings.Join(limit, ","))
	}

	for _, f := range []struct{ key, flag string }{
		{"check", "--check"},
		{"diff", "--diff"},
		{"become", "--become"},
	} {
		if raw, ok := params[f.key]; ok {
			v, ok := raw.(bool)
			if !ok {
				return b.errf("%s must be a boolean", f.key)
			}
			if v {
				b.args = append(b.args, f.flag)
			}
		}
	}
	for _, f := range []struct{ key, flag string }{
		{"become_user", "--become-user"},
		{"become_method", "--become-method"},
		{"remote_user", "--user"},
		{"connection", "--connection"},
		{"private_key", "--private-key"},
		{"vault_password_file", "--vault-password-file"},
		{"vault_id", "--vault-id"},
	} {
		if raw, ok := params[f.key]; ok {
			s, ok := raw.(string)
			if !ok || s == "" {
				return b.errf("%s must be a non-empty string", f.key)
			}
			b.args = append(b.args, f.flag, s)
		}
	}
	for _, f := range []struct{ key, flag string }{
		{"forks", "--forks"},
		{"timeout", "--timeout"},
	} {
		if raw, ok := params[f.key]; ok {
			n, err := intParam(b.name, f.key, raw)
			if err != nil {
				return err
			}
			b.args = append(b.args, f.flag, strconv.Itoa(n))
		}
	}
	if raw, ok := params["verbosity"]; ok {
		n, err := intParam(b.name, "verbosity", raw)
		if err != nil {
			return err
		}
		if n < 0 || n > 6 {
			return b.errf("verbosity must be between 0 and 6")
		}
		if n > 0 {
			b.args = append(b.args, "-"+strings.Repeat("v", n))
		}
	}

	extra, err := stringListParam(b.name, params, "extra_args")
	if err != nil {
		return err
	}
	b.args = append(b.args, extra...)

	b.envList = os.Environ()
	b.envList = append(b.envList, "ANSIBLE_NOCOLOR=1")
	if raw, ok := params["env"]; ok {
		values, ok := stringMapParam(raw)
		if !ok {
			return b.errf("env must be a map of strings")
		}
		for k, v := range values {
			if k == "" || strings.Contains(k, "=") {
				return b.errf("env key %q is invalid", k)
			}
			// Later entries win in exec.Cmd.Env.
			b.envList = append(b.envList, k+"="+v)
		}
	}
	return nil
}

func (b *ansibleBuilder) playbookFlags(params map[string]any) error {
	for _, f := range []struct{ key, flag string }{
		{"tags", "--tags"},
		{"skip_tags", "--skip-tags"},
	} {
		vals, err := stringListParam(b.name, params, f.key)
		if err != nil {
			return err
		}
		if len(vals) > 0 {
			b.args = append(b.args, f.flag, strings.Join(vals, ","))
		}
	}
	if raw, ok := params["start_at_task"]; ok {
		s, ok := raw.(string)
		if !ok || s == "" {
			return b.errf("start_at_task must be a non-empty string")
		}
		b.args = append(b.args, "--start-at-task", s)
	}
	return nil
}

// inventory accepts a string (path or inline host list), a list of those, or
// an inline inventory map that is written to a temporary YAML file.
func (b *ansibleBuilder) inventory(raw any) error {
	switch v := raw.(type) {
	case string:
		if v == "" {
			return b.errf("inventory must not be empty")
		}
		b.args = append(b.args, "-i", v)
	case []string:
		for _, s := range v {
			b.args = append(b.args, "-i", s)
		}
	case []any:
		for i, item := range v {
			s, ok := item.(string)
			if !ok || s == "" {
				return b.errf("inventory[%d] must be a non-empty string", i)
			}
			b.args = append(b.args, "-i", s)
		}
	case map[string]any:
		data, err := yaml.Marshal(v)
		if err != nil {
			return b.errf("encoding inventory: %w", err)
		}
		path, err := b.tempFile("markov-ansible-inventory-*.yml", data)
		if err != nil {
			return err
		}
		b.args = append(b.args, "-i", path)
	default:
		return b.errf("inventory must be a string, list of strings, or map")
	}
	return nil
}

// extraVars accepts a map (passed as a JSON file so values stay off the
// process list) or a string (passed as-is: k=v, JSON, or @file).
func (b *ansibleBuilder) extraVars(raw any) error {
	switch v := raw.(type) {
	case string:
		b.args = append(b.args, "-e", v)
	case map[string]any:
		data, err := json.Marshal(v)
		if err != nil {
			return b.errf("encoding extra_vars: %w", err)
		}
		path, err := b.tempFile("markov-ansible-vars-*.json", data)
		if err != nil {
			return err
		}
		b.args = append(b.args, "-e", "@"+path)
	default:
		return b.errf("extra_vars must be a string or map")
	}
	return nil
}

func (b *ansibleBuilder) tempFile(pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", b.errf("creating temporary file: %w", err)
	}
	b.tmp = append(b.tmp, f.Name())
	if err := os.Chmod(f.Name(), 0600); err != nil {
		_ = f.Close()
		return "", b.errf("securing temporary file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return "", b.errf("writing temporary file: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", b.errf("closing temporary file: %w", err)
	}
	return f.Name(), nil
}

func (b *ansibleBuilder) run(ctx context.Context, params map[string]any, playbook bool) (*Result, error) {
	cmd := exec.CommandContext(ctx, b.binary, b.args...)
	cmd.Dir = b.chdir
	cmd.Env = b.envList
	out, stderr, err := runCapped(ctx, cmd)
	if _, over := outputlimit.As(err); over {
		return nil, limitErr(b.name, err)
	}
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	output := map[string]any{
		"stdout":    out,
		"stderr":    stderr,
		"exit_code": exitCode,
	}
	if playbook {
		recap := parseAnsibleRecap(out)
		output["recap"] = recap
		output["changed"] = recapChanged(recap)
	} else {
		output["changed"] = adhocChangedRe.MatchString(out)
	}
	if err != nil {
		return &Result{Output: output}, fmt.Errorf("%s: %w\nstderr: %s", b.name, err, stderr)
	}
	return &Result{Output: output}, nil
}

var (
	recapLineRe = regexp.MustCompile(`^(\S+)\s*:\s+ok=(\d+)\s+changed=(\d+)\s+unreachable=(\d+)\s+failed=(\d+)(.*)$`)
	recapKVRe   = regexp.MustCompile(`(skipped|rescued|ignored)=(\d+)`)
	// Ad-hoc output lines look like "host | CHANGED => {" or "host | CHANGED | rc=0 >>".
	adhocChangedRe = regexp.MustCompile(`(?m)^\S+ \| CHANGED\b`)
)

// parseAnsibleRecap extracts the PLAY RECAP table into
// {host: {ok, changed, unreachable, failed, skipped, rescued, ignored}}.
func parseAnsibleRecap(stdout string) map[string]any {
	recap := map[string]any{}
	inRecap := false
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "PLAY RECAP") {
			inRecap = true
			continue
		}
		if !inRecap {
			continue
		}
		m := recapLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		host := map[string]any{
			"ok":          atoi(m[2]),
			"changed":     atoi(m[3]),
			"unreachable": atoi(m[4]),
			"failed":      atoi(m[5]),
			"skipped":     0,
			"rescued":     0,
			"ignored":     0,
		}
		for _, kv := range recapKVRe.FindAllStringSubmatch(m[6], -1) {
			host[kv[1]] = atoi(kv[2])
		}
		recap[m[1]] = host
	}
	return recap
}

func recapChanged(recap map[string]any) bool {
	for _, h := range recap {
		if host, ok := h.(map[string]any); ok && host["changed"].(int) > 0 {
			return true
		}
	}
	return false
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func intParam(name, key string, raw any) (int, error) {
	switch v := raw.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		if v == float64(int(v)) {
			return int(v), nil
		}
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n, nil
		}
	}
	return 0, fmt.Errorf("%s: %s must be an integer", name, key)
}

// ansibleStringList reads a param that may be a single string or a list of
// strings. Missing returns nil.
func stringListParam(name string, params map[string]any, key string) ([]string, error) {
	raw, exists := params[key]
	if !exists {
		return nil, nil
	}
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil, nil
		}
		return []string{v}, nil
	case []string:
		return v, nil
	case []any:
		out := make([]string, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s: %s[%d] must be a string", name, key, i)
			}
			out[i] = s
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s: %s must be a string or list of strings", name, key)
	}
}
