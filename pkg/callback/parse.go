package callback

import (
	"fmt"
	"net/url"
	"strings"
)

func ParseCallbackURL(rawURL string, headers map[string]string, bufferSize int, tlsInsecure bool, tlsCertPath string) (Callback, error) {
	if strings.HasPrefix(rawURL, "jsonl://") {
		path := strings.TrimPrefix(rawURL, "jsonl://")
		if path == "" {
			return nil, fmt.Errorf("jsonl callback requires a file path: jsonl:///path/to/file.jsonl")
		}
		return NewJSONLCallback(path)
	}

	if strings.HasPrefix(rawURL, "tty://") {
		return NewTTYCallback(strings.TrimPrefix(rawURL, "tty://"))
	}

	if strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://") {
		if _, err := url.Parse(rawURL); err != nil {
			return nil, fmt.Errorf("invalid HTTP callback URL: %w", err)
		}
		return NewHTTPCallback(rawURL, headers, bufferSize), nil
	}

	if strings.HasPrefix(rawURL, "grpc://") || strings.HasPrefix(rawURL, "grpcs://") {
		addr := strings.TrimPrefix(rawURL, "grpcs://")
		useTLS := strings.HasPrefix(rawURL, "grpcs://")
		if !useTLS {
			addr = strings.TrimPrefix(rawURL, "grpc://")
		}
		if addr == "" {
			return nil, fmt.Errorf("gRPC callback requires an address: grpc://host:port")
		}
		if useTLS && tlsCertPath == "" && !tlsInsecure {
			tlsInsecure = true
		}
		return NewGRPCCallback(addr, !useTLS || tlsInsecure, tlsCertPath)
	}

	return nil, fmt.Errorf("unsupported callback scheme in %q (supported: jsonl://, tty://, http://, https://, grpc://, grpcs://)", rawURL)
}
