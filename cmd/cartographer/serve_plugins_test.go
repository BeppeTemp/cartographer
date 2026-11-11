package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

const serverOnlyPlugin = "schema: 2\nname: team-data\nversion: 1.2.0\ndisplay_name: Team data\ndescription: Server data.\ncartographer: \">=0.29.0\"\ncomponents:\n  server: {}\n"

// vendorServerPlugin stores a bare server-side plugin in a fresh store and
// returns the store root and the tree digest.
func vendorServerPlugin(t *testing.T, manifest string) (string, string) {
	t.Helper()
	opts := core.Options{RunningVersion: "0.29.0"}
	tree := []core.TreeFile{{Path: core.ManifestFile, Content: []byte(manifest)}}
	p, probs := core.ReadTree(tree, opts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	root := t.TempDir()
	if err := (core.Store{Root: root}).Put(p.Manifest.Name, tree, p.Digest, opts); err != nil {
		t.Fatal(err)
	}
	return root, p.Digest
}

func TestLoadServerPlugins(t *testing.T) {
	old := version
	version = "0.29.0"
	t.Cleanup(func() { version = old })
	root, digest := vendorServerPlugin(t, serverOnlyPlugin)
	cfgWith := func(name, d string, enabled bool) *config.Config {
		return &config.Config{PluginsDir: root, Plugins: map[string]config.PluginPin{name: {Digest: d, Enabled: enabled, Line: 7}}}
	}
	if got, err := loadServerPlugins(&config.Config{}); err != nil || got != nil {
		t.Fatalf("no pins load nothing: %v %v", got, err)
	}
	got, err := loadServerPlugins(cfgWith("team-data", digest, true))
	if err != nil || len(got) != 1 || got[0].Manifest.Name != "team-data" {
		t.Fatalf("pinned: %v %v", got, err)
	}
	if got, err := loadServerPlugins(cfgWith("team-data", strings.Repeat("a", 64), false)); err != nil || len(got) != 0 {
		t.Fatalf("a disabled pin is skipped unchecked: %v %v", got, err)
	}
	for name, tc := range map[string]struct {
		cfg  *config.Config
		want string
	}{
		"other digest": {cfgWith("team-data", strings.Repeat("a", 64), true), "team-data"},
		"missing":      {cfgWith("absent", digest, true), "absent"},
	} {
		if _, err := loadServerPlugins(tc.cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want a refusal naming %q", name, err, tc.want)
		}
	}
	// A tree edited after it was pinned is refused, never loaded with a warning.
	edited := filepath.Join(root, "team-data", digest, core.ManifestFile)
	if err := os.WriteFile(edited, []byte(serverOnlyPlugin+"# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadServerPlugins(cfgWith("team-data", digest, true)); err == nil || !strings.Contains(err.Error(), "plugins.team-data (line 7") {
		t.Errorf("modified tree: %v", err)
	}
	// A plugin without a server side is a start error too.
	root2, d2 := vendorServerPlugin(t, strings.Replace(serverOnlyPlugin, "server: {}", "surveyor: {}", 1))
	c := &config.Config{PluginsDir: root2, Plugins: map[string]config.PluginPin{"team-data": {Digest: d2, Enabled: true}}}
	if _, err := loadServerPlugins(c); err == nil || !strings.Contains(err.Error(), "no server side") {
		t.Errorf("no server side: %v", err)
	}
}
