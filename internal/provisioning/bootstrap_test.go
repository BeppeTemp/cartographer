package provisioning_test

// Tests for the client-side bootstrap hook (D60, WP4): EnsureBootstrapHook
// materializes+registers the same hook.json+script schema as KB hooks (D57/
// D58/D59), but without going through a KB/manifest — see bootstrap.go. Covers:
//   - materialization + registration for claude/codex/opencode, idempotent;
//   - removal via PruneManaged (same generic mechanism as KB hooks,
//     no dedicated removal code);
//   - protection from ComputeDiff/Apply: an empty server manifest does not
//     make it disappear as an "orphan";
//   - reserved-name collision in a KB manifest.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func TestEnsureBootstrapHook_Claude_MaterializzaERegistra(t *testing.T) {
	baseDir := t.TempDir()

	lock, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderClaudeCode, provisioning.Lock{}, false)
	if err != nil {
		t.Fatalf("EnsureBootstrapHook: %v", err)
	}

	hookDir := filepath.Join(baseDir, ".claude", "hooks", provisioning.BootstrapHookName)
	for _, f := range []string{"hook.json", "bootstrap.sh"} {
		if _, err := os.Stat(filepath.Join(hookDir, f)); err != nil {
			t.Errorf("expected file %s: %v", f, err)
		}
	}
	scriptData, err := os.ReadFile(filepath.Join(hookDir, "bootstrap.sh"))
	if err != nil {
		t.Fatalf("read bootstrap.sh: %v", err)
	}
	if !strings.Contains(string(scriptData), "cartographer sync --auto-trust") {
		t.Errorf("bootstrap.sh does not call `cartographer sync --auto-trust`: %s", scriptData)
	}

	settingsPath := filepath.Join(baseDir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}
	groups := settings.Hooks["SessionStart"]
	if len(groups) != 1 || len(groups[0].Hooks) != 1 {
		t.Fatalf("expected 1 SessionStart entry, got: %+v", settings.Hooks)
	}
	wantCmd := filepath.Join(hookDir, "bootstrap.sh")
	if groups[0].Hooks[0].Command != wantCmd {
		t.Errorf("command: expected %q, got %q", wantCmd, groups[0].Hooks[0].Command)
	}

	// ManagedFile entries recorded in the returned Lock, for future pruning.
	if len(lock.Managed) != 2 {
		t.Fatalf("expected 2 ManagedFile (hook.json+bootstrap.sh), got %d: %+v", len(lock.Managed), lock.Managed)
	}
	for _, mf := range lock.Managed {
		if mf.Kind != "hook" || mf.Name != provisioning.BootstrapHookName {
			t.Errorf("unexpected ManagedFile: %+v", mf)
		}
	}

	// Idempotent re-run: no duplicates in settings.json, same Lock.
	lock2, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderClaudeCode, lock, false)
	if err != nil {
		t.Fatalf("EnsureBootstrapHook (2): %v", err)
	}
	if len(lock2.Managed) != 2 {
		t.Fatalf("re-run: expected 2 ManagedFile, got %d", len(lock2.Managed))
	}
	data2, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json (2): %v", err)
	}
	var settings2 struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data2, &settings2); err != nil {
		t.Fatalf("parse settings.json (2): %v", err)
	}
	if len(settings2.Hooks["SessionStart"]) != 1 || len(settings2.Hooks["SessionStart"][0].Hooks) != 1 {
		t.Fatalf("re-run: still expected 1 SessionStart entry, got: %+v", settings2.Hooks)
	}
}

func TestEnsureBootstrapHook_Claude_ScriptPreesistenteNonEseguibile(t *testing.T) {
	baseDir := t.TempDir()
	hookDir := filepath.Join(baseDir, ".claude", "hooks", provisioning.BootstrapHookName)
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing bootstrap.sh without the executable bit (written 0600 by
	// an earlier version): WriteFile does not update its permissions, the
	// explicit Chmod does — regression "Permission denied on every SessionStart".
	if err := os.WriteFile(filepath.Join(hookDir, "bootstrap.sh"), []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderClaudeCode, provisioning.Lock{}, false); err != nil {
		t.Fatalf("EnsureBootstrapHook: %v", err)
	}

	info, err := os.Stat(filepath.Join(hookDir, "bootstrap.sh"))
	if err != nil {
		t.Fatalf("stat bootstrap.sh: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("bootstrap.sh: expected executable bit after EnsureBootstrapHook, mode %v", info.Mode())
	}
}

func TestEnsureBootstrapHook_Codex_MaterializzaERegistra(t *testing.T) {
	baseDir := t.TempDir()

	lock, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderCodex, provisioning.Lock{}, false)
	if err != nil {
		t.Fatalf("EnsureBootstrapHook: %v", err)
	}

	hookDir := filepath.Join(baseDir, ".codex", "hooks", provisioning.BootstrapHookName)
	for _, f := range []string{"hook.json", "bootstrap.sh"} {
		if _, err := os.Stat(filepath.Join(hookDir, f)); err != nil {
			t.Errorf("expected file %s: %v", f, err)
		}
	}

	configPath := filepath.Join(baseDir, ".codex", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.toml not written: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "cartographer:hook:"+provisioning.BootstrapHookName+":begin") {
		t.Errorf("bootstrap block marker missing: %s", content)
	}
	if !strings.Contains(content, "[[hooks.SessionStart]]") {
		t.Errorf("[[hooks.SessionStart]] missing: %s", content)
	}

	if len(lock.Managed) != 2 {
		t.Fatalf("expected 2 ManagedFile, got %d: %+v", len(lock.Managed), lock.Managed)
	}

	// Idempotent re-run: a single marker-delimited block in the file.
	if _, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderCodex, lock, false); err != nil {
		t.Fatalf("EnsureBootstrapHook (2): %v", err)
	}
	data2, _ := os.ReadFile(configPath)
	if n := strings.Count(string(data2), "[[hooks.SessionStart]]"); n != 1 {
		t.Errorf("re-run: expected 1 occurrence of [[hooks.SessionStart]], found %d:\n%s", n, data2)
	}
}

func TestEnsureBootstrapHook_OpenCode_MaterializzaERegistra(t *testing.T) {
	baseDir := t.TempDir()

	lock, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderOpenCode, provisioning.Lock{}, false)
	if err != nil {
		t.Fatalf("EnsureBootstrapHook: %v", err)
	}

	hookDir := filepath.Join(baseDir, ".opencode", "hooks", provisioning.BootstrapHookName)
	for _, f := range []string{"hook.json", "bootstrap.sh"} {
		if _, err := os.Stat(filepath.Join(hookDir, f)); err != nil {
			t.Errorf("expected file %s: %v", f, err)
		}
	}

	// Plugin name: double "cartographer-" prefix documented in D60 (verbatim
	// reuse of openCodePluginRelPath, same logic used for pruning).
	pluginPath := filepath.Join(baseDir, ".config", "opencode", "plugins", "cartographer-cartographer-bootstrap.js")
	data, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("plugin not generated in %s: %v", pluginPath, err)
	}
	if !strings.Contains(string(data), `"session.created"`) {
		t.Errorf("expected session.created event in the plugin: %s", data)
	}

	if len(lock.Managed) != 3 {
		t.Fatalf("expected 3 ManagedFile (hook.json+bootstrap.sh+plugin), got %d: %+v", len(lock.Managed), lock.Managed)
	}

	// Idempotent re-run: the plugin duplicates nothing (file rewritten whole, always identical).
	if _, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderOpenCode, lock, false); err != nil {
		t.Fatalf("EnsureBootstrapHook (2): %v", err)
	}
	data2, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin (2): %v", err)
	}
	if string(data) != string(data2) {
		t.Errorf("re-run: the plugin changed for no reason:\n--- before ---\n%s\n--- after ---\n%s", data, data2)
	}
}

func TestEnsureBootstrapHook_Kiro_NoOp(t *testing.T) {
	baseDir := t.TempDir()
	lock, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderKiro, provisioning.Lock{}, false)
	if err != nil {
		t.Fatalf("EnsureBootstrapHook: %v", err)
	}
	if len(lock.Managed) != 0 {
		t.Fatalf("kiro: expected no ManagedFile, got %+v", lock.Managed)
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".kiro")); err == nil {
		t.Errorf("kiro: no .kiro directory was expected to be created")
	}
}

// TestPruneManaged_BootstrapHook_RimuoveTutto verifies that PruneManaged (the same
// generic mechanism `cartographer disconnect` uses for every KB hook, D57/
// D58/D59) also removes the bootstrap hook — materialized files + native
// registration — without any dedicated removal code (D60).
func TestPruneManaged_BootstrapHook_RimuoveTutto(t *testing.T) {
	baseDir := t.TempDir()

	lock, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderClaudeCode, provisioning.Lock{}, false)
	if err != nil {
		t.Fatalf("EnsureBootstrapHook: %v", err)
	}

	pruned, err := provisioning.PruneManaged(lock.Managed, baseDir, false)
	if err != nil {
		t.Fatalf("PruneManaged: %v", err)
	}
	if len(pruned) != len(lock.Managed) {
		t.Fatalf("expected %d pruned, got %d", len(lock.Managed), len(pruned))
	}

	hookDir := filepath.Join(baseDir, ".claude", "hooks", provisioning.BootstrapHookName)
	for _, f := range []string{"hook.json", "bootstrap.sh"} {
		p := filepath.Join(hookDir, f)
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should have disappeared, err=%v", p, err)
		}
	}

	settingsPath := filepath.Join(baseDir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	if strings.Contains(string(data), provisioning.BootstrapHookName) {
		t.Errorf("settings.json still references the bootstrap hook: %s", data)
	}
	if strings.Contains(string(data), "SessionStart") {
		t.Errorf("settings.json: expected hooks.SessionStart fully cleaned up: %s", data)
	}
}

// TestComputeDiff_BootstrapHook_NonOrfano verifies that an empty server manifest
// (or one anyway lacking the bootstrap hook, which by construction it will never
// have) does not make it appear in Diff.Removed — otherwise Apply would delete
// it as an "orphan" on every sync (D60).
func TestComputeDiff_BootstrapHook_NonOrfano(t *testing.T) {
	lock := provisioning.Lock{
		AppliedRevision: "rev1",
		Managed: []provisioning.ManagedFile{
			{Kind: "hook", Name: provisioning.BootstrapHookName, Path: ".claude/hooks/cartographer-bootstrap/hook.json", ContentHash: "x"},
			{Kind: "hook", Name: provisioning.BootstrapHookName, Path: ".claude/hooks/cartographer-bootstrap/bootstrap.sh", ContentHash: "x"},
		},
	}
	m := provisioning.Manifest{Revision: "rev1"} // no artifacts: as if the server did not know the bootstrap (it never could)

	d := provisioning.ComputeDiff(m, lock)
	if len(d.Removed) != 0 {
		t.Fatalf("expected empty Diff.Removed, got %+v", d.Removed)
	}
	if !d.InSync {
		t.Errorf("expected InSync=true (no material difference beyond the reserved bootstrap)")
	}
}

// TestApply_ManifestVuoto_NonRimuoveBootstrap verifies the same invariant at
// the end-to-end Apply level: an Apply with an empty server manifest must
// neither delete the bootstrap hook's files from disk nor drop it from the new Lock.
func TestApply_ManifestVuoto_NonRimuoveBootstrap(t *testing.T) {
	baseDir := t.TempDir()

	lock, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderClaudeCode, provisioning.Lock{}, false)
	if err != nil {
		t.Fatalf("EnsureBootstrapHook: %v", err)
	}

	res, err := provisioning.Apply(provisioning.Manifest{}, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     lock,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for _, mf := range res.Pruned {
		if mf.Name == provisioning.BootstrapHookName {
			t.Errorf("the bootstrap hook should not have been pruned: %+v", mf)
		}
	}
	var stillManaged int
	for _, mf := range res.NewLock.Managed {
		if mf.Kind == "hook" && mf.Name == provisioning.BootstrapHookName {
			stillManaged++
		}
	}
	if stillManaged != 2 {
		t.Errorf("expected the bootstrap hook still managed (2 files) in the new Lock, found %d", stillManaged)
	}

	scriptPath := filepath.Join(baseDir, ".claude", "hooks", provisioning.BootstrapHookName, "bootstrap.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Errorf("bootstrap.sh should not have disappeared from disk: %v", err)
	}
}

// TestApply_NomeRiservatoInKB_Warning verifies the name collision (D60): a KB
// defining a hook named exactly like BootstrapHookName is ignored by Apply
// with a warning, never materialized.
func TestApply_NomeRiservatoInKB_Warning(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, provisioning.BootstrapHookName, "PostToolUse", "", "./notify.sh")

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for _, w := range res.Written {
		if w.Kind == "hook" {
			t.Errorf("expected no hook file written for the reserved name, got %+v", w)
		}
	}
	foundWarning := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "reserved") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Errorf("expected a warning about the reserved name, got: %v", res.Warnings)
	}
	for _, mf := range res.NewLock.Managed {
		if mf.Name == provisioning.BootstrapHookName {
			t.Errorf("the reserved KB hook should not have ended up in the Lock: %+v", mf)
		}
	}
}
