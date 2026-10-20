package provisioning

import (
	"fmt"
	"net/url"
	"strings"
)

// NormalizeMCPHTTPURL returns the canonical identity of an HTTP descriptor
// target (the client approval hash binds to it). Credentials and fragments are rejected because they must not be
// hidden in a descriptor.
func NormalizeMCPHTTPURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !u.IsAbs() || u.Host == "" {
		return "", fmt.Errorf("target must be an absolute HTTP URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("target scheme %q is not http or https", u.Scheme)
	}
	if u.User != nil {
		return "", fmt.Errorf("target must not contain credentials")
	}
	if u.Fragment != "" {
		return "", fmt.Errorf("target must not contain a fragment")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

// MCPDescriptorTarget returns the exact identity of a validated
// descriptor without resolving environment references or a local executable.
func MCPDescriptorTarget(spec MCPServerSpec) (string, error) {
	switch spec.Type {
	case "http":
		return NormalizeMCPHTTPURL(spec.URL)
	case "stdio":
		if err := ValidateMCPStdioCommand(spec.Command); err != nil {
			return "", err
		}
		return spec.Command, nil
	default:
		return "", fmt.Errorf("unsupported transport %q", spec.Type)
	}
}
