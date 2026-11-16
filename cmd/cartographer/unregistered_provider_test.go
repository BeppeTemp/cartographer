package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// outOfRangeMachine is a client that synced a plugin provider (fork-cli, next to
// claude) under a version the plugin accepts, and then runs under one it does
// not: the plugin is invalid, registers no provider, and its name stays in
// agents (#2089). It returns the home, the config as saved, and the file the
// provider had delivered.
func outOfRangeMachine(t *testing.T, srvURL string) (home string, cfg *clientconfig.Config, delivered string) {
	t.Helper()
	home, cfg = deliveryMachine(t, srvURL)
	t.Setenv("XDG_CONFIG_HOME", "")
	freshProcess(t, home)
	if code, out := runPlugin(t, "install", providerPlugin(t, "team-forks"), "--yes"); code != 0 {
		t.Fatalf("install: %d\n%s", code, out)
	}
	registerPluginProviders(&bytes.Buffer{})
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ServerURL, cfg.ServerName, cfg.Agents = srvURL+"/mcp", "cartographer", []string{"claude", "fork-cli"}
	bound := clientconfig.ClientBinding{KBs: []string{}, KBsSet: true}
	cfg.Clients = map[string]clientconfig.ClientBinding{"claude": bound, "fork-cli": bound}
	if _, err := runSync(home, cfg, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	lf, err := provisioning.ReadLockFile(lockFilePath(home))
	if err != nil || len(lf.ForProvider("fork-cli").Managed) == 0 {
		t.Fatalf("the provider was not materialized: %v %+v", err, lf.Providers)
	}
	delivered = filepath.Join(home, filepath.FromSlash(lf.ForProvider("fork-cli").Managed[0].Path))

	// The next process runs a version outside the plugin's range.
	old := version
	version = "0.1.0"
	t.Cleanup(func() { version = old })
	freshProcess(t, home)
	registerPluginProviders(&bytes.Buffer{})
	if registered("fork-cli") {
		t.Fatal("an out-of-range plugin must register no provider")
	}
	cfg, err = clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if !slicesHas(cfg.Agents, "fork-cli") {
		t.Fatalf("the load dropped the plugin's agent: %v", cfg.Agents)
	}
	return home, cfg, delivered
}

func slicesHas(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// A provider that is not registered is skipped with a warning naming the plugin,
// the range and the fix; the other clients sync, the run exits 0, and the config
// entry and the lock section of the skipped one stay as they were.
func TestSyncSkipsAProviderThatIsNotRegistered(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	home, cfg, delivered := outOfRangeMachine(t, srv.URL)

	var out, errw bytes.Buffer
	if _, err := runSync(home, cfg, syncOptions{Streams: syncStreams{Out: &out, Err: &errw}}); err != nil {
		t.Fatalf("sync must skip the unregistered provider: %v\nstderr: %s", err, errw.String())
	}
	for _, want := range []string{"fork-cli", "team-forks", "needs Cartographer >=0.28.0", "this is 0.1.0", "plugin update", "plugin remove"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("warning lacks %q:\n%s", want, errw.String())
		}
	}
	lf, _ := provisioning.ReadLockFile(lockFilePath(home))
	if len(lf.ForProvider("fork-cli").Managed) == 0 {
		t.Error("the lock section of the skipped provider was changed")
	}
	if _, err := os.Stat(delivered); err != nil {
		t.Errorf("a file the skipped provider delivered is gone: %v", err)
	}
	if _, ok := lf.Providers["claude"]; !ok {
		t.Errorf("the registered client was not synchronized: %+v", lf.Providers)
	}
	if got, err := clientconfig.Load(home); err != nil || !slicesHas(got.Agents, "fork-cli") {
		t.Errorf("the config entry of the skipped provider must stay: %v %v", got, err)
	}
}

// --client names the provider on purpose: that is an error with the reason.
func TestSyncClientNamingAnUnregisteredProviderFails(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	home, cfg, _ := outOfRangeMachine(t, srv.URL)

	_, err := runSync(home, cfg, syncOptions{Clients: []string{"fork-cli"}})
	if err == nil {
		t.Fatal("--client fork-cli must fail")
	}
	for _, want := range []string{"fork-cli", "not registered", "team-forks", "needs Cartographer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

// The safety net behind disconnect and every other caller: an unknown provider
// is a refusal of that provider, and the others are still processed.
func TestRemoveMCPEntriesRefusesAnUnregisteredProviderAlone(t *testing.T) {
	home := pluginFixture(t)
	freshProcess(t, home)
	_, removed, err := removeMCPEntries("cartographer", nil, []string{"ghost", "claude"}, home, nil, false, "", true, nil)
	if !provisioning.IsRefusal(err) {
		t.Fatalf("err = %v; want a refusal", err)
	}
	if !errors.Is(err, provisioning.ErrProviderNotRegistered) || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("the refusal must name ghost: %v", err)
	}
	if _, ok := removed["claude"]; !ok {
		t.Errorf("claude was not processed after ghost: %v", removed)
	}
}

// plugin remove works on a plugin the running version is outside the range of:
// its provider is retired through the disconnect path, then the plugin goes.
func TestPluginRemoveOutOfRangePlugin(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	home, _, delivered := outOfRangeMachine(t, srv.URL)

	code, out := runPlugin(t, "remove", "team-forks")
	if code != 0 {
		t.Fatalf("remove: %d\n%s", code, out)
	}
	lf, _ := provisioning.ReadLockFile(lockFilePath(home))
	if _, still := lf.Providers["fork-cli"]; still {
		t.Error("the lock section of the plugin's provider is still there")
	}
	if _, err := os.Stat(delivered); err == nil {
		t.Errorf("%s was not pruned", delivered)
	}
	got, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Plugin("team-forks"); ok {
		t.Error("the plugin entry is still configured")
	}
	if slicesHas(got.Agents, "fork-cli") {
		t.Errorf("agents still lists the retired provider: %v", got.Agents)
	}
	if _, ok := configurator.PluginOf("fork-cli"); ok {
		t.Error("the provider is still registered after the removal")
	}
}

// plugin show reads the tree past the range, so the user can see what to update.
func TestPluginShowOutOfRangePlugin(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	outOfRangeMachine(t, srv.URL)
	code, out := runPlugin(t, "show", "team-forks")
	if code != 0 || !strings.Contains(out, "state: invalid") {
		t.Fatalf("show: %d\n%s", code, out)
	}
	for _, want := range []string{"reason: cartographer: needs Cartographer >=0.28.0", "this is 0.1.0", "fix: run `cartographer plugin update team-forks`"} {
		if !strings.Contains(out, want) {
			t.Errorf("plugin show lacks %q:\n%s", want, out)
		}
	}
	code, out = runPlugin(t, "show", "team-forks", "--output", "json")
	if code != 0 || !strings.Contains(out, `"invalid_reason"`) || !strings.Contains(out, "plugin update team-forks") {
		t.Errorf("plugin show --output json lacks invalid_reason: %d\n%s", code, out)
	}
}

// The start-up warning of an out-of-range plugin says the range, the running
// version and the fix, not only that the plugin is invalid (#2090).
func TestStartupWarningOfAnOutOfRangePluginHoldsTheReason(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	outOfRangeMachine(t, srv.URL)
	var errw bytes.Buffer
	registerPluginProviders(&errw)
	for _, want := range []string{"plugin team-forks is invalid", "needs Cartographer >=0.28.0", "this is 0.1.0", "plugin update team-forks", "not registered this run"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("warning lacks %q:\n%s", want, errw.String())
		}
	}
}

// doctor names the plugin commands, not disconnect, for such a provider.
func TestDoctorHintForAnUnregisteredProvider(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	home, cfg, _ := outOfRangeMachine(t, srv.URL)
	var found bool
	for _, f := range checkClientConfig(home, cfg, nil, "") {
		if strings.Contains(f.Message, "fork-cli") {
			found = true
			if strings.Contains(f.Fix, "disconnect") || !strings.Contains(f.Fix, "plugin remove") || !strings.Contains(f.Message, "needs Cartographer") {
				t.Errorf("finding = %+v", f)
			}
		}
	}
	if !found {
		t.Error("doctor said nothing about the provider")
	}
}

// The common single-client case: every connected provider is skipped. The run
// exits 0 with the warning, pulls nothing and touches no file.
func TestSyncWithOnlyUnregisteredProvidersSucceeds(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	home, cfg, delivered := outOfRangeMachine(t, srv.URL)
	cfg.Agents = []string{"fork-cli"}
	before, err := os.ReadFile(delivered)
	if err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if _, err := runSync(home, cfg, syncOptions{Streams: syncStreams{Out: &out, Err: &errw}}); err != nil {
		t.Fatalf("sync must succeed: %v\nstderr: %s", err, errw.String())
	}
	if !strings.Contains(errw.String(), "fork-cli") || !strings.Contains(errw.String(), "needs Cartographer") {
		t.Errorf("the warning is missing:\n%s", errw.String())
	}
	if after, err := os.ReadFile(delivered); err != nil || !bytes.Equal(before, after) {
		t.Errorf("the delivered file changed: %v", err)
	}
	lf, _ := provisioning.ReadLockFile(lockFilePath(home))
	if len(lf.ForProvider("fork-cli").Managed) == 0 {
		t.Error("the lock section of the skipped provider was changed")
	}
}

// plugin show on a tree invalid for a reason other than the range names it.
func TestPluginShowNamesAnotherInvalidReason(t *testing.T) {
	home := pluginFixture(t)
	store, _ := pluginStoreFn()
	files := []core.TreeFile{{Path: core.ManifestFile, Content: []byte("schema: 1\nname: Bad Name\nversion: 1.0.0\ndisplay_name: Bad\ndescription: Bad.\ncartographer: \">=0.28.0\"\n")}}
	p, probs := core.ReadTree(files, pluginOpts())
	if len(probs) == 0 {
		t.Fatal("the fixture must be invalid")
	}
	dir := filepath.Join(store.Root, "bad-plugin", p.Digest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, core.ManifestFile), files[0].Content, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := clientconfig.Default()
	if err := cfg.SetPlugin("bad-plugin", clientconfig.PluginEntry{Source: "/x", Installed: p.Digest, Approved: p.Digest, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	msg := withStderr(t, func() { _, _ = runPlugin(t, "show", "bad-plugin") })
	if !strings.Contains(msg, "is invalid") || !strings.Contains(msg, "name") || strings.Contains(msg, "plugin install") {
		t.Errorf("message = %q; want the reason, not the reinstall hint", msg)
	}
}

// unfetchedMachine is outOfRangeMachine with the plugin cache cleared too (#2136):
// the approved tree cannot be read at all, so nothing but the lockfile says what
// the plugin installed.
func unfetchedMachine(t *testing.T, srvURL string) (home string, cfg *clientconfig.Config, delivered string) {
	t.Helper()
	home, cfg, delivered = outOfRangeMachine(t, srvURL)
	lf, err := provisioning.ReadLockFile(lockFilePath(home))
	if err != nil || lf.ForProvider("fork-cli").Plugin != "team-forks" {
		t.Fatalf("the lock must record the plugin of the section: %v %+v", err, lf.ForProvider("fork-cli"))
	}
	store, err := pluginStoreFn()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(store.Root, "team-forks")); err != nil {
		t.Fatal(err)
	}
	freshProcess(t, home)
	registerPluginProviders(&bytes.Buffer{})
	cfg, err = clientconfig.Load(home)
	if err != nil || !slicesHas(cfg.Agents, "fork-cli") {
		t.Fatalf("the load dropped the plugin's agent: %v %v", err, cfg)
	}
	return home, cfg, delivered
}

// plugin remove with the plugin cache cleared retires what the lockfile records:
// the files, the lock section and the connected agent, and says why.
func TestPluginRemoveWithoutTheTreeUsesTheLock(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	home, _, delivered := unfetchedMachine(t, srv.URL)

	var code int
	var out string
	msg := withStderr(t, func() { code, out = runPlugin(t, "remove", "team-forks") })
	if code != 0 {
		t.Fatalf("remove: %d\n%s\n%s", code, out, msg)
	}
	if !strings.Contains(msg, "cannot be read") || !strings.Contains(msg, "lockfile") {
		t.Errorf("the note must say the tree was missing and the lock used:\n%s", msg)
	}
	if _, err := os.Stat(delivered); err == nil {
		t.Errorf("%s was not pruned", delivered)
	}
	lf, _ := provisioning.ReadLockFile(lockFilePath(home))
	if _, still := lf.Providers["fork-cli"]; still {
		t.Error("the lock section of the plugin's provider is still there")
	}
	if _, ok := lf.Providers["claude"]; !ok {
		t.Error("the section of another provider must stay")
	}
	got, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Plugin("team-forks"); ok || slicesHas(got.Agents, "fork-cli") {
		t.Errorf("the entry or its agent is still configured: %v", got.Agents)
	}
}

// Removal without the tree touches only paths the lock records inside the client
// folder: a recorded path that leaves it is ignored, never joined and deleted.
func TestPluginRemoveWithoutTheTreeIgnoresAPathOutsideTheClientFolder(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	home, _, _ := unfetchedMachine(t, srv.URL)

	outside := filepath.Join(filepath.Dir(home), "outside-victim.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(t.TempDir(), "abs-victim.txt")
	if err := os.WriteFile(abs, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(lockFilePath(home))
	if err != nil {
		t.Fatal(err)
	}
	planted := strings.Replace(string(raw), `"managed": [`, `"managed": [{"kind":"skill","name":"x","path":"../outside-victim.txt","content_hash":"h"},{"kind":"skill","name":"y","path":`+strconv.Quote(abs)+`,"content_hash":"h"},`, -1)
	if planted == string(raw) {
		t.Fatalf("the lockfile has no managed list to plant into:\n%s", raw)
	}
	if err := os.WriteFile(lockFilePath(home), []byte(planted), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, out := runPlugin(t, "remove", "team-forks"); code != 0 {
		t.Fatalf("remove: %d\n%s", code, out)
	}
	for _, p := range []string{outside, abs} {
		if b, err := os.ReadFile(p); err != nil || string(b) != "keep" {
			t.Errorf("%s was touched by the removal: %v", p, err)
		}
	}
}

// status lists the provider of a plugin that is out of range, or not fetched, as
// skipped with the reason, instead of leaving it out as if never connected.
func TestStatusListsAnUnregisteredProviderAsSkipped(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	for name, setup := range map[string]func(*testing.T, string) (string, *clientconfig.Config, string){
		"out of range": outOfRangeMachine,
		"not fetched":  unfetchedMachine,
	} {
		t.Run(name, func(t *testing.T) {
			_, cfg, _ := setup(t, srv.URL)
			var found *providerStatus
			ps := providerStatuses(cfg)
			for i := range ps {
				if ps[i].Name == "fork-cli" {
					found = &ps[i]
				}
			}
			if found == nil {
				t.Fatalf("status does not list the provider: %+v", ps)
			}
			if found.State != "skipped" || found.Connected || found.Plugin != "team-forks" {
				t.Errorf("provider = %+v", *found)
			}
			for _, want := range []string{"team-forks", "plugin update", "plugin remove"} {
				if !strings.Contains(found.Skipped, want) {
					t.Errorf("reason lacks %q: %s", want, found.Skipped)
				}
			}
			b, _ := json.Marshal(found)
			if !strings.Contains(string(b), `"skipped"`) {
				t.Errorf("json lacks the reason: %s", b)
			}
		})
	}
}

// legacyLock removes the `plugin` record from every section, as a lockfile
// written before it has none, and returns the provider's section.
func legacyLock(t *testing.T, home string) {
	t.Helper()
	lf, err := provisioning.ReadLockFile(lockFilePath(home))
	if err != nil {
		t.Fatal(err)
	}
	for p, l := range lf.Providers {
		l.Plugin = ""
		lf.Providers[p] = l
	}
	if err := provisioning.WriteLockFile(lockFilePath(home), lf); err != nil {
		t.Fatal(err)
	}
}

// The reported install: a lock written before `plugin` existed, the plugin out
// of range and its tree gone. remove still retires the section and the agent,
// and status lists the provider as skipped, naming the plugin.
func TestPluginRemoveAttributesALegacySectionWithoutTheTree(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	home, cfg, delivered := unfetchedMachine(t, srv.URL)
	legacyLock(t, home)

	var found bool
	for _, p := range providerStatuses(cfg) {
		if p.Name == "fork-cli" {
			found = p.State == "skipped" && p.Plugin == "team-forks" && strings.Contains(p.Skipped, "team-forks")
		}
	}
	if !found {
		t.Errorf("status must list fork-cli as skipped, from team-forks: %+v", providerStatuses(cfg))
	}

	if code, out := runPlugin(t, "remove", "team-forks"); code != 0 {
		t.Fatalf("remove: %d\n%s", code, out)
	}
	lf, _ := provisioning.ReadLockFile(lockFilePath(home))
	if _, still := lf.Providers["fork-cli"]; still {
		t.Error("the unattributed section of the plugin's provider is still there")
	}
	if _, err := os.Stat(delivered); err == nil {
		t.Errorf("%s was not pruned", delivered)
	}
	if got, err := clientconfig.Load(home); err != nil || slicesHas(got.Agents, "fork-cli") {
		t.Errorf("the agent must go: %v %v", got, err)
	}
}

// An inferred attribution prunes only what the plugin's own entries record: a
// file another source put in the section stays, and so does the section.
func TestPluginRemoveOfAnInferredSectionKeepsOtherSources(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	home, _, _ := unfetchedMachine(t, srv.URL)
	legacyLock(t, home)
	keep := filepath.Join(home, "kb-owned.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	lf, _ := provisioning.ReadLockFile(lockFilePath(home))
	l := lf.Providers["fork-cli"]
	l.Managed = append(l.Managed, provisioning.ManagedFile{Kind: "skill", Name: "k", Path: "kb-owned.txt", ContentHash: "h", Source: "kb:kb-a"})
	lf.Providers["fork-cli"] = l
	if err := provisioning.WriteLockFile(lockFilePath(home), lf); err != nil {
		t.Fatal(err)
	}
	if code, out := runPlugin(t, "remove", "team-forks"); code != 0 {
		t.Fatalf("remove: %d\n%s", code, out)
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "keep" {
		t.Errorf("a file of another source was pruned: %v", err)
	}
	lf, _ = provisioning.ReadLockFile(lockFilePath(home))
	if len(lf.Providers["fork-cli"].Managed) != 1 {
		t.Errorf("the other source's entry must stay: %+v", lf.Providers["fork-cli"].Managed)
	}
}

// With a second plugin whose tree is also unreadable, either could own the
// provider: the section and the agent stay, and the output says so.
func TestPluginRemoveLeavesAnAmbiguousLegacySection(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	home, _, _ := unfetchedMachine(t, srv.URL)
	legacyLock(t, home)
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetPlugin("other-forks", clientconfig.PluginEntry{Source: "/x", Installed: strings.Repeat("a", 64), Approved: strings.Repeat("a", 64), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	var code int
	msg := withStderr(t, func() { code, _ = runPlugin(t, "remove", "team-forks") })
	if code != 0 || !strings.Contains(msg, "does not say this provider is from team-forks alone") {
		t.Fatalf("exit %d\n%s", code, msg)
	}
	lf, _ := provisioning.ReadLockFile(lockFilePath(home))
	if _, ok := lf.Providers["fork-cli"]; !ok {
		t.Error("an ambiguous section must stay")
	}
	if got, _ := clientconfig.Load(home); !slicesHas(got.Agents, "fork-cli") {
		t.Error("an ambiguous provider's agent must stay")
	}
}

// With no entry naming the plugin (it ships only providers), the only plugin
// whose tree cannot be read is the one picked; and a section with nothing to
// prune is never retired on that inference alone, so its agent stays.
func TestPluginOfSectionPicksTheSoleUnreadablePluginAndKeepsAnEmptySection(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	home, cfg, _ := unfetchedMachine(t, srv.URL)
	legacyLock(t, home)
	store, err := pluginStoreFn()
	if err != nil {
		t.Fatal(err)
	}
	lf, _ := provisioning.ReadLockFile(lockFilePath(home))
	l := lf.Providers["fork-cli"]
	for i := range l.Managed {
		l.Managed[i].Source = ""
	}
	if who, inferred := pluginOfSection(cfg, l, "fork-cli", store); who != "team-forks" || !inferred {
		t.Errorf("pluginOfSection = %q, %v; want team-forks inferred", who, inferred)
	}
	l.Managed = nil
	lf.Providers["fork-cli"] = l
	if err := provisioning.WriteLockFile(lockFilePath(home), lf); err != nil {
		t.Fatal(err)
	}
	if code, out := runPlugin(t, "remove", "team-forks"); code != 0 {
		t.Fatalf("remove: %d\n%s", code, out)
	}
	lf, _ = provisioning.ReadLockFile(lockFilePath(home))
	if _, ok := lf.Providers["fork-cli"]; !ok {
		t.Error("an inferred section with nothing to prune must stay")
	}
	// Read raw: Load drops an agent no plugin registers once the plugin is gone.
	if raw, err := os.ReadFile(clientconfig.Path(home)); err != nil || !strings.Contains(string(raw), "fork-cli") {
		t.Errorf("its agent must stay in the config: %v", err)
	}
}

// A readable plugin that declares the provider could be its owner: no inference.
func TestPluginOfSectionIsBlockedByAReadablePluginDeclaringTheProvider(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	home, _, _ := unfetchedMachine(t, srv.URL)
	legacyLock(t, home)
	old := version
	version = "1.0.0" // in range, to install it
	code, out := runPlugin(t, "install", providerPlugin(t, "other-forks"), "--yes")
	version = old
	if code != 0 {
		t.Fatalf("install: %d\n%s", code, out)
	}
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	store, err := pluginStoreFn()
	if err != nil {
		t.Fatal(err)
	}
	lf, _ := provisioning.ReadLockFile(lockFilePath(home))
	if who, _ := pluginOfSection(cfg, lf.Providers["fork-cli"], "fork-cli", store); who != "" {
		t.Errorf("attributed to %q though another plugin declares the provider", who)
	}
}
