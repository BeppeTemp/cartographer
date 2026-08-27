package provisioning

// Read-only inspectors for `cartographer doctor` (D143). Every function here
// answers a question about what is on disk and writes NOTHING — no lockfile
// migration, no cache refresh, no directory creation. They live in this package
// because the answers depend on marker spellings, destination paths and
// ownership rules that belong to provisioning, not to a command.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// LockFileFormat is the on-disk format of a lockfile: v2 multi-provider, the
// v1 single-provider format ReadLockFile migrates in memory (the file itself
// stays v1 until something rewrites it), or absent.
type LockFileFormat int

const (
	LockFileAbsent LockFileFormat = iota
	LockFileV1
	LockFileV2
)

// InspectLockFile reports the format of the lockfile at path without rewriting
// it. An unreadable or malformed file is an error, never a silent v1.
func InspectLockFile(path string) (LockFileFormat, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return LockFileAbsent, nil
	}
	if err != nil {
		return LockFileAbsent, err
	}
	var probe struct {
		Providers map[string]json.RawMessage `json:"providers"`
		Provider  string                     `json:"provider"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return LockFileAbsent, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	if probe.Providers != nil {
		return LockFileV2, nil
	}
	return LockFileV1, nil
}

// InstructionsFile returns the provider's instructions file, relative to its
// base dir, or "" for a provider with no instructions destination.
func InstructionsFile(provider configurator.Provider) string {
	return destDir("instructions", "", provider)
}

// InstructionsBlockMarkers counts the managed instructions block's begin and
// end markers in the file at path. The begin marker is recognized by PREFIX,
// like everywhere else: a block written by an older version carries different
// display text after the em dash and must still be counted, or the diagnosis
// would report a missing block that is right there.
//
// A file that does not exist is (0, 0, nil): absence is the caller's finding to
// make, not an error here.
func InstructionsBlockMarkers(path string) (begins, ends int, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	content := string(data)
	return strings.Count(content, instructionsBlockBeginPrefix), strings.Count(content, instructionsBlockEnd), nil
}

// codexMCPTableHeader matches an [mcp_servers.<name>] table header in Codex's
// config.toml, capturing the server name.
var codexMCPTableHeader = regexp.MustCompile(`(?m)^\s*\[mcp_servers\.([^\[\]]+)\]\s*$`)

// MCPServerEntryNames lists every MCP server declared in provider's native
// config file under baseDir, sorted. The caller decides which of them
// Cartographer owns: enumerating is the only way to see an entry for a KB the
// server no longer mounts, since such a KB has already dropped out of the
// client's own recorded list.
//
// For a TOML provider it reads the table headers rather than the block markers:
// `connect` writes its entries inside one shared managed block, and Codex's own
// rewrites drop every comment — markers included — while keeping the tables
// (D99), so the table is the durable identity.
func MCPServerEntryNames(baseDir string, provider configurator.Provider) ([]string, error) {
	d, ok := configurator.Lookup(provider)
	if !ok || !d.ManagesMCPConfig() {
		return nil, nil
	}
	full := filepath.Join(baseDir, d.ConfigPath())

	if d.MCPFormat == configurator.FormatTOMLBlock {
		data, err := os.ReadFile(full)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var names []string
		for _, m := range codexMCPTableHeader.FindAllStringSubmatch(string(data), -1) {
			names = append(names, strings.Trim(strings.TrimSpace(m[1]), `"`))
		}
		sort.Strings(names)
		return names, nil
	}

	settings, err := loadJSONObject(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("parse %s: %w", d.ConfigPath(), err)
	}
	servers, ok := settings[d.MCPServerKey].(map[string]interface{})
	if !ok {
		return nil, nil
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// HookRegistrations counts the provider-native registrations of hookName under
// baseDir: managed is how many Cartographer owns and expects (exactly one when
// the hook is registered), stray is how many live outside the mechanism's
// managed span — the D99 double-fire, where Codex's own rewrite of config.toml
// left a marker-less copy that fires alongside the block's.
//
// Only the two providers with a native registration file are inspected;
// OpenCode registers through a generated plugin, which is a managed file and is
// therefore already covered by on-disk verification (D139).
func HookRegistrations(baseDir string, provider configurator.Provider, hookName string) (managed, stray int, err error) {
	switch provider {
	case configurator.ProviderClaudeCode:
		settings, err := loadJSONObject(claudeSettingsPath(baseDir))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("parse %s: %w", HookRegistrationFile(provider), err)
		}
		// One flat JSON array per event: there is no "outside", so a second
		// entry is a duplicate, not a stray.
		return countClaudeHookEntries(settings, hookOwnershipMarker(hookName)), 0, nil
	case configurator.ProviderCodex:
		path := codexConfigTOMLPath(baseDir)
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return 0, 0, nil
		}
		if err != nil {
			return 0, 0, err
		}
		begin, _ := codexHookMarkers(hookName)
		if strings.Contains(string(data), begin) {
			managed = 1
		}
		// Only the path-fragment identity is used: the command a registration
		// for this hook would carry lives in the materialized hook.json and is
		// not known here, so an inline one-liner hook (D127) is not diagnosable
		// from outside — reporting it on a guess would be worse than not
		// reporting it. [hooks.state."…"] is Codex's own bookkeeping, never a
		// registration (D99).
		marker := codexHookOwnershipMarker(hookName)
		orphans, err := configurator.CodexOrphanTables(path, func(key []string, body string) bool {
			if len(key) < 2 || key[0] != "hooks" || key[1] == "state" {
				return false
			}
			return strings.Contains(body, marker)
		})
		if err != nil {
			return managed, 0, err
		}
		return managed, len(orphans), nil
	}
	return 0, 0, nil
}

// countClaudeHookEntries counts the entries in settings["hooks"] whose command
// carries marker — the same ownership rule stripHookEntries applies, read-only.
func countClaudeHookEntries(settings map[string]interface{}, marker string) int {
	hooksMap, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		return 0
	}
	count := 0
	for _, groupsRaw := range hooksMap {
		groups, ok := groupsRaw.([]interface{})
		if !ok {
			continue
		}
		for _, groupRaw := range groups {
			group, ok := groupRaw.(map[string]interface{})
			if !ok {
				continue
			}
			entries, ok := group["hooks"].([]interface{})
			if !ok {
				continue
			}
			for _, entryRaw := range entries {
				entry, ok := entryRaw.(map[string]interface{})
				if !ok {
					continue
				}
				if command, ok := entry["command"].(string); ok && strings.Contains(command, marker) {
					count++
				}
			}
		}
	}
	return count
}
