package provisioning_test

// Round-trip test for the complete prune (D63, WP7): connect (MCP config + bootstrap
// hook + skill/agent/hook/instructions from the KB manifest, for every provider) →
// disconnect (configurator.Remove + provisioning.PruneManaged on the whole
// lockfile) must bring targetDir back exactly to its initial state, with only two
// documented exceptions:
//
//   - provisioning's own root directories (.claude, .codex, .kiro,
//     .opencode, .config, .config/opencode — see provisioningRootDirs in
//     provisioning.go) are never removed even if they end up empty: they are
//     deliberate boundaries, not a residue of this feature (they could hold
//     other content, not managed by Cartographer, in a real system);
//   - .claude.json is NEVER deleted (it's Claude Code's shared state):
//     it stays on disk, but reduced to "{}" once just the "cartographer"
//     entry is removed — it wasn't there at the start, so it too is a residue
//     accepted by construction (see the absolute rule in configurator.Remove).
//
// Every other file/directory created by connect must disappear: skill/agent/hook/
// instructions materialized for claude/codex/kiro/opencode, the entries in
// settings.json/config.toml, the OpenCode plugins, the kiro/opencode MCP configs.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// walkAll returns every path under root (files and directories, root excluded),
// relative to root, sorted — used to snapshot a directory tree before/after the
// connect→disconnect round trip.
func walkAll(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(paths)
	return paths
}

func TestRoundTrip_ConnectDisconnect_NessunResiduo(t *testing.T) {
	targetDir := t.TempDir()

	// --- Initial state: pre-existing user files, untouched by Cartographer ---
	claudeSettingsPath := filepath.Join(targetDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(claudeSettingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	userSettings := `{"model":"opus","permissions":{"allow":["Bash(ls:*)"]}}`
	if err := os.WriteFile(claudeSettingsPath, []byte(userSettings), 0o644); err != nil {
		t.Fatal(err)
	}

	codexConfigPath := filepath.Join(targetDir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codexConfigPath), 0o755); err != nil {
		t.Fatal(err)
	}
	userCodexConfig := "# my config\nmodel = \"gpt-5.3-codex\"\n"
	if err := os.WriteFile(codexConfigPath, []byte(userCodexConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	initialSnapshot := walkAll(t, targetDir)

	// --- KB with skill + agent + hook ---
	kbRoot := t.TempDir()
	skillDir := filepath.Join(kbRoot, "skills", "roundtrip-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: roundtrip-skill\ndescription: Round-trip test skill\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	agentsDir := filepath.Join(kbRoot, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "roundtrip-agent.md"),
		[]byte("---\ndescription: Round-trip test agent\n---\nBody agent.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	hookDir := filepath.Join(kbRoot, "hooks", "roundtrip-hook")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "hook.json"),
		[]byte(`{"event":"PostToolUse","command":"./notify.sh"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "notify.sh"), []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	mcpDir := filepath.Join(kbRoot, "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mcpDir, "roundtrip-mcp.json"),
		[]byte(`{"type":"http","url":"https://roundtrip.example.com/mcp","headers":{"Authorization":"Bearer ${ROUNDTRIP_TOKEN}"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	// The "mcp" kind is never signed by BuildManifest, regardless of autoTrust
	// (D69 WP5, a stricter policy than the other kinds) — simulate here the
	// operator's explicit approval to exercise the materialization/prune path
	// in the round trip.
	for i := range m.Artifacts {
		if m.Artifacts[i].Kind == "mcp" && m.Artifacts[i].Name == "roundtrip-mcp" {
			m.Artifacts[i].Signed = true
		}
	}

	providers := []configurator.Provider{
		configurator.ProviderClaudeCode,
		configurator.ProviderCodex,
		configurator.ProviderKiro,
		configurator.ProviderOpenCode,
	}
	scfg := &configurator.ServerConfig{Name: "cartographer", URL: "http://localhost:8080/mcp"}

	// --- Connect: MCP config + bootstrap hook + manifest, same order as doConnect ---
	lockPath := filepath.Join(targetDir, provisioning.LockFileName)
	lf := provisioning.LockFile{Providers: map[string]provisioning.Lock{}}

	for _, p := range providers {
		r, err := configurator.Emit(scfg, p)
		if err != nil {
			t.Fatalf("Emit %s: %v", p, err)
		}
		if _, err := configurator.Apply([]*configurator.EmitResult{r}, targetDir, false); err != nil {
			t.Fatalf("configurator.Apply %s: %v", p, err)
		}

		lock, err := provisioning.EnsureBootstrapHook(targetDir, p, lf.ForProvider(string(p)), false)
		if err != nil {
			t.Fatalf("EnsureBootstrapHook %s: %v", p, err)
		}
		lf.SetProvider(string(p), lock)

		res, err := provisioning.Apply(m, provisioning.ApplyOptions{
			KBRoots:       map[string]string{"kb": kbRoot},
			Provider:      p,
			BaseDir:       targetDir,
			Lock:          lf.ForProvider(string(p)),
			SkipLockWrite: true,
		})
		if err != nil {
			t.Fatalf("provisioning.Apply %s: %v", p, err)
		}
		lf.SetProvider(string(p), res.NewLock)
	}
	if err := provisioning.WriteLockFile(lockPath, lf); err != nil {
		t.Fatalf("WriteLockFile: %v", err)
	}

	// Sanity check: something must have actually been materialized,
	// otherwise the round trip would be true for the wrong reason (nothing to
	// disconnect).
	afterConnect := walkAll(t, targetDir)
	if len(afterConnect) <= len(initialSnapshot) {
		t.Fatalf("connect: expected new files/dirs to be created, snapshot unchanged: %v", afterConnect)
	}

	// --- Disconnect: same path as doDisconnect (configurator.Remove + PruneManaged) ---
	lockFile, err := provisioning.ReadLockFile(lockPath)
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	for _, p := range providers {
		if _, err := configurator.Remove(scfg, p, targetDir, false); err != nil {
			t.Fatalf("configurator.Remove %s: %v", p, err)
		}
		lock := lockFile.ForProvider(string(p))
		if _, err := provisioning.PruneManaged(lock.Managed, targetDir, false); err != nil {
			t.Fatalf("PruneManaged %s: %v", p, err)
		}
		delete(lockFile.Providers, string(p))
	}
	if len(lockFile.Providers) == 0 {
		if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove lockfile: %v", err)
		}
	} else if err := provisioning.WriteLockFile(lockPath, lockFile); err != nil {
		t.Fatalf("WriteLockFile (residue): %v", err)
	}

	// --- Verify: targetDir returns to its initial state, except for the two documented exceptions ---
	finalSnapshot := walkAll(t, targetDir)

	allowedExtra := map[string]bool{
		// Roots freshly created by this run (kiro/opencode didn't exist
		// before): pruneEmptyDirs boundaries, never removed even if they end up
		// empty — see provisioningRootDirs.
		".kiro":            true,
		".opencode":        true,
		".config":          true,
		".config/opencode": true,
		// .claude.json: never deleted by construction (absolute rule), even
		// if reduced to "{}" once just the cartographer entry is removed.
		".claude.json": true,
	}

	initialSet := make(map[string]bool, len(initialSnapshot))
	for _, p := range initialSnapshot {
		initialSet[p] = true
	}

	var unexpected []string
	for _, p := range finalSnapshot {
		if initialSet[p] || allowedExtra[p] {
			continue
		}
		unexpected = append(unexpected, p)
	}
	if len(unexpected) > 0 {
		t.Errorf("round-trip: unexpected residue after disconnect: %v\nfull final snapshot: %v", unexpected, finalSnapshot)
	}

	// The allowed exceptions must be genuinely empty (no files inside
	// them), otherwise the prune failed to clean up something it should have.
	for _, dir := range []string{".kiro", ".opencode", ".config/opencode"} {
		full := filepath.Join(targetDir, dir)
		entries, err := os.ReadDir(full)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("ReadDir %s: %v", dir, err)
		}
		if len(entries) != 0 {
			t.Errorf("round-trip: %s expected empty, contains %v", dir, entries)
		}
	}

	// Every pre-existing user file survives, Cartographer content excluded.
	// .claude/settings.json: structural comparison (not byte-exact — JSON
	// re-serialization doesn't guarantee the same key order), but the
	// user keys must be intact and "hooks" (added then removed)
	// must not remain.
	gotSettings, err := os.ReadFile(claudeSettingsPath)
	if err != nil {
		t.Fatalf("settings.json disappeared: %v", err)
	}
	var gotMap, wantMap map[string]any
	if err := json.Unmarshal(gotSettings, &gotMap); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v\n%s", err, gotSettings)
	}
	if err := json.Unmarshal([]byte(userSettings), &wantMap); err != nil {
		t.Fatal(err)
	}
	if _, ok := gotMap["hooks"]; ok {
		t.Errorf("settings.json: residual hooks key: %s", gotSettings)
	}
	delete(gotMap, "hooks")
	if !jsonEqual(gotMap, wantMap) {
		t.Errorf("settings.json: user content altered: got %v, want %v", gotMap, wantMap)
	}

	// .codex/config.toml: the managed blocks (mcp + hook) must be gone;
	// the user content remains (except for residual blank lines accumulated by
	// successive append/remove on blocktext, hence not byte-exact here).
	gotCodex, err := os.ReadFile(codexConfigPath)
	if err != nil {
		t.Fatalf("config.toml disappeared: %v", err)
	}
	if strings.Contains(string(gotCodex), "cartographer") {
		t.Errorf("config.toml: residual cartographer block: %s", gotCodex)
	}
	if strings.TrimSpace(string(gotCodex)) != strings.TrimSpace(userCodexConfig) {
		t.Errorf("config.toml: user content altered: got %q, want (trimmed) %q", gotCodex, userCodexConfig)
	}

	// .claude.json: never deleted, but reduced to "{}" (cartographer entry
	// removed, mcpServers emptied and removed).
	claudeJSONPath := filepath.Join(targetDir, ".claude.json")
	gotClaudeJSON, err := os.ReadFile(claudeJSONPath)
	if err != nil {
		t.Fatalf(".claude.json: expected present (never deleted), error: %v", err)
	}
	var claudeJSONMap map[string]any
	if err := json.Unmarshal(gotClaudeJSON, &claudeJSONMap); err != nil {
		t.Fatalf(".claude.json is not valid JSON: %v", err)
	}
	if len(claudeJSONMap) != 0 {
		t.Errorf(".claude.json: expected reduced to {}, got %v", claudeJSONMap)
	}
}

// jsonEqual compares two JSON-decoded values for structural equality
// (via re-marshaling: normalizes map key order, irrelevant for the
// intended comparison).
func jsonEqual(a, b any) bool {
	ja, err := json.Marshal(a)
	if err != nil {
		return false
	}
	jb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(ja) == string(jb)
}
