package surveyor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

const pluginDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

const surveyorPlugin = "schema: 2\nname: feeds\nversion: 1.0.0\ndisplay_name: Feeds\ndescription: A feed reader.\ncartographer: \">=0.29.0\"\ncomponents:\n  surveyor: {}\n"

func TestLoadConfigPluginErrors(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, want string }{
		"no dir":       {"plugins:\n  feeds: {digest: " + pluginDigest + "}\n", "plugins_dir (line "},
		"bad name":     {"plugins_dir: /srv/p\nplugins:\n  Bad_Name: {digest: " + pluginDigest + "}\n", "plugins.Bad_Name (line "},
		"no digest":    {"plugins_dir: /srv/p\nplugins:\n  feeds: {enabled: true}\n", "plugins.feeds.digest (line "},
		"short digest": {"plugins_dir: /srv/p\nplugins:\n  feeds: {digest: abc}\n", "plugins.feeds.digest (line "},
		"disabled bad": {"plugins_dir: /srv/p\nplugins:\n  feeds: {digest: abc, enabled: false}\n", "plugins.feeds.digest"},
		"unknown key":  {"plugins_dir: /srv/p\nplugins:\n  feeds: {digest: " + pluginDigest + ", digests: x}\n", `unknown key "digests"`},
		"nested":       {"plugins_dir: /srv/p\nplugins:\n  feeds: {digest: " + pluginDigest + ", settings: {k: [1]}}\n", "plugins.feeds.settings.k"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := cfgLoad(t, cfgGolden+tc.yaml)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadConfigPlugins(t *testing.T) {
	c, err := cfgLoad(t, cfgGolden)
	if err != nil || len(c.PluginPins) != 0 || c.LoadPlugins(core.Options{}) != nil || len(c.Plugins) != 0 {
		t.Fatalf("no plugins key changes nothing: %v", err)
	}
	c, err = cfgLoad(t, cfgGolden+"plugins_dir: store\nplugins:\n  feeds: {digest: "+pluginDigest+", settings: {limit: 3}}\n  off: {digest: "+pluginDigest+", enabled: false}\n")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(c.PluginsDir) && !strings.HasSuffix(c.PluginsDir, "store") {
		t.Errorf("a relative plugins_dir is resolved against the config directory: %q", c.PluginsDir)
	}
	if p := c.PluginPins["feeds"]; !p.Enabled || p.Digest != pluginDigest || p.Settings["limit"] != 3 || p.Line == 0 || c.PluginPins["off"].Enabled {
		t.Errorf("%+v", c.PluginPins)
	}
}

func TestConfigLoadPluginsRefusals(t *testing.T) {
	opts := core.Options{RunningVersion: "0.29.0"}
	tree := []core.TreeFile{{Path: core.ManifestFile, Content: []byte(surveyorPlugin)}}
	p, probs := core.ReadTree(tree, opts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	store := t.TempDir()
	if err := (core.Store{Root: store}).Put("feeds", tree, p.Digest, opts); err != nil {
		t.Fatal(err)
	}
	load := func(name, digest string) (*Config, error) {
		c, err := cfgLoad(t, cfgGolden+"plugins_dir: "+store+"\nplugins:\n  "+name+": {digest: "+digest+"}\n")
		if err != nil {
			t.Fatal(err)
		}
		return c, c.LoadPlugins(opts)
	}
	c, err := load("feeds", p.Digest)
	if err != nil || len(c.Plugins) != 1 {
		t.Fatalf("pinned: %v %v", c.Plugins, err)
	}
	if _, err := load("feeds", pluginDigest); err == nil || !strings.Contains(err.Error(), "plugins.feeds (line ") {
		t.Errorf("a different digest must stop the start, naming the key: %v", err)
	}
	if _, err := load("absent", p.Digest); err == nil || !strings.Contains(err.Error(), "plugins.absent") {
		t.Errorf("missing: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store, "feeds", p.Digest, core.ManifestFile), []byte(surveyorPlugin+"# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := load("feeds", p.Digest); err == nil || !strings.Contains(err.Error(), "modified") {
		t.Errorf("a tree edited after the pin is refused: %v", err)
	}
}
