package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// #656: the exclude block is one per workspace, so it carries the roots of
// every provider bound there, and unbinding one keeps the other's.
func TestWorkspaceExclusionsAreTheUnionOfBoundProviders(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ws := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", ws).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	read := func() string {
		b, err := os.ReadFile(filepath.Join(ws, ".git", "info", "exclude"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	has := func(content string, provider configurator.Provider) bool {
		for _, p := range provisioning.ProjectOwnedPaths(provider) {
			if !strings.Contains(content, "/"+filepath.ToSlash(p)+"\n") {
				return false
			}
		}
		return true
	}

	both := []string{string(configurator.ProviderClaudeCode), string(configurator.ProviderKiro)}
	if err := writeWorkspaceExclusions(ws, both); err != nil {
		t.Fatal(err)
	}
	if got := read(); !has(got, configurator.ProviderClaudeCode) || !has(got, configurator.ProviderKiro) {
		t.Fatalf("block lacks a provider's roots:\n%s", got)
	}

	if err := writeWorkspaceExclusions(ws, []string{string(configurator.ProviderKiro)}); err != nil {
		t.Fatal(err)
	}
	if got := read(); !has(got, configurator.ProviderKiro) || strings.Contains(got, "/.claude/") {
		t.Fatalf("after claude left, want kiro's roots only:\n%s", got)
	}

	if err := writeWorkspaceExclusions(ws, nil); err != nil {
		t.Fatal(err)
	}
	if got := read(); strings.Contains(got, "/.kiro/") {
		t.Fatalf("no provider left, block still there:\n%s", got)
	}
}

// A provider this run does not touch (`sync --client` names a subset) keeps
// its place; one this run touches but no longer declares is leaving.
func TestWorkspaceProvidersKeepsUntouchedAndDropsLeaving(t *testing.T) {
	ws := "/ws"
	var lf provisioning.LockFile
	for _, p := range []string{"claude", "kiro", "codex"} {
		lf.SetProjection(provisioning.Projection{Provider: p, Workspace: ws}, provisioning.Lock{}, "")
	}
	declared := []syncProjection{
		{Provider: "claude", Workspace: ws, Scope: provisioning.ScopeProject},
		{Provider: "codex", Workspace: "/other", Scope: provisioning.ScopeProject},
	}
	got := strings.Join(workspaceProviders(lf, declared, ws), ",")
	if got != "claude,kiro" {
		t.Errorf("workspaceProviders = %q, want claude,kiro (codex is in play and left /ws)", got)
	}
}
