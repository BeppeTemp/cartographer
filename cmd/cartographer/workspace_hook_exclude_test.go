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

func hookManifest() provisioning.Manifest {
	m := provisioning.Manifest{Revision: "r", Artifacts: []provisioning.Artifact{
		{Kind: "hook", Name: "greet", Source: "bundle", BuiltIn: true, ContentHash: "h-hook",
			Files: []provisioning.ArtifactFile{
				{Path: "hook.json", Content: []byte(`{"event":"SessionStart","command":"./run.sh"}` + "\n")},
				{Path: "run.sh", Content: []byte("#!/bin/sh\nexit 0\n"), Executable: true},
			}},
		{Kind: "skill", Name: "example", Source: "bundle", BuiltIn: true, ContentHash: "h-skill",
			Files: []provisioning.ArtifactFile{{Path: "SKILL.md", Content: []byte("# example\n")}}},
	}}
	return m
}

func wsGitOut(t *testing.T, ws string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", ws}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// #1199: after a workspace sync of codex and opencode with a hook, the exclude
// block covers every path the projection wrote (the hook companions, Codex's
// hooks.json and OpenCode's generated plugin, included), the repository stays
// clean, and the block holds no bare "/" line. The expected set is what Apply
// reported, not a list written by hand.
func TestWorkspaceSync_ExcludeBlockCoversEveryWrittenPath(t *testing.T) {
	ws := trackedRepo(t, map[string]string{"README.md": "x\n"})
	var projections []syncProjection
	manifests := map[string]provisioning.Manifest{}
	for _, prov := range []configurator.Provider{configurator.ProviderCodex, configurator.ProviderOpenCode} {
		p := syncProjection{Provider: string(prov), Workspace: ws, BaseDir: ws, Scope: provisioning.ScopeProject}
		projections = append(projections, p)
		manifests[projectionKey(p)] = hookManifest()
	}
	results, err := materializeForProviders(manifests, projections, t.TempDir(), "", false, false, portabilityOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	written := map[string]bool{}
	for _, r := range results {
		for _, w := range r.Written {
			written[filepath.ToSlash(w.Path)] = true
		}
	}
	for _, must := range []string{".codex/hooks/greet/hook.json", ".config/opencode/plugins/cartographer-greet.js"} {
		if !written[must] {
			t.Fatalf("fixture drifted: %s not reported as written (got %v)", must, written)
		}
	}
	// A registration patched into a shared file is not in Written: it is on disk.
	if _, err := os.Stat(filepath.Join(ws, ".codex", "hooks.json")); err != nil {
		t.Fatalf("codex did not register the hook: %v", err)
	}
	written[".codex/hooks.json"] = true

	for p := range written {
		if err := exec.Command("git", "-C", ws, "check-ignore", "-q", "--", p).Run(); err != nil {
			t.Errorf("%s is written by the projection and not excluded", p)
		}
	}
	if untracked := wsGitOut(t, ws, "ls-files", "--others", "--exclude-standard"); untracked != "" {
		t.Errorf("the working tree is dirty after sync:\n%s", untracked)
	}
	block, err := os.ReadFile(filepath.Join(ws, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(block), "\n") {
		if strings.TrimSpace(line) == "/" {
			t.Errorf("the exclude block holds a bare / line:\n%s", block)
		}
	}
	if left := uncoveredProjectionFiles(configurator.ProviderCodex, ws); len(left) != 0 {
		t.Errorf("doctor reports files the block covers: %v", left)
	}
}

// #1199: a tracked hooks.json is the repository's: neither refused nor excluded.
func TestWorkspaceSync_TrackedCodexHooksFileIsNotExcluded(t *testing.T) {
	ws := trackedRepo(t, map[string]string{".codex/hooks.json": "{}\n"})
	p := syncProjection{Provider: "codex", Workspace: ws, BaseDir: ws, Scope: provisioning.ScopeProject}
	manifests := map[string]provisioning.Manifest{projectionKey(p): hookManifest()}
	if _, err := materializeForProviders(manifests, []syncProjection{p}, t.TempDir(), "", false, false, portabilityOptions{}, nil); err != nil {
		t.Fatalf("a tracked hooks.json was refused: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(ws, ".git", "info", "exclude"))
	if strings.Contains(string(b), "/.codex/hooks.json\n") {
		t.Errorf("a tracked file was excluded:\n%s", b)
	}
}

// #1199: doctor lists what the block does not cover.
func TestUncoveredProjectionFiles(t *testing.T) {
	ws := trackedRepo(t, map[string]string{"README.md": "x\n"})
	for _, rel := range []string{".codex/hooks.json", ".config/opencode/plugins/cartographer-x.js", ".opencode/skills/mine/SKILL.md"} {
		abs := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := uncoveredProjectionFiles(configurator.ProviderCodex, ws); len(got) != 1 || got[0] != ".codex/hooks.json" {
		t.Errorf("codex: %v, want .codex/hooks.json", got)
	}
	got := strings.Join(uncoveredProjectionFiles(configurator.ProviderOpenCode, ws), ",")
	if got != ".config/opencode/plugins/cartographer-x.js,.opencode/skills/mine/SKILL.md" {
		t.Errorf("opencode: %s", got)
	}
	if err := provisioning.EnsureWorkspaceExcluded(ws, []string{".codex/hooks.json", ".config/opencode/plugins/cartographer-x.js", ".opencode/skills"}); err != nil {
		t.Fatal(err)
	}
	if got := uncoveredProjectionFiles(configurator.ProviderOpenCode, ws); len(got) != 0 {
		t.Errorf("covered files still reported: %v", got)
	}
}

// #1199: the workspace root is not a path to exclude.
func TestEnsureWorkspaceExcluded_NoBareSlash(t *testing.T) {
	ws := trackedRepo(t, map[string]string{"README.md": "x\n"})
	if err := provisioning.EnsureWorkspaceExcluded(ws, []string{"", ".", "/", ".kiro/skills"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(ws, ".git", "info", "exclude"))
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == "/" || l == "//" || l == "/." {
			t.Errorf("bad line %q in:\n%s", l, b)
		}
	}
	if !strings.Contains(string(b), "/.kiro/skills\n") {
		t.Errorf("real path lost:\n%s", b)
	}
}

// #1200: the per-workspace line label is printable, never the NUL-joined key.
func TestProjectionLabel_IsPrintable(t *testing.T) {
	home, _ := os.UserHomeDir()
	key := projectionKey(syncProjection{Provider: "claude", Workspace: filepath.Join(home, "projects", "area-a")})
	if got := projectionLabel(key); got != "claude $HOME/projects/area-a" {
		t.Errorf("label = %q", got)
	}
	if got := projectionLabel("codex"); got != "codex" {
		t.Errorf("global label = %q", got)
	}
	if got := projectionLabel(projectionKey(syncProjection{Provider: "kiro", Workspace: "/srv/x"})); got != "kiro /srv/x" {
		t.Errorf("label = %q", got)
	}
}

func TestPrintApplySummary_WorkspaceDryRunHasNoNUL(t *testing.T) {
	ws := trackedRepo(t, map[string]string{"README.md": "x\n"})
	p := syncProjection{Provider: "codex", Workspace: ws, BaseDir: ws, Scope: provisioning.ScopeProject}
	manifests := map[string]provisioning.Manifest{projectionKey(p): hookManifest()}
	results, err := materializeForProviders(manifests, []syncProjection{p}, t.TempDir(), "", true, false, portabilityOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("no result")
	}
	out := withStdout(t, func() { printApplySummary(t.TempDir(), results, true) })
	if out == "" || strings.ContainsRune(out, 0) {
		t.Fatalf("dry-run output empty or holds a NUL byte: %q", out)
	}
	if !strings.Contains(out, "[codex "+ws+"] would write") {
		t.Errorf("want a [codex <workspace>] label, got:\n%s", out)
	}
}
