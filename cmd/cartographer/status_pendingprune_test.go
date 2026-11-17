package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// #2238: a projection the lock still records and no binding declares (what a
// refused prune keeps, #2218) is neither in sync nor silent: status names the
// workspace and the reason, in text and in JSON.
func TestStatus_PendingPruneNamesWorkspaceAndReason(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	ws := filepath.Join(t.TempDir(), "repo-x")
	key := provisioning.Projection{Provider: "codex", Workspace: ws}
	var lf provisioning.LockFile
	lf.SetProjection(key, provisioning.Lock{AppliedRevision: "rev1", BaseDir: ws})

	got := pendingPrunes(lf, "codex", nil, home)
	if len(got) != 1 || got[0].Workspace != ws || !strings.Contains(got[0].Reason, "next sync retries") {
		t.Fatalf("pendingPrunes = %+v", got)
	}
	// Another provider's orphan, a declared workspace and the global projection are not this provider's.
	declared := []syncProjection{{Provider: "codex", Workspace: ws, BaseDir: ws, Scope: contract.ScopeProject}}
	if got := pendingPrunes(lf, "codex", declared, home); len(got) != 0 {
		t.Errorf("a declared workspace is not pending: %+v", got)
	}
	if got := pendingPrunes(lf, "claude", nil, home); len(got) != 0 {
		t.Errorf("another provider's projection is not pending: %+v", got)
	}
	// A listing that failed keeps every undeclared projection without judging it.
	if got := pendingPrunes(lf, "codex", []syncProjection{{Provider: "codex", WorktreesUnlisted: true}}, home); len(got) != 0 {
		t.Errorf("unlisted worktrees: %+v", got)
	}

	// Through the snapshot: the provider is drift, never in-sync, with the field in JSON.
	if err := provisioning.WriteLockFile(lockFilePath(home), lf); err != nil {
		t.Fatal(err)
	}
	cfg := &clientconfig.Config{ServerURL: "http://127.0.0.1:1/mcp", ServerName: "cartographer", Agents: []string{"codex"}}
	stubBareStatus(t, &client.Health{Version: "v1.0.0"}, nil, contract.Manifest{Revision: "rev1"})
	snap := snapshotForConfig(home, cfg, false)
	var p *providerStatus
	for i := range snap.Providers {
		if snap.Providers[i].Name == "codex" {
			p = &snap.Providers[i]
		}
	}
	if p == nil || p.State != "drift" || len(p.PendingPrune) != 1 || p.PendingPrune[0].Workspace != ws {
		t.Fatalf("snapshot provider = %+v", p)
	}
	js := withStdout(t, func() { renderStatus("json", snap, 1) })
	var back statusSnapshot
	if err := json.Unmarshal([]byte(js), &back); err != nil || !strings.Contains(js, `"pending_prune"`) {
		t.Errorf("json = %s (%v)", js, err)
	}
	text := withStdout(t, func() { renderStatus("", snap, 1) })
	if !strings.Contains(text, "prune pending in "+ws) || !strings.Contains(text, "next sync retries") || strings.Contains(text, "no itemized difference") {
		t.Errorf("text = %q", text)
	}
}

// Refs #2216: a provider whose only pending entries wait for `cartographer
// approve` (a hook never applied: needs_approval; approval_stale) carries
// approval_pending; any real drift next to them keeps it off.
func TestOnlyWaitingForApproval(t *testing.T) {
	wait := statusArtifact{Kind: "hook", Name: "guard", Trust: "needs_approval"}
	stale := statusArtifact{Kind: "hook", Name: "g2", Trust: "approval_stale"}
	real := statusArtifact{Kind: "skill", Name: "s", Trust: "trusted"}
	if onlyWaitingForApproval(nil, nil) {
		t.Error("nothing pending is not approval pending")
	}
	if !onlyWaitingForApproval([]statusArtifact{wait}, []statusArtifact{stale}) {
		t.Error("added needs_approval and updated approval_stale are approval pending")
	}
	if onlyWaitingForApproval([]statusArtifact{wait, real}, nil) {
		t.Error("real drift next to a waiting hook stays drift")
	}
}

func TestStatus_NeverAppliedHookIsApprovalPending(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	hook := contract.Artifact{Kind: "hook", Name: "guard", Source: "kb:kb-a", ContentHash: "h", Files: []contract.ArtifactFile{{Path: "hook.json", Content: []byte(`{"event":"SessionStart","command":"./run.sh"}`)}}}
	skill := contract.Artifact{Kind: "skill", Name: "plain", Source: "kb:kb-a", ContentHash: "s1", Files: []contract.ArtifactFile{{Path: "SKILL.md", Content: []byte("---\nname: plain\ndescription: d\n---\n")}}}
	cfg := &clientconfig.Config{ServerURL: "http://127.0.0.1:1/mcp", ServerName: "cartographer", Agents: []string{"claude"}}
	for _, tc := range []struct {
		name string
		arts []contract.Artifact
		want bool
	}{
		{"only the hook", []contract.Artifact{hook}, true},
		{"hook and a real change", []contract.Artifact{hook, skill}, false},
	} {
		stubBareStatus(t, &client.Health{Version: "v1.0.0"}, nil, contract.Manifest{Revision: "rev1", Artifacts: tc.arts})
		snap := snapshotForConfig(home, cfg, false)
		for _, p := range snap.Providers {
			if p.Name == "claude" && (p.State != "drift" || p.ApprovalPending != tc.want) {
				t.Errorf("%s: state %q approval_pending %v, want drift and %v (added %+v)", tc.name, p.State, p.ApprovalPending, tc.want, p.Added)
			}
		}
	}
}
