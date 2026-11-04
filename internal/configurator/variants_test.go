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
	ResetVariants()
	t.Cleanup(ResetVariants)
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
	ResetVariants()
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
