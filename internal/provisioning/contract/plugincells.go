package contract

import (
	"path"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// PluginEntry is the provisioning view of one registered plugin provider.
type PluginEntry struct {
	Base         configurator.Provider
	AgentFormat  string
	Active       bool
	Cells        map[Scope]map[string]Destination
	Capabilities map[string]string
	InvokeHint   string
	// roots are the prune boundaries the cells add (pluginBoundaries).
	Roots []string
	// stale are the fixed prefixes of the provider's mcp.stale files, as slash paths.
	Stale []string
}

// PluginRegistryMu guards PluginRegistry. Only provisioning's RegisterPluginCells,
// ReplacePluginCells and UnregisterPluginCells write it.
var PluginRegistryMu sync.RWMutex

var PluginRegistry = map[configurator.Provider]PluginEntry{}

// pluginCell is the plugin half of CellFor.
func pluginCell(scope Scope, kind string, provider configurator.Provider) (Destination, bool) {
	PluginRegistryMu.RLock()
	defer PluginRegistryMu.RUnlock()
	if v, ok := PluginRegistry[provider]; ok {
		if cell, ok := v.Cells[scope][kind]; ok {
			return cell, true
		}
	}
	return unsupportedDest, false
}

// PluginFor returns the registered plugin provider.
func PluginFor(provider configurator.Provider) (PluginEntry, bool) {
	PluginRegistryMu.RLock()
	defer PluginRegistryMu.RUnlock()
	v, ok := PluginRegistry[provider]
	return v, ok
}

// NativeProvider is the id an agent's `providers.<id>` frontmatter block is read
// under: a plugin provider honours its base's block.
func NativeProvider(provider configurator.Provider) configurator.Provider {
	if v, ok := PluginFor(provider); ok {
		return v.Base
	}
	return provider
}

// pluginCapability is CapabilityStatus for a plugin provider: its declared value,
// else what the base's probes recorded, else unverified; forced absent where the
// provider has no cell for the kind in the scope. Hook capabilities stay absent:
// no probe of a fork's hooks exists, and the base's rows say nothing about them.
func pluginCapability(v PluginEntry, provider configurator.Provider, scope Scope, id Capability) CapStatus {
	var kind string
	switch id {
	case CapSkill, CapMCP:
		kind = string(id)
	case CapAgentDelegate, CapAgentInvoke:
		kind = "agent"
	default:
		return CapAbsent
	}
	if DestDirScoped(kind, "x", provider, scope) == "" {
		return CapAbsent
	}
	if s, ok := v.Capabilities[string(id)]; ok {
		return CapStatus(s)
	}
	// A probe of the base says nothing about a different agent file format or MCP
	// file: only the skill row is inherited across such a difference.
	if (kind == "agent" && v.AgentFormat != baseAgentFormat(v.Base)) || (kind == "mcp" && !sameMCPFile(v, scope)) {
		return CapUnverified
	}
	if s, ok := capabilityProbed[v.Base][scope][id]; ok {
		return s
	}
	return CapUnverified
}

// baseAgentFormat is the agent.format a built-in's own agent file corresponds
// to, "" for a base whose agent file is no format a plugin provider may declare.
func baseAgentFormat(base configurator.Provider) string {
	switch base {
	case configurator.ProviderClaudeCode:
		return "claude"
	case configurator.ProviderOpenCode, configurator.ProviderAntigravity, configurator.ProviderCopilot, configurator.ProviderCline:
		return string(base)
	}
	return ""
}

// sameMCPFile reports whether the provider's mcp cell is the base's file.
func sameMCPFile(v PluginEntry, scope Scope) bool {
	cell, ok := v.Cells[scope]["mcp"]
	d, found := configurator.Lookup(v.Base)
	return ok && found && path.Join(cell.Dir...) == d.MCPConfigPath
}
