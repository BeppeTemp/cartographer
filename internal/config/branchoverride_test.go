package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadKBsYAML(t *testing.T, body string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

// D1093: kbs[].branch loads into its own field, trimmed, and leaves the
// settings view's Branch alone.
func TestLoadKBBranchOverride(t *testing.T) {
	cfg, err := loadKBsYAML(t, "kbs:\n  - remote: https://git.example.com/user/kb-a.git\n    branch: \"  try-cartographer \"\n  - remote: https://git.example.com/user/kb-b.git\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.KBs[0].BranchOverride; got != "try-cartographer" {
		t.Errorf("BranchOverride = %q", got)
	}
	if cfg.KBs[0].Branch != "" || cfg.KBs[1].BranchOverride != "" {
		t.Errorf("KBs = %+v: Branch is the KB's setting, and an absent key is no override", cfg.KBs)
	}
	if len(cfg.Warnings) != 0 || len(cfg.Refusals) != 0 {
		t.Errorf("warnings %v refusals %v", cfg.Warnings, cfg.Refusals)
	}
}

// An invalid override stops startup, naming the key and the entry.
func TestLoadKBBranchOverrideInvalid(t *testing.T) {
	for _, v := range []string{"a..b", "-x", "HEAD", `"   "`, "has space"} {
		_, err := loadKBsYAML(t, "kbs:\n  - remote: https://git.example.com/user/kb-a.git\n  - remote: https://git.example.com/user/kb-b.git\n    branch: "+quoteIfNeeded(v)+"\n")
		if err == nil || !strings.Contains(err.Error(), "kbs[1].branch") {
			t.Errorf("branch %s: err = %v, want kbs[1].branch named", v, err)
		}
	}
}

func quoteIfNeeded(v string) string {
	if strings.Contains(v, " ") && !strings.HasPrefix(v, `"`) {
		return `"` + v + `"`
	}
	return v
}

// ResolveKBSettings carries the override onto the effective spec, which is what
// the mount sees (D1093).
func TestResolveKBSettingsKeepsTheOverride(t *testing.T) {
	eff, _ := ResolveKBSettings(KBSpec{Name: "kb-a", BranchOverride: "mine"}, parse(t, "branch: team-kb\n"))
	if eff.BranchOverride != "mine" || eff.Branch != "team-kb" {
		t.Errorf("effective spec = %+v, want the override and the KB's own branch kept apart", eff)
	}
}
