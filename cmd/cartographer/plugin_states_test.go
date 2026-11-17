package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

func pluginStateName(t *testing.T, name string) core.Status {
	t.Helper()
	dir, err := clientconfig.TargetDir()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := pluginStoreFn()
	st, _ := core.State(name, cfg.Plugins[name], store, pluginOpts())
	return st
}

func pluginRunWord(name string) (int, bool) { return runPluginCommand(name, nil) }

func tamperApproved(t *testing.T, home, name string) {
	t.Helper()
	e := loadEntry(t, home, name)
	store, _ := pluginStoreFn()
	if err := os.WriteFile(filepath.Join(store.Root, name, e.Approved, "skills", "helper", "SKILL.md"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// #2197 item 1: list shows the active version, the pending one apart.
func TestPluginListShowsTheActiveVersionAndTheUpdateApart(t *testing.T) {
	pluginFixture(t)
	dir := writePlugin(t, "team-forks", nil)
	runPlugin(t, "install", dir, "--yes")
	manifest := "schema: 1\nname: team-forks\nversion: 1.1.0\ndisplay_name: Team forks\ndescription: Skills for the team.\ncartographer: \">=0.28.0\"\n"
	if err := os.WriteFile(filepath.Join(dir, "cartographer-plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	runPlugin(t, "update", "team-forks")
	_, out := runPlugin(t, "list")
	if !strings.Contains(out, "update available: 1.1.0") || strings.Contains(out, "team-forks          1.1.0") {
		t.Errorf("list must show the active version and the pending one apart:\n%s", out)
	}
	_, js := runPlugin(t, "list", "--output", "json")
	if !strings.Contains(js, `"pending_version": "1.1.0"`) && !strings.Contains(js, `"pending_version":"1.1.0"`) {
		t.Errorf("json lacks pending_version:\n%s", js)
	}
}

// #2197 item 2: one remedy per state, the same everywhere, and show works on a modified store.
func TestPluginStatesHaveOneRemedy(t *testing.T) {
	home := pluginFixture(t)
	installPlugin(t, writePlugin(t, "plug-modified", nil), true)
	installPlugin(t, writePlugin(t, "plug-off", nil), true)
	tamperApproved(t, home, "plug-modified")
	if code, out := runPlugin(t, "disable", "plug-off"); code != 0 {
		t.Fatalf("disable: %d %s", code, out)
	}

	code, out := runPlugin(t, "show", "plug-modified")
	if code != 0 || !strings.Contains(out, "state: modified") || !strings.Contains(out, "run `cartographer plugin update plug-modified`") {
		t.Errorf("show on a modified store: exit %d\n%s", code, out)
	}
	if code, js := runPlugin(t, "show", "plug-modified", "--output", "json"); code != 0 || !strings.Contains(js, "plugin update plug-modified") {
		t.Errorf("show json on a modified store: exit %d\n%s", code, js)
	}

	for name, want := range map[string]string{
		"plug-modified": "run `cartographer plugin update plug-modified`",
		"plug-off":      "run `cartographer plugin enable plug-off`",
	} {
		if got := stateRemedy(name, pluginStateName(t, name)); got != want {
			t.Errorf("remedy of %s = %q, want %q", name, got, want)
		}
	}
	var code2 int
	msg := stderrOf(t, func() { code2, _ = pluginRunWord("plug-modified") })
	if code2 != 2 || !strings.Contains(msg, "run `cartographer plugin update plug-modified`") || strings.Contains(msg, "plugin show") {
		t.Errorf("plugin command on a modified store: exit %d\n%s", code2, msg)
	}
	msg = stderrOf(t, func() { code2, _ = pluginRunWord("plug-off") })
	if !strings.Contains(msg, "run `cartographer plugin enable plug-off`") {
		t.Errorf("plugin command on a disabled plugin: exit %d\n%s", code2, msg)
	}
}

// #2197 item 3: update re-fetches a modified store.
func TestPluginUpdateRepairsAModifiedStore(t *testing.T) {
	home := pluginFixture(t)
	installPlugin(t, writePlugin(t, "plug-modified", nil), true)
	tamperApproved(t, home, "plug-modified")
	if st := pluginStateName(t, "plug-modified"); st != "modified" {
		t.Fatalf("fixture state = %s", st)
	}
	code, out := runPlugin(t, "update", "plug-modified", "--yes")
	if code != 0 || strings.Contains(out, "up to date") || !strings.Contains(out, "fetched again") {
		t.Fatalf("update: exit %d\n%s", code, out)
	}
	if st := pluginStateName(t, "plug-modified"); st != "active" {
		t.Errorf("state after update = %s, want active", st)
	}
}
