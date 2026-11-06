package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const scriptV1 = `def hello():
    return "hi"

command("hello", hello, "say hello")
tool("lookup", hello, "look a thing up", writes=True)
on("sync.after", hello)
`

func TestPluginValidateListsTheScriptsAndWhatTheyRegister(t *testing.T) {
	pluginFixture(t)
	dir := writePlugin(t, "team-forks", map[string]string{"plugin.star": scriptV1, "lib/util.star": "x = 1\n"})
	code, out := runPlugin(t, "validate", dir)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{
		"command team-forks hello  (plugin.star:4)", "tool team-forks_lookup (writes)  (plugin.star:5)", "event sync.after  (plugin.star:6)",
		"plugin.star  6 lines  sha256 ", "lib/util.star  1 lines  sha256 ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("validate lacks %q:\n%s", want, out)
		}
	}
	code, out = runPlugin(t, "validate", dir, "--output", "json")
	if code != 0 || !strings.Contains(out, `"registers"`) || !strings.Contains(out, "command team-forks hello") {
		t.Errorf("json: exit %d\n%s", code, out)
	}
}

func TestPluginValidateNamesWhereAScriptFails(t *testing.T) {
	pluginFixture(t)
	dir := writePlugin(t, "team-forks", map[string]string{"plugin.star": "x = 1\ncommand(\"Bad\", x, \"h\")\n"})
	code, out := runPlugin(t, "validate", dir)
	if code != 1 || !strings.Contains(out, "plugin.star:2:") || !strings.Contains(out, "must match") {
		t.Errorf("exit %d\n%s", code, out)
	}
	// An install of the same tree is refused: a plugin whose load fails is never installed.
	code, out = runPlugin(t, "install", dir, "--yes")
	if code == 0 || strings.Contains(out, "Enabled") {
		t.Errorf("a script that does not load was installed: exit %d\n%s", code, out)
	}
}

func TestPluginInstallShowsTheScriptsBeforeTheApproval(t *testing.T) {
	pluginFixture(t)
	dir := writePlugin(t, "team-forks", map[string]string{"plugin.star": scriptV1 + "# \x1b[2Jhidden\n"})
	code, out := runPlugin(t, "install", dir)
	if code != 0 || !strings.Contains(out, "pending approval") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"scripts (they run", "registers:", "tool team-forks_lookup (writes)", "--- plugin.star ---", `tool("lookup", hello`} {
		if !strings.Contains(out, want) {
			t.Errorf("install lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("a control character reached the terminal:\n%q", out)
	}
	if _, out = runPlugin(t, "show", "team-forks", "--files"); !strings.Contains(out, "--- plugin.star ---") {
		t.Errorf("show --files lacks the script:\n%s", out)
	}
}

func TestPluginUpdateShowsTheChangedScriptAndRegistrations(t *testing.T) {
	pluginFixture(t)
	dir := writePlugin(t, "team-forks", map[string]string{"plugin.star": scriptV1})
	runPlugin(t, "install", dir, "--yes")
	v2 := strings.Replace(scriptV1, `on("sync.after", hello)`, `tool("fetch", hello, "fetch")`+"\n# NEW-LINE", 1)
	if err := os.WriteFile(filepath.Join(dir, "plugin.star"), []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runPlugin(t, "update", "team-forks")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"file changed: plugin.star", "registers, added: tool team-forks_fetch", "registers, removed: event sync.after", "# NEW-LINE", "stays active"} {
		if !strings.Contains(out, want) {
			t.Errorf("update lacks %q:\n%s", want, out)
		}
	}
}
