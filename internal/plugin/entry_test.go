package plugin_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/plugin"
)

const entryManifest = "schema: 1\nname: team-forks\nversion: 1.2.3\ndisplay_name: Team forks\ndescription: Closed forks of supported clients.\ncartographer: \">=0.28.0 <0.30.0\"\n"
const entrySkill = "---\nname: helper\ndescription: Helps with a thing, use it when asked.\n---\n\n# Helper\n"

var entryOpts = plugin.Options{RunningVersion: "0.28.0"}

func entryTree() []plugin.TreeFile {
	return []plugin.TreeFile{
		{Path: plugin.ManifestFile, Content: []byte(entryManifest)},
		{Path: "skills/helper/SKILL.md", Content: []byte(entrySkill)},
	}
}

// The client's entry and a stub give the same status on every row of the table.
func TestStateClientEntryMatchesAnyEntry(t *testing.T) {
	s := plugin.Store{Root: filepath.Join(t.TempDir(), "plugins")}
	p, probs := plugin.ReadTree(entryTree(), entryOpts)
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
		opts     plugin.Options
		want     plugin.Status
	}{
		{"active", true, p.Digest, entryOpts, plugin.StatusActive},
		{"disabled", false, p.Digest, entryOpts, plugin.StatusDisabled},
		{"disabled wins over pending", false, "", entryOpts, plugin.StatusDisabled},
		{"pending", true, "", entryOpts, plugin.StatusPending},
		{"not fetched", true, other, entryOpts, plugin.StatusNotFetched},
		{"invalid", true, p.Digest, plugin.Options{RunningVersion: "0.0.1"}, plugin.StatusInvalid},
	}
	// A newer Installed digest never affects State: the approved tree is what runs.
	rows = append(rows, struct {
		name     string
		enabled  bool
		approved string
		opts     plugin.Options
		want     plugin.Status
	}{"newer installed is ignored", true, p.Digest, entryOpts, plugin.StatusActive})
	for _, r := range rows {
		ce := clientconfig.PluginEntry{Source: "/x", Installed: other, Approved: r.approved, Enabled: r.enabled}
		got, pl := plugin.State("team-forks", ce, s, r.opts)
		if got != r.want || (got == plugin.StatusActive) != (pl != nil) {
			t.Errorf("%s: %q, want %q", r.name, got, r.want)
		}
	}
}

// The config package cannot import this one, so it repeats the name rule.
func TestConfigNameRuleMatchesManifest(t *testing.T) {
	for _, n := range []string{"ab", "a", "team-forks", "Team", "a_b", "1ab", "../x", strings.Repeat("a", 41), strings.Repeat("a", 42), "kb", "bundle", "claude"} {
		var c clientconfig.Config
		err := c.SetPlugin(n, clientconfig.PluginEntry{Source: "/x"})
		if (err == nil) != plugin.NameRe.MatchString(n) {
			t.Errorf("%q: config accepts=%v, manifest rule=%v", n, err == nil, plugin.NameRe.MatchString(n))
		}
	}
}
