// Package configurator generates MCP configuration files for multiple LLM providers
// (Claude Code, Codex CLI, Kiro, OpenCode).
package configurator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/blocktext"
)

// ServerConfig holds the MCP server configuration. The client always talks to the
// server over HTTP (see decisions.md): there is no stdio transport option.
type ServerConfig struct {
	Name        string
	URL         string
	AuthEnabled bool
	TokenEnv    string
	KBNames     []string
}

// DefaultConfig returns a ServerConfig with reasonable defaults.
func DefaultConfig() *ServerConfig {
	return &ServerConfig{
		Name:        "cartographer",
		URL:         "http://localhost:8080/mcp",
		AuthEnabled: false,
		TokenEnv:    "CARTOGRAPHER_TOKENS",
		KBNames:     []string{},
	}
}

// ServerSpec is the provider-neutral description of a single MCP server's HTTP
// transport config (D69): the shared core EmitServer renders for any given
// provider, used both for Cartographer's own entry (Emit, via ServerConfig.toSpec
// below) and for third-party servers distributed by a KB (internal/provisioning
// "mcp" artifact kind — see internal/provisioning/mcpspec.go and mcpsettings.go).
//
// Header values are expected to reference the client's own environment via a
// "${VAR}" placeholder (translated to each provider's native syntax by the
// per-provider emitters below) — never a literal secret: enforced upstream by
// internal/provisioning's KB-side validator for third-party servers, and always
// true by construction for Cartographer's own entry (toSpec below).
type ServerSpec struct {
	Type    string // "http" (only transport in this iteration, D69)
	URL     string
	Headers map[string]string
}

// toSpec converts cfg into the provider-neutral ServerSpec EmitServer consumes.
func (cfg *ServerConfig) toSpec() ServerSpec {
	spec := ServerSpec{Type: "http", URL: cfg.URL}
	if cfg.AuthEnabled {
		spec.Headers = map[string]string{
			"Authorization": "Bearer ${" + cfg.TokenEnv + "}",
		}
	}
	return spec
}

// Provider identifies the LLM provider.
type Provider string

const (
	ProviderClaudeCode Provider = "claude"
	ProviderCodex      Provider = "codex"
	ProviderKiro       Provider = "kiro"
	ProviderOpenCode   Provider = "opencode"
)

// EmitResult contains the generated config for a provider.
type EmitResult struct {
	Provider Provider
	FilePath string // path relative to baseDir where the file should be written
	// Content is the file content to write for JSON-format providers (claude,
	// kiro, opencode). For TOML-format providers (codex, FilePath ends in
	// ".toml") it is instead just the body of the Cartographer-managed block —
	// Apply/Remove wrap it with codexMCPBlockBegin/End and merge it into the
	// file via blocktext, never touching the rest of a hand-curated
	// config.toml (see D58).
	Content []byte
	// Warnings holds non-fatal messages about parts of the spec that this
	// provider cannot represent natively (D69 — e.g. Codex's config.toml only
	// exposes bearer_token_env_var for auth, not arbitrary headers) and so were
	// dropped rather than silently guessed. Nil unless populated; empty for
	// every one of Cartographer's own EmitAll calls today, since its own
	// header (Authorization: Bearer ${TokenEnv}) is always representable.
	// Surfaced by callers (internal/provisioning's "mcp" Apply path) instead of
	// failing.
	Warnings []string
}

// Emit generates the configuration for Cartographer's own MCP entry (cfg) for
// the specified provider — a thin wrapper over EmitServer (see ServerSpec/
// toSpec above), kept so `cartographer connect` and configurator's own
// Apply/Remove don't need to know about ServerSpec at all.
func Emit(cfg *ServerConfig, provider Provider) (*EmitResult, error) {
	return EmitServer(cfg.Name, cfg.toSpec(), provider)
}

// EmitServer generates the config file contents that materialize an MCP server
// named name with spec, for provider — the provider-neutral core shared by
// Emit (Cartographer's own entry) and internal/provisioning's "mcp" artifact
// kind (D69, third-party servers distributed by a KB — see
// internal/provisioning/mcpsettings.go, registerMCPServer/removeMCPServer).
func EmitServer(name string, spec ServerSpec, provider Provider) (*EmitResult, error) {
	switch provider {
	case ProviderClaudeCode:
		return emitClaudeCodeServer(name, spec)
	case ProviderCodex:
		return emitCodexServer(name, spec)
	case ProviderKiro:
		return emitKiroServer(name, spec)
	case ProviderOpenCode:
		return emitOpenCodeServer(name, spec)
	default:
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}
}

// EmitAll generates the configuration for all providers.
func EmitAll(cfg *ServerConfig) ([]*EmitResult, error) {
	providers := []Provider{ProviderClaudeCode, ProviderCodex, ProviderKiro, ProviderOpenCode}
	results := make([]*EmitResult, 0, len(providers))
	for _, p := range providers {
		r, err := Emit(cfg, p)
		if err != nil {
			return nil, fmt.Errorf("emit %s: %w", p, err)
		}
		results = append(results, r)
	}
	return results, nil
}

// Apply writes the emitted files into baseDir and returns the (baseDir-relative)
// paths written — or, if dryRun=true, the paths that would be written, without
// writing anything. Apply does not print anything: callers that want CLI-style
// "wrote <path>" output (cmdConnect) render it themselves from the returned
// paths, so callers that must not touch stdout (the TUI dashboard) can ignore it.
// Creates required directories. If the target file already exists, Apply merges
// the new entry into it non-destructively.
func Apply(results []*EmitResult, baseDir string, dryRun bool) ([]string, error) {
	written := make([]string, 0, len(results))
	for _, r := range results {
		fullPath := filepath.Join(baseDir, r.FilePath)
		if dryRun {
			written = append(written, r.FilePath)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return written, fmt.Errorf("mkdir %s: %w", filepath.Dir(fullPath), err)
		}

		// TOML providers (codex): merge via the marker-delimited managed block
		// instead of parsing/re-serializing the user's hand-curated config.toml
		// (D58) — see EmitResult.Content's doc comment.
		if filepath.Ext(fullPath) == ".toml" {
			if err := blocktext.Write(fullPath, codexMCPBlockBegin, codexMCPBlockEnd, string(r.Content)); err != nil {
				return written, fmt.Errorf("write %s: %w", fullPath, err)
			}
			written = append(written, r.FilePath)
			continue
		}

		content := r.Content
		// Non-destructive merge for JSON files: merge our entry into existing content.
		// Non-JSON files (e.g. SKILL.md) are always overwritten.
		if filepath.Ext(fullPath) == ".json" {
			if existing, err := os.ReadFile(fullPath); err == nil {
				merged, mergeErr := mergeJSON(existing, r.Content)
				if mergeErr != nil {
					return written, fmt.Errorf("existing file %s is not valid JSON: %w — fix or delete it manually", fullPath, mergeErr)
				}
				content = merged
			}
		}

		if err := os.WriteFile(fullPath, content, 0o644); err != nil {
			return written, fmt.Errorf("write %s: %w", fullPath, err)
		}
		written = append(written, r.FilePath)
	}
	return written, nil
}

// mcpServerKeys are the top-level JSON keys used by provider config files to hold
// the map of MCP server entries: "mcpServers" (claude/codex/kiro) or "mcp"
// (opencode). Kept as a lookup list (checked both ways) rather than a
// provider-specific switch so Remove doesn't need to special-case OpenCode.
var mcpServerKeys = []string{"mcpServers", "mcp"}

// Remove deletes the MCP server entry named cfg.Name from provider's config file
// in baseDir — the inverse of Apply's non-destructive merge: it reads the
// existing JSON, deletes only that key from the servers map, and rewrites the
// file. Everything else in the file (other servers, other top-level keys) is
// left untouched; if the servers map becomes empty it is kept as an empty map,
// not deleted, mirroring Apply's rule of never destroying user content.
//
// If the file does not exist, or the key is not present, Remove is a no-op and
// returns removed=false with no error. If dryRun is true, Remove computes and
// returns whether the key would be removed but does not write anything.
func Remove(cfg *ServerConfig, provider Provider, baseDir string, dryRun bool) (removed bool, err error) {
	r, err := Emit(cfg, provider)
	if err != nil {
		return false, err
	}
	fullPath := filepath.Join(baseDir, r.FilePath)

	if filepath.Ext(fullPath) == ".toml" {
		removed, err := blocktext.Remove(fullPath, codexMCPBlockBegin, codexMCPBlockEnd, dryRun)
		if err != nil {
			return false, fmt.Errorf("remove block %s: %w", fullPath, err)
		}
		if provider == ProviderCodex {
			// Best-effort migration cleanup: earlier Cartographer versions wrote
			// a legacy .codex/config.json that Codex CLI never actually reads
			// (D58) — if it's still on disk from a stale connect, strip the
			// entry (or the file, if that empties it) too.
			legacyRemoved, err := removeLegacyCodexJSON(cfg.Name, baseDir, dryRun)
			if err != nil {
				return false, err
			}
			removed = removed || legacyRemoved
		}
		return removed, nil
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", fullPath, err)
	}

	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return false, fmt.Errorf("existing file %s is not valid JSON: %w — fix or delete it manually", fullPath, err)
	}

	found := false
	for _, key := range mcpServerKeys {
		servers, ok := root[key].(map[string]any)
		if !ok {
			continue
		}
		if _, ok := servers[cfg.Name]; !ok {
			continue
		}
		found = true
		if !dryRun {
			delete(servers, cfg.Name)
			// Drop the (now empty) server-map key entirely rather than leave
			// an empty "mcpServers": {}/"mcp": {} shell — mirrors Apply's own
			// non-destructive-merge spirit in reverse: for claude.json this is
			// as far as file-level cleanup ever goes (see the ProviderClaudeCode
			// guard below); for kiro/opencode it also feeds isEmptyProviderShell.
			if len(servers) == 0 {
				delete(root, key)
			} else {
				root[key] = servers
			}
		}
	}
	if !found || dryRun {
		return found, nil
	}

	// File-level cleanup (D63): .claude.json is Claude Code's own shared state
	// file (MCP servers, but also model/permissions/other agent state) and must
	// NEVER be deleted — only the (now possibly absent) mcpServers key was
	// touched above, everything else in the file is left alone. kiro/opencode's
	// config files exist purely to hold MCP server entries: if removing ours
	// leaves nothing behind but that (now-deleted) key and, for opencode, the
	// "$schema" hint, there is nothing worth keeping — delete the file outright
	// instead of leaving an empty shell nobody reads.
	if provider != ProviderClaudeCode && isEmptyProviderShell(root) {
		if err := os.Remove(fullPath); err != nil {
			return false, fmt.Errorf("remove %s: %w", fullPath, err)
		}
		// Best-effort (D63): also drop the file's own parent directory if that
		// leaves it empty too — kiro's mcp.json lives in ".kiro/settings/", a
		// level provisioning creates solely to hold it. Never climb as far as
		// baseDir itself (opencode.json has no such parent to begin with —
		// filepath.Dir(fullPath) == baseDir there, guarded below) or above
		// ".kiro" (a provisioning root boundary that may still hold skills/
		// agents/hooks; os.Remove's own "not empty" error is the natural guard
		// even without this check, but the guard keeps intent explicit).
		if parent := filepath.Dir(fullPath); parent != baseDir {
			_ = os.Remove(parent)
		}
		return true, nil
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(fullPath, out, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", fullPath, err)
	}
	return true, nil
}

// isEmptyProviderShell reports whether root — a provider config file's top-level
// JSON object, after Remove has already deleted its (now-empty) server-map key
// (see the loop above) — has nothing left worth keeping: no keys at all, or, for
// OpenCode, only its "$schema" hint (always present, see emitOpenCode). Used only
// for kiro/opencode (never claude.json, see the ProviderClaudeCode guard at the
// call site) to decide whether Remove should delete the whole file instead of
// rewriting a near-empty one.
func isEmptyProviderShell(root map[string]any) bool {
	for k := range root {
		if k == "$schema" {
			continue
		}
		return false
	}
	return true
}

// removeLegacyCodexJSON best-effort removes the "cartographer" mcpServers entry
// (or the whole file, if that's all it contains) from <baseDir>/.codex/config.json
// — the file emitCodex wrote before D58 discovered Codex CLI only ever reads
// config.toml. Codex itself never consumed config.json, so this is pure
// migration cleanup of a leftover from a stale connect, not a live config.
// No-op if the file is absent, malformed, or has no matching entry.
func removeLegacyCodexJSON(name, baseDir string, dryRun bool) (bool, error) {
	legacyPath := filepath.Join(baseDir, ".codex", "config.json")
	data, err := os.ReadFile(legacyPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", legacyPath, err)
	}

	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		// Malformed legacy file: not ours to fix — Codex never read it anyway.
		return false, nil
	}
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		return false, nil
	}
	if _, ok := servers[name]; !ok {
		return false, nil
	}
	if dryRun {
		return true, nil
	}

	delete(servers, name)
	if len(servers) == 0 && len(root) == 1 {
		// The legacy file held nothing but an (now empty) mcpServers map:
		// remove it outright rather than leave an empty shell nobody reads.
		return true, os.Remove(legacyPath)
	}
	root["mcpServers"] = servers
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(legacyPath, out, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// mergeJSON deep-merges incoming JSON into existing JSON at the top map level.
// Returns an error if existing is malformed JSON (user must fix or delete the file).
// If incoming cannot be parsed, incoming bytes are returned as-is.
func mergeJSON(existing, incoming []byte) ([]byte, error) {
	var existMap map[string]any
	if err := json.Unmarshal(existing, &existMap); err != nil {
		return nil, err
	}
	var incomMap map[string]any
	if err := json.Unmarshal(incoming, &incomMap); err != nil {
		return incoming, nil
	}
	jsonDeepMerge(existMap, incomMap)
	return json.MarshalIndent(existMap, "", "  ")
}

// jsonDeepMerge merges src into dst. For nested maps, recurses.
// For string arrays, deduplicates by appending elements not already present.
// For all other types, src wins.
func jsonDeepMerge(dst, src map[string]any) {
	for k, sv := range src {
		if dv, ok := dst[k]; ok {
			if dm, isDstMap := dv.(map[string]any); isDstMap {
				if sm, isSrcMap := sv.(map[string]any); isSrcMap {
					jsonDeepMerge(dm, sm)
					continue
				}
			}
			// Array merge: concatenate, deduplicating string elements.
			if newArr, ok := sv.([]interface{}); ok {
				if existingArr, ok := dv.([]interface{}); ok {
					seen := make(map[string]bool)
					for _, v := range existingArr {
						if s, ok := v.(string); ok {
							seen[s] = true
						}
					}
					merged := existingArr
					for _, v := range newArr {
						if s, ok := v.(string); ok && !seen[s] {
							merged = append(merged, v)
							seen[s] = true
						}
					}
					dst[k] = merged
					continue
				}
			}
		}
		dst[k] = sv
	}
}

// --- Provider adapters ---

// emitClaudeCodeServer generates the .claude.json entry for name/spec (Claude
// Code format). Header values are passed through verbatim: Claude Code natively
// resolves "${VAR}" against its own environment.
func emitClaudeCodeServer(name string, spec ServerSpec) (*EmitResult, error) {
	entry := map[string]any{
		"url":  spec.URL,
		"type": "http",
	}
	if len(spec.Headers) > 0 {
		entry["headers"] = spec.Headers
	}

	root := map[string]any{
		"mcpServers": map[string]any{
			name: entry,
		},
	}
	content, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return &EmitResult{
		Provider: ProviderClaudeCode,
		FilePath: ".claude.json",
		Content:  content,
	}, nil
}

// codexMCPBlockBegin/End delimit the Cartographer-managed block inside Codex's
// config.toml that holds the [mcp_servers.<name>] entry (D58). config.toml is
// hand-curated (comments, ordering, unrelated sections) so it is never
// parsed/re-serialized as TOML — only this marker-delimited slice is ever
// touched, via internal/blocktext. A hook's own registration (see
// internal/provisioning/hooksettings.go, registerHookConfigTOML) lives in the
// same file under its own "cartographer:hook:<name>:*" markers — distinct
// text, no collision.
const (
	codexMCPBlockBegin = "# cartographer:mcp:begin — block managed by Cartographer, do not edit by hand"
	codexMCPBlockEnd   = "# cartographer:mcp:end"
)

// bearerEnvPattern matches a header value of the exact form "Bearer ${VAR}",
// the only shape emitCodexServer can translate to Codex's bearer_token_env_var
// (see below) — capturing VAR.
var bearerEnvPattern = regexp.MustCompile(`^Bearer \$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// emitCodexServer generates the Cartographer-managed block for name/spec in
// Codex CLI's ~/.codex/config.toml (D58 — see
// https://developers.openai.com/codex/config-reference for the
// [mcp_servers.<id>] schema: `url` marks it as a remote/HTTP server,
// `bearer_token_env_var` names the env var holding the bearer token). Content
// is only the block body (see EmitResult's doc comment) — Apply/registration
// wraps it with a begin/end marker and merges it via blocktext.
//
// Codex's schema exposes exactly one auth mechanism for a remote MCP server:
// bearer_token_env_var. So only a header literally named "Authorization" with
// value "Bearer ${VAR}" is representable (D69); it is translated to
// bearer_token_env_var = "VAR". Any other header (or an Authorization value in
// a different shape) cannot be expressed here and is dropped, surfaced via
// EmitResult.Warnings instead of silently guessed or failing.
//
// Before D58, this wrote .codex/config.json with a "mcpServers" key — a format
// Codex CLI never actually reads (it only reads config.toml); see
// docs/decisions.md D58.
func emitCodexServer(name string, spec ServerSpec) (*EmitResult, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[mcp_servers.%s]\n", name)
	fmt.Fprintf(&sb, "url = %s\n", QuoteTOMLString(spec.URL))

	var warnings []string
	if auth, ok := spec.Headers["Authorization"]; ok {
		if m := bearerEnvPattern.FindStringSubmatch(auth); m != nil {
			fmt.Fprintf(&sb, "bearer_token_env_var = %s\n", QuoteTOMLString(m[1]))
		} else {
			warnings = append(warnings, fmt.Sprintf(
				"mcp %q: Authorization header not in the form \"Bearer ${VAR}\", ignored for codex", name))
		}
	}
	for k := range spec.Headers {
		if k != "Authorization" {
			warnings = append(warnings, fmt.Sprintf(
				"mcp %q: header %q not supported by codex (only Authorization Bearer ${VAR}), ignored", name, k))
		}
	}

	return &EmitResult{
		Provider: ProviderCodex,
		FilePath: filepath.Join(".codex", "config.toml"),
		Content:  []byte(sb.String()),
		Warnings: warnings,
	}, nil
}

// QuoteTOMLString returns s as a TOML basic string (double-quoted, escaping
// backslashes, double quotes and the common control characters) — safe to
// splice into a single-line `key = "value"` entry. Exported so
// internal/provisioning can reuse it when generating Codex agent TOML files
// (D58), keeping TOML string-escaping in one place.
func QuoteTOMLString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			sb.WriteString(`\\`)
		case '"':
			sb.WriteString(`\"`)
		case '\n':
			sb.WriteString(`\n`)
		case '\t':
			sb.WriteString(`\t`)
		case '\r':
			sb.WriteString(`\r`)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// QuoteTOMLMultiline returns s as a TOML multi-line basic string
// ("""..."""), escaping backslashes and any run of three consecutive double
// quotes (the only sequence that would otherwise prematurely close the
// string) — used for Codex agent TOML's `developer_instructions` field (D58),
// which can hold an arbitrary Markdown body. A newline immediately follows
// the opening delimiter, which the TOML spec trims, so the body itself is
// reproduced unindented.
func QuoteTOMLMultiline(s string) string {
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"""`, `\"\"\"`)
	return "\"\"\"\n" + escaped + "\n\"\"\""
}

// emitKiroServer generates the .kiro/settings/mcp.json entry for name/spec
// (Kiro format). Kiro's schema has no known auth-header field: spec.Headers is
// intentionally not represented here (pre-existing gap, unrelated to D69 — see
// docs/decisions.md D69's note on internal/provisioning surfacing a Warning for
// KB-sourced servers that need it, since Cartographer's own entry never has
// relied on Kiro auth headers either).
func emitKiroServer(name string, spec ServerSpec) (*EmitResult, error) {
	entry := map[string]any{
		"autoApprove": []string{},
		"url":         spec.URL,
		"type":        "http",
	}

	root := map[string]any{
		"mcpServers": map[string]any{
			name: entry,
		},
	}
	content, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return &EmitResult{
		Provider: ProviderKiro,
		FilePath: ".kiro/settings/mcp.json",
		Content:  content,
	}, nil
}

// openCodeEnvRefPattern matches a "${VAR_NAME}" placeholder (the provider-
// neutral syntax used everywhere else — see ServerSpec's doc comment and
// internal/provisioning/mcpspec.go), so it can be translated to OpenCode's own
// "{env:VAR_NAME}" syntax (see emitOpenCodeServer below).
var openCodeEnvRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// emitOpenCodeServer generates the opencode.json entry for name/spec (OpenCode
// v1.17.10 format, schema: https://opencode.ai/config.json).
//
// Note: OpenCode uses the {env:VAR} syntax for environment variables (unlike the
// ${VAR} of the other providers) — every "${VAR}" reference in spec.Headers
// values is translated here.
// Known risk: OpenCode is SSE-first and custom-header support on remote MCP may
// require mcp-remote/mcp-auth.json; see docs/interoperability.md §Known
// configurator risks.
func emitOpenCodeServer(name string, spec ServerSpec) (*EmitResult, error) {
	entry := map[string]any{
		"type":    "remote",
		"url":     spec.URL,
		"enabled": true,
	}
	if len(spec.Headers) > 0 {
		headers := make(map[string]string, len(spec.Headers))
		for k, v := range spec.Headers {
			headers[k] = openCodeEnvRefPattern.ReplaceAllString(v, "{env:$1}")
		}
		entry["headers"] = headers
	}

	root := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"mcp": map[string]any{
			name: entry,
		},
	}
	content, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return &EmitResult{
		Provider: ProviderOpenCode,
		FilePath: "opencode.json",
		Content:  content,
	}, nil
}
