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
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/execbit"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func TestEnsureBootstrapHook_Claude_MaterializzaERegistra(t *testing.T) {
	baseDir := t.TempDir()

	lock, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderClaudeCode, provisioning.Lock{}, false)
	if err != nil {
		t.Fatalf("EnsureBootstrapHook: %v", err)
	}

	hookDir := filepath.Join(baseDir, ".claude", "hooks", provisioning.BootstrapHookName)
	for _, f := range []string{"hook.json", provisioning.BootstrapScriptNameForTest} {
		if _, err := os.Stat(filepath.Join(hookDir, f)); err != nil {
			t.Errorf("expected file %s: %v", f, err)
		}
	}
	scriptData, err := os.ReadFile(filepath.Join(hookDir, provisioning.BootstrapScriptNameForTest))
	if err != nil {
		t.Fatalf("read %s: %v", provisioning.BootstrapScriptNameForTest, err)
	}
	if !strings.Contains(string(scriptData), "cartographer sync --auto-trust") {
		t.Errorf("%s does not call `cartographer sync --auto-trust`: %s", provisioning.BootstrapScriptNameForTest, scriptData)
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
	wantCmd := filepath.Join(hookDir, provisioning.BootstrapScriptNameForTest)
	if groups[0].Hooks[0].Command != wantCmd {
		t.Errorf("command: expected %q, got %q", wantCmd, groups[0].Hooks[0].Command)
	}

	// ManagedFile entries recorded in the returned Lock, for future pruning.
	if len(lock.Managed) != 2 {
		t.Fatalf("expected 2 ManagedFile (hook.json+script), got %d: %+v", len(lock.Managed), lock.Managed)
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
	// Pre-existing script without the executable bit (written 0600 by
	// an earlier version): WriteFile does not update its permissions, the
	// explicit Chmod does — regression "Permission denied on every SessionStart".
	if err := os.WriteFile(filepath.Join(hookDir, provisioning.BootstrapScriptNameForTest), []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderClaudeCode, provisioning.Lock{}, false); err != nil {
		t.Fatalf("EnsureBootstrapHook: %v", err)
	}

	info, err := os.Stat(filepath.Join(hookDir, provisioning.BootstrapScriptNameForTest))
	if err != nil {
		t.Fatalf("stat %s: %v", provisioning.BootstrapScriptNameForTest, err)
	}
	if execbit.Supported && info.Mode()&0o111 == 0 {
		t.Errorf("%s: expected executable bit after EnsureBootstrapHook, mode %v", provisioning.BootstrapScriptNameForTest, info.Mode())
	}
}

func TestEnsureBootstrapHook_Codex_MaterializzaERegistra(t *testing.T) {
	baseDir := t.TempDir()

	lock, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderCodex, provisioning.Lock{}, false)
	if err != nil {
		t.Fatalf("EnsureBootstrapHook: %v", err)
	}

	hookDir := filepath.Join(baseDir, ".codex", "hooks", provisioning.BootstrapHookName)
	for _, f := range []string{"hook.json", provisioning.BootstrapScriptNameForTest} {
		if _, err := os.Stat(filepath.Join(hookDir, f)); err != nil {
			t.Errorf("expected file %s: %v", f, err)
		}
	}

	// Registered in hooks.json (D230), once, however many times it runs.
	hooksPath := filepath.Join(baseDir, ".codex", "hooks.json")
	for run := 0; run < 2; run++ {
		if run == 1 {
			if _, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderCodex, lock, false); err != nil {
				t.Fatalf("EnsureBootstrapHook (2): %v", err)
			}
		}
		data, err := os.ReadFile(hooksPath)
		if err != nil {
			t.Fatalf("hooks.json not written: %v", err)
		}
		if n := strings.Count(string(data), provisioning.BootstrapHookName); n != 1 {
			t.Errorf("run %d: bootstrap hook named %d times in hooks.json, want 1:\n%s", run, n, data)
		}
		var doc map[string]map[string][]interface{}
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("hooks.json: %v", err)
		}
		if n := len(doc["hooks"]["SessionStart"]); n != 1 {
			t.Errorf("run %d: expected 1 SessionStart group, found %d:\n%s", run, n, data)
		}
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Errorf("the bootstrap hook must not touch config.toml: %v", err)
	}

	if len(lock.Managed) != 2 {
		t.Fatalf("expected 2 ManagedFile, got %d: %+v", len(lock.Managed), lock.Managed)
	}
}

func TestEnsureBootstrapHook_OpenCode_MaterializzaERegistra(t *testing.T) {
	baseDir := t.TempDir()

	lock, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderOpenCode, provisioning.Lock{}, false)
	if err != nil {
		t.Fatalf("EnsureBootstrapHook: %v", err)
	}

	hookDir := filepath.Join(baseDir, ".opencode", "hooks", provisioning.BootstrapHookName)
	for _, f := range []string{"hook.json", provisioning.BootstrapScriptNameForTest} {
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
		t.Fatalf("expected 3 ManagedFile (hook.json+script+plugin), got %d: %+v", len(lock.Managed), lock.Managed)
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

// SupportsSessionHook is the single answer both EnsureBootstrapHook and the
// client's "install the sync timer" advice derive from, and it has two distinct
// negative cases that must not collapse into one: kiro has no hook mechanism at
// all, antigravity has one whose engine exposes no session-start event (D194).
func TestSupportsSessionHook(t *testing.T) {
	for _, tc := range []struct {
		provider configurator.Provider
		want     bool
	}{
		{configurator.ProviderClaudeCode, true},
		{configurator.ProviderCodex, true},
		{configurator.ProviderOpenCode, true},
		{configurator.ProviderKiro, false},
		{configurator.ProviderHermes, false},
		{configurator.ProviderAntigravity, false},
	} {
		if got := provisioning.SupportsSessionHook(tc.provider); got != tc.want {
			t.Errorf("SupportsSessionHook(%s) = %v; want %v", tc.provider, got, tc.want)
		}
	}
}

func TestEnsureBootstrapHook_Antigravity_NoOp(t *testing.T) {
	baseDir := t.TempDir()
	lock, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderAntigravity, provisioning.Lock{}, false)
	if err != nil {
		t.Fatalf("EnsureBootstrapHook: %v", err)
	}
	if len(lock.Managed) != 0 {
		t.Fatalf("antigravity: expected no ManagedFile, got %+v", lock.Managed)
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".gemini")); err == nil {
		t.Errorf("antigravity: no .gemini directory was expected to be created")
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
	for _, f := range []string{"hook.json", provisioning.BootstrapScriptNameForTest} {
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
			{Kind: "hook", Name: provisioning.BootstrapHookName, Path: ".claude/hooks/cartographer-bootstrap/" + provisioning.BootstrapScriptNameForTest, ContentHash: "x"},
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

	scriptPath := filepath.Join(baseDir, ".claude", "hooks", provisioning.BootstrapHookName, provisioning.BootstrapScriptNameForTest)
	if _, err := os.Stat(scriptPath); err != nil {
		t.Errorf("the bootstrap script should not have disappeared from disk: %v", err)
	}
}

// TestApply_NomeRiservatoInKB_Warning verifies the name collision (D60): a KB
// defining a hook named exactly like BootstrapHookName is ignored by Apply
// with a warning, never materialized.
func TestApply_NomeRiservatoInKB_Warning(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, provisioning.BootstrapHookName, "PostToolUse", "", "./notify.sh")

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
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

// D216 WP2: the generated script must be one this platform can actually run.
// Asserted by property, not by exact text — the batch spelling is an
// implementation detail, the three guarantees are the contract (D60): silent,
// always exit 0, and an immediate exit when `cartographer` is not resolvable.
func TestBootstrapScriptIsRunnableOnThisPlatform(t *testing.T) {
	script := provisioning.BootstrapScriptContentForTest
	name := provisioning.BootstrapScriptNameForTest

	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(name, ".cmd") {
			t.Errorf("script name %q: Windows has no shebang, so the extension is what makes a file executable", name)
		}
		if !strings.Contains(script, "where cartographer") {
			t.Error("the script does not check that cartographer is resolvable: `where` is the batch `command -v`")
		}
		if !strings.Contains(script, ">nul") {
			t.Error("the script is not silent: >nul is the batch >/dev/null")
		}
		if !strings.Contains(script, "exit /b 0") {
			t.Error("the script cannot guarantee exit 0")
		}
		if !strings.Contains(script, "\r\n") {
			t.Error("a batch file is read by cmd.exe: CRLF, not LF")
		}
		if strings.Contains(script, "#!/bin/sh") {
			t.Error("the POSIX script leaked onto Windows: it is not executable there in any sense")
		}
	} else {
		if !strings.HasPrefix(script, "#!/bin/sh") {
			t.Errorf("script %q does not start with a shebang", script)
		}
		if !strings.Contains(script, "command -v cartographer") {
			t.Error("the script does not check that cartographer is resolvable")
		}
		if !strings.Contains(script, ">/dev/null") {
			t.Error("the script is not silent")
		}
		if !strings.Contains(script, "|| exit 0") && !strings.Contains(script, "|| true") {
			t.Error("the script cannot guarantee exit 0")
		}
	}

	if !strings.Contains(script, "cartographer sync --auto-trust") {
		t.Error("the script does not run the sync it exists for")
	}

	// D254: the update notice runs after the sync, keeps its stdout (that is
	// what reaches the agent) and discards its stderr.
	var noticeLine string
	for _, l := range strings.Split(strings.ReplaceAll(script, "\r\n", "\n"), "\n") {
		if strings.Contains(l, "cartographer update notice") {
			noticeLine = l
		}
	}
	if noticeLine == "" {
		t.Fatal("the script does not run `cartographer update notice`")
	}
	if strings.Index(script, "cartographer update notice") < strings.Index(script, "cartographer sync --auto-trust") {
		t.Error("the notice must run after the sync")
	}
	if stdout := strings.NewReplacer("2>nul", "", "2>/dev/null", "").Replace(noticeLine); strings.Contains(stdout, ">") {
		t.Errorf("the notice's stdout is redirected: %q", noticeLine)
	}
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(noticeLine, "2>nul") {
			t.Errorf("the notice's stderr is not discarded: %q", noticeLine)
		}
		if !strings.HasSuffix(script, "exit /b 0\r\n") {
			t.Error("the script must still end on exit /b 0")
		}
	} else if noticeLine != "cartographer update notice 2>/dev/null || true" {
		t.Errorf("notice line = %q", noticeLine)
	}
	// The session hook keeps --auto-trust, unlike the scheduled timer of D140.
	if strings.Contains(script, "service sync-timer") {
		t.Error("the bootstrap script is layer 1, not the scheduled trigger")
	}
}

// The hook JSON must name this platform's script, and the relative form it uses
// must be one resolveHookCommand resolves — it contains a separator, so it does
// (D216 WP3).
func TestBootstrapHookJSONReferencesThePlatformScript(t *testing.T) {
	baseDir := t.TempDir()
	if _, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderClaudeCode, provisioning.Lock{}, false); err != nil {
		t.Fatalf("EnsureBootstrapHook: %v", err)
	}
	hookDir := filepath.Join(baseDir, ".claude", "hooks", provisioning.BootstrapHookName)
	data, err := os.ReadFile(filepath.Join(hookDir, "hook.json"))
	if err != nil {
		t.Fatalf("read hook.json: %v", err)
	}
	var spec struct {
		Event   string `json:"event"`
		Command string `json:"command"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("parse hook.json: %v", err)
	}
	if want := "./" + provisioning.BootstrapScriptNameForTest; spec.Command != want {
		t.Errorf("hook.json command = %q, want %q", spec.Command, want)
	}
	if spec.Event != "SessionStart" {
		t.Errorf("hook.json event = %q, want SessionStart", spec.Event)
	}

	// And the command actually registered is the resolved absolute path of that
	// script, not the relative form: that is what the provider runs.
	settings, err := os.ReadFile(filepath.Join(baseDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	if !strings.Contains(string(settings), provisioning.BootstrapScriptNameForTest) {
		t.Errorf("settings.json does not reference %s:\n%s", provisioning.BootstrapScriptNameForTest, settings)
	}
}

// D254 WP2 trap: the bootstrap script's content hash is fixed and the hook is
// excluded from diffing, so nothing compares an installed script against the
// current one. What delivers a changed script to an already-connected client
// is that EnsureBootstrapHook rewrites both files unconditionally on every
// call — and `sync` calls it every run (cmd/cartographer runSync →
// ensureBootstrapForProviders), which the session-start hook itself triggers.
// Pinned here so a future "skip if already present" optimisation cannot strand
// existing machines on the old script until a reconnect.
func TestEnsureBootstrapHook_RewritesAnOutdatedScript(t *testing.T) {
	for _, p := range []configurator.Provider{configurator.ProviderClaudeCode, configurator.ProviderCodex, configurator.ProviderOpenCode} {
		t.Run(string(p), func(t *testing.T) {
			baseDir := t.TempDir()
			lock, err := provisioning.EnsureBootstrapHook(baseDir, p, provisioning.Lock{}, false)
			if err != nil {
				t.Fatal(err)
			}
			var scriptPath string
			for _, mf := range lock.Managed {
				if filepath.Base(mf.Path) == provisioning.BootstrapScriptNameForTest {
					scriptPath = filepath.Join(baseDir, mf.Path)
				}
			}
			if scriptPath == "" {
				t.Fatalf("no script among %+v", lock.Managed)
			}
			// The script an earlier release installed: sync only.
			old := "#!/bin/sh\ncommand -v cartographer >/dev/null 2>&1 || exit 0\ncartographer sync --auto-trust >/dev/null 2>&1 || true\n"
			if err := os.WriteFile(scriptPath, []byte(old), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := provisioning.EnsureBootstrapHook(baseDir, p, lock, false); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(scriptPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != provisioning.BootstrapScriptContentForTest {
				t.Fatalf("an outdated script was not rewritten:\n%s", got)
			}
		})
	}
}
