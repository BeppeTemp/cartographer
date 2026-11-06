package clientconfig

import (
	"os"
	"strings"
	"testing"
)

const hex64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestPluginsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := Default()
	e := PluginEntry{Source: "https://example.com/team/forks.git", Ref: "v1.2", SHA: strings.Repeat("a", 40),
		Version: "1.2.0", Installed: hex64, Approved: hex64, Enabled: true,
		Settings: map[string]any{"mode": "fast", "retries": 3, "loud": true}}
	if err := cfg.SetPlugin("team-forks", e); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path(dir))
	if !strings.Contains(string(b), "plugins:") {
		t.Fatalf("not saved:\n%s", b)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	g, ok := got.Plugin("team-forks")
	if !ok || g.Source != e.Source || g.Approved != hex64 || !g.Enabled || g.Settings["retries"] != 3 || g.Settings["mode"] != "fast" {
		t.Errorf("round trip: %+v", g)
	}
	if !got.RemovePlugin("team-forks") || got.Plugins != nil || got.RemovePlugin("team-forks") {
		t.Error("RemovePlugin")
	}
	var nilCfg *Config
	if _, ok := nilCfg.Plugin("x"); ok {
		t.Error("nil config has a plugin")
	}
}

func TestPluginsLoadErrors(t *testing.T) {
	cases := map[string]struct{ yaml, want string }{
		"bad name":       {"../x:\n  source: /a\n", "not a plugin name"},
		"upper name":     {"Team:\n  source: /a\n", "not a plugin name"},
		"empty source":   {"team-forks:\n  enabled: true\n", "plugins.team-forks: source is empty"},
		"short sha":      {"team-forks:\n  source: /a\n  sha: abc\n", "sha"},
		"short digest":   {"team-forks:\n  source: /a\n  installed: abc\n", "installed"},
		"bad approved":   {"team-forks:\n  source: /a\n  installed: " + hex64 + "\n  approved: xyz\n", "approved"},
		"orphan approve": {"team-forks:\n  source: /a\n  approved: " + hex64 + "\n", "installed is empty"},
		"list setting":   {"team-forks:\n  source: /a\n  settings:\n    k: [1]\n", "settings.k"},
		"null setting":   {"team-forks:\n  source: /a\n  settings:\n    k: ~\n", "settings.k"},
	}
	for name, c := range cases {
		dir := t.TempDir()
		body := "plugins:\n" + indent(c.yaml)
		if err := os.WriteFile(Path(dir), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(dir)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	dir := t.TempDir()
	_ = os.WriteFile(Path(dir), []byte("plugins:\n  team-forks:\n    source: /a\n"), 0o600)
	if _, err := Load(dir); err != nil {
		t.Errorf("a pending entry: %v", err)
	}
	cfg := Default()
	if err := cfg.SetPlugin("../x", PluginEntry{Source: "/a"}); err == nil {
		t.Error("SetPlugin accepted a bad name")
	}
}

func indent(s string) string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		out = append(out, "  "+l)
	}
	return strings.Join(out, "\n") + "\n"
}

// A hidden key is in the table but in nothing a person reads: not in the comment
// block of a saved file and not in the settings report.
func TestHiddenKeyIsNotRendered(t *testing.T) {
	k, ok := Schema.Lookup("plugins")
	if !ok || !k.Hidden {
		t.Fatal("plugins must be a hidden schema row")
	}
	dir := t.TempDir()
	cfg := Default()
	if err := cfg.SetPlugin("team-forks", PluginEntry{Source: "/a"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path(dir))
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#") && strings.Contains(l, "plugins") {
			t.Errorf("a comment mentions the hidden key: %q", l)
		}
	}
	if !strings.Contains(string(b), "kb_options") {
		t.Errorf("the other keys lost their block:\n%s", b)
	}
	r, err := Report(dir, "v")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.Entries {
		if e.Key == "plugins" {
			t.Error("the settings report lists the hidden key")
		}
	}
}
