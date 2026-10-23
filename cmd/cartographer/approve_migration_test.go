package main

import (
	"bytes"
	"os"
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

func TestApprovalsMigratedMarkerLivesWithTheApprovals(t *testing.T) {
	dir := t.TempDir()
	cfg := &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", ApprovalsMigrated: true}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := clientconfig.Load(dir)
	if err != nil || !loaded.ApprovalsMigrated {
		t.Fatalf("marker lost across Save/Load: %v", err)
	}
	if err := clientconfig.Save(dir, loaded); err != nil {
		t.Fatal(err)
	}
	again, _ := clientconfig.Load(dir)
	if !again.ApprovalsMigrated {
		t.Fatal("marker cleared by a later save")
	}
}

// The state file is a cache and may be deleted or corrupt: that must not re-arm
// the migration over a revoke (D1166).
func TestMigratePreApprovalInstalls_StateFileLossDoesNotUndoARevoke(t *testing.T) {
	dir := t.TempDir()
	cfg := &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", Agents: []string{"claude"}}
	_, lf, manifests, projs := migrationFixture("h1", "h1")
	migratePreApprovalInstalls(&bytes.Buffer{}, cfg, lf, manifests, projs, true)
	cfg.RevokeMCP("kb-a", "hook/guard")
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range []bool{false, true} {
		if corrupt {
			if err := os.WriteFile(clientconfig.StatePath(dir), []byte("{not json"), 0o644); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Remove(clientconfig.StatePath(dir)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		loaded, err := clientconfig.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		migratePreApprovalInstalls(&out, loaded, lf, manifests, projs, true)
		if len(loaded.MCPApprovals) != 0 || out.Len() != 0 {
			t.Errorf("corrupt=%v: revoke undone: %+v %q", corrupt, loaded.MCPApprovals, out.String())
		}
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
	if !strings.Contains(out, "Approve hook guard") || strings.Contains(out, "plain") {
		t.Errorf("listing wrong:\n%s", out)
	}
	cfg, _ := clientconfig.Load(home)
	if len(cfg.MCPApprovals) != 0 {
		t.Errorf("approved without --yes: %+v", cfg.MCPApprovals)
	}
}

func TestCmdApprovePending_YesApprovesAllAndKBFilters(t *testing.T) {
	home := pendingFixture(t, false)
	if code := cmdApprove([]string{"--kb", "other", "--pending", "--yes"}); code != 0 {
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

func TestCmdApprovePending_ShowsWhatWillRun(t *testing.T) {
	pendingFixture(t, false)
	var code int
	out := withStdout(t, func() { code = cmdApprove([]string{"--pending", "--yes"}) })
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"Approve hook guard from KB kb-a", `"command":"./run.sh"`, "SCRIPT-BODY", "run.sh", "(executable)"} {
		if !strings.Contains(out, want) {
			t.Errorf("--pending must show %q before approving:\n%s", want, out)
		}
	}
}

func TestCmdApprovePending_LeavesMCPDescriptorsToTheSingleForm(t *testing.T) {
	home := pendingFixture(t, false)
	approveManifest = func(*clientconfig.Config) (provisioning.Manifest, error) {
		return provisioning.Manifest{Artifacts: []provisioning.Artifact{
			{Kind: "mcp", Name: "tools", Source: "kb:kb-a", ContentHash: "mcp-hash-0123456789", Files: []provisioning.ArtifactFile{
				{Path: "tools.json", Content: []byte(`{"type":"http","url":"https://mcp.example.com/mcp"}`)},
			}},
		}}, nil
	}
	var code int
	out := withStdout(t, func() { code = cmdApprove([]string{"--pending", "--yes"}) })
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "cartographer approve mcp tools --kb kb-a") {
		t.Errorf("the MCP descriptor must be named with its own command:\n%s", out)
	}
	cfg, _ := clientconfig.Load(home)
	if len(cfg.MCPApprovals) != 0 {
		t.Errorf("--pending --yes approved an MCP descriptor: %+v", cfg.MCPApprovals)
	}
}

func TestCmdApprovePending_EscapesWhatTheServerChose(t *testing.T) {
	pendingFixture(t, false)
	approveManifest = func(*clientconfig.Config) (provisioning.Manifest, error) {
		return provisioning.Manifest{Artifacts: []provisioning.Artifact{
			{Kind: "hook", Name: "evil\nApproved: all\x1b[2J", Source: "kb:kb-a", ContentHash: "h-0123456789ab", Files: []provisioning.ArtifactFile{
				{Path: "hook.json", Content: []byte("{}")},
				{Path: "run\n.sh", Content: []byte("echo hi\x1b[31m\n"), Executable: true},
			}},
		}}, nil
	}
	out := withStdout(t, func() { cmdApprove([]string{"--pending", "--yes"}) })
	for _, raw := range []string{"\x1b", "evil\nApproved", "run\n.sh"} {
		if strings.Contains(out, raw) {
			t.Errorf("output carries raw %q:\n%q", raw, out)
		}
	}
	if !strings.Contains(out, `evil\nApproved: all\x1b[2J`) {
		t.Errorf("name not shown escaped:\n%s", out)
	}
	// The single form's header and file list go through the same path.
	out = withStdout(t, func() { cmdApprove([]string{"hook", "evil\nApproved: all\x1b[2J", "--yes"}) })
	if strings.Contains(out, "\x1b") || strings.Contains(out, "evil\nApproved") {
		t.Errorf("single form prints raw server text:\n%q", out)
	}
}

// Text a KB serves can only read as what it is: a bidi override or a zero-width
// character would make a previewed command read as another.
func TestEscapeControl_NonPrintableRunesAreEscaped(t *testing.T) {
	for in, want := range map[string]string{
		"rm -rf /\u202e":        `rm -rf /\u202e`,
		"a\u200bb":              `a\u200bb`,
		"\ufeffx":               `\ufeffx`,
		"line\nforged":          `line\nforged`,
		"\x1b[2J":               `\x1b[2J`,
		"tab\tkept, caff\u00e8": "tab\tkept, caff\u00e8",
	} {
		if got := escapeControl(in); got != want {
			t.Errorf("escapeControl(%q) = %q, want %q", in, got, want)
		}
	}
}
