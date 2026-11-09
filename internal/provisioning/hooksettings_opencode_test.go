package provisioning_test

// Tests for materializing OpenCode hooks via a generated JS plugin
// (D59): destDir hook×opencode → .opencode/hooks/<name>/ (script + hook.json,
// same as the other providers), plus the plugin wrapper generated in
// .config/opencode/plugins/cartographer-<name>.js. See hooksettings_test.go
// (Claude, D57) and provisioning_codex_test.go (Codex, D58) for the equivalent
// in the other providers.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func pluginPath(baseDir, name string) string {
	return filepath.Join(baseDir, ".config", "opencode", "plugins", "cartographer-"+name+".js")
}

func TestApply_OpenCode_Hook_GeneraPluginToolBefore(t *testing.T) {
	defer provisioning.SetOpenCodeVersionForTest("1.18.34")() // a readable version: no D719 warning
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", "PreToolUse", "Bash", "./notify.sh")

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{ApprovedMCP: approveAll(m),
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
	// Slash form: the plugin runs the command through `sh -c` (D267).
	scriptPath := filepath.ToSlash(filepath.Join(baseDir, ".opencode", "hooks", "notify", "notify.sh"))
	// The plugin embeds the command as a JS string literal, so on Windows the
	// path's separators are escaped in it; compare against that encoding.
	scriptLit, err := json.Marshal(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, strings.Trim(string(scriptLit), `"`)) {
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
	defer provisioning.SetOpenCodeVersionForTest("1.18.34")() // a readable version: no D719 warning
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "greet", "SessionStart", "", "./notify.sh")

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{ApprovedMCP: approveAll(m),
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

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	opts := provisioning.ApplyOptions{ApprovedMCP: approveAll(m),
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

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	opts := provisioning.ApplyOptions{ApprovedMCP: approveAll(m),
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
	m2, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
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

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{ApprovedMCP: approveAll(m),
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

func applyOpenCodeHook(t *testing.T, version, event, matcher string) string {
	t.Helper()
	got, _ := applyOpenCodeHookWarnings(t, version, event, matcher)
	return got
}

func applyOpenCodeHookWarnings(t *testing.T, version, event, matcher string) (string, []string) {
	t.Helper()
	defer provisioning.SetOpenCodeVersionForTest(version)()
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", event, matcher, "./notify.sh")
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	baseDir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{ApprovedMCP: approveAll(m),
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderOpenCode, BaseDir: baseDir, Lock: provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(pluginPath(baseDir, "notify"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data), res.Warnings
}

// D359: OpenCode 2.x needs a default export with an id and a setup function.
func TestApply_OpenCode2_PluginShape(t *testing.T) {
	for _, tc := range []struct{ event, matcher, want string }{
		{"PreToolUse", "Bash", `ctx.tool.hook("execute.before"`},
		{"PostToolUse", "Bash", `ctx.tool.hook("execute.after"`},
		{"SessionStart", "", `ctx.event.subscribe`},
	} {
		got := applyOpenCodeHook(t, "2.0.20\n", tc.event, tc.matcher)
		for _, w := range []string{"export default {", `id: "cartographer.notify"`, "setup(ctx)", tc.want, "opencode-plugin-shape=2"} {
			if !strings.Contains(got, w) {
				t.Errorf("%s: missing %q in:\n%s", tc.event, w, got)
			}
		}
		if strings.Contains(got, "export const") {
			t.Errorf("%s: 1.x named export in a 2.x plugin:\n%s", tc.event, got)
		}
		if provisioning.OpenCodePluginShape(got) != 2 {
			t.Errorf("%s: shape not read back as 2", tc.event)
		}
	}
}

func TestApply_OpenCode1_KeepsV1Shape(t *testing.T) {
	got, warnings := applyOpenCodeHookWarnings(t, "1.18.34", "PreToolUse", "Bash")
	if !strings.Contains(got, "export const CartographerHookNotify = async ({ $ })") || strings.Contains(got, "export default") {
		t.Errorf("expected the 1.x shape:\n%s", got)
	}
	if provisioning.OpenCodePluginShape(got) != 1 {
		t.Error("shape not read back as 1")
	}
	if len(warnings) != 0 {
		t.Errorf("readable version must not warn: %v", warnings)
	}
}

// D719: an unreadable version writes the newest known shape and says so.
func TestApply_OpenCodeUnknown_NewestShapeWithWarning(t *testing.T) {
	got, warnings := applyOpenCodeHookWarnings(t, "", "PreToolUse", "Bash")
	for _, w := range []string{"export default {", "opencode-plugin-shape=2"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"notify"`) || !strings.Contains(warnings[0], "unreadable") {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestWriteFindingsHook_OpenCodeUnknownVersionStaysUnsupported(t *testing.T) {
	defer provisioning.SetOpenCodeVersionForTest("")()
	if provisioning.SupportsWriteFindingsHook(configurator.ProviderOpenCode) {
		t.Error("an unreadable version must not enable the write-findings hook (D362)")
	}
}

func TestOpenCodePluginShape_NoMarkerIsV1(t *testing.T) {
	if provisioning.OpenCodePluginShape("export const X = async () => ({})") != 1 {
		t.Error("a file written before D359 is shape 1")
	}
}

func TestOpenCodeRegistry_DeclaresPluginMajors(t *testing.T) {
	d, ok := configurator.Lookup(configurator.ProviderOpenCode)
	if !ok || len(d.HookPluginMajors) != 2 || d.HookPluginMajors[0] != 1 || d.HookPluginMajors[1] != 2 {
		t.Errorf("HookPluginMajors = %v, want [1 2]", d.HookPluginMajors)
	}
}

// #1827: OpenCode reads a project's plugins from <project>/.opencode/plugins/,
// never from <project>/.config/opencode/plugins/. The workspace scope writes
// the first, the global scope keeps the second, and a prune removes the file
// wherever the lock recorded it.
func TestApply_OpenCode_Hook_ProjectScopeWritesProjectPluginDir(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", "PostToolUse", "", "./notify.sh")
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	ws := t.TempDir()
	opts := provisioning.ApplyOptions{ApprovedMCP: approveAll(m),
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderOpenCode,
		Scope:    provisioning.ScopeProject,
		BaseDir:  ws,
	}
	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".opencode", "plugins", "cartographer-notify.js")); err != nil {
		t.Errorf("project plugin not at .opencode/plugins: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".config")); !os.IsNotExist(err) {
		t.Errorf("the workspace got a .config directory: %v", err)
	}

	// Prune: the hook leaves the KB, the plugin goes with it.
	if err := os.RemoveAll(filepath.Join(kbRoot, "hooks", "notify")); err != nil {
		t.Fatal(err)
	}
	m2, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest (2): %v", err)
	}
	opts.Lock = res.NewLock
	if _, err := provisioning.Apply(m2, opts); err != nil {
		t.Fatalf("Apply (removal): %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".opencode", "plugins", "cartographer-notify.js")); !os.IsNotExist(err) {
		t.Error("project plugin not removed with its hook")
	}
}

// A lock written before the fix records the workspace plugin under
// .config/opencode/plugins: one sync writes the new file, removes the old one
// and reports the hook as moved; the next sync changes nothing.
func TestApply_OpenCode_Hook_ProjectScopeRelocatesOldPlugin(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", "PostToolUse", "", "./notify.sh")
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	ws := t.TempDir()
	opts := provisioning.ApplyOptions{ApprovedMCP: approveAll(m),
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderOpenCode,
		Scope:    provisioning.ScopeProject,
		BaseDir:  ws,
	}
	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Rewind to the old layout: plugin file and lock entry at the old path.
	newRel := ".opencode/plugins/cartographer-notify.js"
	oldRel := ".config/opencode/plugins/cartographer-notify.js"
	oldFull := filepath.Join(ws, filepath.FromSlash(oldRel))
	if err := os.MkdirAll(filepath.Dir(oldFull), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(ws, filepath.FromSlash(newRel)), oldFull); err != nil {
		t.Fatal(err)
	}
	lock := res.NewLock
	for i, mf := range lock.Managed {
		if filepath.ToSlash(mf.Path) == newRel {
			lock.Managed[i].Path = oldRel
		}
	}

	opts.Lock = lock
	res2, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply (relocation): %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, filepath.FromSlash(newRel))); err != nil {
		t.Errorf("plugin not written at the new path: %v", err)
	}
	if _, err := os.Stat(oldFull); !os.IsNotExist(err) {
		t.Errorf("old plugin still on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".config", "opencode", "plugins")); !os.IsNotExist(err) {
		t.Errorf("empty .config/opencode/plugins left behind: %v", err)
	}
	moved := false
	for _, mf := range res2.Relocated {
		moved = moved || (mf.Kind == "hook" && mf.Name == "notify")
	}
	if !moved {
		t.Errorf("Relocated = %+v, want hook/notify", res2.Relocated)
	}
	pruned := false
	for _, mf := range res2.Pruned {
		pruned = pruned || filepath.ToSlash(mf.Path) == oldRel
	}
	if !pruned {
		t.Errorf("Pruned = %+v, want %s", res2.Pruned, oldRel)
	}
	for _, mf := range res2.NewLock.Managed {
		if filepath.ToSlash(mf.Path) == oldRel {
			t.Errorf("the new lock still records %s", oldRel)
		}
	}

	opts.Lock = res2.NewLock
	res3, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply (again): %v", err)
	}
	if len(res3.Written) != 0 || len(res3.Relocated) != 0 || len(res3.Pruned) != 0 {
		t.Errorf("second sync not a no-op: written %v relocated %v pruned %v", res3.Written, res3.Relocated, res3.Pruned)
	}
}

// #1827: a symlinked .opencode/plugins in a workspace is refused before the
// hook's script is written: the guard checks the project plugin path, not the
// global one (the hook script itself sits in a real .opencode/hooks).
func TestApply_OpenCode_Hook_ProjectScopeSymlinkedPluginsRefusedBeforeWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", "PostToolUse", "", "./notify.sh")
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	ws, elsewhere := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(ws, ".opencode", "plugins")); err != nil {
		t.Fatal(err)
	}
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{ApprovedMCP: approveAll(m),
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderOpenCode,
		Scope:    provisioning.ScopeProject,
		BaseDir:  ws,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Refused) != 1 || res.Refused[0].Name != "notify" {
		t.Fatalf("Refused = %+v, want the notify hook", res.Refused)
	}
	if _, err := os.Stat(filepath.Join(ws, ".opencode", "hooks", "notify")); !os.IsNotExist(err) {
		t.Errorf("the hook's files were written before the refusal: %v", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("files written through the symlinked plugins directory: %v", entries)
	}
}

// #1827: a script of the KB's own named cartographer-<name>.js in the hook's
// directory is not the generated plugin: it is never read as a moved plugin.
func TestApply_OpenCode_Hook_OwnScriptNamedLikePluginIsNotMoved(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", "PostToolUse", "", "./notify.sh")
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	ws := t.TempDir()
	opts := provisioning.ApplyOptions{ApprovedMCP: approveAll(m),
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderOpenCode,
		Scope:    provisioning.ScopeProject,
		BaseDir:  ws,
	}
	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	lock := res.NewLock
	lock.Managed = append(lock.Managed, provisioning.ManagedFile{Kind: "hook", Name: "notify", Path: ".opencode/hooks/notify/cartographer-notify.js"})
	opts.Lock = lock
	res2, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply (2): %v", err)
	}
	for _, mf := range res2.Relocated {
		t.Errorf("relocated %+v", mf)
	}
}
