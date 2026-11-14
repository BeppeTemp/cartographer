package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/pluginserver"
)

const serverOnlyPlugin = "schema: 2\nname: team-data\nversion: 1.2.0\ndisplay_name: Team data\ndescription: Server data.\ncartographer: \">=0.29.0\"\ncomponents:\n  server: {}\n"

// vendorServerPlugin stores a bare server-side plugin in a fresh store and
// returns the store root and the tree digest.
func vendorServerPlugin(t *testing.T, manifest string) (string, string) {
	t.Helper()
	return vendorTree(t, []core.TreeFile{{Path: core.ManifestFile, Content: []byte(manifest)}})
}

// serverTree is a server plugin with the given files (path, content pairs).
func serverTree(files ...string) []core.TreeFile {
	out := []core.TreeFile{{Path: core.ManifestFile, Content: []byte(strings.Replace(serverOnlyPlugin, "server: {}", "server:\n    sdk: {roles: 1, presets: 1}", 1))}}
	for i := 0; i+1 < len(files); i += 2 {
		out = append(out, core.TreeFile{Path: files[i], Content: []byte(files[i+1])})
	}
	return out
}

// vendorTree is vendorServerPlugin for a whole tree.
func vendorTree(t *testing.T, tree []core.TreeFile) (string, string) {
	t.Helper()
	opts := core.Options{RunningVersion: "0.29.0"}
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
	if got, _, err := loadPinnedPlugins(&config.Config{}); err != nil || got != nil {
		t.Fatalf("no pins load nothing: %v %v", got, err)
	}
	got, _, err := loadPinnedPlugins(cfgWith("team-data", digest, true))
	if err != nil || len(got) != 1 || got[0].Manifest.Name != "team-data" {
		t.Fatalf("pinned: %v %v", got, err)
	}
	if got, _, err := loadPinnedPlugins(cfgWith("team-data", strings.Repeat("a", 64), false)); err != nil || len(got) != 0 {
		t.Fatalf("a disabled pin is skipped unchecked: %v %v", got, err)
	}
	for name, tc := range map[string]struct {
		cfg  *config.Config
		want string
	}{
		"other digest": {cfgWith("team-data", strings.Repeat("a", 64), true), "team-data"},
		"missing":      {cfgWith("absent", digest, true), "absent"},
	} {
		if _, _, err := loadPinnedPlugins(tc.cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want a refusal naming %q", name, err, tc.want)
		}
	}
	// A tree edited after it was pinned is refused, never loaded with a warning.
	edited := filepath.Join(root, "team-data", digest, core.ManifestFile)
	if err := os.WriteFile(edited, []byte(serverOnlyPlugin+"# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadPinnedPlugins(cfgWith("team-data", digest, true)); err == nil || !strings.Contains(err.Error(), "plugins.team-data (line 7") {
		t.Errorf("modified tree: %v", err)
	}
	// A plugin with neither a server nor an atlas side is a start error too.
	root2, d2 := vendorServerPlugin(t, strings.Replace(serverOnlyPlugin, "server: {}", "surveyor: {}", 1))
	c := &config.Config{PluginsDir: root2, Plugins: map[string]config.PluginPin{"team-data": {Digest: d2, Enabled: true}}}
	if _, _, err := loadPinnedPlugins(c); err == nil || !strings.Contains(err.Error(), "neither a server nor an atlas side") {
		t.Errorf("neither side: %v", err)
	}
}

// TestServerRolesAreConsumed drives a pinned plugin through the start path: its
// role expands into the operator's role, a token that names that role gets
// exactly its permissions and no other token gets any.
func TestServerRolesAreConsumed(t *testing.T) {
	old := version
	version = "0.29.0"
	t.Cleanup(func() { version = old })
	root, digest := vendorTree(t, serverTree(
		"roles/runbook-editor.yaml", "description: Edit runbooks.\nrules:\n  - access: rw\n    maps: [infra]\n    types: [Runbook]\n  - access: r\n",
	))
	cfg := &config.Config{PluginsDir: root, Plugins: map[string]config.PluginPin{"team-data": {Digest: digest, Enabled: true}}, Auth: config.AuthConfig{
		Roles:  []config.RoleSpec{{Name: "ops", From: "team-data/runbook-editor", KBs: []string{"docs"}}},
		Tokens: []config.TokenSpec{{Token: "t1", ID: "ci", Roles: []string{"ops"}}, {Token: "t2", ID: "other", Scopes: []string{"kb:docs:r"}}},
	}}
	plugins, _, err := loadPinnedPlugins(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a, err := config.ExpandPluginRoles(cfg.Auth, plugins, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	out := scopedTokensWithRoles(a.Tokens, a.Roles)
	p := out[0].Policy
	if !p.Allows("docs", "infra", "", "Runbook", true) || p.Allows("docs", "infra", "", "Service", true) || p.Allows("docs", "other", "", "Runbook", true) {
		t.Error("the write rule must grant exactly its map and type")
	}
	if !p.Allows("docs", "other", "", "Service", false) || p.Allows("elsewhere", "infra", "", "Runbook", false) {
		t.Error("the read rule applies to the named KB only")
	}
	if out[1].Policy.Allows("docs", "infra", "", "Runbook", true) {
		t.Error("a token that does not name the role gets nothing from it")
	}
}

// TestProxyCeilingJudgesPluginRoles: a proxy token naming a role from a plugin
// whose template selects maps is refused at start and on a reload, as the same
// rule written in auth.roles is.
func TestProxyCeilingJudgesPluginRoles(t *testing.T) {
	old := version
	version = "0.29.0"
	t.Cleanup(func() { version = old })
	root, digest := vendorTree(t, serverTree("roles/mapped.yaml", "description: One map.\nrules:\n  - access: r\n    maps: [infra]\n"))
	cfg := &config.Config{PluginsDir: root, Plugins: map[string]config.PluginPin{"team-data": {Digest: digest, Enabled: true}}, Auth: config.AuthConfig{
		Roles:  []config.RoleSpec{{Name: "ops", From: "team-data/mapped", KBs: []string{"docs"}}},
		Tokens: []config.TokenSpec{{Token: "t", ID: "sso", Roles: []string{"ops"}, Proxy: true}},
	}}
	plugins, _, err := loadPinnedPlugins(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for name, now := range map[string]time.Time{"start": time.Now(), "reload": {}} {
		if _, err := config.ExpandPluginRoles(cfg.Auth, plugins, now); err == nil || !strings.Contains(err.Error(), `"sso"`) {
			t.Errorf("%s: err = %v, want the proxy ceiling refusal", name, err)
		}
	}
}

// TestServerPresetsReachPresetList: a pinned plugin's preset becomes an entry of
// preset_list with the templates at their paths.
func TestServerPresetsReachPresetList(t *testing.T) {
	old := version
	version = "0.29.0"
	t.Cleanup(func() { version = old })
	root, digest := vendorTree(t, serverTree(
		"presets/runbooks/preset.yaml", "description: Runbook map.\ntitle: Runbooks\nkind: map\ntemplates: [runbook]\n",
		"presets/runbooks/templates/runbook.md", "---\ntype: Runbook\ntitle: {{title}}\n---\n# Procedure\n",
	))
	cfg := &config.Config{PluginsDir: root, Plugins: map[string]config.PluginPin{"team-data": {Digest: digest, Enabled: true}}}
	plugins, _, err := loadPinnedPlugins(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := presetEntries(plugins)
	if len(got) != 1 || got[0].ID != "team-data/runbooks" || got[0].PluginVersion != "1.2.0" || got[0].Templates[0].Path != "templates/runbook.md" || got[0].Templates[0].Content == "" {
		t.Fatalf("presets = %+v", got)
	}
}

// A pin needs one of the two sides the Meridian serves: an atlas-only plugin
// loads on the atlas side and is not a server plugin; a view over a route that
// is not a public GET stops the start.
func TestLoadPinnedPluginsAtlasSide(t *testing.T) {
	old := version
	version = "0.29.0"
	t.Cleanup(func() { version = old })
	manifest := strings.Replace(serverOnlyPlugin, "server: {}", "atlas:\n    sdk: {views: 1}", 1)
	view := func(route string) core.TreeFile {
		return core.TreeFile{Path: "views/sources.yaml", Content: []byte("title: Sources\nsource:\n  route: " + route + "\nrenderer: table\ncolumns:\n  - {key: name, label: Name}\n")}
	}
	tree := func(route string) []core.TreeFile {
		return []core.TreeFile{{Path: core.ManifestFile, Content: []byte(manifest)}, view(route)}
	}
	root, digest := vendorTree(t, tree("/api/v1/kbs/{kb}/sources"))
	cfg := &config.Config{PluginsDir: root, Plugins: map[string]config.PluginPin{"team-data": {Digest: digest, Enabled: true}}}
	server, atlas, err := loadPinnedPlugins(cfg)
	if err != nil || len(server) != 0 || len(atlas) != 1 || len(atlas[0].Contributions["views"]) != 1 {
		t.Fatalf("server %v atlas %v err %v", server, atlas, err)
	}
	if err := mcpserver.ValidateAtlasPlugins(atlas); err != nil {
		t.Fatal(err)
	}
	root, digest = vendorTree(t, tree("/api/ui/v1/kbs"))
	cfg = &config.Config{PluginsDir: root, Plugins: map[string]config.PluginPin{"team-data": {Digest: digest, Enabled: true}}}
	_, atlas, err = loadPinnedPlugins(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := mcpserver.ValidateAtlasPlugins(atlas); err == nil || !strings.Contains(err.Error(), "source.route") {
		t.Errorf("a UI API route must be refused: %v", err)
	}
}

const brandingOnlyPlugin = "schema: 2\nname: acme-brand\nversion: 1.0.0\ndisplay_name: Acme brand\ndescription: Acme branding.\ncartographer: \">=0.29.0\"\ncomponents:\n  atlas:\n    sdk: {branding: 1}\n"

// A plugin with only an atlas side is an atlas pin, not a server plugin: its
// branding reaches ActiveBranding from the atlas list, and a pin whose tree
// changed is refused like any other.
func TestAtlasLoadsABrandingOnlyPlugin(t *testing.T) {
	old := version
	version = "0.29.0"
	t.Cleanup(func() { version = old })
	root, digest := vendorTree(t, []core.TreeFile{
		{Path: core.ManifestFile, Content: []byte(brandingOnlyPlugin)},
		{Path: "branding/branding.yaml", Content: []byte("logo: {light: l.svg}\n")},
		{Path: "branding/l.svg", Content: []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`)},
	})
	cfg := &config.Config{PluginsDir: root, Plugins: map[string]config.PluginPin{"acme-brand": {Digest: digest, Enabled: true, Line: 4}}}
	server, atlas, err := loadPinnedPlugins(cfg)
	if err != nil || len(server) != 0 || len(atlas) != 1 {
		t.Fatalf("branding-only pin: server %v atlas %v err %v", server, atlas, err)
	}
	b, err := pluginserver.ActiveBranding(atlas)
	if err != nil || b == nil || b.Plugin != "acme-brand" {
		t.Fatalf("active: %+v %v", b, err)
	}
	cfg.Plugins["acme-brand"] = config.PluginPin{Digest: strings.Repeat("a", 64), Enabled: true, Line: 4}
	if _, _, err := loadPinnedPlugins(cfg); err == nil || !strings.Contains(err.Error(), "acme-brand") {
		t.Errorf("wrong digest: %v", err)
	}
}
