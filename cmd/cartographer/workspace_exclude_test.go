package main

import (
	"errors"
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
	if err := writeWorkspaceExclusions(ws, both, rootsOwned); err != nil {
		t.Fatal(err)
	}
	if got := read(); !has(got, configurator.ProviderClaudeCode) || !has(got, configurator.ProviderKiro) {
		t.Fatalf("block lacks a provider's roots:\n%s", got)
	}

	if err := writeWorkspaceExclusions(ws, []string{string(configurator.ProviderKiro)}, rootsOwned); err != nil {
		t.Fatal(err)
	}
	if got := read(); !has(got, configurator.ProviderKiro) || strings.Contains(got, "/.claude/") {
		t.Fatalf("after claude left, want kiro's roots only:\n%s", got)
	}

	if err := writeWorkspaceExclusions(ws, nil, rootsOwned); err != nil {
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

// rootsOwned owns every directory root of a provider, the pre-D1181 answer: the
// tests that are about which providers share the block do not care which paths.
func rootsOwned(provider configurator.Provider) ([]string, error) {
	return provisioning.ProjectOwnedPaths(provider), nil
}

// #1181: a projection owns the artifacts it writes, not the directory. A
// repository's own tracked skill beside a projected one is not a collision and
// is not excluded; a projected skill of the same name still is refused.
func TestPrepareWorkspace_OwnsArtifactsNotDirectories(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ws := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", ws}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	own := filepath.Join(ws, ".agents", "skills", "own", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(own), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte("own\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".agents/skills/own/SKILL.md")

	p := syncProjection{Provider: "codex", Workspace: ws, BaseDir: ws, Scope: provisioning.ScopeProject}
	owner := func(name string) ownedPathsFunc {
		m := provisioning.Manifest{Artifacts: []provisioning.Artifact{{Kind: "skill", Name: name}}}
		return workspaceOwnership(&provisioning.LockFile{}, []syncProjection{p}, map[string]provisioning.Manifest{projectionKey(p): m}, ws)
	}
	exclude := func() string {
		b, _ := os.ReadFile(filepath.Join(ws, ".git", "info", "exclude"))
		return string(b)
	}

	if err := prepareWorkspace(p, []string{"codex"}, false, owner("kb-skill")); err != nil {
		t.Fatalf("a repository's own skill next to a projected one was refused: %v", err)
	}
	got := exclude()
	if !strings.Contains(got, "/.agents/skills/kb-skill\n") {
		t.Errorf("the projected skill is not excluded:\n%s", got)
	}
	if strings.Contains(got, "/.agents/skills\n") || strings.Contains(got, "skills/own") {
		t.Errorf("the directory or the repository's own skill is excluded:\n%s", got)
	}

	err := prepareWorkspace(p, []string{"codex"}, false, owner("own"))
	var collision *provisioning.TrackedCollisionError
	if !errors.As(err, &collision) || !strings.Contains(err.Error(), ".agents/skills/own/SKILL.md") {
		t.Errorf("a projected skill named like a tracked one = %v, want a collision naming it", err)
	}
}
