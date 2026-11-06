package configurator

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func fixturePlugin() PluginSpec {
	return PluginSpec{
		Name: "fork-cli", DisplayName: "Fork CLI", Base: string(ProviderCrush), Origin: "team", Active: true,
		Binaries:   []string{"fork"},
		DetectDirs: [][]string{{".fork"}, {".vscode", "extensions", "acme.fork-*"}},
		MCPMode:    "native",
		MCPConfig:  []string{".fork", "mcp.json"},
	}
}

func registeredPlugins(t *testing.T, specs ...PluginSpec) {
	t.Helper()
	if _, err := ReplacePlugins(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = ReplacePlugins(nil) })
	for _, s := range specs {
		if err := RegisterPlugin(s); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPluginResolvesAndEmitsIntoItsOwnFile(t *testing.T) {
	registeredPlugins(t, fixturePlugin())
	d, ok := Lookup("fork-cli")
	if !ok || d.DisplayName != "Fork CLI" || d.MCPConfigPath != ".fork/mcp.json" {
		t.Fatalf("Lookup = %+v, %v", d, ok)
	}
	if len(d.ConfigDirs) != 1 || len(d.ConfigDirGlobs) != 1 {
		t.Fatalf("dirs %v globs %v", d.ConfigDirs, d.ConfigDirGlobs)
	}
	if d.EnvConfigDirs != nil || d.AppDirs != nil || d.InstructionsPrecedence != nil {
		t.Fatalf("base evidence leaked: %+v", d)
	}
	r, err := EmitServer("kb", ServerSpec{Type: "http", URL: "http://example.com/mcp"}, "fork-cli")
	if err != nil {
		t.Fatal(err)
	}
	if r.Provider != "fork-cli" || r.FilePath != d.ConfigPath() {
		t.Fatalf("emit wrote %s for %s", r.FilePath, r.Provider)
	}
	base, _ := Lookup(ProviderCrush)
	if r.FilePath == base.ConfigPath() {
		t.Fatal("the fork emitted into the base's file")
	}
}

// A native provider with no file of its own keeps the base's file.
func TestPluginWithoutMCPConfigKeepsTheBasesFile(t *testing.T) {
	s := fixturePlugin()
	s.MCPConfig, s.MCPMode = nil, ""
	registeredPlugins(t, s)
	d, _ := Lookup("fork-cli")
	base, _ := Lookup(ProviderCrush)
	if d.MCPConfigPath != base.MCPConfigPath || d.MCPMode != "" {
		t.Fatalf("path %q mode %q", d.MCPConfigPath, d.MCPMode)
	}
}

func TestPluginRefusals(t *testing.T) {
	registeredPlugins(t, fixturePlugin())
	cliWithFile := fixturePlugin()
	cliWithFile.Name, cliWithFile.MCPMode = "other", MCPModeCLI
	for name, s := range map[string]PluginSpec{
		"built-in":        {Name: string(ProviderClaudeCode), Base: string(ProviderCrush)},
		"duplicate":       fixturePlugin(),
		"bad base":        {Name: "other", Base: "nope"},
		"plugin as base":  {Name: "other", Base: "fork-cli"},
		"empty":           {Base: string(ProviderCrush)},
		"unknown mode":    {Name: "other", Base: string(ProviderCrush), MCPMode: "seed"},
		"cli with a file": cliWithFile,
		"none with stale": {Name: "other", Base: string(ProviderCrush), MCPMode: MCPModeNone, MCPStale: [][]string{{".x", "y"}}},
		"wrong extension": {Name: "other", Base: string(ProviderCrush), MCPConfig: []string{".fork", "mcp.toml"}},
		"toml base, json": {Name: "other", Base: string(ProviderCodex), MCPConfig: []string{".fork", "mcp.json"}},
	} {
		if err := RegisterPlugin(s); err == nil {
			t.Errorf("%s: registered", name)
		}
	}
	ok := PluginSpec{Name: "fork-toml", Base: string(ProviderCodex), MCPConfig: []string{".fork", "config.toml"}}
	if err := RegisterPlugin(ok); err != nil {
		t.Errorf("matching TOML refused: %v", err)
	}
}

func TestPluginsStayOutOfBuiltInLists(t *testing.T) {
	n, o, l := len(Providers()), len(ProviderList()), len(DetectionOrder())
	emitted, err := EmitAll(&ServerConfig{Name: "kb", URL: "http://example.com/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	registeredPlugins(t, fixturePlugin())
	if len(Providers()) != n || len(ProviderList()) != o || len(DetectionOrder()) != l {
		t.Fatal("a registration entered a built-in list")
	}
	again, err := EmitAll(&ServerConfig{Name: "kb", URL: "http://example.com/mcp"})
	if err != nil || len(again) != len(emitted) {
		t.Fatalf("EmitAll changed: %d -> %d (%v)", len(emitted), len(again), err)
	}
	all := AllProviders()
	if len(all) != n+1 || all[n].Provider != "fork-cli" || len(Plugins()) != 1 {
		t.Fatalf("AllProviders = %d", len(all))
	}
	if _, err := ReplacePlugins(nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := Lookup("fork-cli"); ok {
		t.Fatal("reset kept the provider")
	}
}

func TestPluginConcurrentRegisterLookup(t *testing.T) {
	registeredPlugins(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := fixturePlugin()
			s.Name = fmt.Sprintf("fork-%d", i)
			_ = RegisterPlugin(s)
			Lookup(Provider(s.Name))
			AllProviders()
		}()
	}
	wg.Wait()
	if len(Plugins()) != 8 {
		t.Fatalf("got %d", len(Plugins()))
	}
}

func TestReplacePluginsIsAtomic(t *testing.T) {
	registeredPlugins(t, fixturePlugin())
	bad := fixturePlugin()
	bad.Name, bad.Base = "other", "nope"
	if _, err := ReplacePlugins([]PluginSpec{bad}); err == nil {
		t.Fatal("refused spec accepted")
	}
	if _, ok := Lookup("fork-cli"); !ok {
		t.Fatal("a refused replace dropped the registered set")
	}
	next := fixturePlugin()
	next.Name = "fork-two"
	if _, err := ReplacePlugins([]PluginSpec{next}); err != nil {
		t.Fatal(err)
	}
	if _, ok := Lookup("fork-cli"); ok {
		t.Fatal("old provider kept")
	}
	if _, ok := Lookup("fork-two"); !ok {
		t.Fatal("new provider missing")
	}
}

// A name claimed twice is refused for every claimant and reported; the other
// providers, of those specs and of the rest, still register.
func TestReplacePluginsRefusesAClaimedTwiceName(t *testing.T) {
	registeredPlugins(t)
	a, b, c := fixturePlugin(), fixturePlugin(), fixturePlugin()
	a.Origin, b.Origin = "team-a", "team-b"
	a.MCPConfig = []string{".fork", "mcp.json"}
	c.Name, c.Origin = "fork-solo", "team-a"
	other := fixturePlugin()
	other.Name, other.Origin = "fork-also", "team-b"
	refused, err := ReplacePlugins([]PluginSpec{a, c, b, other})
	if err != nil || !reflect.DeepEqual(refused, []string{"fork-cli"}) {
		t.Fatalf("refused %v, err %v", refused, err)
	}
	if _, ok := Lookup("fork-cli"); ok {
		t.Error("a name claimed by two plugins registered")
	}
	for _, n := range []Provider{"fork-solo", "fork-also"} {
		if _, ok := Lookup(n); !ok {
			t.Errorf("%s was dropped with the clash", n)
		}
	}
}

// A pattern is carried beside MCPConfigPath, never in it: callers join the path
// literally.
func TestPluginMCPGlobStaysOutOfThePath(t *testing.T) {
	g := fixturePlugin()
	g.Name, g.MCPConfig = "fork-glob", []string{".vscode", "extensions", "acme.fork-*", "mcp.json"}
	registeredPlugins(t, g)
	d, ok := Lookup("fork-glob")
	if !ok || d.MCPConfigPath != "" || d.ConfigPath() != "" || d.MCPConfigGlob != ".vscode/extensions/acme.fork-*/mcp.json" {
		t.Fatalf("glob provider = %+v, %v", d, ok)
	}
	if _, err := EmitServer("kb", ServerSpec{Type: "http", URL: "http://example.com/mcp"}, "fork-glob"); err == nil {
		t.Error("a pattern was emitted as a file path")
	}
	if b, _ := Lookup(ProviderCrush); b.MCPConfigGlob != "" {
		t.Error("a built-in carries a glob")
	}
}

func TestPluginResultsAreCopies(t *testing.T) {
	registeredPlugins(t, fixturePlugin())
	d, _ := Lookup("fork-cli")
	d.Binaries[0], d.ConfigDirs[0][0], d.ConfigDirGlobs[0][0] = "x", "x", "x"
	reg := RegisteredPlugins()[0]
	reg.Spec.MCPConfig[0], reg.Spec.Binaries[0] = "x", "x"
	spec, _ := PluginOf("fork-cli")
	for _, got := range []Descriptor{mustLookupPlugin(t), Plugins()[0], RegisteredPlugins()[0].Descriptor} {
		if got.Binaries[0] != "fork" || got.ConfigDirs[0][0] != ".fork" || got.ConfigDirGlobs[0][0] != ".vscode" {
			t.Fatalf("registry changed through a returned descriptor: %+v", got)
		}
	}
	if !reflect.DeepEqual(spec.MCPConfig, []string{".fork", "mcp.json"}) || spec.Binaries[0] != "fork" {
		t.Fatalf("registry changed through a returned spec: %+v", spec)
	}
}

func mustLookupPlugin(t *testing.T) Descriptor {
	t.Helper()
	d, ok := Lookup("fork-cli")
	if !ok {
		t.Fatal("missing")
	}
	return d
}

// The mode is data the registry reads from the spec: only cli uses the shell,
// and none writes no entry at all.
func TestPluginMCPModes(t *testing.T) {
	cli := fixturePlugin()
	cli.Name, cli.MCPMode, cli.MCPConfig = "fork-shell", MCPModeCLI, nil
	none := fixturePlugin()
	none.Name, none.MCPMode, none.MCPConfig = "fork-quiet", MCPModeNone, nil
	registeredPlugins(t, cli, none, fixturePlugin())
	if !UsesCLI("fork-shell") || UsesCLI("fork-quiet") || UsesCLI("fork-cli") || UsesCLI(ProviderCrush) {
		t.Error("only a cli provider uses the CLI")
	}
	if d, _ := Lookup("fork-quiet"); d.MCPMode != MCPModeNone || d.MCPConfigPath != "" {
		t.Errorf("none = %+v", d)
	}
	for _, n := range []Provider{"fork-shell", "fork-quiet"} {
		if _, err := EmitServer("kb", ServerSpec{Type: "http", URL: "http://example.com/mcp"}, n); err == nil {
			t.Errorf("%s emitted an entry", n)
		}
	}
}

func TestPluginKeepsOriginAndActive(t *testing.T) {
	off := fixturePlugin()
	off.Active = false
	registeredPlugins(t, off)
	s, ok := PluginOf("fork-cli")
	if !ok || s.Origin != "team" || s.Active || s.Base != string(ProviderCrush) {
		t.Fatalf("spec = %+v, %v", s, ok)
	}
	if _, ok := PluginOf(ProviderCrush); ok {
		t.Error("a built-in has no plugin spec")
	}
}
