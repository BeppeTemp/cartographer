package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

const forkProviderYAML = `schema: 1
name: fork-cli
display_name: Fork CLI
base: crush
detect:
  binaries: [fork-cli]
mcp:
  mode: cli
paths:
  global:
    skill: {dir: "{home}/.fork-cli/skills"}
    agent: {dir: "{home}/.fork-cli/agents", format: opencode}
    instructions: {file: "{home}/.fork-cli/FORK-CLI.md"}
  project:
    skill: {dir: ".fork-cli/skills"}
    instructions: {file: "AGENTS.md"}
`

// providerPlugin is a plugin folder that declares the fork-cli provider.
func providerPlugin(t *testing.T, name string) string {
	t.Helper()
	return writePlugin(t, name, map[string]string{"providers/fork-cli.yaml": forkProviderYAML})
}

// freshProcess forgets what a previous registration did, as a new process
// starts with nothing registered, and restores the global state afterwards.
func freshProcess(t *testing.T, home string) {
	t.Helper()
	old := clientconfig.PluginProvidersComplete
	reset := func() {
		_, _ = configurator.ReplacePlugins(nil)
		provisioning.ReplacePluginCells(nil, home)
		clientconfig.PluginProvidersComplete = old
	}
	reset()
	t.Cleanup(reset)
}

func registered(name string) bool {
	_, ok := configurator.Lookup(configurator.Provider(name))
	return ok
}

func TestRegisterPluginProvidersFromTheApprovedStore(t *testing.T) {
	home := pluginFixture(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	freshProcess(t, home)
	if code, out := runPlugin(t, "install", providerPlugin(t, "team-forks"), "--yes"); code != 0 {
		t.Fatalf("install: %d\n%s", code, out)
	}
	var errw bytes.Buffer
	registerPluginProviders(&errw)
	if !registered("fork-cli") || errw.Len() != 0 {
		t.Fatalf("registered=%v warnings=%q", registered("fork-cli"), errw.String())
	}
	if !clientconfig.PluginProvidersComplete(nil) {
		t.Error("a complete registration answers false")
	}
	if rp, _ := configurator.PluginOf("fork-cli"); !rp.Active || rp.Origin != "team-forks" {
		t.Errorf("spec: %+v", rp)
	}
}

// The D898 trap: an unreadable store registers nothing and the config keeps the
// agents, the scopes and the lock section of the provider.
func TestPurgedStoreDropsNothingFromConfigOrLock(t *testing.T) {
	home := pluginFixture(t)
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
	cfg.Agents = []string{"claude", "fork-cli"}
	cfg.Clients = map[string]clientconfig.ClientBinding{"fork-cli": {KBs: []string{}, KBsSet: true}}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	lock := provisioning.LockFile{Providers: map[string]provisioning.Lock{"fork-cli": {BaseDir: home}}}
	if err := provisioning.WriteLockFile(lockFilePath(home), lock); err != nil {
		t.Fatal(err)
	}

	// A new process whose store was purged.
	store, _ := pluginStoreFn()
	if err := os.RemoveAll(store.Root); err != nil {
		t.Fatal(err)
	}
	freshProcess(t, home)
	var errw bytes.Buffer
	registerPluginProviders(&errw)
	if registered("fork-cli") || clientconfig.PluginProvidersComplete(nil) {
		t.Fatalf("a purged store registered the provider or claimed completeness (%q)", errw.String())
	}
	if !strings.Contains(errw.String(), "team-forks") {
		t.Errorf("no warning names the plugin: %q", errw.String())
	}
	cfg, err = clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.Agents, ","); got != "claude,fork-cli" || len(cfg.Clients) != 1 || len(cfg.Unsupported) != 0 {
		t.Errorf("the config lost the provider: agents %q clients %v unsupported %v", got, cfg.Clients, cfg.Unsupported)
	}
	handleUnsupportedProviders(&bytes.Buffer{}, home, cfg, true)
	if lf, err := provisioning.ReadLockFile(lockFilePath(home)); err != nil || len(lf.Providers) != 1 {
		t.Errorf("the lock section was dropped: %v %+v", err, lf.Providers)
	}
}

func TestRegisteredProviderIsKeptAndAnUnknownNameIsStillCleaned(t *testing.T) {
	home := pluginFixture(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	freshProcess(t, home)
	if code, out := runPlugin(t, "install", providerPlugin(t, "team-forks"), "--yes"); code != 0 {
		t.Fatalf("install: %d\n%s", code, out)
	}
	freshProcess(t, home)
	registerPluginProviders(&bytes.Buffer{})
	// Written by hand: Load is what drops, and the unknown name is in the file.
	doc := "clients:\n  fork-cli:\n    kbs: []\n  ghost-client:\n    kbs: []\n"
	if err := os.WriteFile(clientconfig.Path(home), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Clients["fork-cli"]; !ok || len(cfg.Clients) != 1 {
		t.Errorf("clients = %v: the registered provider must stay and the unknown one go", cfg.Clients)
	}
	if len(cfg.Unsupported) != 1 || cfg.Unsupported[0] != "ghost-client" {
		t.Errorf("unsupported = %v", cfg.Unsupported)
	}
}

// Two plugins declaring one provider name register neither, and the run says so.
func TestTwoPluginsClaimingAProviderRegisterNeither(t *testing.T) {
	home := pluginFixture(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	freshProcess(t, home)
	for _, n := range []string{"team-forks", "other-forks"} {
		if code, out := runPlugin(t, "install", providerPlugin(t, n), "--yes"); code != 0 {
			t.Fatalf("install %s: %d\n%s", n, code, out)
		}
	}
	var errw bytes.Buffer
	registerPluginProviders(&errw)
	if registered("fork-cli") || clientconfig.PluginProvidersComplete(nil) {
		t.Errorf("a claimed name registered or the registration claimed completeness")
	}
	if !strings.Contains(errw.String(), "fork-cli") {
		t.Errorf("the refusal is not reported: %q", errw.String())
	}
}

// A disabled plugin's provider stays registered inactive, and sync disconnects
// it: its lock section, its files and its agents entry go.
func TestSyncRetiresTheProviderOfADisabledPlugin(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	home, cfg := deliveryMachine(t, srv.URL)
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
	cfg.ServerURL, cfg.ServerName, cfg.Agents = srv.URL+"/mcp", "cartographer", []string{"claude", "fork-cli"}
	// No KB is bound: the run pulls nothing and delivers what the providers' own cells hold.
	bound := clientconfig.ClientBinding{KBs: []string{}, KBsSet: true}
	cfg.Clients = map[string]clientconfig.ClientBinding{"claude": bound, "fork-cli": bound}
	if _, err := runSync(home, cfg, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	lf, err := provisioning.ReadLockFile(lockFilePath(home))
	if err != nil || len(lf.ForProvider("fork-cli").Managed) == 0 {
		t.Fatalf("the provider was not materialized: %v %+v", err, lf.Providers)
	}
	written := filepath.Join(home, filepath.FromSlash(lf.ForProvider("fork-cli").Managed[0].Path))

	e := cfg.Plugins["team-forks"]
	e.Enabled = false
	if err := cfg.SetPlugin("team-forks", e); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	freshProcess(t, home)
	registerPluginProviders(&bytes.Buffer{})
	if rp, ok := configurator.PluginOf("fork-cli"); !ok || rp.Active {
		t.Fatalf("a disabled plugin's provider must stay registered inactive: %+v %v", rp, ok)
	}
	if _, err := runSync(home, cfg, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	lf, _ = provisioning.ReadLockFile(lockFilePath(home))
	if _, still := lf.Providers["fork-cli"]; still {
		t.Error("the lock section of the retired provider is still there")
	}
	if _, err := os.Stat(written); err == nil {
		t.Errorf("%s was not pruned", written)
	}
	for _, a := range cfg.Agents {
		if a == "fork-cli" {
			t.Errorf("agents still lists the retired provider: %v", cfg.Agents)
		}
	}
	if got, _ := clientconfig.Load(home); got != nil {
		for _, a := range got.Agents {
			if a == "fork-cli" {
				t.Errorf("the saved config still lists the retired provider: %v", got.Agents)
			}
		}
	}
}

// registerPluginProviders must run before anything loads the client config.
func TestRegistrationPrecedesRunInMain(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if id, ok := c.Fun.(*ast.Ident); ok {
					calls = append(calls, id.Name)
				}
			}
			return true
		})
	}
	reg, run := -1, -1
	for i, c := range calls {
		switch c {
		case "registerPluginProviders":
			reg = i
		case "run":
			run = i
		}
	}
	if reg < 0 || run < 0 || reg > run {
		t.Errorf("main must call registerPluginProviders before run (calls: %v)", calls)
	}
}

// A skill directory that is a dangling link is left out of the provider (#1790).
// The registration stays complete, the reason is an issue, sync and status say it
// once and doctor reports it; a link listed under allow_symlinks keeps the cell,
// although the listing is read before the full Load.
func TestRegisterPluginProvidersSaysTheCellItLeftOut(t *testing.T) {
	home := pluginFixture(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	freshProcess(t, home)
	t.Cleanup(safewrite.ResetAllowedLinks)
	if err := os.MkdirAll(filepath.Join(home, ".fork-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".fork-cli", "skills")
	if err := os.Symlink(filepath.Join(home, "missing"), link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if code, out := runPlugin(t, "install", providerPlugin(t, "team-forks"), "--yes"); code != 0 {
		t.Fatalf("install: %d\n%s", code, out)
	}
	var errw bytes.Buffer
	registerPluginProviders(&errw)
	if !registered("fork-cli") || !clientconfig.PluginProvidersComplete(nil) || errw.Len() != 0 {
		t.Fatalf("registered=%v complete=%v stderr=%q", registered("fork-cli"), clientconfig.PluginProvidersComplete(nil), errw.String())
	}
	if len(pluginProviderIssues) != 1 || !pluginProviderIssues[0].LeftOut || pluginProviderIssues[0].Provider != "fork-cli" ||
		!strings.Contains(pluginProviderIssues[0].Reason, "paths.global.skill.dir") {
		t.Fatalf("issues: %+v", pluginProviderIssues)
	}

	// sync and connect: one line, from the summary they share.
	results := map[string]provisioning.AppliedResult{
		"claude": {Written: []provisioning.ManagedFile{{Kind: "skill", Name: "runbooks", Path: ".claude/skills/runbooks/SKILL.md"}}},
	}
	out := withStdout(t, func() { printApplySummary(os.Stdout, home, results, false, nil) })
	if n := strings.Count(out, "[fork-cli] warning: cell left out: paths.global.skill.dir"); n != 1 || !strings.Contains(out, "[claude] wrote") {
		t.Errorf("sync warning printed %d times, or the summary is missing:\n%s", n, out)
	}
	// status
	var st bytes.Buffer
	printPluginSection(&st, statusSnapshot{
		Plugins:   []pluginStatus{{Name: "team-forks", State: "active"}},
		Providers: []providerStatus{{Name: "fork-cli", Plugin: "team-forks"}},
	})
	if !strings.Contains(st.String(), "[fork-cli] warning: cell left out") {
		t.Errorf("status:\n%s", st.String())
	}
	// doctor
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range checkPlugins(cfg, "", nil) {
		if f.Check == "plugin-provider" && strings.Contains(f.Message, "provider fork-cli") && strings.Contains(f.Message, "without a cell") &&
			f.Severity == doctorWarning && strings.Contains(f.Fix, "providers.fork-cli.allow_symlinks") {
			found = true
		}
	}
	if !found {
		t.Errorf("no doctor finding: %+v", checkPlugins(cfg, "", nil))
	}

	// Listed: the cell is kept.
	path := clientconfig.Path(home)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "\nproviders:") {
		t.Fatalf("fixture already has providers:\n%s", b)
	}
	if err := os.WriteFile(path, append(b, []byte("\nproviders:\n  fork-cli:\n    allow_symlinks: [\"~/.fork-cli/skills\"]\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	// The allow set in place is not touched by a registration (it is restored).
	sentinel := filepath.Join(home, "sentinel-link")
	safewrite.SetAllowedLinks([]string{sentinel})
	registerPluginProviders(&errw)
	if len(pluginProviderIssues) != 0 {
		t.Errorf("a listed link keeps the cell: %+v", pluginProviderIssues)
	}
	if got := safewrite.AllowedLinks(); len(got) != 1 || got[0] != sentinel {
		t.Errorf("the registration left the allow set at %v", got)
	}
}

// Only a link reason gets the allow_symlinks fix; any other left-out path names
// the descriptor (C2).
func TestLeftOutFindingFixMatchesTheReason(t *testing.T) {
	link := providerIssueFinding(pluginProviderIssue{Plugin: "team-forks", Provider: "fork-cli", LeftOut: true,
		Reason: "paths.global.skill.dir: safewrite: refusing to write through symlink \"/h/x\" -> \"/y\" (replace it)"})
	if !strings.Contains(link.Fix, "providers.fork-cli.allow_symlinks") {
		t.Errorf("link fix: %q", link.Fix)
	}
	out := providerIssueFinding(pluginProviderIssue{Plugin: "team-forks", Provider: "fork-cli", LeftOut: true,
		Reason: "paths.global.skill.dir: path escapes the home directory"})
	if strings.Contains(out.Fix, "allow_symlinks") || !strings.Contains(out.Fix, "descriptor") || !strings.Contains(out.Fix, "plugin update team-forks") {
		t.Errorf("non-link fix: %q", out.Fix)
	}
}
