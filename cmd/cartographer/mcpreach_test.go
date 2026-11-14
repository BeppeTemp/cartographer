package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

func reachFixture(approved bool) (map[string]contract.Manifest, map[string]string) {
	a := contract.Artifact{Kind: "mcp", Name: "tracker", Source: "kb:kb-a", ContentHash: "h1"}
	approvals := map[string]string{}
	if approved {
		approvals[a.Source+"\x00"+provisioning.ApprovalName(a)] = "h1"
	}
	m := contract.Manifest{Artifacts: []contract.Artifact{a}}
	return map[string]contract.Manifest{"claude-code": m, "claude-code\x00/ws": m, "opencode\x00/ws": m, "codex\x00/ws": m, "crush\x00/ws": m}, approvals
}

func TestMCPNotReceivedReasons(t *testing.T) {
	manifests, approvals := reachFixture(true)
	ps := []syncProjection{
		{Provider: "claude-code", Scope: contract.ScopeGlobal, KBs: []string{"kb-a"}},
		{Provider: "opencode", Workspace: "/ws", BaseDir: "/ws", Scope: contract.ScopeProject, Derived: true, KBs: []string{"kb-a"}, SkipKinds: []string{"mcp"}, SharedSkipped: []string{"opencode.json"}},
		{Provider: "codex", Workspace: "/ws", BaseDir: "/ws", Scope: contract.ScopeProject, KBs: []string{"kb-a"}},
	}
	got := mcpNotReceivedReasons(ps, manifests, approvals, t.TempDir())
	if len(got["claude-code"]) != 0 {
		t.Errorf("claude receives it: %+v", got["claude-code"])
	}
	oc := got["opencode\x00/ws"]
	if len(oc) != 1 || !strings.Contains(oc[0].Reason, "opencode.json is tracked by git") || !strings.Contains(oc[0].Reason, "workspace bind opencode /ws --kb kb-a") {
		t.Errorf("opencode: %+v", oc)
	}
	cx := got["codex\x00/ws"]
	if len(cx) != 1 || !strings.Contains(cx[0].Reason, "does not trust this project") {
		t.Errorf("codex: %+v", cx)
	}

	var out bytes.Buffer
	printMCPNotReceived(&out, got, false)
	if n := strings.Count(out.String(), "mcp tracker: not written:"); n != 2 {
		t.Errorf("want two lines, got %d:\n%s", n, out.String())
	}
	out.Reset()
	printMCPNotReceived(&out, got, true)
	if !strings.Contains(out.String(), "mcp tracker: would not write:") {
		t.Errorf("dry run wording:\n%s", out.String())
	}
}

func TestMCPNotReceivedReasonsPriority(t *testing.T) {
	// A pending approval is reported as approval, not as the tracked file.
	manifests, approvals := reachFixture(false)
	ps := []syncProjection{{Provider: "opencode", Workspace: "/ws", Scope: contract.ScopeProject, Derived: true, SkipKinds: []string{"mcp"}, SharedSkipped: []string{"opencode.json"}}}
	got := mcpNotReceivedReasons(ps, manifests, approvals, t.TempDir())["opencode\x00/ws"]
	if len(got) != 1 || got[0].Reason != "needs approval: cartographer approve mcp tracker --kb kb-a" {
		t.Errorf("%+v", got)
	}
	// No destination wins over everything.
	ps = []syncProjection{{Provider: "crush", Workspace: "/ws", Scope: contract.ScopeProject}}
	if cell, ok := contract.CellFor(contract.ScopeProject, "mcp", "crush"); ok && !cell.Unsupported {
		t.Skip("crush has a project MCP cell")
	}
	got = mcpNotReceivedReasons(ps, manifests, approvals, t.TempDir())["crush\x00/ws"]
	if len(got) != 1 || !strings.Contains(got[0].Reason, "has no MCP file for a workspace projection") {
		t.Errorf("%+v", got)
	}
}

// A plugin provider in cli or none mode, or with a glob mcp.config, has no "mcp"
// cell, so a KB descriptor never reaches it: sync and status say why, in any
// scope, from its descriptor (#2091). A provider with a literal file receives it.
func TestMCPNotReceivedReasonsOfPluginProviders(t *testing.T) {
	home := t.TempDir()
	t.Cleanup(func() {
		_, _ = configurator.ReplacePlugins(nil)
		provisioning.ReplacePluginCells(nil, home)
	})
	specs := []configurator.PluginSpec{
		{Name: "fork-cli", Base: "cline", Origin: "team-forks", Active: true, MCPMode: "cli"},
		{Name: "fork-none", Base: "cline", Origin: "team-forks", Active: true, MCPMode: "none"},
		{Name: "fork-glob", Base: "cline", Origin: "team-forks", Active: true, MCPMode: "native", MCPConfig: []string{".vscode", "extensions", "acme.fork-ext-*", "settings", "mcp.json"}},
		{Name: "fork-file", Base: "cline", Origin: "team-forks", Active: true, MCPMode: "native", MCPConfig: []string{".fork-file", "mcp.json"}},
	}
	if refused, err := configurator.ReplacePlugins(specs); err != nil || len(refused) != 0 {
		t.Fatalf("register: %v %v", refused, err)
	}
	cells := []provisioning.PluginCells{
		{Name: "fork-cli", Base: "cline", Active: true},
		{Name: "fork-none", Base: "cline", Active: true},
		{Name: "fork-glob", Base: "cline", Active: true},
		{Name: "fork-file", Base: "cline", Active: true, MCPFile: []string{".fork-file", "mcp.json"}},
	}
	if _, refused := provisioning.ReplacePluginCells(cells, home); len(refused) != 0 {
		t.Fatalf("cells refused: %+v", refused)
	}

	manifests, approvals := reachFixture(true)
	ps := []syncProjection{
		{Provider: "fork-cli", Scope: contract.ScopeGlobal, KBs: []string{"kb-a"}},
		{Provider: "fork-none", Scope: contract.ScopeGlobal, KBs: []string{"kb-a"}},
		{Provider: "fork-glob", Scope: contract.ScopeGlobal, KBs: []string{"kb-a"}},
		{Provider: "fork-file", Scope: contract.ScopeGlobal, KBs: []string{"kb-a"}},
		{Provider: "fork-cli", Workspace: "/ws", BaseDir: "/ws", Scope: contract.ScopeProject, KBs: []string{"kb-a"}},
	}
	for _, p := range ps {
		m := manifests["claude-code"]
		manifests[projectionKey(p)] = m
	}
	got := mcpNotReceivedReasons(ps, manifests, approvals, t.TempDir())
	for provider, want := range map[string]string{
		"fork-cli":  "`cartographer call`",
		"fork-none": "mcp mode none",
		"fork-glob": "Cartographer's own entry is written into each match, a KB descriptor is not",
	} {
		r := got[provider]
		if len(r) != 1 || r[0].Name != "tracker" || !strings.Contains(r[0].Reason, want) || !strings.Contains(r[0].Reason, provider) {
			t.Errorf("%s: %+v; want one reason with %q", provider, r, want)
		}
	}
	if r := got["fork-file"]; len(r) != 0 {
		t.Errorf("a literal-file provider receives the descriptor: %+v", r)
	}
	// The project scope says it too, from the descriptor.
	if r := got["fork-cli\x00/ws"]; len(r) != 1 || !strings.Contains(r[0].Reason, "`cartographer call`") {
		t.Errorf("project scope: %+v", r)
	}

	var out bytes.Buffer
	printMCPNotReceived(&out, got, false)
	for _, want := range []string{"[fork-cli] mcp tracker: not written: fork-cli reaches Cartographer through `cartographer call`", "[fork-none] mcp tracker: not written:", "[fork-glob] mcp tracker: not written:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("sync output lacks %q:\n%s", want, out.String())
		}
	}
	// status reads the same function.
	if st := snapshotMCPNotReceived(ps[:1], manifests, approvals, t.TempDir()); len(st) != 1 || !strings.Contains(st[0].Reason, "`cartographer call`") {
		t.Errorf("status: %+v", st)
	}
}
