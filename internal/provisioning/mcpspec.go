// mcpspec.go (D69, WP1) — source format and validation for third-party MCP
// servers distributed by a KB: one JSON file per server in mcp/<name>.json (KB),
// single-file like agents (agents/<name>.md), not a directory like skills.
//
// Only the "http" transport in this first iteration (D69's starting decision):
// "stdio" (command/args) implies referencing a binary present on the client,
// thornier (distribution, paths, security) — deferred. The Type field is
// present from the start anyway so the schema needs no migration when it is
// added.
package provisioning

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// MCPServerSpec is the on-disk format of mcp/<name>.json in a KB: provider-neutral,
// translated for each provider by internal/configurator.EmitServer (WP3).
type MCPServerSpec struct {
	Type    string            `json:"type"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Env is validated with the same security rule as Headers (no literal
	// secret) for future compatibility with "stdio", but is not yet emitted
	// by any provider in this HTTP-only iteration (WP3/WP5): none of today's
	// 4 providers exposes a native channel for generic env vars on an
	// already-verified "http" MCP server — only the Authorization Bearer
	// header via ${VAR} is reliably representable today.
	Env map[string]string `json:"env,omitempty"`
}

// envRefPattern spots a "${VAR_NAME}" reference inside a header/env value —
// the only allowed form (WP1): the client resolves it against its own
// environment at apply time (see configurator.EmitServer), so no secret ever
// lives in the KB file. A value with a literal prefix/suffix (e.g.
// "Bearer ${TOKEN}") is allowed — at least one "${...}" reference is enough; a
// value with no reference at all is rejected as a probable hardcoded secret.
var envRefPattern = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}`)

// ParseMCPServerSpec is the exported equivalent of parseMCPServerSpec, used
// from outside the package to validate an mcp/<name>.json file before writing
// it (artifact_write, D71) with the same rule as BuildManifest — no
// duplication of the validation.
func ParseMCPServerSpec(name string, data []byte) (MCPServerSpec, error) {
	return parseMCPServerSpec(name, data)
}

// parseMCPServerSpec parses and validates the content of an mcp/<name>.json file:
// invalid json, unsupported Type, missing url, or a headers/env value with no
// "${VAR}" reference at all (looks like a literal secret) fail here — so a
// malformed/unsafe file fails BuildManifest, not Apply (WP2).
func parseMCPServerSpec(name string, data []byte) (MCPServerSpec, error) {
	var spec MCPServerSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return MCPServerSpec{}, fmt.Errorf("mcp %q: invalid json: %w", name, err)
	}
	if spec.Type != "http" {
		return MCPServerSpec{}, fmt.Errorf("mcp %q: type %q not supported (only \"http\" in this iteration, D69)", name, spec.Type)
	}
	if strings.TrimSpace(spec.URL) == "" {
		return MCPServerSpec{}, fmt.Errorf("mcp %q: missing url", name)
	}
	if err := validateEnvRefs(name, "headers", spec.Headers); err != nil {
		return MCPServerSpec{}, err
	}
	if err := validateEnvRefs(name, "env", spec.Env); err != nil {
		return MCPServerSpec{}, err
	}
	return spec, nil
}

// validateEnvRefs rejects, in values, every entry whose value does not
// reference at least one "${VAR}" — see envRefPattern.
func validateEnvRefs(serverName, field string, values map[string]string) error {
	for k, v := range values {
		if !envRefPattern.MatchString(v) {
			return fmt.Errorf(
				"mcp %q: %s[%q] does not reference an env var (\"${VAR}\"): literal value rejected for security",
				serverName, field, k)
		}
	}
	return nil
}
