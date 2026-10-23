package openshell

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archive(t *testing.T, entries ...*tar.Header) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range entries {
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(h.Name))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(h.Name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestReadTarRefusesToLeaveTheDestination(t *testing.T) {
	outside := t.TempDir()
	for name, entries := range map[string][]*tar.Header{
		"dot-dot":            {{Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644}},
		"absolute":           {{Name: "/tmp/evil", Typeflag: tar.TypeReg, Mode: 0o644}},
		"through a symlink":  {{Name: "esc", Typeflag: tar.TypeSymlink, Linkname: outside}, {Name: "esc/evil", Typeflag: tar.TypeReg, Mode: 0o644}},
		"onto a symlink":     {{Name: "esc", Typeflag: tar.TypeSymlink, Linkname: filepath.Join(outside, "evil")}, {Name: "esc", Typeflag: tar.TypeReg, Mode: 0o644}},
		"nested dir symlink": {{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o755}, {Name: "d/up", Typeflag: tar.TypeSymlink, Linkname: "../.."}, {Name: "d/up/evil", Typeflag: tar.TypeReg, Mode: 0o644}},
	} {
		t.Run(name, func(t *testing.T) {
			dest := t.TempDir()
			_ = readTar(archive(t, entries...), dest)
			if _, err := os.Stat(filepath.Join(outside, "evil")); err == nil {
				t.Fatal("a file was written outside the destination")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "evil")); err == nil {
				t.Fatal("a file was written next to the destination")
			}
		})
	}
}

func TestTarRoundTripKeepsTreeModesAndLinks(t *testing.T) {
	src := filepath.Join(t.TempDir(), "site")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "run.sh"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub/run.sh", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := writeTar(&buf, src+"/", false); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := readTar(&buf, dest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dest, "site", "sub", "run.sh"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("run.sh: %v %v", info, err)
	}
	if link, err := os.Readlink(filepath.Join(dest, "site", "link")); err != nil || link != "sub/run.sh" {
		t.Errorf("link: %q %v", link, err)
	}
}

func TestResolveEndpointUsesTheCLIsGateways(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("OPENSHELL_GATEWAY", "")
	dir := filepath.Join(cfgHome, "openshell", "gateways", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "metadata.json"), `{"name":"local","gateway_endpoint":"https://127.0.0.1:17670","auth_mode":"mtls"}`)
	write(filepath.Join(cfgHome, "openshell", "active_gateway"), "local\n")

	ep, err := ResolveEndpoint(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if ep.Address != "127.0.0.1:17670" || ep.CertFile != filepath.Join(dir, "mtls", "tls.crt") || ep.KeyFile != filepath.Join(dir, "mtls", "tls.key") {
		t.Errorf("active gateway: %+v", ep)
	}
	if ep, err := ResolveEndpoint(Config{Gateway: "gw.example.com:443", CAFile: "/ca"}); err != nil || ep.Address != "gw.example.com:443" || ep.CAFile != "/ca" {
		t.Errorf("address: %+v %v", ep, err)
	}
	if _, err := ResolveEndpoint(Config{Gateway: "nope"}); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Errorf("unknown gateway: %v", err)
	}
	write(filepath.Join(dir, "metadata.json"), `{"gateway_endpoint":"https://x:1","auth_mode":"oidc"}`)
	if _, err := ResolveEndpoint(Config{Gateway: "local"}); err == nil || !strings.Contains(err.Error(), "use transport: cli") {
		t.Errorf("oidc gateway: %v", err)
	}
	t.Setenv("MARKOV_TEST_TOKEN", "")
	if _, err := ResolveEndpoint(Config{Gateway: "h:1", TokenEnv: "MARKOV_TEST_TOKEN"}); err == nil {
		t.Error("an unset token_env was accepted")
	}
}
