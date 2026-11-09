package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func stateOf(t *testing.T, cfg *clientconfig.Config, name string) pluginStatus {
	t.Helper()
	for _, p := range snapshotPlugins(cfg) {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no plugin %s in the snapshot", name)
	return pluginStatus{}
}

func installPlugin(t *testing.T, folder string, approve bool) {
	t.Helper()
	args := []string{"install", folder}
	if approve {
		args = append(args, "--yes")
	}
	if code, out := runPlugin(t, args...); code != 0 {
		t.Fatalf("install %s: exit %d\n%s", folder, code, out)
	}
}

func TestSnapshotPluginsReportsEveryState(t *testing.T) {
	home := pluginFixture(t)
	installPlugin(t, writePlugin(t, "plug-active", nil), true)
	installPlugin(t, writePlugin(t, "plug-pending", nil), false)
	installPlugin(t, writePlugin(t, "plug-off", nil), true)
	installPlugin(t, writePlugin(t, "plug-gone", nil), true)
	installPlugin(t, writePlugin(t, "plug-modified", nil), true)
	if code, out := runPlugin(t, "disable", "plug-off"); code != 0 {
		t.Fatalf("disable: %d\n%s", code, out)
	}
	store, _ := pluginStoreFn()
	if err := os.RemoveAll(filepath.Join(store.Root, "plug-gone")); err != nil {
		t.Fatal(err)
	}
	e := loadEntry(t, home, "plug-modified")
	if err := os.WriteFile(filepath.Join(store.Root, "plug-modified", e.Approved, "skills", "helper", "SKILL.md"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := mustCfg(t, home)
	// A newer tree installed beside the approved one.
	up := cfg.Plugins["plug-active"]
	up.Installed = strings.Repeat("a", 64)
	cfg.Plugins["plug-active"] = up

	want := map[string]string{
		"plug-active": "active", "plug-pending": "pending approval", "plug-off": "disabled",
		"plug-gone": "not fetched", "plug-modified": "modified",
	}
	for n, st := range want {
		if got := stateOf(t, cfg, n).State; got != st {
			t.Errorf("%s: state %q, want %q", n, got, st)
		}
	}
	if !stateOf(t, cfg, "plug-active").UpdateAvailable || stateOf(t, cfg, "plug-pending").UpdateAvailable || stateOf(t, cfg, "plug-off").UpdateAvailable {
		t.Error("update_available must be true only where installed differs from a non-empty approved")
	}

	// JSON golden: stable keys, sorted by name, no digest-less noise.
	b, err := json.Marshal(snapshotPlugins(cfg)[:1])
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if r["name"] != "plug-active" || r["state"] != "active" || r["version"] != "1.0.0" || r["update_available"] != true || r["installed"] == nil || r["approved"] == nil {
		t.Errorf("json row: %s", b)
	}
	if _, ok := r["sha"]; ok {
		t.Errorf("a folder has no sha: %s", b)
	}
}

func TestSnapshotPluginsInvalidAndStoreUnavailable(t *testing.T) {
	home := pluginFixture(t)
	installPlugin(t, writePlugin(t, "plug-new", nil), true)
	cfg := mustCfg(t, home)
	old := version
	version = "0.1.0" // older than the manifest's ">=0.28.0": the stored tree does not validate
	if got := stateOf(t, cfg, "plug-new").State; got != "invalid" {
		t.Errorf("state %q, want invalid", got)
	}
	version = old
	pluginStoreFn = func() (plugin.Store, error) { return plugin.Store{}, os.ErrPermission }
	if got := stateOf(t, cfg, "plug-new").State; got != "store unavailable" {
		t.Errorf("state %q, want store unavailable", got)
	}
}

func TestNothingAboutPluginsWithNoneConfigured(t *testing.T) {
	home := pluginFixture(t)
	cfg := clientconfig.Default()
	if snapshotPlugins(cfg) != nil || snapshotPlugins(nil) != nil {
		t.Fatal("a snapshot of nobody's plugins must be nil")
	}
	s := emptySnapshot()
	if b, _ := json.Marshal(s); strings.Contains(string(b), "plugin") {
		t.Errorf("status JSON mentions plugins with none configured: %s", b)
	}
	var buf bytes.Buffer
	printPluginSection(&buf, s)
	if buf.Len() != 0 {
		t.Errorf("printed %q", buf.String())
	}
	if got := checkPlugins(cfg, "", nil); got != nil {
		t.Errorf("doctor findings with no plugin: %+v", got)
	}
	_ = home
}

func TestPluginSectionPrintsStatesPendingHintProvidersAndEscapes(t *testing.T) {
	s := statusSnapshot{
		Plugins: []pluginStatus{
			{Name: "team-forks", Version: "1.2.0", Source: "https://gitlab.example.com/x.git", Ref: "main", SHA: "0123456789abcdef", Installed: "ffff", Approved: "eeee", State: "active", UpdateAvailable: true},
			{Name: "plug-pending", Version: "0.1.0", Source: "/tmp/\x1b[31mevil", Installed: "abcdef0123", State: "pending approval"},
		},
		Providers: []providerStatus{
			{Name: "claude"},
			{Name: "fork-cli", Plugin: "team-forks", Base: "crush", Connected: true},
			{Name: "fork-ext", Plugin: "team-forks", Base: "cline", Installed: true},
			{Name: "fork-none", Plugin: "team-forks", Base: "codex"},
		},
	}
	var buf bytes.Buffer
	printPluginSection(&buf, s)
	out := buf.String()
	for _, want := range []string{
		"plugins:",
		"team-forks 1.2.0  active  0123456  https://gitlab.example.com/x.git@main  update available",
		"pending approval: cartographer plugin approve plug-pending",
		"provider fork-cli (plugin team-forks, base crush): connected",
		"provider fork-ext (plugin team-forks, base cline): detected",
		"provider fork-none (plugin team-forks, base codex): not detected",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") || strings.Contains(out, "provider claude") {
		t.Errorf("unescaped control character or a built-in provider listed:\n%q", out)
	}
}

func TestAgentsAndStatusListPluginsAndProviders(t *testing.T) {
	home := pluginFixture(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	freshProcess(t, home)
	installPlugin(t, providerPlugin(t, "team-forks"), true)
	installPlugin(t, writePlugin(t, "plug-pending", nil), false)
	registerPluginProviders(&bytes.Buffer{})
	cfg := mustCfg(t, home)
	cfg.ServerURL, cfg.ServerName = "http://127.0.0.1:1/mcp", "cartographer"
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}

	var code int
	out := withStdout(t, func() { code = cmdAgents(nil) })
	if code != 0 || !strings.Contains(out, "fork-cli (plugin team-forks, base crush)") || !strings.Contains(out, "plug-pending") || !strings.Contains(out, "pending approval: cartographer plugin approve plug-pending") {
		t.Fatalf("agents: exit %d\n%s", code, out)
	}
	out = withStdout(t, func() { code = cmdAgents([]string{"--output", "json"}) })
	var snap statusSnapshot
	if err := json.Unmarshal([]byte(out), &snap); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(snap.Plugins) != 2 || snap.Schema != statusSchema {
		t.Errorf("json plugins: %+v", snap.Plugins)
	}
	found := false
	for _, p := range snap.Providers {
		if p.Name == "fork-cli" && p.Plugin == "team-forks" && p.Base == "crush" {
			found = true
		}
	}
	if !found {
		t.Errorf("the plugin provider carries no plugin and base: %+v", snap.Providers)
	}

	// With no agent connected, status still says where the plugins stand.
	out = withStdout(t, func() { code = cmdStatus(nil) })
	if !strings.Contains(out, "no agent connected") || !strings.Contains(out, "team-forks") || !strings.Contains(out, "pending approval: cartographer plugin approve plug-pending") {
		t.Errorf("status: exit %d\n%s", code, out)
	}
}

func TestDoctorPluginFindingsEachWithItsFix(t *testing.T) {
	home := pluginFixture(t)
	installPlugin(t, writePlugin(t, "plug-gone", nil), true)
	installPlugin(t, writePlugin(t, "plug-modified", nil), true)
	installPlugin(t, writePlugin(t, "plug-pending", nil), false)
	installPlugin(t, writePlugin(t, "plug-off", nil), true)
	if code, _ := runPlugin(t, "disable", "plug-off"); code != 0 {
		t.Fatal("disable")
	}
	store, _ := pluginStoreFn()
	if err := os.RemoveAll(filepath.Join(store.Root, "plug-gone")); err != nil {
		t.Fatal(err)
	}
	e := loadEntry(t, home, "plug-modified")
	if err := os.WriteFile(filepath.Join(store.Root, "plug-modified", e.Approved, "skills", "helper", "SKILL.md"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := mustCfg(t, home)
	// A newer installed tree on a plugin whose approved one is intact.
	installPlugin(t, writePlugin(t, "plug-newer", nil), true)
	cfg = mustCfg(t, home)
	n := cfg.Plugins["plug-newer"]
	n.Installed = strings.Repeat("b", 64)
	cfg.Plugins["plug-newer"] = n

	byMsg := map[string]doctorFinding{}
	for _, f := range checkPlugins(cfg, "", nil) {
		byMsg[f.Check+" "+f.Message] = f
	}
	expect := func(check, contains, fix string, sev string) {
		t.Helper()
		for _, f := range byMsg {
			if f.Check == check && strings.Contains(f.Message, contains) {
				if f.Fix != fix || f.Severity != sev {
					t.Errorf("%s %q: fix %q severity %s, want %q %s", check, contains, f.Fix, f.Severity, fix, sev)
				}
				return
			}
		}
		t.Errorf("no %s finding about %q in %+v", check, contains, byMsg)
	}
	expect("plugin-store", "plugin plug-gone is approved but its tree is not in the store", "cartographer plugin update plug-gone", doctorWarning)
	expect("plugin-store", "plugin plug-modified", "cartographer plugin update plug-modified", doctorWarning)
	expect("plugin-pending", "plugin plug-pending has never been approved", "cartographer plugin show plug-pending, then cartographer plugin approve plug-pending", doctorWarning)
	expect("plugin-pending", "plugin plug-newer has a newer version", "cartographer plugin show plug-newer, then cartographer plugin approve plug-newer", doctorInfo)
	for _, f := range byMsg {
		if strings.Contains(f.Message, "plug-off") {
			t.Errorf("a disabled plugin is a state, not a finding: %+v", f)
		}
	}

	// A range this build does not satisfy is plugin-invalid, with the reason.
	cfg = mustCfg(t, home)
	old := version
	version = "0.1.0"
	got := checkPlugins(cfg, "", nil)
	version = old
	invalid := 0
	for _, f := range got {
		if f.Check == "plugin-invalid" {
			invalid++
			if f.Message == "" || f.Fix == "" {
				t.Errorf("a plugin-invalid finding needs its reason and its fix: %+v", f)
			}
		}
	}
	if invalid == 0 {
		t.Errorf("no plugin-invalid finding for a range the build does not satisfy: %+v", got)
	}
}

func TestDoctorPluginCollisionAndProviderRefusals(t *testing.T) {
	home := pluginFixture(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	freshProcess(t, home)
	installPlugin(t, providerPlugin(t, "plug-a"), true)
	installPlugin(t, providerPlugin(t, "plug-b"), true)
	registerPluginProviders(&bytes.Buffer{})
	cfg := mustCfg(t, home)

	var collision, provider int
	for _, f := range checkPlugins(cfg, "", nil) {
		switch f.Check {
		case "plugin-collision":
			collision++
			if !strings.Contains(f.Message, "skill helper is declared by plugin plug-a and plugin plug-b: installed from neither") || f.Fix == "" {
				t.Errorf("collision finding: %+v", f)
			}
		case "plugin-provider":
			provider++
			if !strings.Contains(f.Message, "fork-cli") || !strings.Contains(f.Message, "not registered") || f.Fix == "" {
				t.Errorf("provider finding: %+v", f)
			}
		}
	}
	if collision != 1 || provider == 0 {
		t.Errorf("collision findings %d, provider findings %d", collision, provider)
	}

	// Against a KB file the lockfile records.
	lock := provisioning.LockFile{Providers: map[string]provisioning.Lock{"claude": {Managed: []provisioning.ManagedFile{{Kind: "skill", Name: "helper", Path: "skills/helper/SKILL.md", Source: "kb:work-kb"}}}}}
	cfg2 := mustCfg(t, home)
	delete(cfg2.Plugins, "plug-b")
	found := false
	for _, f := range checkPlugins(cfg2, "", &lock) {
		if f.Check == "plugin-collision" && strings.Contains(f.Message, "KB work-kb and plugin plug-a") {
			found = true
		}
	}
	if !found {
		t.Error("a plugin skill named like an installed KB skill is not reported")
	}

	// --provider narrows to the refusals about it.
	only := checkPlugins(cfg, "fork-cli", nil)
	if len(only) == 0 || only[0].Check != "plugin-provider" {
		t.Errorf("--provider fork-cli: %+v", only)
	}
	if len(checkPlugins(cfg, "claude", nil)) != 0 {
		t.Error("--provider claude must not report plugin findings")
	}
}

func TestDoctorPluginFindingsAreEscaped(t *testing.T) {
	f := providerIssueFinding(pluginProviderIssue{Plugin: "p\x1b[2J", Provider: "x\x07", Reason: "bad\x1b[31m"})
	for _, s := range []string{f.Message, f.Fix} {
		if strings.ContainsAny(s, "\x1b\x07") {
			t.Errorf("control character reaches the terminal: %q", s)
		}
	}
}

func TestDoctorStoreUnavailableIsOneFinding(t *testing.T) {
	home := pluginFixture(t)
	installPlugin(t, writePlugin(t, "plug-x", nil), true)
	cfg := mustCfg(t, home)
	pluginStoreFn = func() (plugin.Store, error) { return plugin.Store{}, os.ErrPermission }
	var stores int
	for _, f := range checkPlugins(cfg, "", nil) {
		if f.Check == "plugin-store" {
			stores++
			if f.Severity != doctorError {
				t.Errorf("severity %s", f.Severity)
			}
		}
	}
	if stores != 1 {
		t.Errorf("store findings %d, want exactly 1", stores)
	}
}
