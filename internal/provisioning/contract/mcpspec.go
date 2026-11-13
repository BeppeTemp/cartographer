// mcpspec.go (D69, WP1) — source format and validation for third-party MCP
// servers distributed by a KB: one JSON file per server in mcp/<name>.json (KB),
// single-file like agents (agents/<name>.md), not a directory like skills.
//
// D116 adds the local "stdio" transport alongside D69 HTTP. The descriptor is
// still provider-neutral; client-side preflight verifies the local executable
// before a provider receives it.

package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/secretscan"
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
	// EnvLiteral holds non-secret constants (NODE_OPTIONS=--use-system-ca): the
	// value is written verbatim, so it is scanned for secrets and may not hold
	// a "${" a client would expand. Its keys are disjoint from Env's.
	EnvLiteral map[string]string `json:"env_literal,omitempty"`
	// Requires is a free label for the network a server is only reachable from
	// ("vpn", "office-lan"). It only words the probe, doctor and status lines
	// when the host does not answer; it is never written into a client file.
	Requires string `json:"requires,omitempty"`
}

// requiresPattern is a label a person reads in a message: no space, no control.
var requiresPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// SecretRef is one "${secret:<concept-id>#NAME}" reference: NAME is a
// secret_refs name of the concept, resolved at launch by `mcp exec`, never at
// apply time and never written to a client file.
type SecretRef struct {
	Concept string
	Name    string
}

// secretRefPattern spots every opening of a secret reference; each one must
// then parse in full (parseSecretRefs), so a typo is an error, not a literal.
var secretRefPattern = regexp.MustCompile(`\$\{secret:([^}]*)\}`)
var conceptIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// parseSecretRefs returns the references in value, or an error when a
// "${secret:" does not parse as <concept-id>#NAME.
func parseSecretRefs(value string) ([]SecretRef, error) {
	var refs []SecretRef
	for _, m := range secretRefPattern.FindAllStringSubmatch(value, -1) {
		concept, name, ok := strings.Cut(m[1], "#")
		valid := ok && conceptIDPattern.MatchString(concept) && envNamePattern.MatchString(name)
		if valid {
			for _, seg := range strings.Split(concept, "/") {
				if seg == "." || seg == ".." {
					valid = false
				}
			}
		}
		if !valid {
			return nil, fmt.Errorf("malformed secret reference (want ${secret:<concept-id>#NAME})")
		}
		refs = append(refs, SecretRef{Concept: concept, Name: name})
	}
	// An opening the pattern did not consume (no closing brace) is malformed too.
	if strings.Count(value, "${secret:") != len(refs) {
		return nil, fmt.Errorf("malformed secret reference (want ${secret:<concept-id>#NAME})")
	}
	return refs, nil
}

// SecretRefs returns the secret references in value, in order. A malformed
// one is not returned (ParseMCPServerSpec rejects it before this is reached).
func SecretRefs(value string) []SecretRef {
	refs, _ := parseSecretRefs(value)
	return refs
}

// HasSecretRef reports whether any env or headers value holds a secret
// reference: such a descriptor is started through the launcher.
func (s MCPServerSpec) HasSecretRef() bool {
	for _, m := range []map[string]string{s.Env, s.Headers} {
		for _, v := range m {
			if len(SecretRefs(v)) > 0 {
				return true
			}
		}
	}
	return false
}

// EnvRefNames returns the sorted names of the "${VAR}" references in the env
// and headers values: the variables a launcher reads from the environment its
// client gives it (Codex filters that environment, so they are passed on).
func (s MCPServerSpec) EnvRefNames() []string {
	seen := map[string]bool{}
	for _, m := range []map[string]string{s.Env, s.Headers} {
		for _, v := range m {
			for _, r := range envRefPattern.FindAllString(v, -1) {
				seen[r[2:len(r)-1]] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// envRefPattern spots a "${VAR_NAME}" reference inside a header/env value —
// the only allowed form (WP1): the client resolves it against its own
// environment at apply time (see configurator.EmitServer), so no secret ever
// lives in the KB file. A value with a literal prefix/suffix (e.g.
// "Bearer ${TOKEN}") is allowed — at least one "${...}" reference is enough; a
// value with no reference at all is rejected as a probable hardcoded secret.
var envRefPattern = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}`)
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// MCPEnvRefs lists the names of the "${VAR}" references in the env and headers
// values of spec, sorted and unique. Only those two maps are looked at, as the
// validator does; a "${secret:...}" form is not a name envRefPattern matches, so
// it is never listed: the launcher resolves it, the client's environment does not.
func MCPEnvRefs(spec MCPServerSpec) []string {
	seen := map[string]bool{}
	for _, m := range []map[string]string{spec.Env, spec.Headers} {
		for _, v := range m {
			for _, ref := range envRefPattern.FindAllString(v, -1) {
				seen[ref[2:len(ref)-1]] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

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
	// A reference is resolved by the launcher in env and headers only: in the
	// url or the args it would reach a client entry (or the server) as text.
	if strings.Contains(spec.URL, "${secret:") {
		return MCPServerSpec{}, fmt.Errorf("mcp %q: a ${secret:...} reference is allowed in env and headers only, not in url", name)
	}
	for _, a := range spec.Args {
		if strings.Contains(a, "${secret:") {
			return MCPServerSpec{}, fmt.Errorf("mcp %q: a ${secret:...} reference is allowed in env and headers only, not in args", name)
		}
	}
	if spec.Requires != "" {
		if spec.Type != "http" {
			return MCPServerSpec{}, fmt.Errorf("mcp %q: requires is for an http descriptor: a stdio one has no host to reach", name)
		}
		if !requiresPattern.MatchString(spec.Requires) {
			return MCPServerSpec{}, fmt.Errorf("mcp %q: requires must be a short label (letters, digits, . _ -)", name)
		}
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
	if err := validateEnvLiteral(name, spec); err != nil {
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
	forbidden := "|&;<>()$`*?\"\n\r"
	absolute := IsAbsCommandPath(command)
	if absolute {
		// Parentheses are allowed for an absolute command, and only there.
		// C:\Program Files (x86)\... is the most common absolute path on
		// Windows, so the ban rejected the platform's normal case; and it bought
		// nothing, because an absolute command is handed to exec.Command with a
		// separate argv and is never handed to a shell. It stays in force for a
		// bare name, where the string is short, has no reason to contain one,
		// and a resolver — not this package — decides what it means.
		forbidden = "|&;<>$`*?\"\n\r"
	}
	if strings.ContainsAny(command, forbidden) {
		return fmt.Errorf("command contains shell metacharacters")
	}
	if absolute {
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

// IsAbsCommandPath recognises an absolute path in either platform's spelling.
// A descriptor lives in the KB and is read by every client: a unix command
// (/usr/local/bin/tool) must stay a valid absolute path when the file is
// validated on Windows, and a Windows one (C:\tools\mcp.exe, \\host\share\x)
// when it is validated on unix. Whether the command exists *here* is the
// preflight's question, not this one's.
func IsAbsCommandPath(s string) bool {
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
//
// The separator swap works on a single backslash, deliberately: written `\\` in
// a Go raw string it matches only a *doubled* backslash, which never appears
// inside a path, so nothing was normalised and `C:\srv\..\srv.exe` came back
// unchanged — i.e. reported clean. The UNC prefix above is the one place a
// doubled backslash is genuinely meant.
func windowsCleanPath(s string) string {
	prefix := ""
	rest := s
	switch {
	case strings.HasPrefix(s, `\\`):
		prefix, rest = `\\`, s[2:]
	case len(s) >= 2 && s[1] == ':' && isDriveLetter(s[0]):
		prefix, rest = s[:2], s[2:]
	}
	cleaned := path.Clean(strings.ReplaceAll(rest, `\`, "/"))
	return prefix + strings.ReplaceAll(cleaned, "/", `\`)
}

// validateEnvRefs rejects, in values, every entry whose value does not
// reference at least one "${VAR}" — see envRefPattern.
func validateEnvRefs(serverName, field string, values map[string]string) error {
	for k, v := range values {
		if field == "env" && !envNamePattern.MatchString(k) {
			return fmt.Errorf("mcp %q: env key %q is not a portable variable name", serverName, k)
		}
		refs, err := parseSecretRefs(v)
		if err != nil {
			return fmt.Errorf("mcp %q: %s[%q]: %w", serverName, field, k, err)
		}
		if !envRefPattern.MatchString(v) && len(refs) == 0 {
			return fmt.Errorf(
				"mcp %q: %s[%q] does not reference an env var (\"${VAR}\"): literal value rejected for security",
				serverName, field, k)
		}
	}
	return nil
}

// validateEnvLiteral checks env_literal: portable names, disjoint from env, no
// "${" (a client would expand it), no NUL or newline, and nothing the secret
// scanner recognises. The error names the kind and the key, never the value.
func validateEnvLiteral(serverName string, spec MCPServerSpec) error {
	for k, v := range spec.EnvLiteral {
		if !envNamePattern.MatchString(k) {
			return fmt.Errorf("mcp %q: env_literal key %q is not a portable variable name", serverName, k)
		}
		if _, dup := spec.Env[k]; dup {
			return fmt.Errorf("mcp %q: %q is in both env and env_literal", serverName, k)
		}
		if strings.Contains(v, "${") || strings.ContainsAny(v, "\x00\n\r") {
			return fmt.Errorf("mcp %q: env_literal[%q] holds \"${\", a NUL or a newline", serverName, k)
		}
		if m := secretscan.Find(v); len(m) > 0 {
			return fmt.Errorf("mcp %q: env_literal[%q] looks like a secret (%s): use a ${secret:...} reference", serverName, k, m[0].Kind)
		}
	}
	return nil
}
