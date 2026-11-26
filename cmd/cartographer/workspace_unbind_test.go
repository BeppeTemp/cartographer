package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// copilotWorkspaceSync applies one agent into ws as a Copilot project
// projection, the sync that makes .github, and returns the lockfile holding it.
func copilotWorkspaceSync(t *testing.T, ws string) (*provisioning.LockFile, provisioning.Projection) {
	t.Helper()
	m := contract.Manifest{Revision: "r", Artifacts: []contract.Artifact{{
		Kind: "agent", Name: "reviewer", Source: "bundle", BuiltIn: true, ContentHash: "h-agent",
		Files: []contract.ArtifactFile{{Path: "reviewer.md", Content: []byte("---\nname: reviewer\ndescription: Reviews\n---\nBody.\n")}},
	}}}
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderCopilot, BaseDir: ws, Scope: contract.ScopeProject, Lock: provisioning.Lock{},
	})
	if err != nil || len(res.NewLock.Managed) == 0 {
		t.Fatalf("Apply: %v (managed %d)", err, len(res.NewLock.Managed))
	}
	key := provisioning.Projection{Provider: string(configurator.ProviderCopilot), Workspace: ws}
	lock := res.NewLock
	lock.BaseDir = ws
	lf := &provisioning.LockFile{}
	lf.SetProjection(key, lock)
	return lf, key
}

// otherWorkspace declares one more Copilot workspace, so the provider is "in
// play" and the unbound one is an orphan to prune.
func otherWorkspace() []syncProjection {
	return []syncProjection{{Provider: string(configurator.ProviderCopilot), Workspace: "/elsewhere", Scope: contract.ScopeProject}}
}

// AC-sdc-13: after `workspace unbind` the next sync prunes the projection: a
// .github the sync created and left empty goes, one that holds a workflow stays
// with its ledger entry; the ledger is excluded for a Codex-only workspace.
func TestUnbindSyncRemovesCreatedGithubDir(t *testing.T) {
	var out, errb bytes.Buffer
	st := syncStreams{Out: &out, Err: &errb}

	fresh := t.TempDir()
	lf, _ := copilotWorkspaceSync(t, fresh)
	if !configurator.WasDirCreated(fresh, ".github") {
		t.Fatal("the sync did not record the .github it made")
	}
	if _, err := pruneOrphanProjections(st, lf, otherWorkspace(), nil, t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(fresh, ".github")); !os.IsNotExist(err) {
		t.Errorf(".github made by the sync survived the unbind: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(fresh, configurator.CreatedLedgerName)); !os.IsNotExist(err) {
		t.Errorf("the ledger survived its last entry: %v", err)
	}

	held := t.TempDir()
	lf, _ = copilotWorkspaceSync(t, held)
	wf := filepath.Join(held, ".github", "workflows", "ci.yml")
	if err := os.MkdirAll(filepath.Dir(wf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wf, []byte("name: ci\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pruneOrphanProjections(st, lf, otherWorkspace(), nil, t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wf); err != nil {
		t.Errorf("the user's workflow was touched: %v", err)
	}
	if !configurator.WasDirCreated(held, ".github") {
		t.Error("the entry of a root that stays must be kept")
	}

	ws := trackedRepo(t, map[string]string{"README.md": "x\n"})
	owned := func(configurator.Provider) ([]string, error) { return nil, nil }
	if err := writeWorkspaceExclusions(ws, []string{string(configurator.ProviderCodex)}, owned); err != nil {
		t.Fatal(err)
	}
	block, err := os.ReadFile(filepath.Join(ws, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(block), configurator.CreatedLedgerName) {
		t.Errorf("the ledger is not excluded for a Codex-only workspace:\n%s", block)
	}
}
