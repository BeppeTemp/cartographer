package clientconfig_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

const legacyApprovalsYAML = `server_url: http://x/mcp
# my note
mcp_approvals:
  kb-a:
    hook/guard:
      content_hash: h1
      approved_at: 2026-01-01T00:00:00Z
approvals_migrated: true
`

// Approvals, the migration marker and the opt-outs live in the machine file,
// never in the YAML (D-new-approvals-file).
func TestMachineFileHoldsApprovalsAndOptOuts(t *testing.T) {
	dir := t.TempDir()
	cfg := clientconfig.Default()
	if err := cfg.ApproveMCP("kb-a", "hook/guard", "h1", time.Now()); err != nil {
		t.Fatal(err)
	}
	cfg.ApprovalsMigrated, cfg.SyncTimerOptOut = true, true
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if raw := readFile(t, clientconfig.Path(dir)); hasKey(raw, "mcp_approvals") || !hasKey(raw, "approvals_migrated") {
		t.Errorf("the YAML must hold the tombstone and no approval:\n%s", raw)
	}
	if st := readFile(t, clientconfig.StatePath(dir)); !strings.Contains(st, `"sync_timer_opt_out": true`) {
		t.Errorf("the state file lacks the opt-out copy:\n%s", st)
	}
	again, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.MCPApprovals["kb-a"]["hook/guard"].ContentHash != "h1" || !again.ApprovalsMigrated || !again.SyncTimerOptOut || len(again.Warnings) != 0 {
		t.Errorf("round trip lost data: %+v", again)
	}
}

// The first Save over a pre-split machine moves the data with a backup of every
// file that held it, and a revoke afterwards sticks.
func TestMachineFileMigratesTheOldHomesWithBackups(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, legacyApprovalsYAML)
	state := `{"known_kbs": [], "sync_timer_opt_out": true, "tray_opt_out": true}` + "\n"
	if err := os.WriteFile(clientconfig.StatePath(dir), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MCPApprovals["kb-a"]["hook/guard"].ContentHash != "h1" || !cfg.ApprovalsMigrated || !cfg.SyncTimerOptOut || !cfg.TrayOptOut {
		t.Fatalf("legacy homes not read: %+v", cfg)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, clientconfig.Path(dir)+".bak-pre-machine"); got != legacyApprovalsYAML {
		t.Errorf("YAML backup differs:\n%s", got)
	}
	if raw := readFile(t, clientconfig.Path(dir)); hasKey(raw, "mcp_approvals") || !hasKey(raw, "approvals_migrated") || !strings.Contains(raw, "# my note") {
		t.Errorf("YAML after migration:\n%s", raw)
	}
	cfg, err = clientconfig.Load(dir)
	if err != nil || len(cfg.Warnings) != 0 || cfg.MCPApprovals["kb-a"]["hook/guard"].ContentHash != "h1" || !cfg.TrayOptOut {
		t.Fatalf("after migration: %+v %v", cfg, err)
	}
	cfg.RevokeMCP("kb-a", "hook/guard")
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if again, _ := clientconfig.Load(dir); len(again.MCPApprovals) != 0 || !again.ApprovalsMigrated {
		t.Errorf("revoke undone: %+v", again)
	}
}

// With the machine file present, a stale copy in the YAML of a read-only mount
// is ignored: it can never bring a revoked approval back.
func TestMachineFileWinsOverTheLegacyYAML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(clientconfig.MachinePath(dir), []byte(`{"approvals_migrated": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeConfigFile(t, dir, legacyApprovalsYAML)
	cfg, err := clientconfig.Load(dir)
	if err != nil || len(cfg.MCPApprovals) != 0 {
		t.Fatalf("stale YAML approvals granted: %+v %v", cfg.MCPApprovals, err)
	}
}

// Fail closed: an unreadable machine file approves nothing, says so, and is
// never replaced by a save that only knew the empty state.
func TestUnreadableMachineFileApprovesNothing(t *testing.T) {
	for name, content := range map[string]string{"not json": "{broken", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			// The YAML still carries an approval: it must not be granted either.
			writeConfigFile(t, dir, legacyApprovalsYAML)
			if err := os.WriteFile(clientconfig.MachinePath(dir), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := clientconfig.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.MCPApprovals) != 0 || len(cfg.ApprovedMCPHashes()) != 0 {
				t.Fatalf("approved with an unreadable file: %+v", cfg.MCPApprovals)
			}
			if !cfg.ApprovalsMigrated {
				t.Error("an unreadable file must not re-arm the pre-approval migration")
			}
			if len(cfg.Warnings) == 0 || !strings.Contains(strings.Join(cfg.Warnings, "\n"), "unreadable") {
				t.Errorf("no explicit warning: %v", cfg.Warnings)
			}
			// A save that changes nothing machine-side leaves the file as it was,
			// and the old keys of the YAML with it.
			if err := clientconfig.Save(dir, cfg); err != nil {
				t.Fatal(err)
			}
			if raw := readFile(t, clientconfig.Path(dir)); !hasKey(raw, "mcp_approvals") {
				t.Errorf("the legacy approvals were dropped from the YAML:\n%s", raw)
			}
			if got := readFile(t, clientconfig.MachinePath(dir)); got != content {
				t.Errorf("the unreadable file was replaced: %q", got)
			}
			// A new approval cannot be recorded: an error, not a silent loss.
			if err := cfg.ApproveMCP("kb-a", "hook/x", "h2", time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := clientconfig.Save(dir, cfg); err == nil || !strings.Contains(err.Error(), "unreadable") {
				t.Errorf("Save err = %v, want the unreadable-file refusal", err)
			}
			if got := readFile(t, clientconfig.MachinePath(dir)); got != content {
				t.Errorf("the unreadable file was replaced: %q", got)
			}
		})
	}
}

// A machine with nothing to record gets no machine file.
func TestNoMachineFileWithoutMachineData(t *testing.T) {
	dir := t.TempDir()
	if err := clientconfig.Save(dir, clientconfig.Default()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(clientconfig.MachinePath(dir)); !os.IsNotExist(err) {
		t.Errorf("machine file created: %v", err)
	}
}

// The realignment moves the approvals before the registry drops their keys.
func TestRealignMovesTheApprovalsToTheMachineFile(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, legacyApprovalsYAML)
	if _, _, err := clientconfig.RealignFile(dir, "0.30.0"); err != nil {
		t.Fatal(err)
	}
	if raw := readFile(t, clientconfig.Path(dir)); hasKey(raw, "mcp_approvals") || !hasKey(raw, "approvals_migrated") {
		t.Errorf("mcp_approvals must be gone and the tombstone kept:\n%s", raw)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil || cfg.MCPApprovals["kb-a"]["hook/guard"].ContentHash != "h1" || !cfg.ApprovalsMigrated || len(cfg.Warnings) != 0 {
		t.Fatalf("after realign: %+v %v", cfg, err)
	}
}

// After the migration the machine file can vanish (deleted, a dangling link):
// the tombstone in the YAML then forbids both a new migration and any approval.
func TestDeletedMachineFileApprovesNothingAndDoesNotMigrate(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, legacyApprovalsYAML)
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(clientconfig.MachinePath(dir)); err != nil {
		t.Fatal(err)
	}
	again, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.MCPApprovals) != 0 || !again.ApprovalsMigrated {
		t.Fatalf("approvals=%+v migrated=%v: want none and the migration spent", again.MCPApprovals, again.ApprovalsMigrated)
	}
	if !strings.Contains(strings.Join(again.Warnings, "\n"), "cartographer approve") {
		t.Errorf("no warning that the approvals must be given again: %v", again.Warnings)
	}
}

// An older client reads the YAML and the state file only: after the migration it
// finds the marker set, no approval and the opt-outs, so it asks and re-installs
// nothing it should not.
func TestOlderClientViewAfterMigration(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, legacyApprovalsYAML+"sync_timer_opt_out: true\n")
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.TrayOptOut = true
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, clientconfig.Path(dir))
	if !regexp.MustCompile(`(?m)^approvals_migrated: true`).MatchString(raw) || hasKey(raw, "mcp_approvals") {
		t.Errorf("YAML as an older client sees it:\n%s", raw)
	}
	st := readFile(t, clientconfig.StatePath(dir))
	if !strings.Contains(st, `"sync_timer_opt_out": true`) || !strings.Contains(st, `"tray_opt_out": true`) {
		t.Errorf("state as an older client sees it:\n%s", st)
	}
}

// An unreadable machine file keeps the old keys of the YAML (no backup exists
// yet) and the realignment refuses to drop them.
func TestUnreadableMachineFileKeepsTheLegacyKeysAndStopsRealign(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, legacyApprovalsYAML+"sync_timer_opt_out: true\n")
	if err := os.WriteFile(clientconfig.MachinePath(dir), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := clientconfig.RealignFile(dir, "0.30.0"); err == nil {
		t.Fatal("realign must stop while the machine file is unreadable")
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, clientconfig.Path(dir))
	if !hasKey(raw, "mcp_approvals") || !hasKey(raw, "sync_timer_opt_out") {
		t.Errorf("legacy keys dropped without a backup:\n%s", raw)
	}
}

// The state file's copy of an opt-out is what an unreadable machine file would
// have held: it neither makes every save fail nor reads as false.
func TestUnreadableMachineFileUsesTheStateOptOuts(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "server_url: http://x/mcp\napprovals_migrated: true\n")
	if err := os.WriteFile(clientconfig.StatePath(dir), []byte(`{"known_kbs": [], "sync_timer_opt_out": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clientconfig.MachinePath(dir), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil || !cfg.SyncTimerOptOut {
		t.Fatalf("opt-out lost: %+v %v", cfg, err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatalf("an unchanged opt-out must not refuse the save: %v", err)
	}
	cfg.SyncTimerOptOut = false
	if err := clientconfig.Save(dir, cfg); err == nil {
		t.Error("a changed opt-out must be refused while the file is unreadable")
	}
}
