package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// #1897: an explicit `workspace bind` writes its instructions block into the
// project file, which a repository often tracks. The "Local paths" table holds
// this machine's absolute paths, so it stays out of a tracked file — in sync
// AND in status, which must judge the same hash or report drift for good.

const trackedPathsMachinePath = "/srv/design-machine"

// syncExplicitCodex binds codex to repo, syncs twice and judges the lock the
// way `status` does. It returns AGENTS.md as the sync left it.
func syncExplicitCodex(t *testing.T, repo string) string {
	t.Helper()
	target := t.TempDir()
	t.Setenv("HOME", target)
	paths := map[string]string{"design": trackedPathsMachinePath}
	cfg := workspaceScoped("codex", "kb-a")
	cfg.Paths = paths
	if err := cfg.BindWorkspace("codex", repo, []string{"kb-a"}); err != nil {
		t.Fatal(err)
	}
	projs, err := projectionsFor(cfg, "codex", target)
	if err != nil {
		t.Fatal(err)
	}
	manifests := map[string]contract.Manifest{}
	for _, p := range projs {
		if p.BundleOnly {
			manifests[projectionKey(p)] = contract.Manifest{}
			continue
		}
		m := instructionsManifest("kb-a", "Rules of kb-a.\n")
		// Cited by a concept only: the table is the one way the path can reach the file.
		m.Placeholders = map[string][]string{"path:design": {"kb-a"}}
		manifests[projectionKey(p)] = m
	}
	portability := portabilityOptions{Paths: paths}
	if _, err := materializeForProviders(manifests, projs, target, "", false, false, portability, nil); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	block, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}

	lockFile, err := provisioning.ReadLockFile(lockFilePath(target))
	if err != nil {
		t.Fatal(err)
	}
	if _, d, _ := workspaceProviderDiff(cfg, projs, manifests, nil, lockFile, target); !d.InSync {
		t.Errorf("status after the sync must be in sync, not drift forever: %+v", d)
	}
	results, err := materializeForProviders(manifests, projs, target, "", false, false, portability, nil)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	for key, r := range results {
		if len(r.Written) != 0 {
			t.Errorf("the second sync rewrote %s: %+v", key, r.Written)
		}
	}
	return string(block)
}

func TestExplicitBind_TrackedAgentsMDGetsNoPathsTable(t *testing.T) {
	repo := cloneOf(t, "example.com/owner/repo", "AGENTS.md")
	block := syncExplicitCodex(t, repo)
	if !strings.Contains(block, "Rules of kb-a.") || !strings.Contains(block, "cartographer resolve") {
		t.Errorf("the block keeps the instructions and the paragraph that points to `cartographer resolve`:\n%s", block)
	}
	if strings.Contains(block, "| Placeholder | Local path |") || strings.Contains(block, trackedPathsMachinePath) {
		t.Errorf("a machine path reached the tracked AGENTS.md:\n%s", block)
	}
}

func TestExplicitBind_UntrackedAgentsMDKeepsThePathsTable(t *testing.T) {
	repo := cloneOf(t, "example.com/owner/repo")
	block := syncExplicitCodex(t, repo)
	if !strings.Contains(block, "| Placeholder | Local path |") || !strings.Contains(block, trackedPathsMachinePath) {
		t.Errorf("an untracked AGENTS.md keeps the table:\n%s", block)
	}
}

func TestWithholdLocalPaths(t *testing.T) {
	repo := cloneOf(t, "example.com/owner/repo", "AGENTS.md")
	ws := syncProjection{Provider: "codex", Workspace: repo, BaseDir: repo, Scope: contract.ScopeProject}
	if !withholdLocalPaths(ws) {
		t.Error("a workspace projection whose AGENTS.md is tracked omits the table")
	}
	global := syncProjection{Provider: "codex", BaseDir: repo, Scope: contract.ScopeGlobal}
	if withholdLocalPaths(global) {
		t.Error("the global projection is never judged")
	}
	ws.Provider = "claude"
	ws.InstructionsRel = "CLAUDE.local.md"
	if withholdLocalPaths(ws) {
		t.Error("a derived projection's untracked CLAUDE.local.md keeps the table")
	}
}
