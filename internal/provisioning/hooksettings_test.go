package provisioning_test

// Tests for the automatic registration of Claude Code hooks in settings.json
// (D57). See provisioning_agent_hook_test.go for the pre-existing
// materialization tests (D48) and docs/decisions.md D57.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// hookEntryJSON/hookGroupJSON/settingsJSON mirror the shape Claude Code expects
// in .claude/settings.json for hooks, used to decode and assert on the file in
// these tests.
type hookEntryJSON struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type hookGroupJSON struct {
	Matcher string          `json:"matcher,omitempty"`
	Hooks   []hookEntryJSON `json:"hooks"`
}

type settingsJSON struct {
	Model string                     `json:"model,omitempty"`
	Hooks map[string][]hookGroupJSON `json:"hooks,omitempty"`
}

func readSettings(t *testing.T, path string) settingsJSON {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var s settingsJSON
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("parse settings.json: %v\n%s", err, data)
	}
	return s
}

// writeHookKB creates in kbRoot/hooks/<name>/ a hook.json (event/matcher/command) +
// a script, in the real format scanned by BuildManifest (see
// provisioning_agent_hook_test.go for the same format).
func writeHookKB(t *testing.T, kbRoot, name, event, matcher, command string) {
	t.Helper()
	hookDir := filepath.Join(kbRoot, "hooks", name)
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := map[string]string{"event": event, "command": command}
	if matcher != "" {
		spec["matcher"] = matcher
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "hook.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "notify.sh"), []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestApply_Hook_RegistraSettingsJSON(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")

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
	if len(res.Written) == 0 {
		t.Fatalf("Apply: expected Written not empty")
	}

	settingsPath := filepath.Join(baseDir, ".claude", "settings.json")
	s := readSettings(t, settingsPath)

	groups := s.Hooks["PostToolUse"]
	if len(groups) != 1 {
		t.Fatalf("expected 1 group for PostToolUse, got %d: %+v", len(groups), groups)
	}
	g := groups[0]
	if g.Matcher != "concept_write" {
		t.Errorf("matcher: expected concept_write, got %q", g.Matcher)
	}
	if len(g.Hooks) != 1 || g.Hooks[0].Type != "command" {
		t.Fatalf("hooks[]: expected 1 entry type=command, got %+v", g.Hooks)
	}
	wantCmd := filepath.Join(baseDir, ".claude", "hooks", "notify", "notify.sh")
	if g.Hooks[0].Command != wantCmd {
		t.Errorf("command: expected %q, got %q", wantCmd, g.Hooks[0].Command)
	}
}

func TestApply_Hook_ReApply_NessunDuplicato(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	opts := provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	}

	if _, err := provisioning.Apply(m, opts); err != nil {
		t.Fatalf("Apply (1): %v", err)
	}
	// Re-apply from scratch (same hook, same baseDir, no lock persisted in
	// between) — simulates the real case of a second sync/connect round:
	// it must update the existing entry, not append a second one.
	if _, err := provisioning.Apply(m, opts); err != nil {
		t.Fatalf("Apply (2): %v", err)
	}
	if _, err := provisioning.Apply(m, opts); err != nil {
		t.Fatalf("Apply (3): %v", err)
	}

	settingsPath := filepath.Join(baseDir, ".claude", "settings.json")
	s := readSettings(t, settingsPath)

	groups := s.Hooks["PostToolUse"]
	if len(groups) != 1 {
		t.Fatalf("expected 1 group after 3 applies, got %d: %+v (no duplicate expected)", len(groups), groups)
	}
	if len(groups[0].Hooks) != 1 {
		t.Fatalf("expected 1 hooks[] entry after 3 applies, got %d: %+v", len(groups[0].Hooks), groups[0].Hooks)
	}
}

func TestApply_Hook_ScriptMaterializzatoEseguibile(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	if _, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	hookDir := filepath.Join(baseDir, ".claude", "hooks", "notify")
	script, err := os.Stat(filepath.Join(hookDir, "notify.sh"))
	if err != nil {
		t.Fatalf("stat notify.sh: %v", err)
	}
	if script.Mode()&0o111 == 0 {
		t.Errorf("notify.sh: expected executable bit, mode %v", script.Mode())
	}
	spec, err := os.Stat(filepath.Join(hookDir, "hook.json"))
	if err != nil {
		t.Fatalf("stat hook.json: %v", err)
	}
	if spec.Mode()&0o111 != 0 {
		t.Errorf("hook.json: expected NOT executable, mode %v", spec.Mode())
	}
}

func TestApply_Hook_ComandoBare_VerbatimConMarkerCommento(t *testing.T) {
	kbRoot := t.TempDir()
	// "Bare" command (executable resolved via PATH, not a file of the hook): must
	// be registered verbatim — not joined to the hook's dir — with the D57
	// ownership marker appended as an inert trailing shell comment.
	bare := "jq -e '.tool_input.x' >/dev/null 2>&1 && exit 2 || true"
	writeHookKB(t, kbRoot, "env-block", "PreToolUse", "Edit|Write", bare)

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	opts := provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	}
	if _, err := provisioning.Apply(m, opts); err != nil {
		t.Fatalf("Apply (1): %v", err)
	}
	// Re-apply: the marker in the comment must make the entry recognizable → just one.
	if _, err := provisioning.Apply(m, opts); err != nil {
		t.Fatalf("Apply (2): %v", err)
	}

	settingsPath := filepath.Join(baseDir, ".claude", "settings.json")
	s := readSettings(t, settingsPath)
	groups := s.Hooks["PreToolUse"]
	if len(groups) != 1 || len(groups[0].Hooks) != 1 {
		t.Fatalf("expected 1 group with 1 entry for PreToolUse, got %+v", groups)
	}
	want := bare + " # cartographer-hook: .claude/hooks/env-block/"
	if got := groups[0].Hooks[0].Command; got != want {
		t.Errorf("command: expected %q, got %q", want, got)
	}
}

func TestApply_Hook_PreservaChiaviUtenteESettingsPreesistente(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	settingsPath := filepath.Join(baseDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	preexisting := `{
  "model": "sonnet",
  "hooks": {
    "PostToolUse": [
      {"matcher": "other_tool", "hooks": [{"type": "command", "command": "/usr/local/bin/user-hook.sh"}]}
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(preexisting), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	s := readSettings(t, settingsPath)
	if s.Model != "sonnet" {
		t.Errorf("model: expected preserved \"sonnet\", got %q", s.Model)
	}

	groups := s.Hooks["PostToolUse"]
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups for PostToolUse (user + cartographer), got %d: %+v", len(groups), groups)
	}

	var foundUser, foundOurs bool
	for _, g := range groups {
		if g.Matcher == "other_tool" && len(g.Hooks) == 1 && g.Hooks[0].Command == "/usr/local/bin/user-hook.sh" {
			foundUser = true
		}
		if g.Matcher == "concept_write" {
			foundOurs = true
		}
	}
	if !foundUser {
		t.Errorf("user hook not preserved: %+v", groups)
	}
	if !foundOurs {
		t.Errorf("cartographer hook not registered: %+v", groups)
	}
}

func TestApply_Hook_Removed_RimuoveEntrySettings(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	settingsPath := filepath.Join(baseDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	preexisting := `{
  "model": "sonnet",
  "hooks": {
    "PostToolUse": [
      {"matcher": "other_tool", "hooks": [{"type": "command", "command": "/usr/local/bin/user-hook.sh"}]}
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(preexisting), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply (materialize): %v", err)
	}

	// Remove the hook from the KB: the next Apply, with the previous round's
	// lock, must see it as Removed and strip the entry.
	if err := os.RemoveAll(filepath.Join(kbRoot, "hooks", "notify")); err != nil {
		t.Fatal(err)
	}
	m2, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest (2): %v", err)
	}

	res2, err := provisioning.Apply(m2, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     res.NewLock,
	})
	if err != nil {
		t.Fatalf("Apply (removal): %v", err)
	}
	if len(res2.Pruned) == 0 {
		t.Fatalf("Apply (removal): expected Pruned not empty")
	}

	s := readSettings(t, settingsPath)
	if s.Model != "sonnet" {
		t.Errorf("model: expected preserved \"sonnet\", got %q", s.Model)
	}
	groups := s.Hooks["PostToolUse"]
	if len(groups) != 1 {
		t.Fatalf("expected 1 remaining group (user only) after removal, got %d: %+v", len(groups), groups)
	}
	if groups[0].Matcher != "other_tool" || groups[0].Hooks[0].Command != "/usr/local/bin/user-hook.sh" {
		t.Errorf("user hook not preserved after removal: %+v", groups)
	}

	// The removed hook's files must no longer exist.
	if _, err := os.Stat(filepath.Join(baseDir, ".claude", "hooks", "notify", "hook.json")); !os.IsNotExist(err) {
		t.Error("Apply (removal): hook.json not removed")
	}
}

func TestPruneManaged_Hook_RimuoveEntrySettings(t *testing.T) {
	baseDir := t.TempDir()

	settingsPath := filepath.Join(baseDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	preexisting := `{
  "model": "sonnet",
  "hooks": {
    "PostToolUse": [
      {"matcher": "other_tool", "hooks": [{"type": "command", "command": "/usr/local/bin/user-hook.sh"}]},
      {"matcher": "concept_write", "hooks": [{"type": "command", "command": "` + filepath.Join(baseDir, ".claude", "hooks", "notify", "notify.sh") + `"}]}
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(preexisting), 0o644); err != nil {
		t.Fatal(err)
	}

	hookPath := filepath.Join(baseDir, ".claude", "hooks", "notify", "hook.json")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookPath, []byte(`{"event":"PostToolUse"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	managed := []provisioning.ManagedFile{
		{Kind: "hook", Name: "notify", Path: filepath.Join(".claude", "hooks", "notify", "hook.json"), ContentHash: "h"},
	}
	pruned, err := provisioning.PruneManaged(managed, baseDir, false)
	if err != nil {
		t.Fatalf("PruneManaged: %v", err)
	}
	if len(pruned) != 1 {
		t.Errorf("expected 1 pruned file, got %d", len(pruned))
	}
	if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
		t.Error("PruneManaged: hook.json not removed")
	}

	s := readSettings(t, settingsPath)
	if s.Model != "sonnet" {
		t.Errorf("model: expected preserved, got %q", s.Model)
	}
	groups := s.Hooks["PostToolUse"]
	if len(groups) != 1 {
		t.Fatalf("expected 1 remaining group (user only), got %d: %+v", len(groups), groups)
	}
	if groups[0].Matcher != "other_tool" {
		t.Errorf("user hook not preserved: %+v", groups)
	}
}

func TestPruneManaged_Hook_SettingsAssente_NoOp(t *testing.T) {
	// No pre-existing settings.json: PruneManaged must remove the files
	// without creating an empty one out of nowhere.
	baseDir := t.TempDir()
	hookPath := filepath.Join(baseDir, ".claude", "hooks", "notify", "hook.json")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookPath, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	managed := []provisioning.ManagedFile{
		{Kind: "hook", Name: "notify", Path: filepath.Join(".claude", "hooks", "notify", "hook.json"), ContentHash: "h"},
	}
	if _, err := provisioning.PruneManaged(managed, baseDir, false); err != nil {
		t.Fatalf("PruneManaged: %v", err)
	}

	settingsPath := filepath.Join(baseDir, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Error("PruneManaged must not create settings.json when it didn't exist")
	}
}
