package clientconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
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

// The plugins key is a visible schema row: the settings report lists it and the
// saved file documents it.
func TestPluginsKeyIsRendered(t *testing.T) {
	k, ok := Schema.Lookup("plugins")
	if !ok || k.Hidden {
		t.Fatal("plugins must be a visible schema row")
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
	if !strings.Contains(string(b), "knowledge_bases") || !strings.Contains(string(b), "team-forks") {
		t.Errorf("the saved file lost a block:\n%s", b)
	}
	r, err := Report(dir, "v")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range r.Entries {
		found = found || e.Key == "plugins"
	}
	if !found {
		t.Error("the settings report does not list plugins")
	}
}

func TestLoadPluginsReadsOnlyThePluginsKey(t *testing.T) {
	dir := t.TempDir()
	if got, err := LoadPlugins(dir); err != nil || got != nil {
		t.Errorf("no file: %v %v", got, err)
	}
	doc := "agents: [ghost-client]\nclients:\n  ghost-client: {kbs: []}\nplugins:\n  team-forks:\n    source: /tmp/x\n    enabled: true\n    approved: " + strings.Repeat("a", 64) + "\n    installed: " + strings.Repeat("a", 64) + "\n"
	if err := os.WriteFile(Path(dir), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPlugins(dir)
	if err != nil || !got["team-forks"].Enabled || got["team-forks"].Approved == "" {
		t.Errorf("LoadPlugins: %+v %v", got, err)
	}
}

// With plugins configured and no registration run, nothing is dropped; a
// complete registration restores the clean-up of names nobody knows (D898).
func TestUnknownProvidersAreKeptWhileThePluginProvidersAreIncomplete(t *testing.T) {
	dir := t.TempDir()
	doc := "clients:\n  ghost-client: {kbs: []}\nplugins:\n  team-forks:\n    source: /tmp/x\n    enabled: true\n"
	if err := os.WriteFile(Path(dir), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	old := PluginProvidersComplete
	t.Cleanup(func() { PluginProvidersComplete = old })
	cfg, err := Load(dir)
	if err != nil || len(cfg.Clients) != 1 || len(cfg.Unsupported) != 0 {
		t.Fatalf("default: %v clients %v unsupported %v", err, cfg.Clients, cfg.Unsupported)
	}
	PluginProvidersComplete = func(map[string]PluginEntry) bool { return true }
	cfg, err = Load(dir)
	if err != nil || len(cfg.Clients) != 0 || len(cfg.Unsupported) != 1 {
		t.Fatalf("complete: %v clients %v unsupported %v", err, cfg.Clients, cfg.Unsupported)
	}
}

// The light reader finds the entries of both layouts, expanded, skips what Load
// would refuse and a missing file is none.
func TestLoadAllowedSymlinks(t *testing.T) {
	dir := t.TempDir()
	if got, err := LoadAllowedSymlinks(dir); err != nil || got != nil {
		t.Fatalf("no file: %v %v", got, err)
	}
	src := "providers:\n  fork-cli:\n    allow_symlinks: [\"~/.fork-cli/skills\"]\nclients:\n  claude:\n    allow_symlinks: [\"$HOME/.claude/agents\"]\n"
	if err := os.WriteFile(Path(dir), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAllowedSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{filepath.Join(dir, ".claude", "agents"), filepath.Join(dir, ".fork-cli", "skills")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// The light set never exceeds what Load gives (C1): providers.<p> replaces
// clients.<p> for the same provider, and one invalid entry empties it as Load
// fails.
func TestLoadAllowedSymlinksNeverExceedsLoad(t *testing.T) {
	dir := t.TempDir()
	write := func(src string) {
		t.Helper()
		if err := os.WriteFile(Path(dir), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The same value under both keys loads; different ones are refused by Load.
	write("clients:\n  fork-cli:\n    allow_symlinks: [\"~/same\"]\nproviders:\n  fork-cli:\n    allow_symlinks: [\"~/same\"]\n")
	got, err := LoadAllowedSymlinks(dir)
	if _, lerr := Load(dir); err != nil || lerr != nil || !reflect.DeepEqual(got, []string{filepath.Join(dir, "same")}) {
		t.Fatalf("same value: %v %v %v", got, err, lerr)
	}
	write("clients:\n  fork-cli:\n    allow_symlinks: [\"~/old\"]\nproviders:\n  fork-cli:\n    allow_symlinks: [\"~/new\"]\n")
	if _, lerr := Load(dir); lerr == nil {
		t.Fatal("Load accepts two different values")
	}
	if got, err := LoadAllowedSymlinks(dir); err != nil || got != nil {
		t.Errorf("both keys differing must empty the set, got %v %v", got, err)
	}
	write("providers:\n  fork-cli:\n    allow_symlinks: [\"~/ok\", \"relative/x\"]\n")
	if _, lerr := Load(dir); lerr == nil {
		t.Fatal("Load accepts an invalid entry")
	}
	if got, err := LoadAllowedSymlinks(dir); err != nil || got != nil {
		t.Errorf("an invalid entry must empty the set, got %v %v", got, err)
	}
}
