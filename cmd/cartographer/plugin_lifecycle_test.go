package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func loadEntry(t *testing.T, home, name string) clientconfig.PluginEntry {
	t.Helper()
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := cfg.Plugin(name)
	if !ok {
		t.Fatalf("no entry for %s", name)
	}
	return e
}

func stderrOf(t *testing.T, f func()) string { return withStderr(t, f) }

func TestPluginApproveNeedsYesWithoutTerminalThenActivates(t *testing.T) {
	home := pluginFixture(t)
	dir := writePlugin(t, "team-forks", nil)
	if code, out := runPlugin(t, "install", dir); code != 0 {
		t.Fatalf("install: %d\n%s", code, out)
	}
	var code int
	errOut := stderrOf(t, func() { code, _ = runPlugin(t, "approve", "team-forks") })
	if code != 2 || !strings.Contains(errOut, "non-interactive approval requires --yes") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if e := loadEntry(t, home, "team-forks"); e.Approved != "" {
		t.Fatalf("approved without consent: %+v", e)
	}
	code, out := runPlugin(t, "approve", "team-forks", "--yes")
	if code != 0 || !strings.Contains(out, "Enabled team-forks") || !strings.Contains(out, `skill "helper"`) {
		t.Fatalf("exit %d\n%s", code, out)
	}
	cfg, _ := clientconfig.Load(home)
	if set := loadActivePlugins(cfg); len(set.Artifacts) != 1 {
		t.Errorf("approved plugin delivers %d artifacts, want 1", len(set.Artifacts))
	}
	if code, out = runPlugin(t, "approve", "team-forks", "--yes"); code != 0 || !strings.Contains(out, "nothing is waiting") {
		t.Errorf("second approve: exit %d\n%s", code, out)
	}
	if code, _ = runPlugin(t, "approve", "nope", "--yes"); code != 2 {
		t.Errorf("unknown plugin: exit %d", code)
	}
}

func TestPluginApproveRefusesAModifiedStore(t *testing.T) {
	home := pluginFixture(t)
	dir := writePlugin(t, "team-forks", nil)
	runPlugin(t, "install", dir)
	e := loadEntry(t, home, "team-forks")
	store, _ := pluginStoreFn()
	if err := os.WriteFile(filepath.Join(store.Root, "team-forks", e.Installed, "skills", "helper", "SKILL.md"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	var code int
	errOut := stderrOf(t, func() { code, _ = runPlugin(t, "approve", "team-forks", "--yes") })
	if code != 1 || !strings.Contains(errOut, "cartographer plugin update team-forks") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if e = loadEntry(t, home, "team-forks"); e.Approved != "" {
		t.Errorf("a modified tree was approved: %+v", e)
	}
}

func TestPluginUpdateShowsTheDiffAndKeepsTheApprovedVersionActive(t *testing.T) {
	home := pluginFixture(t)
	dir := writePlugin(t, "team-forks", nil)
	runPlugin(t, "install", dir, "--yes")
	old := loadEntry(t, home, "team-forks")

	// Unchanged tree: nothing happens.
	if code, out := runPlugin(t, "update"); code != 0 || !strings.Contains(out, "up to date") {
		t.Fatalf("no-op update: exit %d\n%s", code, out)
	}
	if e := loadEntry(t, home, "team-forks"); !reflect.DeepEqual(e, old) {
		t.Fatalf("a no-op update changed the entry: %+v", e)
	}

	manifest := "schema: 1\nname: team-forks\nversion: 1.1.0\ndisplay_name: Team forks\ndescription: Skills for the team.\ncartographer: \">=0.28.0\"\npermissions:\n  net: [a.example.com, b.example.com]\n"
	if err := os.WriteFile(filepath.Join(dir, "cartographer-plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runPlugin(t, "update", "team-forks")
	if code != 0 {
		t.Fatalf("update: exit %d\n%s", code, out)
	}
	for _, want := range []string{"PERMISSION ADDED: net: b.example.com", "file changed: cartographer-plugin.yaml", "stays active until 1.1.0 is approved", "not approved"} {
		if !strings.Contains(out, want) {
			t.Errorf("update output lacks %q:\n%s", want, out)
		}
	}
	e := loadEntry(t, home, "team-forks")
	if e.Approved != old.Approved || e.Installed == old.Installed || e.Version != "1.1.0" {
		t.Fatalf("update must move Installed only: before %+v after %+v", old, e)
	}
	cfg, _ := clientconfig.Load(home)
	if set := loadActivePlugins(cfg); len(set.Artifacts) != 1 || len(set.Unreadable) != 0 {
		t.Errorf("the approved version must stay active: %+v", set)
	}
	if _, out = runPlugin(t, "list"); !strings.Contains(out, "update available") {
		t.Errorf("list lacks the marker:\n%s", out)
	}

	if code, out = runPlugin(t, "approve", "team-forks", "--yes"); code != 0 {
		t.Fatalf("approve: exit %d\n%s", code, out)
	}
	store, _ := pluginStoreFn()
	if _, err := store.Open("team-forks", old.Approved, pluginOpts()); err == nil {
		t.Errorf("the previous approved tree should be garbage-collected")
	}
	if e = loadEntry(t, home, "team-forks"); e.Approved != e.Installed {
		t.Errorf("approve did not copy installed: %+v", e)
	}
}

func TestPluginUpdateWithYesApprovesAndAFailureKeepsTheEntry(t *testing.T) {
	home := pluginFixture(t)
	dir := writePlugin(t, "team-forks", nil)
	runPlugin(t, "install", dir, "--yes")
	old := loadEntry(t, home, "team-forks")
	if err := os.WriteFile(filepath.Join(dir, "skills", "helper", "SKILL.md"), []byte("---\nname: helper\ndescription: Helps with another thing, use it when asked.\n---\n\n# Helper 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runPlugin(t, "update", "--yes"); code != 0 || !strings.Contains(out, "Enabled team-forks") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	cur := loadEntry(t, home, "team-forks")
	if cur.Approved == old.Approved || cur.Approved != cur.Installed {
		t.Fatalf("--yes should approve the new digest: %+v", cur)
	}
	// A source that no longer validates leaves the entry as it was.
	if err := os.WriteFile(filepath.Join(dir, "cartographer-plugin.yaml"), []byte("schema: 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var code int
	errOut := stderrOf(t, func() { code, _ = runPlugin(t, "update", "--yes") })
	if code != 1 || !strings.Contains(errOut, "the entry is unchanged") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if again := loadEntry(t, home, "team-forks"); !reflect.DeepEqual(again, cur) {
		t.Errorf("a failing update changed the entry: %+v vs %+v", again, cur)
	}
}

func dependsPlugin(t *testing.T, name, dep string) string {
	t.Helper()
	return writePlugin(t, name, map[string]string{
		"cartographer-plugin.yaml": "schema: 1\nname: " + name + "\nversion: 1.0.0\ndisplay_name: " + name + "\ndescription: Skills for the team.\ncartographer: \">=0.28.0\"\ndepends:\n  " + dep + ": \">=1.0.0\"\n",
	})
}

func TestPluginEnableDisableRemoveAndDependents(t *testing.T) {
	home := pluginFixture(t)
	runPlugin(t, "install", writePlugin(t, "base-plugin", nil), "--yes")
	runPlugin(t, "install", dependsPlugin(t, "uses-base", "base-plugin"), "--yes")

	fails := func(want string, args ...string) {
		t.Helper()
		var code int
		errOut := stderrOf(t, func() { code, _ = runPlugin(t, args...) })
		if code != 1 || !strings.Contains(errOut, want) {
			t.Fatalf("%v: exit %d, want 1 with %q\n%s", args, code, want, errOut)
		}
	}
	fails("needed by enabled uses-base", "disable", "base-plugin")
	fails("needed by enabled uses-base", "remove", "base-plugin")
	if code, out := runPlugin(t, "disable", "uses-base"); code != 0 || !strings.Contains(out, "Disabled uses-base") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if code, out := runPlugin(t, "disable", "uses-base"); code != 0 || !strings.Contains(out, "already disabled") {
		t.Fatalf("second disable: exit %d\n%s", code, out)
	}
	if code, _ := runPlugin(t, "disable", "base-plugin"); code != 0 {
		t.Fatal("disable of a plugin nobody enabled depends on")
	}
	fails("uses-base needs base-plugin >=1.0.0: base-plugin is not enabled", "enable", "uses-base")
	if code, _ := runPlugin(t, "enable", "base-plugin"); code != 0 {
		t.Fatal("enable base")
	}
	if code, out := runPlugin(t, "enable", "uses-base"); code != 0 || !strings.Contains(out, "Enabled uses-base") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if e := loadEntry(t, home, "uses-base"); !e.Enabled {
		t.Errorf("not enabled: %+v", e)
	}
	if code, _ := runPlugin(t, "enable", "nope"); code != 2 {
		t.Errorf("unknown: exit %d", code)
	}

	// A pending plugin cannot be enabled.
	runPlugin(t, "install", writePlugin(t, "pending-one", nil))
	fails("pending approval", "enable", "pending-one")

	// remove: the dependent first, then the base.
	if code, _ := runPlugin(t, "remove", "uses-base"); code != 0 {
		t.Fatal("remove dependent")
	}
	if code, out := runPlugin(t, "remove", "base-plugin"); code != 0 || !strings.Contains(out, "Removed base-plugin") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	cfg, _ := clientconfig.Load(home)
	if _, ok := cfg.Plugin("base-plugin"); ok {
		t.Error("entry still configured")
	}
	store, _ := pluginStoreFn()
	if _, err := os.Stat(filepath.Join(store.Root, "base-plugin")); err == nil {
		t.Error("stored trees of a removed plugin remain")
	}
	if code, _ := runPlugin(t, "remove", "base-plugin"); code != 2 {
		t.Errorf("remove of an unknown name: exit %d", code)
	}
}

// lockWith writes a lockfile with a claude section holding the entries.
func lockWith(t *testing.T, home string, managed ...provisioning.ManagedFile) {
	t.Helper()
	lf := provisioning.LockFile{Providers: map[string]provisioning.Lock{"claude": {Provider: "claude", Managed: managed}}}
	if err := provisioning.WriteLockFile(lockFilePath(home), lf); err != nil {
		t.Fatal(err)
	}
}

func skillEntry(t *testing.T, home, name, source string) provisioning.ManagedFile {
	t.Helper()
	rel := filepath.Join(".claude", "skills", name, "SKILL.md")
	full := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return provisioning.ManagedFile{Kind: "skill", Name: name, Path: rel, Source: source}
}

func TestPluginRemovePrunesItsArtifactsAndLeavesOthersAlone(t *testing.T) {
	home := pluginFixture(t)
	runPlugin(t, "install", writePlugin(t, "team-forks", nil), "--yes")
	runPlugin(t, "install", writePlugin(t, "other-plugin", nil), "--yes")
	mine := skillEntry(t, home, "helper", "plugin:team-forks")
	theirs := skillEntry(t, home, "kept", "plugin:other-plugin")
	kb := skillEntry(t, home, "kbskill", "kb:kb-a")
	lockWith(t, home, mine, theirs, kb)

	code, out := runPlugin(t, "remove", "team-forks")
	if code != 0 || !strings.Contains(out, "pruned claude: .claude/skills/helper/SKILL.md") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, mine.Path)); err == nil {
		t.Error("the plugin's skill is still on disk")
	}
	for _, mf := range []provisioning.ManagedFile{theirs, kb} {
		if _, err := os.Stat(filepath.Join(home, mf.Path)); err != nil {
			t.Errorf("%s was removed: %v", mf.Name, err)
		}
	}
	lf, err := provisioning.ReadLockFile(lockFilePath(home))
	if err != nil || len(lf.Providers["claude"].Managed) != 2 {
		t.Fatalf("lock entries after remove: %v %+v", err, lf.Providers["claude"].Managed)
	}
	for _, mf := range lf.Providers["claude"].Managed {
		if provisioning.PluginName(mf.Source) == "team-forks" {
			t.Errorf("lock still lists %+v", mf)
		}
	}
}

func TestPluginRemoveStopsOnARefusedPrune(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	home := pluginFixture(t)
	runPlugin(t, "install", writePlugin(t, "team-forks", nil), "--yes")
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Join(elsewhere, "helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "helper", "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(home, ".claude", "skills")); err != nil {
		t.Skip("cannot create a symlink:", err)
	}
	lockWith(t, home, provisioning.ManagedFile{Kind: "skill", Name: "helper", Path: filepath.Join(".claude", "skills", "helper", "SKILL.md"), Source: "plugin:team-forks"})

	var code int
	errOut := stderrOf(t, func() { code, _ = runPlugin(t, "remove", "team-forks") })
	if code != 1 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "helper", "SKILL.md")); err != nil {
		t.Error("a file behind a symlink was deleted")
	}
	if _, ok := mustCfg(t, home).Plugin("team-forks"); !ok {
		t.Error("the entry was deleted though the prune was refused")
	}
	lf, _ := provisioning.ReadLockFile(lockFilePath(home))
	if len(lf.Providers["claude"].Managed) != 1 {
		t.Errorf("the refused entry must stay in the lockfile: %+v", lf.Providers["claude"].Managed)
	}
}

func mustCfg(t *testing.T, home string) *clientconfig.Config {
	t.Helper()
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestPluginRemoveRefusesAnUnreadableApprovedTree(t *testing.T) {
	home := pluginFixture(t)
	runPlugin(t, "install", writePlugin(t, "team-forks", nil), "--yes")
	e := loadEntry(t, home, "team-forks")
	store, _ := pluginStoreFn()
	if err := os.RemoveAll(filepath.Join(store.Root, "team-forks", e.Approved)); err != nil {
		t.Fatal(err)
	}
	var code int
	errOut := stderrOf(t, func() { code, _ = runPlugin(t, "remove", "team-forks") })
	if code != 1 || !strings.Contains(errOut, "cannot be retired") {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if _, ok := mustCfg(t, home).Plugin("team-forks"); !ok {
		t.Error("an unreadable plugin must hold its entry")
	}
}

func TestPluginLifecycleJSON(t *testing.T) {
	pluginFixture(t)
	runPlugin(t, "install", writePlugin(t, "team-forks", nil))
	code, out := runPlugin(t, "approve", "team-forks", "--yes", "--output", "json")
	if code != 0 || !strings.Contains(out, pluginSchema) || !strings.Contains(out, `"action": "approve"`) || strings.Contains(out, "plugin team-forks") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}
