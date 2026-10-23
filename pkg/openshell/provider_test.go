package openshell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGateway is an openshell stand-in that keeps one provider and one profile in files and logs
// each call's argv, plus the credential it received through the environment.
func fakeGateway(t *testing.T) (*CLI, string, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := `#!/bin/sh
state="` + dir + `"
echo "$*" >> "` + log + `"
case "$1 $2" in
  "provider get")
    if [ -f "$state/provider" ]; then printf 'Provider:\n\n  Name: %s\n  Type: %s\n' "$3" "$(cat "$state/provider")"; exit 0; fi
    echo "Error: provider not found" >&2; exit 1 ;;
  "provider create") echo "$6" > "$state/provider"; echo "secret-from-env=$ANTHROPIC_API_KEY" >> "` + log + `" ;;
  "provider update") echo "secret-from-env=$ANTHROPIC_API_KEY" >> "` + log + `" ;;
  "profile export")
    if [ -f "$state/profile" ]; then cat "$state/profile"; exit 0; fi
    echo "Error: provider profile not found" >&2; exit 1 ;;
  "profile import") { cat "$4"; echo "resource_version: 1"; echo "source: user"; } > "$state/profile" ;;
  "profile update") cp "$5" "$state/profile"; cp "$5" "$state/last-update" ;;
esac
`
	bin := filepath.Join(dir, "openshell")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return NewCLI(Config{Binary: bin}), log, dir
}

func TestCLIEnsureProviderCreatesThenUpdatesWithoutSecretsOnTheCommandLine(t *testing.T) {
	c, log, _ := fakeGateway(t)
	ctx := context.Background()
	req := ProviderRequest{Name: "relay", Type: "relay-claude", Credentials: map[string]string{"ANTHROPIC_API_KEY": "sk-secret-1"}}
	res, err := c.EnsureProvider(ctx, req)
	if err != nil || !res.Created || strings.Join(res.CredentialKeys, ",") != "ANTHROPIC_API_KEY" {
		t.Fatalf("create: %+v %v", res, err)
	}
	req.Credentials["ANTHROPIC_API_KEY"] = "sk-secret-2"
	if res, err = c.EnsureProvider(ctx, req); err != nil || res.Created {
		t.Fatalf("update: %+v %v", res, err)
	}
	got := calls(t, log)
	want := []string{
		"provider get relay",
		"provider create --name relay --type relay-claude --credential ANTHROPIC_API_KEY",
		"secret-from-env=sk-secret-1",
		"provider get relay",
		"provider update relay --credential ANTHROPIC_API_KEY",
		"secret-from-env=sk-secret-2",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if _, err := c.EnsureProvider(ctx, ProviderRequest{Name: "relay", Type: "other"}); err == nil || !strings.Contains(err.Error(), "exists with type relay-claude") {
		t.Errorf("a type change was not refused: %v", err)
	}
}

func TestCLIEnsureProfileImportsLeavesAloneAndUpdates(t *testing.T) {
	c, _, dir := fakeGateway(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "p.yaml")
	write := func(s string) {
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("id: relay\nendpoints:\n  - host: relay-api.local\n    port: 443\n")
	for i, want := range []ProfileChange{ProfileImported, ProfileUnchanged} {
		if got, err := c.EnsureProfile(ctx, path); err != nil || got != want {
			t.Fatalf("call %d: %q %v, want %q", i, got, err, want)
		}
	}
	write("id: relay\nendpoints:\n  - host: relay-api.local\n    port: 8443\n")
	if got, err := c.EnsureProfile(ctx, path); err != nil || got != ProfileUpdated {
		t.Fatalf("changed file: %q %v", got, err)
	}
	sent, _ := os.ReadFile(filepath.Join(dir, "last-update"))
	if !strings.Contains(string(sent), "resource_version: 1") || !strings.Contains(string(sent), "port: 8443") {
		t.Errorf("the update must carry the registered resource_version:\n%s", sent)
	}
	write("endpoints: []\n")
	if _, err := c.EnsureProfile(ctx, path); err == nil {
		t.Error("a profile without an id was accepted")
	}
}

func TestProfileMatchesIgnoresGatewayDefaults(t *testing.T) {
	file := map[string]any{"id": "p", "credentials": []any{map[string]any{"name": "t", "env_vars": []any{"T"}}}, "binaries": []any{"/usr/bin/curl"}}
	registered := map[string]any{"id": "p", "resource_version": 3, "source": "user",
		"credentials": []any{map[string]any{"name": "t", "env_vars": []any{"T"}, "description": "", "query_param": ""}}, "binaries": []any{"/usr/bin/curl"}}
	if !profileMatches(file, registered) {
		t.Error("defaults the gateway filled in counted as a change")
	}
	registered["binaries"] = []any{"/usr/bin/curl", "/usr/bin/wget"}
	if profileMatches(file, registered) {
		t.Error("an extra binary was not noticed")
	}
}
