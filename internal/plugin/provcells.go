package plugin

import (
	"strings"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// ProviderCells is what provisioning reads of the provider: its destination cells, the
// agent layout, the capability row and the invoke hint. active is the state of
// the plugin: an inactive provider's cells stay resolvable (what it installed is
// retired through them) but its hook directory leaves the dispatcher. The MCP
// file is a cell only when it is the provider's own literal file: the base's
// file is the base's, and a pattern is written by the registry's own expansion
// (configurator.Apply), never as a destination of the KB's MCP descriptors.
func ProviderCells(r core.ResolvedProvider, active bool) provisioning.PluginCells {
	conv := func(c *core.ProviderCell) *provisioning.PluginCell {
		if c == nil {
			return nil
		}
		return &provisioning.PluginCell{Dir: c.Dir, File: c.File, AgentFormat: c.AgentFormat}
	}
	scope := func(s core.ProviderScope) provisioning.PluginScopeCells {
		return provisioning.PluginScopeCells{Skill: conv(s.Skill), Agent: conv(s.Agent), Instructions: conv(s.Instructions), Hook: conv(s.Hook)}
	}
	out := provisioning.PluginCells{
		Name: r.Name, Base: r.Base, Active: active,
		Global: scope(r.Global), Project: scope(r.Project),
		Capabilities: r.Capabilities, InvokeHint: r.InvokeHint,
	}
	if r.MCPMode == "native" && len(r.MCPConfig) > 0 && !strings.Contains(strings.Join(r.MCPConfig, "/"), "*") {
		out.MCPFile = r.MCPConfig
	}
	if r.MCPMode == "native" {
		out.MCPStale = r.MCPStale
	}
	return out
}
