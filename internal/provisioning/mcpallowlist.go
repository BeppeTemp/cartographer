package provisioning

import (
	"fmt"
	"net/url"
	"strings"
)

// MCPAllowlistEntry is an exact operator grant for one KB-provided MCP
// descriptor. Target is a normalised absolute endpoint for HTTP transports.
type MCPAllowlistEntry struct {
	Name      string `yaml:"name" json:"name"`
	Transport string `yaml:"transport" json:"transport"`
	Target    string `yaml:"target" json:"target"`
}

// NormalizeMCPHTTPURL returns the canonical identity used by the server
// allow-list. Credentials and fragments are rejected because they must not be
// hidden in an operator policy entry.
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

// ValidateMCPAllowlist validates syntax independent of the KB contents. A
// well-formed entry that does not yet have a descriptor is intentionally valid
// to support staged rollouts.
func ValidateMCPAllowlist(entries []MCPAllowlistEntry) error {
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.Name == "" || e.Name != strings.TrimSpace(e.Name) {
			return fmt.Errorf("entry has an empty or invalid name")
		}
		if seen[e.Name] {
			return fmt.Errorf("duplicate artifact name %q", e.Name)
		}
		seen[e.Name] = true
		switch e.Transport {
		case "http":
			normalized, err := NormalizeMCPHTTPURL(e.Target)
			if err != nil {
				return fmt.Errorf("artifact %q: %w", e.Name, err)
			}
			if normalized != e.Target {
				return fmt.Errorf("artifact %q target must be normalized as %q", e.Name, normalized)
			}
		case "stdio":
			if err := ValidateMCPStdioCommand(e.Target); err != nil {
				return fmt.Errorf("artifact %q: invalid stdio target: %w", e.Name, err)
			}
		default:
			return fmt.Errorf("artifact %q has unsupported transport %q", e.Name, e.Transport)
		}
	}
	return nil
}

// MCPAllowed checks the exact descriptor identity after validation. An absent
// or empty list intentionally permits no MCP descriptors.
func MCPAllowed(entries []MCPAllowlistEntry, name string, spec MCPServerSpec) bool {
	target, err := MCPDescriptorTarget(spec)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name == name && e.Transport == spec.Type && e.Target == target {
			return true
		}
	}
	return false
}

// MCPDescriptorTarget returns the exact policy identity of a validated
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
