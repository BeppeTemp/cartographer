package configurator

import (
	"fmt"
	"sync"
	"testing"
)

func fixtureVariant() VariantSpec {
	return VariantSpec{
		Name: "fork-cli", DisplayName: "Fork CLI", Base: string(ProviderCrush),
		Binaries:   []string{"fork"},
		DetectDirs: [][]string{{".fork"}, {".vscode", "extensions", "acme.fork-*"}},
		MCPFile:    []string{".fork", "mcp.json"},
	}
}

func registered(t *testing.T, specs ...VariantSpec) {
	t.Helper()
	ReplaceVariants(nil)
	t.Cleanup(func() { ReplaceVariants(nil) })
	for _, s := range specs {
		if err := RegisterVariant(s); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVariantResolvesAndEmitsIntoItsOwnFile(t *testing.T) {
	registered(t, fixtureVariant())
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
		t.Fatal("variant emitted into the base's file")
	}
	if _, ok := Lookup(r.Provider); !ok {
		t.Fatal("result's provider not found")
	}
}

func TestVariantRefusals(t *testing.T) {
	registered(t, fixtureVariant())
	for name, s := range map[string]VariantSpec{
		"built-in":        {Name: string(ProviderClaudeCode), Base: string(ProviderCrush)},
		"duplicate":       fixtureVariant(),
		"bad base":        {Name: "other", Base: "nope"},
		"variant as base": {Name: "other", Base: "fork-cli"},
		"empty":           {Base: string(ProviderCrush)},
	} {
		if err := RegisterVariant(s); err == nil {
			t.Errorf("%s: registered", name)
		}
	}
}

func TestVariantsStayOutOfBuiltInLists(t *testing.T) {
	n, o, l := len(Providers()), len(ProviderList()), len(DetectionOrder())
	registered(t, fixtureVariant())
	if len(Providers()) != n || len(ProviderList()) != o || len(DetectionOrder()) != l {
		t.Fatal("a variant entered a built-in list")
	}
	all := AllProviders()
	if len(all) != n+1 || all[n].Provider != "fork-cli" || len(Variants()) != 1 {
		t.Fatalf("AllProviders = %d", len(all))
	}
	ReplaceVariants(nil)
	if _, ok := Lookup("fork-cli"); ok {
		t.Fatal("reset kept the variant")
	}
}

func TestVariantConcurrentRegisterLookup(t *testing.T) {
	registered(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := fixtureVariant()
			s.Name = fmt.Sprintf("fork-%d", i)
			_ = RegisterVariant(s)
			Lookup(Provider(s.Name))
			AllProviders()
		}()
	}
	wg.Wait()
	if len(Variants()) != 8 {
		t.Fatalf("got %d", len(Variants()))
	}
}

func TestVariantMCPFileMustMatchBaseFormat(t *testing.T) {
	registered(t)
	toml := fixtureVariant()
	toml.MCPFile = []string{".fork", "mcp.toml"} // base is JSON
	if err := RegisterVariant(toml); err == nil {
		t.Error("a .toml file for a JSON base was accepted")
	}
	json := fixtureVariant()
	json.Base, json.MCPFile = string(ProviderCodex), []string{".fork", "mcp.json"} // base is TOML
	if err := RegisterVariant(json); err == nil {
		t.Error("a .json file for a TOML base was accepted")
	}
	ok := fixtureVariant()
	ok.Base, ok.MCPFile = string(ProviderCodex), []string{".fork", "config.toml"}
	if err := RegisterVariant(ok); err != nil {
		t.Errorf("matching TOML refused: %v", err)
	}
}

func TestVariantWithoutMCPFileCannotEmit(t *testing.T) {
	s := fixtureVariant()
	s.MCPFile = nil
	registered(t, s)
	if _, err := EmitServer("kb", ServerSpec{Type: "http", URL: "http://example.com/mcp"}, "fork-cli"); err == nil {
		t.Fatal("emitted with no MCP file")
	}
}

func TestReplaceVariantsIsAtomic(t *testing.T) {
	registered(t, fixtureVariant())
	bad := fixtureVariant()
	bad.Name = "other"
	bad.Base = "nope"
	if err := ReplaceVariants([]VariantSpec{bad}); err == nil {
		t.Fatal("refused spec accepted")
	}
	if _, ok := Lookup("fork-cli"); !ok {
		t.Fatal("a refused replace dropped the registered set")
	}
	next := fixtureVariant()
	next.Name = "fork-two"
	if err := ReplaceVariants([]VariantSpec{next}); err != nil {
		t.Fatal(err)
	}
	if _, ok := Lookup("fork-cli"); ok {
		t.Fatal("old variant kept")
	}
	if _, ok := Lookup("fork-two"); !ok {
		t.Fatal("new variant missing")
	}
	if err := ReplaceVariants([]VariantSpec{next, next}); err == nil {
		t.Fatal("duplicate names accepted")
	}
}

func TestVariantResultsAreCopies(t *testing.T) {
	registered(t, fixtureVariant())
	d, _ := Lookup("fork-cli")
	d.Binaries[0], d.ConfigDirs[0][0], d.ConfigDirGlobs[0][0] = "x", "x", "x"
	for _, got := range []Descriptor{mustLookup(t), Variants()[0], RegisteredVariants()[0].Descriptor} {
		if got.Binaries[0] != "fork" || got.ConfigDirs[0][0] != ".fork" || got.ConfigDirGlobs[0][0] != ".vscode" {
			t.Fatalf("registry changed through a returned descriptor: %+v", got)
		}
	}
}

func mustLookup(t *testing.T) Descriptor {
	d, ok := Lookup("fork-cli")
	if !ok {
		t.Fatal("missing")
	}
	return d
}
