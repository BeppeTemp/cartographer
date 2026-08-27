package configurator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// This file is the single description of every supported provider (D137):
// identity, the native MCP config file and its format, and the evidence that
// says the agent is installed on this machine. Adding a provider means adding
// one descriptor here plus whatever genuinely differs (its emitter, and its
// cells in internal/provisioning's kind × provider matrix) — not editing the
// same four constants across eight files.
//
// This package owns provider *identity and config-file shape* only.
// Destination paths for materialized artifacts belong to
// internal/provisioning, which imports this package and never the reverse.

// ConfigFormat is how a provider's MCP config file is written.
type ConfigFormat int

const (
	// FormatJSON: the whole file is a JSON object, merged key by key.
	FormatJSON ConfigFormat = iota
	// FormatTOMLBlock: a hand-curated TOML file into which Cartographer
	// merges only a marker-delimited managed block (D58).
	FormatTOMLBlock
)

// Descriptor describes one supported provider.
type Descriptor struct {
	Provider    Provider
	DisplayName string // human-readable, e.g. "Claude Code"

	// MCPConfigPath is the provider's native MCP configuration file, relative
	// to the client base dir. It is the file `cartographer connect` writes
	// Cartographer's own entry into, and the same file KB-provided "mcp"
	// artifacts are merged into (internal/provisioning/mcpsettings.go).
	MCPConfigPath string
	MCPFormat     ConfigFormat
	// MCPServerKey is the top-level JSON key holding the map of MCP server
	// entries. Empty for FormatTOMLBlock providers.
	MCPServerKey string
	// DeletableWhenEmpty says whether the config file may be deleted once
	// removing Cartographer's entries leaves nothing worth keeping (D63).
	// False for Claude Code: .claude.json is Claude's own shared state file
	// (model, permissions, agent state) and is never deleted, only reduced.
	DeletableWhenEmpty bool

	// SupportsMCPHeaders says whether the provider can carry per-server HTTP
	// headers (auth) in its MCP config. False for kiro, whose emitter has
	// never represented them (D69): a KB-provided server with headers is
	// materialized with a warning rather than silently unauthenticated.
	SupportsMCPHeaders bool

	// FlatToolNamespace marks a client whose MCP tool namespace is flat
	// across servers, so two KBs mounted without a tool_prefix collide and
	// only one stays reachable (D102/D144).
	FlatToolNamespace bool

	// BaseDirEnv, when set, names the environment variable holding this
	// provider's own root directory: artifacts are materialized there
	// instead of under the shared client base dir (D141 — Hermes' root is
	// wherever it was deployed, typically /opt/data, not the user's home).
	// Empty means the shared base dir, which is every other provider.
	BaseDirEnv string

	// Binary is the executable name looked up in PATH by internal/agents.
	Binary string
	// ConfigDirs are directories relative to the user's home, probed in this
	// order when the binary is absent.
	ConfigDirs [][]string
	// DarwinAppDir, when set, is an absolute application bundle path probed
	// last on darwin only.
	DarwinAppDir string

	// emit renders one MCP server entry for this provider. The four output
	// formats genuinely differ, so this stays a function, not data.
	emit func(name string, spec ServerSpec) (*EmitResult, error)
}

// descriptors is the registry, in the order EmitAll and the client
// subcommands iterate providers. That order is user-visible; keep it stable.
var descriptors = []Descriptor{
	{
		Provider:           ProviderClaudeCode,
		DisplayName:        "Claude Code",
		MCPConfigPath:      ".claude.json",
		MCPFormat:          FormatJSON,
		MCPServerKey:       "mcpServers",
		DeletableWhenEmpty: false,
		SupportsMCPHeaders: true,
		Binary:             "claude",
		ConfigDirs:         [][]string{{".claude"}},
		emit:               emitClaudeCodeServer,
	},
	{
		Provider:           ProviderCodex,
		DisplayName:        "Codex CLI",
		MCPConfigPath:      ".codex/config.toml",
		MCPFormat:          FormatTOMLBlock,
		DeletableWhenEmpty: true,
		SupportsMCPHeaders: true,
		Binary:             "codex",
		ConfigDirs:         [][]string{{".codex"}},
		emit:               emitCodexServer,
	},
	{
		Provider:           ProviderKiro,
		DisplayName:        "Kiro",
		MCPConfigPath:      ".kiro/settings/mcp.json",
		MCPFormat:          FormatJSON,
		MCPServerKey:       "mcpServers",
		DeletableWhenEmpty: true,
		FlatToolNamespace:  true,
		Binary:             "kiro",
		ConfigDirs:         [][]string{{".kiro"}},
		DarwinAppDir:       "/Applications/Kiro.app",
		emit:               emitKiroServer,
	},
	{
		Provider:    ProviderHermes,
		DisplayName: "Hermes Agent",
		// No MCPConfigPath and no emitter on purpose (D141): Hermes' MCP
		// endpoint list lives in a config.yaml rendered by its Ansible role
		// and recreated on the next playbook run, so anything Cartographer
		// wrote there would be lost. `connect hermes` registers the provider
		// for artifact delivery and says so.
		DeletableWhenEmpty: true,
		BaseDirEnv:         "HERMES_HOME",
		Binary:             "hermes",
	},
	{
		Provider:           ProviderOpenCode,
		DisplayName:        "OpenCode",
		MCPConfigPath:      "opencode.json",
		MCPFormat:          FormatJSON,
		MCPServerKey:       "mcp",
		DeletableWhenEmpty: true,
		SupportsMCPHeaders: true,
		Binary:             "opencode",
		ConfigDirs:         [][]string{{".config", "opencode"}, {".opencode"}},
		emit:               emitOpenCodeServer,
	},
}

// detectionOrder is the order `cartographer agents` and the TUI list agents
// in. It differs from the registry order above and is equally user-visible:
// both are preserved deliberately rather than unified (D137).
var detectionOrder = []Provider{ProviderClaudeCode, ProviderOpenCode, ProviderCodex, ProviderKiro, ProviderHermes}

// Providers returns every supported provider's descriptor, in registry order.
func Providers() []Descriptor {
	out := make([]Descriptor, len(descriptors))
	copy(out, descriptors)
	return out
}

// DetectionOrder returns the descriptors in the order agent detection reports
// them.
func DetectionOrder() []Descriptor {
	out := make([]Descriptor, 0, len(detectionOrder))
	for _, p := range detectionOrder {
		if d, ok := Lookup(p); ok {
			out = append(out, d)
		}
	}
	return out
}

// Lookup returns the descriptor for p, or ok=false for an unknown provider.
func Lookup(p Provider) (Descriptor, bool) {
	for _, d := range descriptors {
		if d.Provider == p {
			return d, true
		}
	}
	return Descriptor{}, false
}

// ConfigPath returns MCPConfigPath in the local filesystem's separator form.
func (d Descriptor) ConfigPath() string { return filepath.FromSlash(d.MCPConfigPath) }

// ManagesMCPConfig reports whether `cartographer connect`/`disconnect` writes
// this provider's MCP configuration. False only for a provider whose endpoint
// list is owned elsewhere (D141, hermes): connect must skip it and say so,
// rather than silently doing nothing.
func (d Descriptor) ManagesMCPConfig() bool { return d.MCPConfigPath != "" && d.emit != nil }

// ManagesMCPConfig reports the same for a provider identifier. An unknown
// provider is not managed.
func ManagesMCPConfig(p Provider) bool {
	d, ok := Lookup(p)
	return ok && d.ManagesMCPConfig()
}

// ErrBaseDirUnset is returned by ResolveBaseDir when a provider declares a
// BaseDirEnv that is not set in the environment.
var ErrBaseDirUnset = errors.New("provider base directory environment variable is not set")

// ResolveBaseDir returns the directory this provider's artifacts are
// materialized under: defaultDir for every provider that shares the client
// base dir, or the value of BaseDirEnv for one that has its own root (D141).
// An unset or empty BaseDirEnv is an error naming the variable — writing into
// the home directory instead would scatter an agent's files somewhere it never
// looks.
func (d Descriptor) ResolveBaseDir(defaultDir string) (string, error) {
	if d.BaseDirEnv == "" {
		return defaultDir, nil
	}
	value := strings.TrimSpace(os.Getenv(d.BaseDirEnv))
	if value == "" {
		return "", fmt.Errorf("%s: $%s is unset: %w", d.Provider, d.BaseDirEnv, ErrBaseDirUnset)
	}
	return value, nil
}

// ProviderList returns just the provider constants, in registry order.
func ProviderList() []Provider {
	out := make([]Provider, 0, len(descriptors))
	for _, d := range descriptors {
		out = append(out, d.Provider)
	}
	return out
}
