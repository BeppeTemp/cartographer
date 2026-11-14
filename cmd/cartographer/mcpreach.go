package main

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// mcpNotReceived says why one MCP descriptor a bound KB ships does not reach a
// projection's client. Reason is the first cause by priority: the fix differs
// per cause, so only one is named.
type mcpNotReceived struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// trackedMCPNote is the text `workspace status` and sync share for an MCP file a
// derived binding leaves alone because git tracks it, so the two never drift.
func trackedMCPNote(files []string, provider, path string, kbs []string) string {
	return fmt.Sprintf("not written: %s is tracked by git and a derived binding never edits a tracked file — the provider keeps its global entry; `cartographer workspace bind %s %s --kb %s` is the explicit alternative",
		strings.Join(files, ", "), provider, path, strings.Join(kbs, ","))
}

// mcpNotReceivedReasons returns, per projection key, one reason for every MCP
// descriptor in the projection's manifest (before a derived binding's
// withoutKinds) that the client does not receive. It reads and writes nothing.
// Sync and status both call it with the same inputs. A provider that receives
// the descriptor but not its headers is not listed: the writer already warns.
func mcpNotReceivedReasons(projections []syncProjection, manifests map[string]contract.Manifest, approvals map[string]string, clientBaseDir string) map[string][]mcpNotReceived {
	out := map[string][]mcpNotReceived{}
	for _, p := range projections {
		provider := configurator.Provider(p.Provider)
		for _, a := range manifests[projectionKey(p)].Artifacts {
			if a.Kind != "mcp" || a.BuiltIn {
				continue
			}
			kb := strings.TrimPrefix(a.Source, "kb:")
			var reason string
			switch {
			case pluginWritesNoDescriptors(provider, p.Scope) != "":
				reason = pluginWritesNoDescriptors(provider, p.Scope)
			case !mcpCellSupported(p.Scope, provider):
				reason = fmt.Sprintf("%s has no MCP file for a workspace projection (see docs/harnesses.md)", p.Provider)
			case provisioning.PendingApproval(a, provisioning.ApplyOptions{Provider: provider, Scope: p.Scope, ApprovedMCP: approvals}):
				reason = fmt.Sprintf("needs approval: cartographer approve mcp %s --kb %s", a.Name, kb)
			case p.Derived && slices.Contains(p.SkipKinds, "mcp"):
				reason = strings.TrimPrefix(trackedMCPNote(p.SharedSkipped, p.Provider, p.Workspace, p.KBs), "not written: ")
			case provider == configurator.ProviderCodex && p.Workspace != "" &&
				!provisioning.CodexProjectTrusted(provisioning.CodexConfigPath(clientBaseDir), p.Workspace):
				reason = "codex does not trust this project, so it ignores its .codex/ layer; `cartographer workspace status` says how to trust it"
			default:
				continue
			}
			out[projectionKey(p)] = append(out[projectionKey(p)], mcpNotReceived{Name: a.Name, Reason: reason})
		}
		sort.Slice(out[projectionKey(p)], func(i, j int) bool { return out[projectionKey(p)][i].Name < out[projectionKey(p)][j].Name })
	}
	return out
}

// pluginWritesNoDescriptors is why a plugin provider has no "mcp" destination in
// the scope, from its descriptor: `cli` and `none` write no MCP file, and a
// `mcp.config` glob gets Cartographer's own entry in each match, never a KB
// descriptor (docs/plugins.md §Destination cells). "" for a built-in provider
// (its project cell is judged by mcpCellSupported, and the global scope has no
// "mcp" row by design), for a provider that has the cell, and for one whose file
// is the base's or a literal one (only its project scope lacks the cell, and
// mcpCellSupported says so).
func pluginWritesNoDescriptors(provider configurator.Provider, scope contract.Scope) string {
	if _, ok := contract.PluginFor(provider); !ok || contract.DestDirScoped("mcp", "", provider, scope) != "" {
		return ""
	}
	d, ok := configurator.Lookup(provider)
	if !ok {
		return ""
	}
	switch {
	case d.MCPMode == configurator.MCPModeCLI:
		return fmt.Sprintf("%s reaches Cartographer through `cartographer call` (mcp mode cli), so it has no MCP file", provider)
	case d.MCPMode == configurator.MCPModeNone:
		return fmt.Sprintf("%s declares mcp mode none, so it has no MCP file", provider)
	case d.MCPConfigGlob != "":
		return fmt.Sprintf("%s's mcp.config is a pattern (%s): Cartographer's own entry is written into each match, a KB descriptor is not (docs/plugins.md §Destination cells)", provider, d.MCPConfigGlob)
	}
	return ""
}

// mcpCellSupported: the global matrix has no "mcp" row (the entry is merged into
// the client's own config by registerMCPServer), so only a project cell can be
// unsupported.
func mcpCellSupported(scope contract.Scope, provider configurator.Provider) bool {
	if scope != contract.ScopeProject {
		return true
	}
	d, ok := contract.CellFor(scope, "mcp", provider)
	return ok && !d.Unsupported
}

// printMCPNotReceived prints one line per descriptor a client does not receive.
func printMCPNotReceived(wr io.Writer, reasons map[string][]mcpNotReceived, dryRun bool) {
	verb := "not written"
	if dryRun {
		verb = "would not write"
	}
	keys := make([]string, 0, len(reasons))
	for k := range reasons {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, r := range reasons[k] {
			fmt.Fprintf(wr, "[%s] mcp %s: %s: %s\n", projectionLabel(k), escapeControl(r.Name), verb, escapeControl(r.Reason))
		}
	}
}
