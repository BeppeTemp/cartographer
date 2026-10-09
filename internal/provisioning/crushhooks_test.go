package provisioning_test

// Crush hooks and project configuration (D363).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

func crushHookManaged(res provisioning.AppliedResult, name string) []provisioning.ManagedFile {
	var out []provisioning.ManagedFile
	for _, mf := range res.NewLock.Managed {
		if mf.Kind == "hook" && mf.Name == name {
			out = append(out, mf)
		}
	}
	return out
}

func TestApplyAndPrune_CrushGlobalHook(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "guard", "PreToolUse", "Edit|Write", "./notify.sh")
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	baseDir := t.TempDir()
	cfg := filepath.Join(baseDir, ".config", "crush", "crush.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	user := `{"mcp":{"mine":{"type":"http","url":"https://example.com/mcp"}},` +
		`"hooks":{"PreToolUse":[{"name":"mine","command":"echo user"}]}}`
	if err := os.WriteFile(cfg, []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := provisioning.ApplyOptions{
		AutoTrust: true, KBRoots: map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderCrush, BaseDir: baseDir, Lock: provisioning.Lock{},
	}
	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provisioning.Apply(m, opts); err != nil { // upsert, not append
		t.Fatal(err)
	}

	root := readJSONFile(t, cfg)
	if root["mcp"] == nil {
		t.Errorf("user mcp entry lost: %v", root)
	}
	list := root["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(list) != 2 {
		t.Fatalf("want the user's entry and ours, got %v", list)
	}
	if list[0].(map[string]any)["name"] != "mine" {
		t.Errorf("user hook not preserved: %v", list[0])
	}
	ours := list[1].(map[string]any)
	if ours["name"] != "cartographer-guard" {
		t.Errorf("name = %v", ours["name"])
	}
	// Trap: Crush's tool names are lowercase.
	if ours["matcher"] != "(?i)Edit|Write" {
		t.Errorf("matcher = %v, want the (?i) prefix", ours["matcher"])
	}
	wantCmd := filepath.Join(baseDir, ".config", "crush", "hooks", "guard", "notify.sh")
	if ours["command"] != wantCmd {
		t.Errorf("command = %v, want %v", ours["command"], wantCmd)
	}

	if _, err := provisioning.PruneManaged(crushHookManaged(res, "guard"), baseDir, false); err != nil {
		t.Fatal(err)
	}
	root = readJSONFile(t, cfg)
	list = root["hooks"].(map[string]any)["PreToolUse"].([]any)
	if root["mcp"] == nil || len(list) != 1 || list[0].(map[string]any)["name"] != "mine" {
		t.Fatalf("prune touched more than our entry: %v", root)
	}
}

// Removing the last entry takes the emptied list and hooks map with it.
func TestPruneManaged_CrushHookDropsEmptiedContainers(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "guard", "PreToolUse", "", "./notify.sh")
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	baseDir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		AutoTrust: true, KBRoots: map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderCrush, BaseDir: baseDir, Lock: provisioning.Lock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(baseDir, ".config", "crush", "crush.json")
	entry := readJSONFile(t, cfg)["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)
	if _, has := entry["matcher"]; has {
		t.Errorf("an empty KB matcher must stay absent (all tools): %v", entry)
	}
	if _, err := provisioning.PruneManaged(crushHookManaged(res, "guard"), baseDir, false); err != nil {
		t.Fatal(err)
	}
	if _, has := readJSONFile(t, cfg)["hooks"]; has {
		t.Error("an emptied hooks map was left behind")
	}
}

// Only PreToolUse fires on Crush: any other event installs the files, warns,
// and registers nothing.
func TestApply_CrushHookOtherEventWarnsAndRegistersNothing(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "after", "PostToolUse", "Edit", "./notify.sh")
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	baseDir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		AutoTrust: true, KBRoots: map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderCrush, BaseDir: baseDir, Lock: provisioning.Lock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "not fired by this client") {
		t.Errorf("want a no-equivalent warning, got %v", res.Warnings)
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".config", "crush", "hooks", "after", "hook.json")); err != nil {
		t.Errorf("files must still be materialized: %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".config", "crush", "crush.json")); !os.IsNotExist(err) {
		t.Error("nothing may be registered for an event Crush does not fire")
	}
}

// No session event and no PostToolUse: no bootstrap hook, no write-findings hook.
func TestCrushHasNoBootstrapNorWriteFindingsHook(t *testing.T) {
	if provisioning.SupportsSessionHook(configurator.ProviderCrush) {
		t.Error("crush fires no session-start event")
	}
	baseDir := t.TempDir()
	lock, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderCrush, provisioning.Lock{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Managed) != 0 {
		t.Errorf("bootstrap hook installed: %v", lock.Managed)
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".config", "crush")); !os.IsNotExist(err) {
		t.Error("bootstrap wrote into the crush config directory")
	}
}

// Project scope: mcp and hook go to the project .crush.json and .crush/hooks,
// never to the user's global configuration, and prune undoes exactly that.
func TestApplyAndPrune_CrushProjectScope(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "guard", "PreToolUse", "bash", "./notify.sh")
	writeMCPFixture(t, kbRoot, "docs", `{"type":"http","url":"https://example.com/mcp"}`)
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, mcpAllow("docs", "https://example.com/mcp"))
	if err != nil {
		t.Fatal(err)
	}
	baseDir := t.TempDir()
	mcp := findMCPArtifact(t, m, "docs")
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		AutoTrust: true, KBRoots: map[string]string{"kb": kbRoot},
		ApprovedMCP: map[string]string{mcp.Source + "\x00docs": mcp.ContentHash},
		Provider:    configurator.ProviderCrush, BaseDir: baseDir, Scope: provisioning.ScopeProject,
		Lock: provisioning.Lock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(baseDir, ".crush.json")
	root := readJSONFile(t, proj)
	if root["mcp"].(map[string]any)["docs"] == nil {
		t.Errorf("mcp entry missing from .crush.json: %v", root)
	}
	entry := root["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)
	wantCmd := filepath.Join(baseDir, ".crush", "hooks", "guard", "notify.sh")
	if entry["name"] != "cartographer-guard" || entry["command"] != wantCmd {
		t.Errorf("hook entry = %v", entry)
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".crush", "hooks", "guard", "hook.json")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".config")); !os.IsNotExist(err) {
		t.Error("a project projection wrote into the global crush configuration")
	}

	if _, err := provisioning.PruneManaged(res.NewLock.Managed, baseDir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(proj); !os.IsNotExist(err) {
		t.Errorf(".crush.json should go once nothing else is in it: %v", err)
	}
}

// What the hygiene pass excludes from git for a Crush workspace.
func TestProjectOwnedPaths_Crush(t *testing.T) {
	got := strings.Join(provisioning.ProjectOwnedPaths(configurator.ProviderCrush), ",")
	if got != ".crush.json,.crush/hooks,.crush/skills" {
		t.Errorf("owned paths = %s", got)
	}
}
