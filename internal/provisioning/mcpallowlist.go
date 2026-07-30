package provisioning

import (
	"fmt"
	"net/url"
	"strings"
)

// MCPAllowlistEntry is an exact operator grant for one KB-provided HTTP MCP
// descriptor. Target is the normalised absolute endpoint.
type MCPAllowlistEntry struct {
	Name      string `yaml:"name" json:"name"`
	Transport string `yaml:"transport" json:"transport"`
	Target    string `yaml:"target" json:"target"`
}

// NormalizeMCPHTTPURL returns the canonical endpoint identity. Credentials and
// fragments are rejected so policy never conceals sensitive or irrelevant data.
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

// ValidateMCPAllowlist validates policy syntax independently of KB contents.
// A valid entry without a descriptor is a non-fatal staged-configuration case.
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
		if e.Transport != "http" {
			return fmt.Errorf("artifact %q has unsupported transport %q", e.Name, e.Transport)
		}
		normalized, err := NormalizeMCPHTTPURL(e.Target)
		if err != nil {
			return fmt.Errorf("artifact %q: %w", e.Name, err)
		}
		if normalized != e.Target {
			return fmt.Errorf("artifact %q target must be normalized as %q", e.Name, normalized)
		}
	}
	return nil
}

// MCPAllowed checks the exact descriptor identity. Empty policy denies all.
func MCPAllowed(entries []MCPAllowlistEntry, name string, spec MCPServerSpec) bool {
	if spec.Type != "http" {
		return false
	}
	target, err := NormalizeMCPHTTPURL(spec.URL)
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
