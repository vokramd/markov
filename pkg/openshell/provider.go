package openshell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// readProfile parses a provider profile file and returns its id.
func readProfile(path string) (map[string]any, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}
	id, _ := doc["id"].(string)
	if id == "" {
		return nil, "", fmt.Errorf("%s: the profile has no id", path)
	}
	return doc, id, nil
}

// profileMatches reports whether every field the file sets has the same value in the registered
// profile. The gateway adds fields of its own (resource_version, source, scope) and fills in
// defaults, also inside list items (description: "", query_param: ""), which do not count.
func profileMatches(file, registered map[string]any) bool {
	for k, v := range file {
		if k == "resource_version" {
			continue
		}
		if !subset(v, registered[k]) {
			return false
		}
	}
	return true
}

// subset reports whether want is contained in got: maps by the keys want has, lists item by item
// (same length), anything else by equality.
func subset(want, got any) bool {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range w {
			if !subset(v, g[k]) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for i := range w {
			if !subset(w[i], g[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(want, got)
	}
}

func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}

func (c *CLI) EnsureProfile(ctx context.Context, path string) (ProfileChange, error) {
	doc, id, err := readProfile(path)
	if err != nil {
		return "", err
	}
	out, err := c.run(ctx, "profile", "export", id)
	if isNotFound(err) {
		if _, err := c.run(ctx, "profile", "import", "-f", path); err != nil {
			return "", err
		}
		return ProfileImported, nil
	}
	if err != nil {
		return "", err
	}
	var registered map[string]any
	if err := yaml.Unmarshal(out, &registered); err != nil {
		return "", fmt.Errorf("reading the registered profile %s: %w", id, err)
	}
	if profileMatches(doc, registered) {
		return ProfileUnchanged, nil
	}
	// An update must name the version it replaces, which the file does not carry.
	doc["resource_version"] = registered["resource_version"]
	data, err := yaml.Marshal(doc)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp("", "markov-profile-*.yaml")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if _, err := c.run(ctx, "profile", "update", id, "-f", tmp.Name()); err != nil {
		return "", err
	}
	return ProfileUpdated, nil
}

func (c *CLI) EnsureProvider(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	if req.Name == "" || req.Type == "" {
		return nil, errors.New("a provider needs a name and a type")
	}
	keys := sortedKeys(req.Credentials)
	// Credentials go to the CLI as environment variables it looks up by name, so their values
	// never appear in the process list.
	env := make([]string, 0, len(keys))
	var credArgs []string
	for _, k := range keys {
		env = append(env, k+"="+req.Credentials[k])
		credArgs = append(credArgs, "--credential", k)
	}
	out, err := c.run(ctx, "provider", "get", req.Name)
	switch {
	case isNotFound(err):
		args := append([]string{"provider", "create", "--name", req.Name, "--type", req.Type}, credArgs...)
		if _, err := c.runEnv(ctx, env, args...); err != nil {
			return nil, err
		}
		return &ProviderResult{Created: true, CredentialKeys: keys}, nil
	case err != nil:
		return nil, err
	}
	if t := providerField(out, "Type"); t != req.Type {
		return nil, fmt.Errorf("provider %s exists with type %s, not %s; delete it to change its type", req.Name, t, req.Type)
	}
	if len(keys) > 0 {
		if _, err := c.runEnv(ctx, env, append([]string{"provider", "update", req.Name}, credArgs...)...); err != nil {
			return nil, err
		}
	}
	return &ProviderResult{CredentialKeys: keys}, nil
}

// providerField reads "Key: value" from `openshell provider get` output.
func providerField(out []byte, key string) string {
	for _, line := range bytes.Split(out, []byte("\n")) {
		if k, v, ok := strings.Cut(strings.TrimSpace(string(line)), ":"); ok && k == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
