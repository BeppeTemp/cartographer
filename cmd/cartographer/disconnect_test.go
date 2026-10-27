package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
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
// .cartographer.yaml listing every provider as connected. Its explicit 8080
// endpoint represents a configuration created before D112 and must survive.
func setupDisconnectFixture(t *testing.T, providers ...string) string {
	t.Helper()
	dir := t.TempDir()

	cfg := &clientconfig.Config{
		ServerURL:  "http://localhost:8080/mcp",
		ServerName: "wiki",
		Token:      "${CARTOGRAPHER_TOKENS}",
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
	mcp := mcpServersOf(t, openCodeConfig(t, dir), "mcp")
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

// #415: a hook path joined with the host separator and a skill path recorded
// with forward slashes print with the same separator.
func TestPrintDisconnectSummaryUsesForwardSlashes(t *testing.T) {
	res := disconnectResult{Providers: []disconnectProviderResult{{
		Provider: "claude",
		Pruned: []provisioning.ManagedFile{
			{Path: filepath.Join(".claude", "hooks", "cartographer-bootstrap", "hook.json")},
			{Path: ".claude/skills/kb-a/SKILL.md"},
		},
	}}}
	out := withStdout(t, func() { printDisconnectSummary(res, false) })
	for _, want := range []string{
		"[claude] pruned .claude/hooks/cartographer-bootstrap/hook.json\n",
		"[claude] pruned .claude/skills/kb-a/SKILL.md\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// #1169: several instructions artifacts share a single physical file (CLAUDE.md,
// AGENTS.md), so disconnect must report the prune once per file, not once per KB.
func TestPrintDisconnectSummary_CollapsesInstructionsPrune(t *testing.T) {
	res := disconnectResult{Providers: []disconnectProviderResult{{
		Provider: "claude",
		Pruned: []provisioning.ManagedFile{
			{Kind: "instructions", Name: "kb-a", Path: ".claude/CLAUDE.md"},
			{Kind: "instructions", Name: "kb-b", Path: ".claude/CLAUDE.md"},
			{Kind: "skill", Name: "skill-a", Path: ".claude/skills/skill-a/SKILL.md"},
		},
	}, {
		Provider: "codex",
		Pruned: []provisioning.ManagedFile{
			{Kind: "instructions", Name: "kb-a", Path: ".codex/AGENTS.md"},
			{Kind: "instructions", Name: "kb-b", Path: ".codex/AGENTS.md"},
		},
	}, {
		Provider: "opencode",
		Pruned: []provisioning.ManagedFile{
			{Kind: "instructions", Name: "kb-a", Path: ".config/opencode/AGENTS.md"},
			{Kind: "instructions", Name: "kb-b", Path: ".config/opencode/AGENTS.md"},
		},
	}}}
	out := withStdout(t, func() { printDisconnectSummary(res, false) })
	for _, want := range []string{
		"[claude] pruned .claude/CLAUDE.md\n",
		"[claude] pruned .claude/skills/skill-a/SKILL.md\n",
		"[codex] pruned .codex/AGENTS.md\n",
		"[opencode] pruned .config/opencode/AGENTS.md\n",
	} {
		if strings.Count(out, want) != 1 {
			t.Errorf("expected %q exactly once in output, got count %d:\n%s", want, strings.Count(out, want), out)
		}
	}
}

// D325: the timer exists for clients with no reliable session hook; when the
// last one is disconnected it goes, and that is not an operator opt-out.
func TestDisconnect_UninstallsTimerWhenNoClientNeedsIt(t *testing.T) {
	c := stubTimer(t, true)
	dir := setupDisconnectFixture(t, "claude", "kiro")

	res, err := doDisconnect(disconnectOptions{Providers: []string{"kiro"}, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if c.uninstalls != 1 || !res.TimerUninstalled {
		t.Errorf("uninstalls=%d TimerUninstalled=%v, want the timer removed", c.uninstalls, res.TimerUninstalled)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SyncTimerOptOut {
		t.Error("removing the timer because nobody needs it must not record an opt-out")
	}
}

func TestDisconnect_KeepsTimerWhileAClientNeedsIt(t *testing.T) {
	c := stubTimer(t, true)
	dir := setupDisconnectFixture(t, "claude", "kiro")
	if _, err := doDisconnect(disconnectOptions{Providers: []string{"claude"}, Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if c.uninstalls != 0 {
		t.Error("kiro still needs the timer: it must stay (an installed timer must not read as 'nobody needs it')")
	}
}

// --- a symlinked client config is refused for its provider only (D148) ---

// linkCodexDir replaces dir/.codex with a link to a directory holding the same
// files, and returns that directory.
func linkCodexDir(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	elsewhere := filepath.Join(t.TempDir(), "codex-elsewhere")
	if err := os.Rename(filepath.Join(dir, ".codex"), elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(dir, ".codex")); err != nil {
		t.Fatal(err)
	}
	return elsewhere
}

func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		out[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDoDisconnect_ASymlinkedConfigKeepsThatProviderConnectedAndTheRestProceeds(t *testing.T) {
	dir := setupDisconnectFixture(t, "claude", "codex")
	elsewhere := linkCodexDir(t, dir)
	before := readTree(t, elsewhere)

	res, err := doDisconnect(disconnectOptions{Providers: []string{"claude", "codex"}, Dir: dir})
	if err != nil {
		t.Fatalf("doDisconnect = %v, want the refusal reported per provider, not fatal", err)
	}
	var codex, claude *disconnectProviderResult
	for i := range res.Providers {
		switch res.Providers[i].Provider {
		case "codex":
			codex = &res.Providers[i]
		case "claude":
			claude = &res.Providers[i]
		}
	}
	if codex == nil || len(codex.Refused) == 0 || codex.ConfigRemoved {
		t.Fatalf("codex result = %+v, want a refusal", codex)
	}
	if claude == nil || !claude.ConfigRemoved || len(claude.Refused) != 0 {
		t.Fatalf("claude result = %+v, want its entry removed", claude)
	}
	if after := readTree(t, elsewhere); len(after) != len(before) {
		t.Errorf("link target changed: %v -> %v", before, after)
	} else {
		for k, v := range before {
			if after[k] != v {
				t.Errorf("link target file %s changed", k)
			}
		}
	}

	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HasAgent("codex") || cfg.HasAgent("claude") {
		t.Errorf("agents = %v, want codex kept and claude gone", cfg.Agents)
	}
	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lf.Providers["codex"]; !ok {
		t.Error("codex lock entry should remain so the next disconnect retries")
	}
	if _, ok := lf.Providers["claude"]; ok {
		t.Error("claude lock entry should be gone")
	}
}

func TestApplyMCPEntries_ASymlinkedConfigIsAWarningAndOtherProvidersAreWritten(t *testing.T) {
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(dir, ".codex")); err != nil {
		t.Fatal(err)
	}
	entries, err := entriesForKBs("wiki", "https://example.test/mcp", []string{"a"}, []string{"a"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	providers := []string{"codex", "claude", "opencode"}
	written, warnings, err := applyMCPEntries(sameEntriesFor(providers, entries), providers, dir, nil, false, "", false)
	if err != nil {
		t.Fatalf("applyMCPEntries = %v, want the refusal as a warning", err)
	}
	if len(written) != 2 {
		t.Errorf("written = %v, want claude and opencode", written)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], filepath.Join(dir, ".codex")) {
		t.Errorf("warnings = %v, want one naming the .codex link", warnings)
	}
	if got, _ := os.ReadDir(elsewhere); len(got) != 0 {
		t.Errorf("the link target gained %d entries", len(got))
	}
	for _, f := range []string{".claude.json", filepath.FromSlash(configurator.OpenCodeConfigPath)} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s was not written: %v", f, err)
		}
	}
}

func TestRemoveMCPEntries_ASymlinkedConfigIsReportedAfterTheOtherProvidersAreProcessed(t *testing.T) {
	dir := setupDisconnectFixture(t, "codex", "claude")
	elsewhere := linkCodexDir(t, dir)
	before := readTree(t, elsewhere)

	_, removed, err := removeMCPEntries("wiki", nil, []string{"codex", "claude"}, dir, nil, false, "", false)
	if !errors.Is(err, safewrite.ErrSymlinkDestination) {
		t.Fatalf("removeMCPEntries = %v, want ErrSymlinkDestination", err)
	}
	if !removed["claude"] {
		t.Error("claude's entry should still have been removed")
	}
	if removed["codex"] {
		t.Error("codex's entry cannot have been removed through the link")
	}
	for k, v := range readTree(t, elsewhere) {
		if before[k] != v {
			t.Errorf("link target file %s changed", k)
		}
	}
}

// Managed files behind a symlink are refused by the prune as well: what can be
// pruned is, the provider stays connected with its lockfile section, and the
// command exits 1 (D148).
func TestDoDisconnect_APrunedFileBehindASymlinkKeepsTheProviderConnected(t *testing.T) {
	dir := setupDisconnectFixture(t, "claude", "opencode")
	elsewhere := linkOpencodeDir(t, dir)
	before := readTree(t, elsewhere)

	res, err := doDisconnect(disconnectOptions{Providers: []string{"opencode", "claude"}, Dir: dir})
	if err != nil {
		t.Fatalf("doDisconnect = %v, want the refusal reported per provider", err)
	}
	var oc, cl *disconnectProviderResult
	for i := range res.Providers {
		switch res.Providers[i].Provider {
		case "opencode":
			oc = &res.Providers[i]
		case "claude":
			cl = &res.Providers[i]
		}
	}
	if oc == nil || len(oc.Refused) == 0 || !oc.ConfigRemoved {
		t.Fatalf("opencode result = %+v, want its entry removed and its managed files refused", oc)
	}
	if cl == nil || len(cl.Refused) != 0 {
		t.Fatalf("claude result = %+v, want a clean disconnect", cl)
	}
	if kept := res.keptConnected(); len(kept) != 1 || kept[0] != "opencode" {
		t.Errorf("keptConnected = %v, want [opencode]", kept)
	}
	for k, v := range readTree(t, elsewhere) {
		if before[k] != v {
			t.Errorf("link target file %s changed", k)
		}
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HasAgent("opencode") || cfg.HasAgent("claude") {
		t.Errorf("agents = %v, want opencode kept and claude gone", cfg.Agents)
	}
	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lf.Providers["opencode"]; !ok {
		t.Error("opencode lock entry should remain so the next disconnect retries")
	}
	if got := summarizeDisconnect(disconnectResult{Providers: []disconnectProviderResult{*oc}}); !strings.Contains(got, "kept connected") || strings.Contains(got, "already disconnected") {
		t.Errorf("TUI summary = %q, want the refusal reported", got)
	}
}

func TestCmdDisconnect_ExitsNonZeroWhenAProviderWasKeptConnected(t *testing.T) {
	dir := setupDisconnectFixture(t, "claude", "opencode")
	setHome(t, dir)
	linkOpencodeDir(t, dir)
	if code := cmdDisconnect(nil); code != 1 {
		t.Errorf("cmdDisconnect = %d, want 1", code)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil || !cfg.HasAgent("opencode") || cfg.HasAgent("claude") {
		t.Errorf("agents = %v, %v; want opencode kept and claude gone", cfg.Agents, err)
	}
}

func TestSummarizeDisconnect_ReportsARefusalInsteadOfAlreadyDisconnected(t *testing.T) {
	got := summarizeDisconnect(disconnectResult{Providers: []disconnectProviderResult{{Provider: "codex", Refused: []string{"destination is a symlink: /x"}}}})
	if !strings.Contains(got, "kept connected: destination is a symlink: /x") {
		t.Errorf("summary = %q", got)
	}
}

// linkOpencodeDir replaces dir/.opencode (where the fixture put opencode's
// managed skill) with a link to a directory holding the same files.
func linkOpencodeDir(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	elsewhere := filepath.Join(t.TempDir(), "opencode-elsewhere")
	if err := os.Rename(filepath.Join(dir, ".opencode"), elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(dir, ".opencode")); err != nil {
		t.Fatal(err)
	}
	return elsewhere
}

// reconnect honours the disconnect half's refusal: a provider that was kept
// connected is not rebuilt, the others are, and the exit status is 1.
func TestDoReconnect_AProviderKeptConnectedIsNotRebuilt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	srv := multiKBServer(t, `{"status":"ok","version":"1.4.0","kbs":[{"name":"alpha"}]}`)
	defer srv.Close()
	dir := t.TempDir()
	setHome(t, dir)
	if _, err := doConnect(connectOptions{Providers: []string{"claude", "opencode"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer"}); err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	target := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.Rename(openCodeConfig(t, dir), target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, openCodeConfig(t, dir)); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(target)

	res, err := doReconnect(reconnectOptions{Target: "all", Dir: dir})
	if err != nil {
		t.Fatalf("doReconnect = %v", err)
	}
	if kept := res.Disconnected.keptConnected(); len(kept) != 1 || kept[0] != "opencode" {
		t.Fatalf("keptConnected = %v, want [opencode]", kept)
	}
	if res.Connected == nil || strings.Join(res.ConnectOptions.Providers, ",") != "claude" {
		t.Errorf("connect half ran for %v, want only claude", res.ConnectOptions.Providers)
	}
	if after, _ := os.ReadFile(target); string(after) != string(before) {
		t.Errorf("the link target changed:\n%s", after)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil || !cfg.HasAgent("opencode") || !cfg.HasAgent("claude") {
		t.Errorf("agents = %v, %v; want both still connected", cfg.Agents, err)
	}
	if code := cmdReconnect(nil); code != 1 {
		t.Errorf("cmdReconnect = %d, want 1", code)
	}
}
