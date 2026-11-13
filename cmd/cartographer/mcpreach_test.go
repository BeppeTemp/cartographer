package main

import (
	"bytes"
	"strings"
	"testing"

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
