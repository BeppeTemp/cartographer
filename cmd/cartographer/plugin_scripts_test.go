package main

import (
	"encoding/json"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
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

func TestPluginValidateJSONScriptKeysAreLowercase(t *testing.T) {
	pluginFixture(t)
	dir := writePlugin(t, "team-forks", map[string]string{"plugin.star": scriptV1})
	code, out := runPlugin(t, "validate", dir, "--output", "json")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	var doc struct {
		Scripts []map[string]any `json:"scripts"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Scripts) != 1 {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, k := range []string{"path", "lines", "bytes", "sha256"} {
		if _, ok := doc.Scripts[0][k]; !ok {
			t.Errorf("scripts[0] lacks key %q: %v", k, doc.Scripts[0])
		}
	}
	if len(doc.Scripts[0]) != 4 {
		t.Errorf("unexpected keys: %v", doc.Scripts[0])
	}
}

func TestPluginUpdateListsAChangedRegistration(t *testing.T) {
	pluginFixture(t)
	dir := writePlugin(t, "team-forks", map[string]string{"plugin.star": scriptV1})
	runPlugin(t, "install", dir, "--yes")
	v2 := strings.Replace(scriptV1, `"look a thing up"`, `"look a thing up and delete it"`, 1)
	v2 = strings.Replace(v2, `"say hello"`, `"say hello loudly"`, 1)
	if err := os.WriteFile(filepath.Join(dir, "plugin.star"), []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runPlugin(t, "update", "team-forks")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"registers, changed: tool team-forks_lookup (writes)", "registers, changed: command team-forks hello"} {
		if !strings.Contains(out, want) {
			t.Errorf("update lacks %q:\n%s", want, out)
		}
	}
}

func TestPluginApprovalShowsAScriptInFull(t *testing.T) {
	pluginFixture(t)
	body := scriptV1 + strings.Repeat("# filler\n", maxApprovalLines+50) + "# PAYLOAD-AFTER-THE-CAP\n"
	dir := writePlugin(t, "team-forks", map[string]string{"plugin.star": body})
	code, out := runPlugin(t, "install", dir)
	if code != 0 || !strings.Contains(out, "# PAYLOAD-AFTER-THE-CAP") || strings.Contains(out, "not shown") {
		t.Errorf("exit %d, the script was cut:\n%.300s", code, out)
	}
}

func TestSyncReportsAModifiedSourceWithoutRunningItsScript(t *testing.T) {
	pluginFixture(t)
	dir := writePlugin(t, "team-forks", map[string]string{"plugin.star": scriptV1})
	if code, out := runPlugin(t, "install", dir, "--yes"); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	home := os.Getenv("HOME")
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	e := cfg.Plugins["team-forks"]
	// A script that does not load: were it run first, the error would be
	// "not a valid plugin", not "modified".
	if err := os.WriteFile(filepath.Join(dir, "plugin.star"), []byte("this is not starlark (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = fetchApproved("team-forks", e)
	if err == nil || !strings.Contains(err.Error(), "reported as modified") {
		t.Errorf("a modified source: %v", err)
	}
	// The approved tree still passes, scripts included.
	if err := os.WriteFile(filepath.Join(dir, "plugin.star"), []byte(scriptV1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = fetchApproved("team-forks", e); err != nil {
		t.Errorf("the approved tree was refused: %v", err)
	}
}
