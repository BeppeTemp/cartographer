package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

func excludeBlockOf(t *testing.T, ws string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(ws, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func refreshFixture(t *testing.T) (ws, home string, p syncProjection) {
	t.Helper()
	ws = trackedRepo(t, map[string]string{"README.md": "x\n"})
	p = syncProjection{Provider: string(configurator.ProviderCodex), Workspace: ws, BaseDir: ws, Scope: contract.ScopeProject}
	return ws, t.TempDir(), p
}

// #2372: the exclude block is written before Apply from the previous lock, so
// the file-level lines of a fresh binding used to appear only at the second
// sync. The block after sync 1 is the block after sync 2.
func TestWorkspaceSync_ExcludeBlockIsStableFromTheFirstSync(t *testing.T) {
	ws, home, p := refreshFixture(t)
	manifests := map[string]contract.Manifest{projectionKey(p): hookManifest()}
	run := func() {
		if _, err := materializeForProviders(manifests, []syncProjection{p}, home, "", false, false, portabilityOptions{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	run()
	first := excludeBlockOf(t, ws)
	run()
	if second := excludeBlockOf(t, ws); first != second {
		t.Errorf("the block changed between sync 1 and sync 2:\n--- 1\n%s\n--- 2\n%s", first, second)
	}
	if !strings.Contains(first, ".codex/hooks/greet/hook.json") {
		t.Errorf("the first sync's block lacks the file-level lines:\n%s", first)
	}
}

// #2372: a skill removed from the KB leaves the block in the sync that prunes it.
func TestWorkspaceSync_PrunedSkillLeavesTheExcludeBlockInTheSameSync(t *testing.T) {
	ws, home, p := refreshFixture(t)
	key := projectionKey(p)
	run := func(m contract.Manifest) {
		if _, err := materializeForProviders(map[string]contract.Manifest{key: m}, []syncProjection{p}, home, "", false, false, portabilityOptions{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	run(hookManifest())
	if b := excludeBlockOf(t, ws); !strings.Contains(b, "example") {
		t.Fatalf("fixture drifted: the skill is not in the block:\n%s", b)
	}
	m := hookManifest()
	m.Artifacts = m.Artifacts[:1] // drop the skill
	run(m)
	if b := excludeBlockOf(t, ws); strings.Contains(b, "skills/example") {
		t.Errorf("the pruned skill's files are still in the block:\n%s", b)
	}
}
