// mcpsettings.go (D69, WP3/WP4) — merges a third-party MCP server
// (mcp/<name>.json in a KB) into each provider's native config file, with
// per-name markers/ownership so multiple MCP servers (and Cartographer's own
// entry, written by `cartographer connect` via internal/configurator) coexist
// in the same file without stepping on each other.
//
// Mirror of hooksettings.go: claude/opencode/kiro are a JSON merge (via
// loadJSONObject/saveJSONObject, mcpServers/mcp key for the server name);
// codex is a per-name marker-delimited TOML block (blocktext), distinct from
// the un-named "cartographer:mcp:begin/end" block internal/configurator
// writes for the Cartographer server itself via `cartographer connect` — no
// collision, different text.
package provisioning

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BeppeTemp/cartographer/internal/blocktext"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// mcpServerKey is the top-level JSON key each provider uses to hold its map of
// MCP server entries — "mcp" for OpenCode, "mcpServers" for claude/kiro (see
// internal/configurator's own mcpServerKeys, duplicated here since it is
// unexported in that package).
func mcpServerKey(provider configurator.Provider) string {
	if provider == configurator.ProviderOpenCode {
		return "mcp"
	}
	return "mcpServers"
}

// mcpServerMarkers returns the begin/end comment markers that delimit name's
// own managed block in Codex's config.toml — distinct per server name, so
// registering/removing one KB-sourced MCP server never disturbs another's
// block or the un-named "cartographer:mcp:begin/end" block internal/configurator
// writes for Cartographer's own entry via `cartographer connect`.
func mcpServerMarkers(name string) (begin, end string) {
	return "# cartographer:mcp:" + name + ":begin", "# cartographer:mcp:" + name + ":end"
}

// registerMCPServer emits name/spec for provider (configurator.EmitServer) and
// merges it into provider's native config file at baseDir, returning the path
// (relative to baseDir) of the file touched — the same shared file
// `cartographer connect` itself writes Cartographer's own "cartographer" entry
// into (see destDir(kind:"mcp", ...)) — plus any non-fatal warnings from
// EmitServer (e.g. a header codex cannot represent, D69 WP3).
func registerMCPServer(baseDir, name string, spec configurator.ServerSpec, provider configurator.Provider) (relPath string, warnings []string, err error) {
	r, err := configurator.EmitServer(name, spec, provider)
	if err != nil {
		return "", nil, err
	}

	if filepath.Ext(r.FilePath) == ".toml" {
		begin, end := mcpServerMarkers(name)
		if err := blocktext.Write(filepath.Join(baseDir, r.FilePath), begin, end, string(r.Content)); err != nil {
			return "", nil, fmt.Errorf("provisioning: write mcp %s into %s: %w", name, r.FilePath, err)
		}
		return r.FilePath, r.Warnings, nil
	}

	settingsPath := filepath.Join(baseDir, r.FilePath)
	settings, err := loadJSONObject(settingsPath)
	if err != nil {
		return "", nil, err
	}
	var incoming map[string]interface{}
	if err := json.Unmarshal(r.Content, &incoming); err != nil {
		return "", nil, fmt.Errorf("provisioning: decode mcp emission %s: %w", name, err)
	}

	key := mcpServerKey(provider)
	servers, _ := settings[key].(map[string]interface{})
	if servers == nil {
		servers = map[string]interface{}{}
	}
	if incomingServers, ok := incoming[key].(map[string]interface{}); ok {
		servers[name] = incomingServers[name]
	}
	settings[key] = servers
	// OpenCode's "$schema" hint (see configurator's emitOpenCodeServer): keep
	// whatever is already there, only add it if this is a brand-new file.
	if schema, ok := incoming["$schema"]; ok {
		if _, has := settings["$schema"]; !has {
			settings["$schema"] = schema
		}
	}

	if err := saveJSONObject(settingsPath, settings); err != nil {
		return "", nil, err
	}
	return r.FilePath, r.Warnings, nil
}

// removeMCPServer is the inverse of registerMCPServer, called from
// PruneManaged's prune path: strips name's entry (JSON providers) or
// marker-delimited block (codex) from provider's native config file. No-op if
// the file, key, or block is absent.
//
// JSON providers: mirrors configurator.Remove's own D63 file-level cleanup —
// kiro/opencode's config files exist purely to hold MCP server entries, so if
// stripping name's entry leaves nothing behind but an (already emptied)
// server-map key (and, for opencode, the "$schema" hint), the file itself is
// removed instead of left as an empty shell; claude.json is never deleted
// (Claude Code's own shared state file, D63's absolute rule), only reduced.
func removeMCPServer(baseDir, name string, provider configurator.Provider) error {
	filePath := destDir("mcp", "", provider)
	if filePath == "" {
		return nil
	}
	fullPath := filepath.Join(baseDir, filePath)

	if filepath.Ext(fullPath) == ".toml" {
		begin, end := mcpServerMarkers(name)
		_, err := blocktext.Remove(fullPath, begin, end, false)
		return err
	}

	data, err := os.ReadFile(fullPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("provisioning: read %s: %w", fullPath, err)
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("provisioning: parse %s: %w", fullPath, err)
	}

	key := mcpServerKey(provider)
	servers, ok := settings[key].(map[string]interface{})
	if !ok {
		return nil
	}
	if _, ok := servers[name]; !ok {
		return nil
	}
	delete(servers, name)
	if len(servers) == 0 {
		delete(settings, key)
	} else {
		settings[key] = servers
	}

	if provider != configurator.ProviderClaudeCode && isEmptyMCPProviderShell(settings) {
		if err := os.Remove(fullPath); err != nil {
			return fmt.Errorf("provisioning: remove %s: %w", fullPath, err)
		}
		pruneEmptyDirs(baseDir, filePath)
		return nil
	}
	return saveJSONObject(fullPath, settings)
}

// isEmptyMCPProviderShell reports whether settings has nothing left worth
// keeping — no keys at all, or, for OpenCode, only its "$schema" hint — mirrors
// configurator's own (unexported) isEmptyProviderShell, duplicated here for the
// same reason mcpServerKey is.
func isEmptyMCPProviderShell(settings map[string]interface{}) bool {
	for k := range settings {
		if k == "$schema" {
			continue
		}
		return false
	}
	return true
}

// mcpProviderFromPath infers which provider a "mcp" kind ManagedFile was
// materialized for from its Path (destDir gives each provider its own shared
// config file — ".claude.json", ".codex/config.toml", "opencode.json",
// ".kiro/settings/mcp.json"), so PruneManaged can strip the matching
// provider-native registration without needing its own provider parameter
// (same pattern as hookProviderFromPath).
func mcpProviderFromPath(path string) configurator.Provider {
	slash := filepath.ToSlash(path)
	switch slash {
	case ".claude.json":
		return configurator.ProviderClaudeCode
	case filepath.ToSlash(filepath.Join(".codex", "config.toml")):
		return configurator.ProviderCodex
	case "opencode.json":
		return configurator.ProviderOpenCode
	case filepath.ToSlash(filepath.Join(".kiro", "settings", "mcp.json")):
		return configurator.ProviderKiro
	default:
		return ""
	}
}
