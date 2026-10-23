package main

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func TestParseApproveMCPArgs(t *testing.T) {
	name, kb, yes, ok := parseApproveMCPArgs([]string{"tools", "--kb", "wiki", "--yes"})
	if !ok || name != "tools" || kb != "wiki" || !yes {
		t.Fatalf("got %q %q %v %v", name, kb, yes, ok)
	}
	if _, _, _, ok := parseApproveMCPArgs([]string{"--yes"}); ok {
		t.Fatal("missing name accepted")
	}
	if _, _, _, ok := parseApproveMCPArgs([]string{"tools", "--kb"}); ok {
		t.Fatal("missing kb accepted")
	}
}

func approveFixture(t *testing.T, interactive bool) (home string, artifacts []provisioning.Artifact) {
	t.Helper()
	home = t.TempDir()
	setHome(t, home)
	if err := clientconfig.Save(home, &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", Agents: []string{"claude"}}); err != nil {
		t.Fatal(err)
	}
	artifacts = []provisioning.Artifact{
		{Kind: "hook", Name: "guard", Source: "kb:kb-a", ContentHash: "hook-hash-0123456789", Files: []provisioning.ArtifactFile{
			{Path: "hook.json", Content: []byte(`{"event":"SessionStart","command":"./run.sh"}`)},
			{Path: "run.sh", Content: []byte("#!/bin/sh\necho SCRIPT-BODY\n"), Executable: true},
		}},
		{Kind: "skill", Name: "plain", Source: "kb:kb-a", ContentHash: "skill-hash", Files: []provisioning.ArtifactFile{
			{Path: "SKILL.md", Content: []byte("---\nname: plain\ndescription: d\n---\n")},
		}},
	}
	oldM, oldI := approveManifest, approveInteractive
	approveManifest = func(*clientconfig.Config) (provisioning.Manifest, error) {
		return provisioning.Manifest{Revision: "r", Artifacts: artifacts}, nil
	}
	approveInteractive = func() bool { return interactive }
	t.Cleanup(func() { approveManifest, approveInteractive = oldM, oldI })
	return home, artifacts
}

func TestCmdApprove_HookStoresKindNameKeyAndShowsContent(t *testing.T) {
	home, _ := approveFixture(t, false)
	var code int
	out := withStdout(t, func() { code = cmdApprove([]string{"hook", "guard", "--kb", "kb-a", "--yes"}) })
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{`"command":"./run.sh"`, "SCRIPT-BODY", "run.sh", "(executable)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output must show %q:\n%s", want, out)
		}
	}
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MCPApprovals["kb-a"]["hook/guard"].ContentHash; got != "hook-hash-0123456789" {
		t.Fatalf("approval stored under hook/guard = %q (%+v)", got, cfg.MCPApprovals)
	}
	if _, bare := cfg.MCPApprovals["kb-a"]["guard"]; bare {
		t.Error("a hook approval must not be stored under the bare MCP key")
	}

	// Revoke without --kb finds the source through the <kind>/<name> key, and
	// says the installed hook stays.
	out = withStdout(t, func() { code = cmdApprove([]string{"revoke", "hook", "guard"}) })
	if code != 0 || !strings.Contains(out, "stays installed") {
		t.Fatalf("revoke = %d, output %q", code, out)
	}
	cfg, _ = clientconfig.Load(home)
	if len(cfg.MCPApprovals) != 0 {
		t.Errorf("approval not revoked: %+v", cfg.MCPApprovals)
	}
}

func TestCmdApprove_RefusesWhatNeedsNoApproval(t *testing.T) {
	home, _ := approveFixture(t, false)
	if code := cmdApprove([]string{"skill", "plain", "--kb", "kb-a", "--yes"}); code != 2 {
		t.Fatalf("a plain skill must be refused, exit %d", code)
	}
	cfg, _ := clientconfig.Load(home)
	if len(cfg.MCPApprovals) != 0 {
		t.Errorf("nothing must be stored: %+v", cfg.MCPApprovals)
	}
	if code := cmdApprove([]string{"widget", "x"}); code != 2 {
		t.Errorf("unknown kind exit %d, want 2", code)
	}
}

func TestCmdApprove_NonInteractiveWithoutYesDoesNotApprove(t *testing.T) {
	home, _ := approveFixture(t, false)
	if code := cmdApprove([]string{"hook", "guard", "--kb", "kb-a"}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	cfg, _ := clientconfig.Load(home)
	if len(cfg.MCPApprovals) != 0 {
		t.Errorf("approved without --yes: %+v", cfg.MCPApprovals)
	}
}
