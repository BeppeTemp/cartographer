package provisioning

import (
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
