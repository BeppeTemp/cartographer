package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/execbit"
	"github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func pluginFixture(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	setHome(t, home)
	store := core.Store{Root: filepath.Join(t.TempDir(), "plugins")}
	oldStore, oldTTY := pluginStoreFn, pluginInteractive
	pluginStoreFn = func() (core.Store, error) { return store, nil }
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
	data, err := os.ReadFile(filepath.Join(dir, core.ManifestFile))
	if err != nil || !strings.Contains(string(data), `cartographer: ">=0.29.0"`) {
		t.Errorf("manifest %q, err %v", data, err)
	}
}

// writeSurveyorOnly writes a schema 2 plugin that declares no client side.
func writeSurveyorOnly(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := "schema: 2\nname: " + name + "\nversion: 1.0.0\ndisplay_name: Feeds\ndescription: A feed reader.\ncartographer: \">=0.29.0\"\ncomponents:\n  surveyor:\n    sdk: {recipes: 1}\n"
	if err := os.WriteFile(filepath.Join(dir, "cartographer-plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPluginInstallRefusesAPluginWithoutAClientSide(t *testing.T) {
	pluginFixture(t)
	dir := writeSurveyorOnly(t, "feeds")
	code, out := runPlugin(t, "install", dir, "--yes")
	if code != 1 {
		t.Errorf("install: exit %d\n%s", code, out)
	}
	if code, out = runPlugin(t, "list"); strings.Contains(out, "feeds") {
		t.Errorf("the refused plugin was recorded (exit %d):\n%s", code, out)
	}
}

func TestPluginValidateReportsSides(t *testing.T) {
	pluginFixture(t)
	dir := writeSurveyorOnly(t, "feeds")
	code, out := runPlugin(t, "validate", dir)
	for _, want := range []string{"is valid", "sides: surveyor", "surveyor side not loadable by this build: unknown family recipes"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("validate lacks %q: exit %d\n%s", want, code, out)
		}
	}
	_, out = runPlugin(t, "validate", dir, "--output", "json")
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc["sides"] == nil || doc["unsupported"] == nil || doc["unknown"] == nil {
		t.Errorf("json: %v\n%s", err, out)
	}
	// A broken schema 2 manifest has no sides: [] and not null.
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "cartographer-plugin.yaml"), []byte("schema: 2\nname: Bad Name\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out = runPlugin(t, "validate", bad, "--output", "json")
	if !strings.Contains(out, `"sides": []`) && !strings.Contains(out, `"sides":[]`) {
		t.Errorf("broken manifest: sides is not []:\n%s", out)
	}
	// A schema 1 document keeps the 0.28 keys.
	_, out = runPlugin(t, "validate", writePlugin(t, "team-forks", nil), "--output", "json")
	if strings.Contains(out, `"sides"`) {
		t.Errorf("schema 1 json grew keys:\n%s", out)
	}
}

// plugin vendor fills a pinned store, prints the digest and the pin lines, and
// writes neither the client config nor an approval; LoadPinned then accepts it.
func TestPluginVendorFillsAPinnedStore(t *testing.T) {
	home := pluginFixture(t)
	// No sdk family is registered for the surveyor yet, so its side is declared bare.
	src := t.TempDir()
	manifest := "schema: 2\nname: feeds\nversion: 1.0.0\ndisplay_name: Feeds\ndescription: A feed reader.\ncartographer: \">=0.29.0\"\ncomponents:\n  surveyor: {}\n"
	if err := os.WriteFile(filepath.Join(src, "cartographer-plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(t.TempDir(), "vendored")

	code, out := runPlugin(t, "vendor", src, "--to", to)
	if code != 0 {
		t.Fatalf("vendor: exit %d\n%s", code, out)
	}
	_, probs := core.ReadTree(mustTree(t, src), core.Options{RunningVersion: version})
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	m := regexp.MustCompile(`digest ([0-9a-f]{64})`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no digest printed:\n%s", out)
	}
	for _, want := range []string{"plugins_dir:", "plugins:\n  feeds:\n    digest: " + m[1], "pin the surveyor side in the surveyor config"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(clientconfig.Path(home)); err == nil {
		t.Error("vendor wrote the client config")
	}
	pins := map[string]core.Pin{"feeds": {Enabled: true, Digest: m[1]}}
	got, pp := core.LoadPinned(to, pins, core.SideSurveyor, pluginOpts())
	if len(pp) != 0 || len(got) != 1 {
		t.Fatalf("LoadPinned: %v %v", got, pp)
	}
	if _, pp = core.LoadPinned(to, pins, core.SideServer, pluginOpts()); len(pp) != 1 || !strings.Contains(pp[0].Reason, "no server side") {
		t.Errorf("a plugin without the side: %v", pp)
	}

	if code, _ = runPlugin(t, "vendor", src, "--to", to, "--side", "server"); code != 1 {
		t.Errorf("--side the plugin lacks: exit %d", code)
	}
	if code, _ = runPlugin(t, "vendor", src); code != 2 {
		t.Errorf("vendor without --to: exit %d", code)
	}
	bad := writePlugin(t, "team-forks", map[string]string{"stray/x": "no"})
	if code, _ = runPlugin(t, "vendor", bad, "--to", filepath.Join(t.TempDir(), "v")); code != 1 {
		t.Errorf("an invalid tree: exit %d", code)
	}
}

func mustTree(t *testing.T, dir string) []core.TreeFile {
	t.Helper()
	files, _, err := plugin.Fetch(context.Background(), plugin.Source{Path: dir}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// The pin lines skip a side this build cannot load; the atlas side is pinned with the server side.
func TestPluginVendorSkipsSidesThatCannotBePinned(t *testing.T) {
	pluginFixture(t)
	src := t.TempDir()
	manifest := "schema: 2\nname: feeds\nversion: 1.0.0\ndisplay_name: Feeds\ndescription: A feed reader.\ncartographer: \">=0.29.0\"\ncomponents:\n  surveyor:\n    sdk: {recipes: 1}\n  atlas: {}\n  server: {}\n"
	if err := os.WriteFile(filepath.Join(src, "cartographer-plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runPlugin(t, "vendor", src, "--to", filepath.Join(t.TempDir(), "v"))
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if strings.Contains(out, "# pin the surveyor") || strings.Contains(out, "# pin the atlas") {
		t.Errorf("a side that cannot be pinned got pin lines:\n%s", out)
	}
	for _, want := range []string{"surveyor side: not pinnable", "unknown family recipes", "atlas side: loaded by the server", "# pin the server side in the server config"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// --to may not be the client's own cache, which is written under the client lock.
func TestPluginVendorRefusesTheClientCache(t *testing.T) {
	pluginFixture(t)
	cache, _ := pluginStoreFn()
	src := writePlugin(t, "team-forks", nil)
	for _, to := range []string{cache.Root, filepath.Join(cache.Root, "sub")} {
		if code, out := runPlugin(t, "vendor", src, "--to", to); code != 2 {
			t.Errorf("--to %s: exit %d\n%s", to, code, out)
		}
	}
	if entries, _ := os.ReadDir(cache.Root); len(entries) != 0 {
		t.Errorf("the cache was written: %v", entries)
	}
	if code, out := runPlugin(t, "vendor", src, "--to", filepath.Join(t.TempDir(), "elsewhere")); code != 0 {
		t.Errorf("another directory: exit %d\n%s", code, out)
	}
}
