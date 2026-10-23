package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func migrationHook(hash string) provisioning.Artifact {
	return provisioning.Artifact{Kind: "hook", Name: "guard", Source: "kb:kb-a", ContentHash: hash, Files: []provisioning.ArtifactFile{
		{Path: "hook.json", Content: []byte(`{"event":"SessionStart","command":"./run.sh"}`)},
		{Path: "run.sh", Content: []byte("#!/bin/sh\n"), Executable: true},
	}}
}

// migrationFixture returns a config, the lock of a pre-D1141 client that has
// the hook installed at lockHash, and the manifests serving serveHash.
func migrationFixture(lockHash, serveHash string) (*clientconfig.Config, provisioning.LockFile, map[string]provisioning.Manifest, []syncProjection) {
	proj := globalProjection("claude", "/base", []string{"kb-a"})
	var lf provisioning.LockFile
	if lockHash != "" {
		lf.SetProjection(proj.Key(), provisioning.Lock{Provider: "claude", Managed: []provisioning.ManagedFile{
			{Kind: "hook", Name: "guard", Path: "hooks/guard/run.sh", ContentHash: lockHash, Source: "kb:kb-a"},
			{Kind: "hook", Name: "guard", Path: "hooks/guard/hook.json", ContentHash: lockHash, Source: "kb:kb-a"},
		}}, "")
	}
	manifests := map[string]provisioning.Manifest{projectionKey(proj): {Artifacts: []provisioning.Artifact{migrationHook(serveHash)}}}
	return &clientconfig.Config{}, lf, manifests, []syncProjection{proj}
}

func TestMigratePreApprovalInstalls_ApprovesUnchangedOnce(t *testing.T) {
	cfg, lf, manifests, projs := migrationFixture("h1", "h1")
	var out bytes.Buffer
	if !migratePreApprovalInstalls(&out, cfg, lf, manifests, projs, true) {
		t.Fatal("first run must report a change")
	}
	if got := cfg.MCPApprovals["kb-a"]["hook/guard"].ContentHash; got != "h1" {
		t.Fatalf("approval = %q (%+v)", got, cfg.MCPApprovals)
	}
	if want := "approved hook/guard [kb:kb-a]: installed before approvals existed, unchanged\n"; out.String() != want {
		t.Errorf("output %q, want %q", out.String(), want)
	}
	if !cfg.ApprovalsMigrated {
		t.Error("marker not set")
	}

	// A revoke is never undone: the marker is set, so the same lock entry that
	// qualified before no longer does.
	cfg.RevokeMCP("kb-a", "hook/guard")
	out.Reset()
	if migratePreApprovalInstalls(&out, cfg, lf, manifests, projs, true) || len(cfg.MCPApprovals) != 0 || out.Len() != 0 {
		t.Errorf("second run re-armed: %+v %q", cfg.MCPApprovals, out.String())
	}
}

func TestMigratePreApprovalInstalls_NotMigrated(t *testing.T) {
	for name, tc := range map[string]struct{ lock, serve string }{
		"hash differs from the lock": {"old", "new"},
		"not in the lock":            {"", "h1"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, lf, manifests, projs := migrationFixture(tc.lock, tc.serve)
			var out bytes.Buffer
			migratePreApprovalInstalls(&out, cfg, lf, manifests, projs, true)
			if len(cfg.MCPApprovals) != 0 || out.Len() != 0 {
				t.Errorf("migrated: %+v %q", cfg.MCPApprovals, out.String())
			}
			if !cfg.ApprovalsMigrated {
				t.Error("the single run must still be spent")
			}
		})
	}
}

func TestMigratePreApprovalInstalls_KeepsExistingApprovalAndWaitsForACompletePull(t *testing.T) {
	cfg, lf, manifests, projs := migrationFixture("h1", "h1")
	if migratePreApprovalInstalls(&bytes.Buffer{}, cfg, lf, manifests, projs, false) || cfg.ApprovalsMigrated {
		t.Fatal("an incomplete pull must not spend the one run")
	}
	if err := cfg.ApproveMCP("kb-a", "hook/guard", "stale", time.Now()); err != nil {
		t.Fatal(err)
	}
	migratePreApprovalInstalls(&bytes.Buffer{}, cfg, lf, manifests, projs, true)
	if got := cfg.MCPApprovals["kb-a"]["hook/guard"].ContentHash; got != "stale" {
		t.Errorf("a recorded approval, even a stale one, is a decision: %q", got)
	}
}

func TestApprovalsMigratedMarkerPersistsInTheStateFile(t *testing.T) {
	dir := t.TempDir()
	cfg := &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", ApprovalsMigrated: true}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.ApprovalsMigrated {
		t.Fatal("marker lost across Save/Load")
	}
	// Any later save of a loaded config keeps it.
	if err := clientconfig.Save(dir, loaded); err != nil {
		t.Fatal(err)
	}
	again, _ := clientconfig.Load(dir)
	if !again.ApprovalsMigrated {
		t.Fatal("marker cleared by a later save")
	}
}

func pendingFixture(t *testing.T, interactive bool) string {
	t.Helper()
	home, _ := approveFixture(t, interactive)
	return home
}

func TestCmdApprovePending_NonInteractiveNeedsYes(t *testing.T) {
	home := pendingFixture(t, false)
	var code int
	out := withStdout(t, func() { code = cmdApprove([]string{"--pending"}) })
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(out, "hook/guard") || strings.Contains(out, "skill/plain") {
		t.Errorf("listing wrong:\n%s", out)
	}
	cfg, _ := clientconfig.Load(home)
	if len(cfg.MCPApprovals) != 0 {
		t.Errorf("approved without --yes: %+v", cfg.MCPApprovals)
	}
}

func TestCmdApprovePending_YesApprovesAllAndKBFilters(t *testing.T) {
	home := pendingFixture(t, false)
	if code := cmdApprove([]string{"--pending", "--kb", "other", "--yes"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	cfg, _ := clientconfig.Load(home)
	if len(cfg.MCPApprovals) != 0 {
		t.Fatalf("--kb other must approve nothing: %+v", cfg.MCPApprovals)
	}
	if code := cmdApprove([]string{"--pending", "--yes"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	cfg, _ = clientconfig.Load(home)
	if got := cfg.MCPApprovals["kb-a"]["hook/guard"].ContentHash; got != "hook-hash-0123456789" {
		t.Fatalf("approval = %q (%+v)", got, cfg.MCPApprovals)
	}
	if _, ok := cfg.MCPApprovals["kb-a"]["skill/plain"]; ok {
		t.Error("a plain skill needs no approval and must not get one")
	}
	out := withStdout(t, func() { cmdApprove([]string{"--pending", "--yes"}) })
	if !strings.Contains(out, "nothing is waiting") {
		t.Errorf("second run: %q", out)
	}
}
