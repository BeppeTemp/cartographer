package provisioning_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// A settings.json a team versions, keys deliberately out of alphabetical order.
// The round trip keeps order, indent, characters and final newline, not a
// hand-compacted array: this file is written the way the encoder writes one.
const teamSettings = `{
  "permissions": {
    "allow": [
      "Bash(make test)"
    ]
  },
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit",
        "hooks": [
          {
            "type": "command",
            "command": "make fmt && echo <done>",
            "timeout": 30
          }
        ]
      }
    ]
  },
  "model": "sonnet"
}
`

// gitTrack makes dir a git repository tracking rels.
func gitTrack(t *testing.T, dir string, rels ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for _, args := range append([][]string{{"init", "-q"}}, append([]string{"add", "-f"}, rels...)) {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
}

func applyClaudeHook(t *testing.T, kbRoot, baseDir string, scope provisioning.Scope, hookSettings string, lock provisioning.Lock) provisioning.AppliedResult {
	t.Helper()
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots: map[string]string{"kb": kbRoot}, ApprovedMCP: approveAll(m),
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Scope: scope,
		HookSettingsRel: hookSettings, Lock: lock,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// #1198: wherever settings.json is rewritten, the round trip keeps its key
// order, its indent, its characters and its final newline. Registering a hook
// and pruning it gives back the very bytes the team versioned.
func TestClaudeSettings_RoundTripKeepsTheFile(t *testing.T) {
	for name, original := range map[string]string{
		"two spaces":  teamSettings,
		"four spaces": strings.ReplaceAll(teamSettings, "  ", "    "),
		"no newline":  strings.TrimSuffix(teamSettings, "\n"),
		"crlf":        strings.ReplaceAll(teamSettings, "\n", "\r\n"),
	} {
		t.Run(name, func(t *testing.T) {
			kbRoot := t.TempDir()
			writeHookKB(t, kbRoot, "guard", "PreToolUse", "Bash", "./notify.sh")
			baseDir := t.TempDir()
			path := filepath.Join(baseDir, ".claude", "settings.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
				t.Fatal(err)
			}
			res := applyClaudeHook(t, kbRoot, baseDir, provisioning.ScopeProject, "", provisioning.Lock{})
			got, _ := os.ReadFile(path)
			if !strings.Contains(string(got), "cartographer-hook") && !strings.Contains(string(got), ".claude/hooks/guard/") {
				t.Fatalf("the hook was not registered:\n%s", got)
			}
			if strings.HasSuffix(original, "\n") != strings.HasSuffix(string(got), "\n") {
				t.Errorf("final newline changed:\n%q", got)
			}
			for _, kept := range []string{`"matcher": "Edit",` + "\n", `"type": "command",` + "\n", "make fmt && echo <done>"} {
				if !strings.Contains(strings.ReplaceAll(string(got), "\r\n", "\n"), kept) {
					t.Errorf("lost %q (order or escaping):\n%s", kept, got)
				}
			}
			if strings.Index(string(got), `"permissions"`) > strings.Index(string(got), `"model"`) {
				t.Errorf("top-level keys reordered:\n%s", got)
			}

			if _, err := provisioning.PruneManaged(res.NewLock.Managed, baseDir, false); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(path); string(got) != original {
				t.Errorf("register then prune changed the file:\n--- got\n%s\n--- want\n%s", got, original)
			}
		})
	}
}

// #1198: with HookSettingsRel a project-scope Apply registers in
// settings.local.json and strips only there; settings.json is never written.
// A hook an earlier version registered in settings.json stays as it is (doctor
// reports it) and is not registered twice; once the user removes that entry,
// the next sync registers it in settings.local.json.
func TestApply_HookSettingsRelLeavesSettingsJSONAlone(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "guard", "PreToolUse", "Bash", "./notify.sh")
	baseDir := t.TempDir()
	settings := filepath.Join(baseDir, ".claude", "settings.json")
	localPath := filepath.Join(baseDir, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(teamSettings), 0o644); err != nil {
		t.Fatal(err)
	}
	// What a v0.26 derived sync left: the hook registered in a settings.json
	// the repository tracks.
	res := applyClaudeHook(t, kbRoot, baseDir, provisioning.ScopeProject, "", provisioning.Lock{})
	legacy, _ := os.ReadFile(settings)
	if !strings.Contains(string(legacy), ".claude/hooks/guard/") {
		t.Fatalf("setup: no legacy entry:\n%s", legacy)
	}
	gitTrack(t, baseDir, ".claude/settings.json")

	res = applyClaudeHook(t, kbRoot, baseDir, provisioning.ScopeProject, provisioning.ClaudeLocalSettings, res.NewLock)
	if got, _ := os.ReadFile(settings); string(got) != string(legacy) {
		t.Errorf("settings.json was written:\n%s", got)
	}
	// Nor when the hook changes: an update is not registered twice either.
	writeHookKB(t, kbRoot, "guard", "PostToolUse", "Edit", "./notify.sh")
	res = applyClaudeHook(t, kbRoot, baseDir, provisioning.ScopeProject, provisioning.ClaudeLocalSettings, res.NewLock)
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "still registered in the tracked .claude/settings.json") {
		t.Errorf("no warning for the hook left in settings.json: %v", res.Warnings)
	}
	if got, _ := os.ReadFile(settings); string(got) != string(legacy) {
		t.Errorf("settings.json was written:\n%s", got)
	}
	if got, err := os.ReadFile(localPath); err == nil && strings.Contains(string(got), "hooks/guard") {
		t.Errorf("registered twice, in settings.local.json too:\n%s", got)
	}

	// The user removes the old entry: the next sync registers the hook in
	// settings.local.json, and still never writes settings.json.
	if err := os.WriteFile(settings, []byte(teamSettings), 0o644); err != nil {
		t.Fatal(err)
	}
	writeHookKB(t, kbRoot, "guard", "PreToolUse", "Bash", "./notify.sh")
	res = applyClaudeHook(t, kbRoot, baseDir, provisioning.ScopeProject, provisioning.ClaudeLocalSettings, res.NewLock)
	local := readJSONFile(t, localPath)
	if hooks, _ := local["hooks"].(map[string]any); hooks["PreToolUse"] == nil {
		t.Errorf("hook not registered in settings.local.json: %v", local)
	}
	if got, _ := os.ReadFile(settings); string(got) != teamSettings {
		t.Errorf("settings.json was written:\n%s", got)
	}
	again := applyClaudeHook(t, kbRoot, baseDir, provisioning.ScopeProject, provisioning.ClaudeLocalSettings, res.NewLock)
	if len(again.Written) != 0 {
		t.Errorf("a converged projection rewrote %v", again.Written)
	}

	// The KB drops the hook: Apply prunes it from settings.local.json only,
	// even with an old entry for it back in settings.json.
	if err := os.WriteFile(settings, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	if err := os.MkdirAll(filepath.Join(empty, "concepts"), 0o755); err != nil {
		t.Fatal(err)
	}
	applyClaudeHook(t, empty, baseDir, provisioning.ScopeProject, provisioning.ClaudeLocalSettings, res.NewLock)
	if got, _ := os.ReadFile(localPath); strings.Contains(string(got), "hooks/guard") {
		t.Errorf("hook still registered in settings.local.json:\n%s", got)
	}
	if got, _ := os.ReadFile(settings); string(got) != string(legacy) {
		t.Errorf("the prune wrote settings.json:\n%s", got)
	}
}

// Outside a derived binding a prune that does not know where the hook was
// registered (disconnect) strips it from both settings files.
func TestPruneManaged_StripsBothClaudeSettingsFiles(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "guard", "PreToolUse", "Bash", "./notify.sh")
	baseDir := t.TempDir()
	res := applyClaudeHook(t, kbRoot, baseDir, provisioning.ScopeProject, provisioning.ClaudeLocalSettings, provisioning.Lock{})
	if _, err := provisioning.PruneManaged(res.NewLock.Managed, baseDir, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(baseDir, ".claude", "settings.local.json")); strings.Contains(string(got), "hooks/guard") {
		t.Errorf("disconnect left the registration in settings.local.json:\n%s", got)
	}
}

// A settings file that is not one JSON object is refused, never rewritten.
func TestClaudeSettings_MalformedFileIsRefused(t *testing.T) {
	for _, content := range []string{"{} {}", "[1]", "{\"a\": "} {
		kbRoot := t.TempDir()
		writeHookKB(t, kbRoot, "guard", "PreToolUse", "Bash", "./notify.sh")
		baseDir := t.TempDir()
		path := filepath.Join(baseDir, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = provisioning.Apply(m, provisioning.ApplyOptions{KBRoots: map[string]string{"kb": kbRoot}, ApprovedMCP: approveAll(m),
			Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Scope: provisioning.ScopeProject})
		if got, _ := os.ReadFile(path); string(got) != content {
			t.Errorf("%q was rewritten as %q", content, got)
		}
	}
}

// #1198: an entry an earlier version left in a settings.json git does not track
// is Cartographer's own to move: the derived projection strips it and
// registers the hook in settings.local.json, with no content change needed.
func TestApply_HookSettingsRelMovesAnUntrackedSettingsJSONEntry(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "guard", "PreToolUse", "Bash", "./notify.sh")
	baseDir := t.TempDir()
	gitTrack(t, baseDir)
	res := applyClaudeHook(t, kbRoot, baseDir, provisioning.ScopeProject, "", provisioning.Lock{})
	res = applyClaudeHook(t, kbRoot, baseDir, provisioning.ScopeProject, provisioning.ClaudeLocalSettings, res.NewLock)
	if got, _ := os.ReadFile(filepath.Join(baseDir, ".claude", "settings.json")); strings.Contains(string(got), "hooks/guard") {
		t.Errorf("the old entry stayed in the untracked settings.json:\n%s", got)
	}
	if got, _ := os.ReadFile(filepath.Join(baseDir, ".claude", "settings.local.json")); !strings.Contains(string(got), "hooks/guard") {
		t.Errorf("hook not registered in settings.local.json:\n%s", got)
	}
	if again := applyClaudeHook(t, kbRoot, baseDir, provisioning.ScopeProject, provisioning.ClaudeLocalSettings, res.NewLock); len(again.Written) != 0 {
		t.Errorf("a converged projection rewrote %v", again.Written)
	}
}

// A prune that does not know which file holds a registration leaves a
// settings.local.json git tracks alone: Cartographer never writes one.
func TestPruneManaged_LeavesATrackedSettingsLocalAlone(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "guard", "PreToolUse", "Bash", "./notify.sh")
	baseDir := t.TempDir()
	res := applyClaudeHook(t, kbRoot, baseDir, provisioning.ScopeProject, provisioning.ClaudeLocalSettings, provisioning.Lock{})
	gitTrack(t, baseDir, ".claude/settings.local.json")
	local := filepath.Join(baseDir, ".claude", "settings.local.json")
	before, _ := os.ReadFile(local)
	if _, err := provisioning.PruneManaged(res.NewLock.Managed, baseDir, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(local); string(got) != string(before) {
		t.Errorf("a tracked settings.local.json was rewritten:\n%s", got)
	}
}

// A settings file nested deeper than json.Unmarshal accepts is refused with an
// error, never a stack overflow.
func TestClaudeSettings_DeepNestingIsRefused(t *testing.T) {
	baseDir := t.TempDir()
	path := filepath.Join(baseDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := `{"guard": ` + strings.Repeat("[", 20000) + strings.Repeat("]", 20000) + "}"
	if err := os.WriteFile(path, []byte(deep), 0o644); err != nil {
		t.Fatal(err)
	}
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "guard", "PreToolUse", "Bash", "./notify.sh")
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = provisioning.Apply(m, provisioning.ApplyOptions{KBRoots: map[string]string{"kb": kbRoot}, ApprovedMCP: approveAll(m),
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Scope: provisioning.ScopeProject})
	if got, _ := os.ReadFile(path); string(got) != deep {
		t.Error("a too deeply nested settings.json was rewritten")
	}
}

// A workspace below the repository root (no .git of its own) still sees the
// settings.json git tracks there, and leaves it alone.
func TestApply_HookSettingsRelInARepositorySubdirectory(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "guard", "PreToolUse", "Bash", "./notify.sh")
	repo := t.TempDir()
	baseDir := filepath.Join(repo, "sub")
	res := applyClaudeHook(t, kbRoot, baseDir, provisioning.ScopeProject, "", provisioning.Lock{})
	gitTrack(t, repo, "sub/.claude/settings.json")
	settings := filepath.Join(baseDir, ".claude", "settings.json")
	legacy, _ := os.ReadFile(settings)
	writeHookKB(t, kbRoot, "guard", "PostToolUse", "Edit", "./notify.sh")
	applyClaudeHook(t, kbRoot, baseDir, provisioning.ScopeProject, provisioning.ClaudeLocalSettings, res.NewLock)
	if got, _ := os.ReadFile(settings); string(got) != string(legacy) {
		t.Errorf("a tracked settings.json below the repository root was written:\n%s", got)
	}
}
