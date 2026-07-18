package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// testSkillDir mirrors provisioning.skillDestDir's (unexported) provider →
// destination-dir mapping, needed here to build managed-skill fixtures on disk.
var testSkillDir = map[string]string{
	"claude":   ".claude",
	"codex":    ".codex",
	"kiro":     ".kiro",
	"opencode": ".opencode",
}

// setupDisconnectFixture builds, in a fresh t.TempDir(), everything a real
// `cartographer connect` would have left behind for each provider: the MCP
// config file (via configurator.Emit/Apply), a v2 lockfile with one managed
// skill file per provider, the skill file itself on disk, and
// .cartographer.yaml listing every provider as connected.
func setupDisconnectFixture(t *testing.T, providers ...string) string {
	t.Helper()
	dir := t.TempDir()

	cfg := &clientconfig.Config{
		ServerURL:  "http://localhost:8080/mcp",
		ServerName: "wiki",
		TokenEnv:   "CARTOGRAPHER_TOKENS",
		Agents:     append([]string(nil), providers...),
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatalf("clientconfig.Save: %v", err)
	}

	scfg := &configurator.ServerConfig{Name: cfg.ServerName, URL: cfg.ServerURL}
	lockFile := provisioning.LockFile{Providers: map[string]provisioning.Lock{}}

	for _, p := range providers {
		r, err := configurator.Emit(scfg, configurator.Provider(p))
		if err != nil {
			t.Fatalf("Emit(%s): %v", p, err)
		}
		if _, err := configurator.Apply([]*configurator.EmitResult{r}, dir, false); err != nil {
			t.Fatalf("Apply(%s): %v", p, err)
		}

		skillRel := filepath.Join(testSkillDir[p], "skills", "demo-skill", "SKILL.md")
		skillFull := filepath.Join(dir, skillRel)
		if err := os.MkdirAll(filepath.Dir(skillFull), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(skillFull, []byte("---\ntype: skill\n---\ndemo\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		lockFile.SetProvider(p, provisioning.Lock{
			AppliedRevision: "deadbeef",
			Managed: []provisioning.ManagedFile{
				{Kind: "skill", Name: "demo-skill", Path: skillRel, ContentHash: "deadbeef"},
			},
		})
	}

	if err := provisioning.WriteLockFile(lockFilePath(dir), lockFile); err != nil {
		t.Fatalf("WriteLockFile: %v", err)
	}

	return dir
}

func mcpServersOf(t *testing.T, path, key string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	servers, ok := root[key].(map[string]any)
	if !ok {
		t.Fatalf("%s: missing or wrong type for %q", path, key)
	}
	return servers
}

// claudeJSONServers reads .claude.json's "mcpServers" map, if present. Unlike
// mcpServersOf it does not fail when the key is absent (D63: configurator.Remove
// drops "mcpServers" entirely once it's emptied, rather than leaving `{}` — see
// isEmptyProviderShell) — callers that only care about a specific entry being gone
// should treat "key absent" as "entry gone" too.
func claudeJSONServers(t *testing.T, path string) (map[string]any, bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	servers, ok := root["mcpServers"].(map[string]any)
	return servers, ok
}

func TestDoDisconnect_RemovesConfigPrunesSkillsAndCleansUp(t *testing.T) {
	dir := setupDisconnectFixture(t, "claude")

	res, err := doDisconnect(disconnectOptions{Providers: []string{"claude"}, Dir: dir})
	if err != nil {
		t.Fatalf("doDisconnect: %v", err)
	}
	if len(res.Providers) != 1 {
		t.Fatalf("len(res.Providers) = %d, want 1", len(res.Providers))
	}
	pr := res.Providers[0]
	if !pr.ConfigRemoved {
		t.Error("ConfigRemoved should be true")
	}
	if len(pr.Pruned) != 1 {
		t.Fatalf("len(pr.Pruned) = %d, want 1", len(pr.Pruned))
	}

	// .claude.json is never deleted (D63): the "wiki" entry must be gone, but the
	// file survives — with the "mcpServers" key itself dropped once it's left
	// empty (see configurator.Remove/isEmptyProviderShell), so we can't assume
	// the key is still present the way mcpServersOf does for other assertions.
	if _, err := os.Stat(filepath.Join(dir, ".claude.json")); err != nil {
		t.Errorf(".claude.json should still exist (never deleted): %v", err)
	}
	if servers, ok := claudeJSONServers(t, filepath.Join(dir, ".claude.json")); ok {
		if _, ok := servers["wiki"]; ok {
			t.Error("wiki entry should have been removed from .claude.json")
		}
	}

	skillPath := filepath.Join(dir, ".claude", "skills", "demo-skill", "SKILL.md")
	if _, err := os.Stat(skillPath); !os.IsNotExist(err) {
		t.Errorf("skill file should have been pruned, stat err = %v", err)
	}

	if _, err := os.Stat(lockFilePath(dir)); !os.IsNotExist(err) {
		t.Errorf("lockfile should have been removed (no providers left), stat err = %v", err)
	}
	// .cartographer.yaml is preserved (D64), not deleted, even with no agents
	// left: it seeds the next `connect` (server_url in particular).
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf(".cartographer.yaml should still exist (never deleted): %v", err)
	}
	if len(cfg.Agents) != 0 {
		t.Errorf("cfg.Agents = %v, want empty", cfg.Agents)
	}
	if cfg.ServerURL != "http://localhost:8080/mcp" {
		t.Errorf("cfg.ServerURL = %q, want preserved as http://localhost:8080/mcp", cfg.ServerURL)
	}
}

// TestDoDisconnect_RoundTripPreservesServerURL verifies the disconnect→connect
// round trip (D64): after disconnecting the last agent, .cartographer.yaml
// still carries the server_url/server_name that was in effect, so a fresh
// `connect` reads it back as the prefill instead of falling back to the
// hardcoded localhost default.
func TestDoDisconnect_RoundTripPreservesServerURL(t *testing.T) {
	dir := setupDisconnectFixture(t, "claude")

	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("clientconfig.Load: %v", err)
	}
	cfg.ServerURL = "https://wiki.example.com/mcp"
	cfg.ServerName = "wiki"
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatalf("clientconfig.Save: %v", err)
	}

	if _, err := doDisconnect(disconnectOptions{Providers: []string{"claude"}, Dir: dir}); err != nil {
		t.Fatalf("doDisconnect: %v", err)
	}

	after, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("clientconfig.Load after disconnect: %v", err)
	}
	if after.ServerURL != "https://wiki.example.com/mcp" {
		t.Errorf("ServerURL = %q, want preserved https://wiki.example.com/mcp", after.ServerURL)
	}
	if after.ServerName != "wiki" {
		t.Errorf("ServerName = %q, want preserved wiki", after.ServerName)
	}
	if len(after.Agents) != 0 {
		t.Errorf("Agents = %v, want empty after disconnecting the only connected provider", after.Agents)
	}
}

func TestDoDisconnect_Idempotent(t *testing.T) {
	dir := setupDisconnectFixture(t, "claude")

	if _, err := doDisconnect(disconnectOptions{Providers: []string{"claude"}, Dir: dir}); err != nil {
		t.Fatalf("first doDisconnect: %v", err)
	}

	res, err := doDisconnect(disconnectOptions{Providers: []string{"claude"}, Dir: dir})
	if err != nil {
		t.Fatalf("second doDisconnect should not error: %v", err)
	}
	pr := res.Providers[0]
	if pr.ConfigRemoved {
		t.Error("second run: ConfigRemoved should be false, nothing left to remove")
	}
	if len(pr.Pruned) != 0 {
		t.Errorf("second run: len(pr.Pruned) = %d, want 0", len(pr.Pruned))
	}
}

func TestDoDisconnect_KeepsOtherProviders(t *testing.T) {
	dir := setupDisconnectFixture(t, "claude", "opencode")

	if _, err := doDisconnect(disconnectOptions{Providers: []string{"claude"}, Dir: dir}); err != nil {
		t.Fatalf("doDisconnect: %v", err)
	}

	// opencode's config entry is untouched.
	mcp := mcpServersOf(t, filepath.Join(dir, "opencode.json"), "mcp")
	if _, ok := mcp["wiki"]; !ok {
		t.Error("opencode's wiki entry should be untouched")
	}

	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	if _, ok := lf.Providers["opencode"]; !ok {
		t.Error("opencode lock entry should remain")
	}
	if _, ok := lf.Providers["claude"]; ok {
		t.Error("claude lock entry should be gone")
	}

	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("clientconfig.Load: %v", err)
	}
	if cfg.HasAgent("claude") {
		t.Error("claude should be removed from agents")
	}
	if !cfg.HasAgent("opencode") {
		t.Error("opencode should remain in agents")
	}

	opencodeSkill := filepath.Join(dir, ".opencode", "skills", "demo-skill", "SKILL.md")
	if _, err := os.Stat(opencodeSkill); err != nil {
		t.Errorf("opencode's managed skill file should be untouched: %v", err)
	}
}

func TestDoDisconnect_DryRunDoesNotWrite(t *testing.T) {
	dir := setupDisconnectFixture(t, "claude")

	res, err := doDisconnect(disconnectOptions{Providers: []string{"claude"}, Dir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("doDisconnect (dry-run): %v", err)
	}
	pr := res.Providers[0]
	if !pr.ConfigRemoved {
		t.Error("dry-run should still report what would be removed (ConfigRemoved = true)")
	}
	if len(pr.Pruned) != 1 {
		t.Errorf("dry-run: len(pr.Pruned) = %d, want 1", len(pr.Pruned))
	}

	if _, err := os.Stat(clientconfig.Path(dir)); err != nil {
		t.Errorf(".cartographer.yaml should be untouched in dry-run: %v", err)
	}
	if _, err := os.Stat(lockFilePath(dir)); err != nil {
		t.Errorf("lockfile should be untouched in dry-run: %v", err)
	}
	skillPath := filepath.Join(dir, ".claude", "skills", "demo-skill", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Errorf("skill file should be untouched in dry-run: %v", err)
	}
	servers := mcpServersOf(t, filepath.Join(dir, ".claude.json"), "mcpServers")
	if _, ok := servers["wiki"]; !ok {
		t.Error("dry-run should not have removed the wiki entry from disk")
	}
}
