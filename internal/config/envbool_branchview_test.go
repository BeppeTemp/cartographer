package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// off/no are false, not the default true; a typo is an error, not a silent
// fallback that Origins still reports as set by the environment (#1501).
func TestFromEnvBooleansAreStrict(t *testing.T) {
	for _, name := range []string{"CARTOGRAPHER_WEB_ENABLED", "CARTOGRAPHER_UPDATE_CHECK"} {
		for _, v := range []string{"off", "no", "0", "false", "FALSE"} {
			t.Setenv(name, v)
			cfg := Default()
			if err := FromEnv(cfg); err != nil {
				t.Fatalf("%s=%s: %v", name, v, err)
			}
			got := cfg.Web.Enabled
			if name == "CARTOGRAPHER_UPDATE_CHECK" {
				got = cfg.UpdateCheck
			}
			if got {
				t.Errorf("%s=%s left the key true", name, v)
			}
		}
		for _, v := range []string{"on", "yes", "1", "true"} {
			t.Setenv(name, v)
			cfg := Default()
			if err := FromEnv(cfg); err != nil {
				t.Fatalf("%s=%s: %v", name, v, err)
			}
		}
		t.Setenv(name, "maybe")
		err := FromEnv(Default())
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s=maybe: err = %v, want an error naming the variable", name, err)
		}
		t.Setenv(name, "")
	}
}

// A kbs[] entry's own `branch` shows in the view and in the server YAML, with
// the file as origin, even when the KB's settings.yaml names a branch too.
func TestSettingsShowsTheKBEntryBranchOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yaml")
	body := "kbs:\n  - remote: https://git.example.com/user/kb-a.git\n    branch: team\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.KBs) != 1 || cfg.KBs[0].BranchOverride != "team" {
		t.Fatalf("override not loaded: %+v", cfg.KBs)
	}
	// The KB's own `branch` shares the origin key: it must not take over.
	cfg.RecordKBSettings("kb-a", cfg.KBs[0], []string{"branch"})

	view := cfg.Settings([]string{"kb-a"})
	var found *Entry
	for _, s := range view.KBs {
		for i, e := range s.Entries {
			if s.Name == "kb-a" && e.Key == "kbs[].branch" {
				found = &s.Entries[i]
			}
		}
	}
	if found == nil || found.Value != "team" || found.Origin != OriginFile {
		t.Fatalf("kbs[].branch = %+v, want value team, origin file", found)
	}
	out, err := view.ServerYAML()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "branch: team") {
		t.Errorf("server YAML lacks the override:\n%s", out)
	}
}
