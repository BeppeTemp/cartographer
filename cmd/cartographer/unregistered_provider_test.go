package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
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
	_, removed, err := removeMCPEntries("cartographer", nil, []string{"ghost", "claude"}, home, nil, false, "", true)
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
