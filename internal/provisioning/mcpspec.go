// mcpspec.go (D69, WP1) — source format and validation for third-party MCP
// servers distributed by a KB: one JSON file per server in mcp/<name>.json (KB),
// single-file like agents (agents/<name>.md), not a directory like skills.
//
// D116 adds the local "stdio" transport alongside D69 HTTP. The descriptor is
// still provider-neutral; client-side preflight verifies the local executable
// before a provider receives it.

package provisioning

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// MCPServerSpec is the on-disk format of mcp/<name>.json in a KB: provider-neutral,
// translated for each provider by internal/configurator.EmitServer (WP3).
type MCPServerSpec struct {
	Type    string            `json:"type"`
	URL     string            `json:"url,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// envRefPattern spots a "${VAR_NAME}" reference inside a header/env value —
// the only allowed form (WP1): the client resolves it against its own
// environment at apply time (see configurator.EmitServer), so no secret ever
// lives in the KB file. A value with a literal prefix/suffix (e.g.
// "Bearer ${TOKEN}") is allowed — at least one "${...}" reference is enough; a
// value with no reference at all is rejected as a probable hardcoded secret.
var envRefPattern = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}`)
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return MCPServerSpec{}, fmt.Errorf("mcp %q: invalid json: %w", name, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return MCPServerSpec{}, fmt.Errorf("mcp %q: invalid json: multiple values", name)
	}
	switch spec.Type {
	case "http":
		if strings.TrimSpace(spec.URL) == "" {
			return MCPServerSpec{}, fmt.Errorf("mcp %q: missing url", name)
		}
		u, err := url.Parse(spec.URL)
		if err != nil || !u.IsAbs() || u.Host == "" {
			return MCPServerSpec{}, fmt.Errorf("mcp %q: url must be absolute", name)
		}
		if spec.Command != "" || len(spec.Args) != 0 {
			return MCPServerSpec{}, fmt.Errorf("mcp %q: http transport rejects command and args", name)
		}
		if err := validateEnvRefs(name, "headers", spec.Headers); err != nil {
			return MCPServerSpec{}, err
		}
	case "stdio":
		if spec.URL != "" || len(spec.Headers) != 0 {
			return MCPServerSpec{}, fmt.Errorf("mcp %q: stdio transport rejects url and headers", name)
		}
		if err := ValidateMCPStdioCommand(spec.Command); err != nil {
			return MCPServerSpec{}, fmt.Errorf("mcp %q: %w", name, err)
		}
	default:
		return MCPServerSpec{}, fmt.Errorf("mcp %q: unsupported type %q", name, spec.Type)
	}
	if err := validateEnvRefs(name, "env", spec.Env); err != nil {
		return MCPServerSpec{}, err
	}
	return spec, nil
}

// ValidateMCPStdioCommand accepts only an executable name resolved through PATH
// or an absolute, clean path. It deliberately excludes shell syntax: providers
// receive command and args separately and Cartographer never starts a shell.
func ValidateMCPStdioCommand(command string) error {
	if command == "" || command != strings.TrimSpace(command) || strings.IndexByte(command, 0) >= 0 {
		return fmt.Errorf("command is empty or invalid")
	}
	// Defence in depth only: providers get command and args separately and
	// Cartographer never starts a shell. The set is therefore the characters a
	// shell acts on, and not every character that is merely unusual — `~`,
	// brackets, braces, `!` and quotes are legal in file names, and a Windows
	// 8.3 short path (C:\Users\RUNNER~1\AppData\...) contains a tilde.
	if strings.ContainsAny(command, "|&;<>()$`*?\"\n\r") {
		return fmt.Errorf("command contains shell metacharacters")
	}
	if isAbsCommandPath(command) {
		if cleanCommandPath(command) != command {
			return fmt.Errorf("command absolute path must be clean")
		}
		return nil
	}
	if command == "." || command == ".." || strings.ContainsAny(command, `/\\`) || strings.ContainsAny(command, " \t\r\n") {
		return fmt.Errorf("command must be a bare executable name or absolute path")
	}
	return nil
}

// isAbsCommandPath recognises an absolute path in either platform's spelling.
// A descriptor lives in the KB and is read by every client: a unix command
// (/usr/local/bin/tool) must stay a valid absolute path when the file is
// validated on Windows, and a Windows one (C:\tools\mcp.exe, \\host\share\x)
// when it is validated on unix. Whether the command exists *here* is the
// preflight's question, not this one's.
func isAbsCommandPath(s string) bool {
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, `\\`) {
		return true
	}
	return len(s) >= 3 && s[1] == ':' && (s[2] == '\\' || s[2] == '/') && isDriveLetter(s[0])
}

func isDriveLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// cleanCommandPath normalises with the syntax the path is written in, so the
// cleanliness check does not depend on the host: path.Clean would mangle a
// Windows path's separators, filepath.Clean on unix would leave a Windows path
// untouched and call every one of them clean.
func cleanCommandPath(s string) string {
	if strings.ContainsRune(s, '\\') {
		return windowsCleanPath(s)
	}
	return path.Clean(s)
}

// windowsCleanPath is filepath.Clean's Windows behaviour, spelled out so it
// runs the same way on a unix host.
func windowsCleanPath(s string) string {
	prefix := ""
	rest := s
	switch {
	case strings.HasPrefix(s, `\\`):
		prefix, rest = `\\`, s[2:]
	case len(s) >= 2 && s[1] == ':' && isDriveLetter(s[0]):
		prefix, rest = s[:2], s[2:]
	}
	cleaned := path.Clean(strings.ReplaceAll(rest, `\\`, "/"))
	return prefix + strings.ReplaceAll(cleaned, "/", `\\`)
}

// validateEnvRefs rejects, in values, every entry whose value does not
// reference at least one "${VAR}" — see envRefPattern.
func validateEnvRefs(serverName, field string, values map[string]string) error {
	for k, v := range values {
		if field == "env" && !envNamePattern.MatchString(k) {
			return fmt.Errorf("mcp %q: env key %q is not a portable variable name", serverName, k)
		}
		if !envRefPattern.MatchString(v) {
			return fmt.Errorf(
				"mcp %q: %s[%q] does not reference an env var (\"${VAR}\"): literal value rejected for security",
				serverName, field, k)
		}
	}
	return nil
}
