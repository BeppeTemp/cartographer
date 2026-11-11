package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/execbit"
	"github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func pluginFixture(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	setHome(t, home)
	store := plugin.Store{Root: filepath.Join(t.TempDir(), "plugins")}
	oldStore, oldTTY := pluginStoreFn, pluginInteractive
	pluginStoreFn = func() (plugin.Store, error) { return store, nil }
	pluginInteractive = func() bool { return false }
	t.Cleanup(func() {
		pluginStoreFn, pluginInteractive = oldStore, oldTTY
		// The plugin commands register providers (registerPluginProviders): leave no registry behind.
		_, _ = configurator.ReplacePlugins(nil)
		provisioning.ReplacePluginCells(nil, home)
		clientconfig.PluginProvidersComplete = func(p map[string]clientconfig.PluginEntry) bool { return len(p) == 0 }
	})
	return home
}

// writePlugin writes a valid plugin named name into a new folder, plus extra files.
func writePlugin(t *testing.T, name string, extra map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"cartographer-plugin.yaml": "schema: 1\nname: " + name + "\nversion: 1.0.0\ndisplay_name: Team forks\ndescription: Skills for the team.\ncartographer: \">=0.28.0\"\npermissions:\n  net: [a.example.com]\n",
		"skills/helper/SKILL.md":   "---\nname: helper\ndescription: Helps with a thing, use it when asked.\n---\n\n# Helper\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	for p, c := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runPlugin(t *testing.T, args ...string) (code int, out string) {
	t.Helper()
	out = withStdout(t, func() { code = cmdPlugin(args) })
	return code, out
}

func TestPluginInstallYesListShowValidate(t *testing.T) {
	pluginFixture(t)
	dir := writePlugin(t, "team-forks", nil)

	code, out := runPlugin(t, "install", dir, "--yes")
	if code != 0 {
		t.Fatalf("install: exit %d\n%s", code, out)
	}
	for _, want := range []string{"plugin team-forks 1.0.0", "may reach hosts: a.example.com", `skill "helper"`, "Enabled team-forks"} {
		if !strings.Contains(out, want) {
			t.Errorf("install output lacks %q:\n%s", want, out)
		}
	}
	if code, out = runPlugin(t, "list"); code != 0 || !strings.Contains(out, "team-forks") || !strings.Contains(out, "active") {
		t.Errorf("list: exit %d\n%s", code, out)
	}
	code, out = runPlugin(t, "list", "--output", "json")
	var doc struct {
		Schema  string
		Plugins []pluginRow
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 0 {
		t.Fatalf("list json: %v exit %d\n%s", err, code, out)
	}
	if doc.Schema != pluginSchema || len(doc.Plugins) != 1 || doc.Plugins[0].State != "active" || doc.Plugins[0].Contributions["skills"] != 1 {
		t.Errorf("list json: %+v", doc)
	}
	if code, out = runPlugin(t, "show", "team-forks", "--files"); code != 0 || !strings.Contains(out, "showing the approved tree") || !strings.Contains(out, "skills/helper/SKILL.md") {
		t.Errorf("show: exit %d\n%s", code, out)
	}
	if code, out = runPlugin(t, "show", "team-forks", "--output", "json"); code != 0 || !strings.Contains(out, pluginSchema) {
		t.Errorf("show json: exit %d\n%s", code, out)
	}
	if code, out = runPlugin(t, "show", "nope"); code != 2 {
		t.Errorf("show of an unknown plugin: exit %d", code)
	}
	if code, out = runPlugin(t, "validate", dir); code != 0 || !strings.Contains(out, "is valid") {
		t.Errorf("validate: exit %d\n%s", code, out)
	}
}

func TestPluginInstallWithoutTerminalStaysPending(t *testing.T) {
	pluginFixture(t)
	dir := writePlugin(t, "team-forks", nil)
	code, out := runPlugin(t, "install", dir)
	if code != 0 || !strings.Contains(out, "pending approval: run `cartographer plugin install") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if _, out = runPlugin(t, "list"); !strings.Contains(out, "pending approval") {
		t.Errorf("list does not show the pending state:\n%s", out)
	}
}

func TestPluginInstallShowsWhatRuns(t *testing.T) {
	if !execbit.Supported {
		t.Skip("a hook script needs the execute bit")
	}
	pluginFixture(t)
	dir := writePlugin(t, "team-forks", map[string]string{
		"hooks/guard/hook.json": `{"event":"Stop","command":"./run.sh"}`,
		"hooks/guard/run.sh":    "#!/bin/sh\necho SCRIPT-BODY\n",
	})
	if err := os.Chmod(filepath.Join(dir, "hooks", "guard", "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out := runPlugin(t, "install", dir)
	if code != 0 || !strings.Contains(out, "SCRIPT-BODY") || !strings.Contains(out, `"command":"./run.sh"`) {
		t.Errorf("the install output must show the hook script and hook.json: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "description: Helps with a thing, use it when asked.") {
		t.Errorf("the skill description is part of the preview:\n%s", out)
	}
}

func TestPluginInstallRefusesAnotherSourceUnderTheSameName(t *testing.T) {
	pluginFixture(t)
	first, second := writePlugin(t, "team-forks", nil), writePlugin(t, "team-forks", nil)
	if code, out := runPlugin(t, "install", first, "--yes"); code != 0 {
		t.Fatalf("first install: exit %d\n%s", code, out)
	}
	var code int
	errOut := withStderr(t, func() { code, _ = runPlugin(t, "install", second, "--yes") })
	if code != 2 || !strings.Contains(errOut, "cartographer plugin remove team-forks") {
		t.Errorf("exit %d\n%s", code, errOut)
	}
	// The same source is an update and keeps the approval.
	if code, out := runPlugin(t, "install", first); code != 0 || !strings.Contains(out, "already approved and enabled") {
		t.Errorf("same source: exit %d\n%s", code, out)
	}
}

func TestPluginInstallFromGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	pluginFixture(t)
	work := writePlugin(t, "git-forks", nil)
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-q", "-m", "one"}} {
		if out, err := exec.Command("git", append([]string{"-C", work}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	code, out := runPlugin(t, "install", fileURL(work), "--yes")
	if code != 0 || !strings.Contains(out, "Enabled git-forks") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if _, out = runPlugin(t, "list", "--output", "json"); !strings.Contains(out, `"sha": "`) {
		t.Errorf("a git install records the commit:\n%s", out)
	}
}

func TestPluginNewScaffoldValidates(t *testing.T) {
	pluginFixture(t)
	dir := filepath.Join(t.TempDir(), "out")
	if code, out := runPlugin(t, "new", "team-forks", dir); code != 0 {
		t.Fatalf("new: exit %d\n%s", code, out)
	}
	if code, out := runPlugin(t, "validate", dir); code != 0 {
		t.Fatalf("the scaffold must validate: exit %d\n%s", code, out)
	}
	var code int
	errOut := withStderr(t, func() { code, _ = runPlugin(t, "new", "team-forks", dir) })
	if code != 2 || !strings.Contains(errOut, "not empty") {
		t.Errorf("an existing directory: exit %d\n%s", code, errOut)
	}
	target := filepath.Join(t.TempDir(), "sync")
	errOut = withStderr(t, func() { code, _ = runPlugin(t, "new", "sync", target) })
	if _, err := os.Stat(target); code != 2 || !os.IsNotExist(err) {
		t.Errorf("a command name must be refused and nothing written: exit %d, stat %v\n%s", code, err, errOut)
	}
}

func TestPluginValidateListsEveryProblem(t *testing.T) {
	pluginFixture(t)
	dir := writePlugin(t, "team-forks", map[string]string{"foo/x": "1", "skills/empty/notes.md": "x"})
	code, out := runPlugin(t, "validate", dir)
	if code != 1 || !strings.Contains(out, "unknown entry `foo`") || !strings.Contains(out, "no SKILL.md") {
		t.Errorf("exit %d\n%s", code, out)
	}
	if code, _ = runPlugin(t, "validate"); code != 2 {
		t.Errorf("usage: exit %d", code)
	}
}

func TestPluginCommandIsInTheUsage(t *testing.T) {
	out := withStdout(t, func() { run([]string{"help"}) })
	if !strings.Contains(out, "plugin     Install, approve and manage plugins") {
		t.Errorf("the plugin command is not listed in help:\n%s", out)
	}
}

// A snapshot build of a core older than the schema 2 floor still scaffolds: the
// check of its own output does not apply the running-version range.
func TestPluginNewOnAnOlderCore(t *testing.T) {
	pluginFixture(t)
	old := version
	version = "0.28.3"
	t.Cleanup(func() { version = old })
	dir := filepath.Join(t.TempDir(), "out")
	if code, out := runPlugin(t, "new", "team-forks", dir); code != 0 {
		t.Fatalf("new on 0.28.3: exit %d\n%s", code, out)
	}
	data, err := os.ReadFile(filepath.Join(dir, plugin.ManifestFile))
	if err != nil || !strings.Contains(string(data), `cartographer: ">=0.29.0"`) {
		t.Errorf("manifest %q, err %v", data, err)
	}
}
