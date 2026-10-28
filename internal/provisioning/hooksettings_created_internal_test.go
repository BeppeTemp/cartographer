package provisioning

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// #1269: a settings file the user already had, even `{}`, survives the removal
// of our hook; one we created is deleted, with its ledger.
func TestRemoveHookEntries_DeletesOnlyAFileWeCreated(t *testing.T) {
	for name, tc := range map[string]struct {
		pre      string
		wantFile bool
	}{
		"user own {}": {pre: "{}", wantFile: true},
		"created":     {pre: "", wantFile: false},
	} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			settings := filepath.Join(base, ".claude", "settings.json")
			if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.pre != "" {
				if err := os.WriteFile(settings, []byte(tc.pre), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			hookDir := filepath.Join(base, "hook-src")
			if err := os.MkdirAll(hookDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(hookDir, "hook.json"), []byte(`{"event":"PostToolUse","command":"./notify.sh"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := registerHookSettings(base, "", "notify", hookDir); err != nil || !ok {
				t.Fatalf("register = %v, %v", ok, err)
			}
			if err := removeHookEntries(base, "notify", ".claude/settings.json"); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(settings)
			if (err == nil) != tc.wantFile {
				t.Errorf("settings.json exists=%v, want %v", err == nil, tc.wantFile)
			}
			if !tc.wantFile {
				if _, err := os.Stat(filepath.Join(base, ".cartographer-created.json")); !os.IsNotExist(err) {
					t.Errorf("ledger left behind: %v", err)
				}
			}
		})
	}
}

// Provenance ends with our last hook: a created file kept because it gained a
// key, then recreated by the user as `{}`, is theirs.
func TestRemoveHookEntries_StaleProvenanceKeepsUsersFile(t *testing.T) {
	base := t.TempDir()
	settings := filepath.Join(base, ".claude", "settings.json")
	hookDir := filepath.Join(base, "hook-src")
	for _, d := range []string{filepath.Dir(settings), hookDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(hookDir, "hook.json"), []byte(`{"event":"PostToolUse","command":"./notify.sh"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registerHookSettings(base, "", "notify", hookDir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(settings)
	if err := os.WriteFile(settings, append([]byte(`{"model":"opus",`), data[1:]...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeHookEntries(base, "notify", ".claude/settings.json"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(settings); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registerHookSettings(base, "", "notify", hookDir); err != nil {
		t.Fatal(err)
	}
	if err := removeHookEntries(base, "notify", ".claude/settings.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("the user's own settings.json must stay: %v", err)
	}
}

// A recreated user file that merely mentions the hook's name (here in an
// env value) holds no entry of ours: the stale provenance still ends, so the
// file stays once it is `{}` after our removal.
func TestRegisterHookSettings_NameMentionIsNotOwnership(t *testing.T) {
	base := t.TempDir()
	settings := filepath.Join(base, ".claude", "settings.json")
	hookDir := filepath.Join(base, "hook-src")
	for _, d := range []string{filepath.Dir(settings), hookDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(hookDir, "hook.json"), []byte(`{"event":"PostToolUse","command":"./notify.sh"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registerHookSettings(base, "", "notify", hookDir); err != nil {
		t.Fatal(err)
	}
	// The user deletes the file we created and writes their own.
	if err := os.WriteFile(settings, []byte(`{"env":{"NOTE":"notify"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registerHookSettings(base, "", "notify", hookDir); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	data, _ := os.ReadFile(settings)
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "env")
	data, _ = json.Marshal(m)
	if err := os.WriteFile(settings, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeHookEntries(base, "notify", ".claude/settings.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("the user's own settings.json must stay: %v", err)
	}
}
