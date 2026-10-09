package provisioning_test

// Tests for the client-side write-findings hook (D353): a second synthetic hook
// beside the bootstrap one (bootstrap.go), through the same code path.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func readPostToolUse(t *testing.T, baseDir string) []struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Command string `json:"command"`
	} `json:"hooks"`
} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(baseDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("settings.json: %v", err)
	}
	var s struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	return s.Hooks["PostToolUse"]
}

func TestEnsureWriteFindingsHook_Claude_InstallsIdempotently(t *testing.T) {
	baseDir := t.TempDir()
	// A user's own PostToolUse entry must survive.
	settings := `{"hooks":{"PostToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/usr/bin/true"}]}]}}`
	if err := os.MkdirAll(filepath.Join(baseDir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, ".claude", "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}

	lock, err := provisioning.EnsureWriteFindingsHook(baseDir, configurator.ProviderClaudeCode, provisioning.Lock{}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	lock, err = provisioning.EnsureWriteFindingsHook(baseDir, configurator.ProviderClaudeCode, lock, true, false)
	if err != nil {
		t.Fatal(err)
	}

	hookDir := filepath.Join(baseDir, ".claude", "hooks", provisioning.WriteFindingsHookName)
	script, err := os.ReadFile(filepath.Join(hookDir, provisioning.WriteFindingsScriptNameForTest))
	if err != nil {
		t.Fatalf("script: %v", err)
	}
	if !strings.Contains(string(script), "cartographer hook write-findings") {
		t.Errorf("script does not call the subcommand: %s", script)
	}
	if len(lock.Managed) != 2 {
		t.Fatalf("want 2 managed files after two runs, got %+v", lock.Managed)
	}

	var ours, user int
	for _, g := range readPostToolUse(t, baseDir) {
		if g.Matcher == "Bash" {
			user++
			continue
		}
		ours++
		for _, tool := range provisioning.WriteFindingsTools {
			if !strings.Contains(g.Matcher, tool) {
				t.Errorf("matcher %q lacks %s", g.Matcher, tool)
			}
		}
		if !strings.HasPrefix(g.Matcher, "mcp__.*__(") {
			t.Errorf("the server segment must be a wildcard: %q", g.Matcher)
		}
	}
	if ours != 1 || user != 1 {
		t.Errorf("want 1 own + 1 user entry, got own=%d user=%d", ours, user)
	}
}

func TestEnsureWriteFindingsHook_RewritesChangedScript(t *testing.T) {
	baseDir := t.TempDir()
	if _, err := provisioning.EnsureWriteFindingsHook(baseDir, configurator.ProviderClaudeCode, provisioning.Lock{}, true, false); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(baseDir, ".claude", "hooks", provisioning.WriteFindingsHookName, provisioning.WriteFindingsScriptNameForTest)
	if err := os.WriteFile(p, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := provisioning.EnsureWriteFindingsHook(baseDir, configurator.ProviderClaudeCode, provisioning.Lock{}, true, false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != provisioning.WriteFindingsScriptContentForTest {
		t.Errorf("script not rewritten: %q", got)
	}
}

func TestEnsureWriteFindingsHook_DryRunWritesNothing(t *testing.T) {
	baseDir := t.TempDir()
	lock, err := provisioning.EnsureWriteFindingsHook(baseDir, configurator.ProviderClaudeCode, provisioning.Lock{}, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Managed) == 0 {
		t.Error("dry run should simulate the managed paths")
	}
	if entries, _ := os.ReadDir(baseDir); len(entries) != 0 {
		t.Errorf("dry run wrote: %v", entries)
	}
}

func TestEnsureWriteFindingsHook_OtherProvidersAreNoOps(t *testing.T) {
	for _, p := range []configurator.Provider{
		configurator.ProviderOpenCode,
		configurator.ProviderAntigravity, configurator.ProviderKiro,
	} {
		baseDir := t.TempDir()
		lock, err := provisioning.EnsureWriteFindingsHook(baseDir, p, provisioning.Lock{}, true, false)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if len(lock.Managed) != 0 {
			t.Errorf("%s: lock changed: %+v", p, lock.Managed)
		}
		if entries, _ := os.ReadDir(baseDir); len(entries) != 0 {
			t.Errorf("%s: wrote %v", p, entries)
		}
	}
}

func TestEnsureWriteFindingsHook_DisabledRemovesAndIsIdempotent(t *testing.T) {
	baseDir := t.TempDir()
	boot, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderClaudeCode, provisioning.Lock{}, false)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := provisioning.EnsureWriteFindingsHook(baseDir, configurator.ProviderClaudeCode, boot, true, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		lock, err = provisioning.EnsureWriteFindingsHook(baseDir, configurator.ProviderClaudeCode, lock, false, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, mf := range lock.Managed {
		if mf.Name == provisioning.WriteFindingsHookName {
			t.Errorf("entry survived: %+v", mf)
		}
	}
	if len(lock.Managed) != len(boot.Managed) {
		t.Errorf("the bootstrap entries must be untouched: %+v", lock.Managed)
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".claude", "hooks", provisioning.WriteFindingsHookName)); !os.IsNotExist(err) {
		t.Errorf("hook dir still there: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(baseDir, ".claude", "settings.json"))
	if strings.Contains(string(data), provisioning.WriteFindingsHookName) || strings.Contains(string(data), "PostToolUse") {
		t.Errorf("settings.json still registers it: %s", data)
	}
	if !strings.Contains(string(data), provisioning.BootstrapHookName) {
		t.Errorf("the bootstrap registration was lost: %s", data)
	}
}

func TestPruneManaged_WriteFindingsHook_DisconnectRemovesAll(t *testing.T) {
	baseDir := t.TempDir()
	lock, err := provisioning.EnsureWriteFindingsHook(baseDir, configurator.ProviderClaudeCode, provisioning.Lock{}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provisioning.PruneManaged(lock.Managed, baseDir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".claude", "hooks", provisioning.WriteFindingsHookName)); !os.IsNotExist(err) {
		t.Errorf("hook dir still there: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(baseDir, ".claude", "settings.json"))
	if strings.Contains(string(data), provisioning.WriteFindingsHookName) {
		t.Errorf("settings.json still registers it: %s", data)
	}
}

func TestWriteFindingsHook_ReservedNameAndNotAnOrphan(t *testing.T) {
	// Not an orphan in ComputeDiff.
	lock := provisioning.Lock{AppliedRevision: "r", Managed: []provisioning.ManagedFile{
		{Kind: "hook", Name: provisioning.WriteFindingsHookName, Path: ".claude/hooks/cartographer-write-findings/hook.json", ContentHash: "x"},
	}}
	if d := provisioning.ComputeDiff(provisioning.Manifest{Revision: "r"}, lock); len(d.Removed) != 0 {
		t.Errorf("orphan: %+v", d.Removed)
	}

	// A KB hook of the same name is ignored with a warning.
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, provisioning.WriteFindingsHookName, "PostToolUse", "", "./x.sh")
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots: map[string]string{"kb": kbRoot}, Provider: configurator.ProviderClaudeCode,
		BaseDir: t.TempDir(), Lock: provisioning.Lock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range res.Written {
		if w.Kind == "hook" {
			t.Errorf("reserved KB hook written: %+v", w)
		}
	}
	warned := false
	for _, w := range res.Warnings {
		warned = warned || strings.Contains(w, "reserved")
	}
	if !warned {
		t.Errorf("no reserved-name warning: %v", res.Warnings)
	}
}

// D139/D143: the entry carries a MaterializedHash, so on-disk verification
// covers it and reports a modified script.
func TestWriteFindingsHook_CoveredByVerification(t *testing.T) {
	baseDir := t.TempDir()
	lock, err := provisioning.EnsureWriteFindingsHook(baseDir, configurator.ProviderClaudeCode, provisioning.Lock{}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if f := provisioning.VerifyManaged(lock, configurator.ProviderClaudeCode, baseDir); len(f) != 0 {
		t.Fatalf("clean install reports drift: %+v", f)
	}
	p := filepath.Join(baseDir, ".claude", "hooks", provisioning.WriteFindingsHookName, provisioning.WriteFindingsScriptNameForTest)
	if err := os.WriteFile(p, []byte("changed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if f := provisioning.VerifyManaged(lock, configurator.ProviderClaudeCode, baseDir); len(f) == 0 {
		t.Error("a modified script is not reported")
	}
}

func TestWriteFindingsScript_Guarantees(t *testing.T) {
	s := provisioning.WriteFindingsScriptContentForTest
	if !strings.Contains(s, "cartographer hook write-findings") {
		t.Error("does not run the subcommand")
	}
	if strings.Contains(s, "command -v cartographer") == strings.Contains(s, "where cartographer") {
		t.Error("must check that cartographer is resolvable (exactly the platform's spelling)")
	}
}

func TestEnsureWriteFindingsHook_Codex_UsesContextChannel(t *testing.T) {
	baseDir := t.TempDir()
	hooksJSON := `{"hooks":{"PostToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/usr/bin/true"}]}]}}`
	if err := os.MkdirAll(filepath.Join(baseDir, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, ".codex", "hooks.json"), []byte(hooksJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := provisioning.EnsureWriteFindingsHook(baseDir, configurator.ProviderCodex, provisioning.Lock{}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	hookDir := filepath.Join(baseDir, ".codex", "hooks", provisioning.WriteFindingsHookName)
	for _, f := range []string{"hook.json", provisioning.WriteFindingsScriptNameForTest} {
		if _, err := os.Stat(filepath.Join(hookDir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	spec, _ := os.ReadFile(filepath.Join(hookDir, "hook.json"))
	if !strings.Contains(string(spec), "--channel context") {
		t.Errorf("hook.json lacks the channel argument: %s", spec)
	}
	data, err := os.ReadFile(filepath.Join(baseDir, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	var ours, user int
	for _, g := range s.Hooks["PostToolUse"] {
		if g.Matcher == "Bash" {
			user++
			continue
		}
		ours++
		if !strings.HasPrefix(g.Matcher, "mcp__.*__(") {
			t.Errorf("matcher %q", g.Matcher)
		}
		if len(g.Hooks) != 1 || !strings.Contains(g.Hooks[0].Command, provisioning.WriteFindingsScriptNameForTest) || !strings.HasSuffix(strings.SplitN(g.Hooks[0].Command, " #", 2)[0], "--channel context") {
			t.Errorf("command %+v lacks the shim + channel argument", g.Hooks)
		}
	}
	if ours != 1 || user != 1 {
		t.Errorf("want 1 own + 1 user entry, got own=%d user=%d", ours, user)
	}

	// Claude's registration keeps the default channel.
	claudeDir := t.TempDir()
	if _, err := provisioning.EnsureWriteFindingsHook(claudeDir, configurator.ProviderClaudeCode, provisioning.Lock{}, true, false); err != nil {
		t.Fatal(err)
	}
	cs, _ := os.ReadFile(filepath.Join(claudeDir, ".claude", "hooks", provisioning.WriteFindingsHookName, "hook.json"))
	if strings.Contains(string(cs), "--channel") {
		t.Errorf("claude hook.json must keep the default channel: %s", cs)
	}

	// enabled=false removes it, foreign entry preserved.
	for i := 0; i < 2; i++ {
		if lock, err = provisioning.EnsureWriteFindingsHook(baseDir, configurator.ProviderCodex, lock, false, false); err != nil {
			t.Fatal(err)
		}
	}
	if len(lock.Managed) != 0 {
		t.Errorf("entries survived: %+v", lock.Managed)
	}
	if _, err := os.Stat(hookDir); !os.IsNotExist(err) {
		t.Errorf("hook dir still there: %v", err)
	}
	data, _ = os.ReadFile(filepath.Join(baseDir, ".codex", "hooks.json"))
	if strings.Contains(string(data), provisioning.WriteFindingsHookName) || !strings.Contains(string(data), "/usr/bin/true") {
		t.Errorf("hooks.json after removal: %s", data)
	}
}
