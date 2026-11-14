package core_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

const entryManifest = "schema: 1\nname: team-forks\nversion: 1.2.3\ndisplay_name: Team forks\ndescription: Closed forks of supported clients.\ncartographer: \">=0.28.0 <0.30.0\"\n"
const entrySkill = "---\nname: helper\ndescription: Helps with a thing, use it when asked.\n---\n\n# Helper\n"

var entryOpts = core.Options{RunningVersion: "0.28.0"}

func entryTree() []core.TreeFile {
	return []core.TreeFile{
		{Path: core.ManifestFile, Content: []byte(entryManifest)},
		{Path: "skills/helper/SKILL.md", Content: []byte(entrySkill)},
	}
}

// The client's entry and a stub give the same status on every row of the table.
func TestStateClientEntryMatchesAnyEntry(t *testing.T) {
	s := core.Store{Root: filepath.Join(t.TempDir(), "plugins")}
	p, probs := core.ReadTree(entryTree(), entryOpts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	if err := s.Put("team-forks", entryTree(), p.Digest, entryOpts); err != nil {
		t.Fatal(err)
	}
	other := strings.Repeat("d", 64)
	rows := []struct {
		name     string
		enabled  bool
		approved string
		opts     core.Options
		want     core.Status
	}{
		{"active", true, p.Digest, entryOpts, core.StatusActive},
		{"disabled", false, p.Digest, entryOpts, core.StatusDisabled},
		{"disabled wins over pending", false, "", entryOpts, core.StatusDisabled},
		{"pending", true, "", entryOpts, core.StatusPending},
		{"not fetched", true, other, entryOpts, core.StatusNotFetched},
		{"invalid", true, p.Digest, core.Options{RunningVersion: "0.0.1"}, core.StatusInvalid},
	}
	// A newer Installed digest never affects State: the approved tree is what runs.
	rows = append(rows, struct {
		name     string
		enabled  bool
		approved string
		opts     core.Options
		want     core.Status
	}{"newer installed is ignored", true, p.Digest, entryOpts, core.StatusActive})
	for _, r := range rows {
		ce := clientconfig.PluginEntry{Source: "/x", Installed: other, Approved: r.approved, Enabled: r.enabled}
		got, pl := core.State("team-forks", ce, s, r.opts)
		if got != r.want || (got == core.StatusActive) != (pl != nil) {
			t.Errorf("%s: %q, want %q", r.name, got, r.want)
		}
	}
}

// The config package cannot import this one, so it repeats the name rule.
func TestConfigNameRuleMatchesManifest(t *testing.T) {
	for _, n := range []string{"ab", "a", "team-forks", "Team", "a_b", "1ab", "../x", strings.Repeat("a", 41), strings.Repeat("a", 42), "kb", "bundle", "claude"} {
		var c clientconfig.Config
		err := c.SetPlugin(n, clientconfig.PluginEntry{Source: "/x"})
		if (err == nil) != core.NameRe.MatchString(n) {
			t.Errorf("%q: config accepts=%v, manifest rule=%v", n, err == nil, core.NameRe.MatchString(n))
		}
	}
}

// IgnoreRange reads a tree whose `cartographer` range the running version does
// not meet, so plugin remove and plugin show can work on it; any other problem
// still refuses, and State (no IgnoreRange) keeps reading it as invalid.
func TestStoreOpenIgnoreRange(t *testing.T) {
	s := core.Store{Root: filepath.Join(t.TempDir(), "plugins")}
	p, probs := core.ReadTree(entryTree(), entryOpts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	if err := s.Put("team-forks", entryTree(), p.Digest, entryOpts); err != nil {
		t.Fatal(err)
	}
	newer := core.Options{RunningVersion: "0.30.0"}
	if _, err := s.Open("team-forks", p.Digest, newer); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("Open out of range = %v; want ErrInvalid", err)
	}
	newer.IgnoreRange = true
	got, err := s.Open("team-forks", p.Digest, newer)
	if err != nil {
		t.Fatalf("Open with IgnoreRange: %v", err)
	}
	if got.Manifest.Name != "team-forks" || got.Digest != p.Digest {
		t.Errorf("tree = %q %q; want the stored one", got.Manifest.Name, got.Digest)
	}
	ce := clientconfig.PluginEntry{Source: "/x", Installed: p.Digest, Approved: p.Digest, Enabled: true}
	for _, ignore := range []bool{false, true} {
		if st, pl := core.State("team-forks", ce, s, core.Options{RunningVersion: "0.30.0", IgnoreRange: ignore}); st != core.StatusInvalid || pl != nil {
			t.Errorf("State out of range (IgnoreRange %v) = %q; want invalid, never active", ignore, st)
		}
	}
	// Another problem still refuses, whatever IgnoreRange says.
	_, bad := core.ParseManifest([]byte("schema: 1\nname: Bad Name\ncartographer: \">=0.28.0 <0.29.0\"\n"), core.Options{RunningVersion: "0.30.0", IgnoreRange: true})
	if len(bad) == 0 || strings.Contains(strings.Join(bad, ";"), "needs Cartographer") {
		t.Errorf("problems = %q; want the name problem, not the range", bad)
	}
}
