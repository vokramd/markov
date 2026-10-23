package openshell

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Endpoint is how the SDK transport reaches a gateway.
type Endpoint struct {
	// Address is host:port, with http:// for a plaintext gateway.
	Address  string
	CAFile   string
	CertFile string
	KeyFile  string
	Token    string
}

// ResolveEndpoint works out the gateway the SDK transport talks to, the way the openshell CLI
// does: cfg.Gateway as an address (it contains ":" or "://"), else as the name of a gateway the
// CLI registered, else the CLI's active gateway (OPENSHELL_GATEWAY, then the active_gateway
// file). A registered gateway brings its endpoint and mTLS certificates.
func ResolveEndpoint(cfg Config) (*Endpoint, error) {
	ep := &Endpoint{CAFile: cfg.CAFile, CertFile: cfg.CertFile, KeyFile: cfg.KeyFile}
	if cfg.TokenEnv != "" {
		if ep.Token = os.Getenv(cfg.TokenEnv); ep.Token == "" {
			return nil, fmt.Errorf("token_env: %s is not set", cfg.TokenEnv)
		}
	}
	gw := cfg.Gateway
	if strings.Contains(gw, ":") {
		ep.Address = strings.TrimPrefix(gw, "https://")
		return ep, nil
	}
	if gw == "" {
		gw = os.Getenv("OPENSHELL_GATEWAY")
	}
	dirs := gatewayDirs()
	if gw == "" {
		if data, err := os.ReadFile(filepath.Join(filepath.Dir(dirs[0]), "active_gateway")); err == nil {
			gw = strings.TrimSpace(string(data))
		}
	}
	if gw == "" {
		return nil, errors.New("no gateway: set openshell.gateway, or register one with openshell gateway add")
	}
	for _, d := range dirs {
		dir := filepath.Join(d, gw)
		data, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
		if err != nil {
			continue
		}
		var meta struct {
			Endpoint string `json:"gateway_endpoint"`
			AuthMode string `json:"auth_mode"`
		}
		if err := json.Unmarshal(data, &meta); err != nil {
			return nil, fmt.Errorf("gateway %s: %w", gw, err)
		}
		switch meta.AuthMode {
		case "", "none":
			ep.Address = strings.TrimPrefix(meta.Endpoint, "https://")
		case "plaintext":
			ep.Address = "http://" + strings.TrimPrefix(meta.Endpoint, "http://")
		case "mtls":
			ep.Address = strings.TrimPrefix(meta.Endpoint, "https://")
			ep.CAFile = filepath.Join(dir, "mtls", "ca.crt")
			ep.CertFile = filepath.Join(dir, "mtls", "tls.crt")
			ep.KeyFile = filepath.Join(dir, "mtls", "tls.key")
		default:
			return nil, fmt.Errorf("gateway %s uses %s authentication, which the sdk transport does not support; use transport: cli", gw, meta.AuthMode)
		}
		return ep, nil
	}
	return nil, fmt.Errorf("gateway %q is not registered with the openshell CLI (looked in %s)", gw, strings.Join(dirs, ", "))
}

// gatewayDirs are where the CLI keeps registered gateways: the user's config, then the system's.
func gatewayDirs() []string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return []string{filepath.Join(base, "openshell", "gateways"), "/etc/openshell/gateways"}
}
