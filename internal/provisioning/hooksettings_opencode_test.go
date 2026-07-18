package provisioning_test

// Tests for materializing OpenCode hooks via a generated JS plugin
// (D59): destDir hook×opencode → .opencode/hooks/<name>/ (script + hook.json,
// same as the other providers), plus the plugin wrapper generated in
// .config/opencode/plugins/cartographer-<name>.js. See hooksettings_test.go
// (Claude, D57) and provisioning_codex_test.go (Codex, D58) for the equivalent
// in the other providers.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func pluginPath(baseDir, name string) string {
	return filepath.Join(baseDir, ".config", "opencode", "plugins", "cartographer-"+name+".js")
}

func TestApply_OpenCode_Hook_GeneraPluginToolBefore(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", "PreToolUse", "Bash", "./notify.sh")

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderOpenCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("Apply: unexpected warnings %v", res.Warnings)
	}

	// Hook files materialized same as for the other providers.
	for _, f := range []string{"hook.json", "notify.sh"} {
		p := filepath.Join(baseDir, ".opencode", "hooks", "notify", f)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected hook file %s: %v", p, err)
		}
	}

	// Generated plugin.
	pPath := pluginPath(baseDir, "notify")
	data, err := os.ReadFile(pPath)
	if err != nil {
		t.Fatalf("plugin not generated at %s: %v", pPath, err)
	}
	content := string(data)

	if !strings.Contains(content, "cartographer:hook:notify") {
		t.Errorf("missing ownership marker: %s", content)
	}
	if !strings.Contains(content, `"tool.execute.before"`) {
		t.Errorf("expected tool.execute.before mapping: %s", content)
	}
	scriptPath := filepath.Join(baseDir, ".opencode", "hooks", "notify", "notify.sh")
	if !strings.Contains(content, scriptPath) {
		t.Errorf("expected resolved script path %q in the plugin: %s", scriptPath, content)
	}
	if !strings.Contains(content, `"Bash"`) {
		t.Errorf("expected \"Bash\" matcher in the plugin: %s", content)
	}

	// The plugin's ManagedFile present in the new lock (for future pruning).
	var found bool
	for _, mf := range res.NewLock.Managed {
		if mf.Kind == "hook" && mf.Name == "notify" && filepath.Base(mf.Path) == "cartographer-notify.js" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ManagedFile for the generated plugin, lock: %+v", res.NewLock.Managed)
	}
}

func TestApply_OpenCode_Hook_GeneraPluginSessionStart(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "greet", "SessionStart", "", "./notify.sh")

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderOpenCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("Apply: unexpected warnings %v", res.Warnings)
	}

	data, err := os.ReadFile(pluginPath(baseDir, "greet"))
	if err != nil {
		t.Fatalf("plugin not generated: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `event.type !== "session.created"`) {
		t.Errorf("expected SessionStart -> session.created mapping: %s", content)
	}
}

func TestApply_OpenCode_Hook_ReApply_Idempotente(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	opts := provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderOpenCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	}

	if _, err := provisioning.Apply(m, opts); err != nil {
		t.Fatalf("Apply (1): %v", err)
	}
	first, err := os.ReadFile(pluginPath(baseDir, "notify"))
	if err != nil {
		t.Fatalf("plugin not generated after apply 1: %v", err)
	}

	if _, err := provisioning.Apply(m, opts); err != nil {
		t.Fatalf("Apply (2): %v", err)
	}
	if _, err := provisioning.Apply(m, opts); err != nil {
		t.Fatalf("Apply (3): %v", err)
	}
	second, err := os.ReadFile(pluginPath(baseDir, "notify"))
	if err != nil {
		t.Fatalf("plugin not present after apply 3: %v", err)
	}

	if string(first) != string(second) {
		t.Errorf("plugin content not stable across repeated applies:\n--- before ---\n%s\n--- after ---\n%s", first, second)
	}

	// No duplicate: a single .js file for the hook in the plugins folder.
	entries, err := os.ReadDir(filepath.Join(baseDir, ".config", "opencode", "plugins"))
	if err != nil {
		t.Fatalf("read plugins dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 plugin file, got %d: %v", len(entries), entries)
	}
}

func TestApply_OpenCode_Hook_Removed_RimuovePluginEFile(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", "PostToolUse", "", "./notify.sh")

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	opts := provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderOpenCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	}

	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply (materialize): %v", err)
	}
	if _, err := os.Stat(pluginPath(baseDir, "notify")); err != nil {
		t.Fatalf("plugin not materialized: %v", err)
	}

	if err := os.RemoveAll(filepath.Join(kbRoot, "hooks", "notify")); err != nil {
		t.Fatal(err)
	}
	m2, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest (2): %v", err)
	}

	opts.Lock = res.NewLock
	res2, err := provisioning.Apply(m2, opts)
	if err != nil {
		t.Fatalf("Apply (removal): %v", err)
	}
	if len(res2.Pruned) == 0 {
		t.Fatalf("Apply (removal): expected Pruned not empty")
	}

	if _, err := os.Stat(pluginPath(baseDir, "notify")); !os.IsNotExist(err) {
		t.Error("plugin not removed after the hook was removed")
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".opencode", "hooks", "notify", "hook.json")); !os.IsNotExist(err) {
		t.Error("hook.json not removed after the hook was removed")
	}
}

func TestApply_OpenCode_Hook_EventoNonMappato_NessunPluginNessunErrore(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "onprompt", "UserPromptSubmit", "", "./notify.sh")

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderOpenCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: expected no fatal error, got %v", err)
	}

	// The hook's files remain materialized.
	for _, f := range []string{"hook.json", "notify.sh"} {
		p := filepath.Join(baseDir, ".opencode", "hooks", "onprompt", f)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected hook file %s materialized regardless: %v", p, err)
		}
	}

	// No plugin generated.
	if _, err := os.Stat(pluginPath(baseDir, "onprompt")); !os.IsNotExist(err) {
		t.Error("no plugin expected for an event with no OpenCode equivalent")
	}

	// Warning reported, not an error.
	if len(res.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(res.Warnings), res.Warnings)
	}
	if !strings.Contains(res.Warnings[0], "onprompt") || !strings.Contains(res.Warnings[0], "UserPromptSubmit") {
		t.Errorf("non-descriptive warning: %q", res.Warnings[0])
	}
}
