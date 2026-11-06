package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func registerTestProviders(t *testing.T, specs ...configurator.PluginSpec) {
	t.Helper()
	if _, err := configurator.ReplacePlugins(specs); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = configurator.ReplacePlugins(nil) })
}

func shellAndNone() []configurator.PluginSpec {
	return []configurator.PluginSpec{
		{Name: "fork-shell", Base: string(configurator.ProviderCrush), MCPMode: configurator.MCPModeCLI, Active: true},
		{Name: "fork-none", Base: string(configurator.ProviderCrush), MCPMode: configurator.MCPModeNone, Active: true},
	}
}

// A cli or none provider gets no MCP entry on connect or sync, nothing to remove
// on disconnect, and no warning; the MCP provider next to it is unaffected.
func TestCLIAndNoneProvidersHaveNoMCPEntry(t *testing.T) {
	registerTestProviders(t, shellAndNone()...)
	names := []string{"claude", "fork-shell", "fork-none"}
	cfg := bridgeConfigFor(t, names...)
	entries, err := entriesByProviderForKBs(cfg, names, "cartographer", cfg.ServerURL, cfg.KnownKBs, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries["fork-shell"]) != 0 || len(entries["fork-none"]) != 0 || len(entries["claude"]) == 0 {
		t.Fatalf("entries = %v", entries)
	}
	dir := t.TempDir()
	in := map[string][]mcpEntry{}
	for _, n := range names[1:] {
		in[n] = []mcpEntry{{Name: "cartographer", URL: cfg.ServerURL}}
	}
	written, warnings, err := applyMCPEntries(in, names[1:], dir, nil, false, "", false)
	if err != nil || len(written) != 0 || len(warnings) != 0 {
		t.Fatalf("applyMCPEntries = %v, %v, %v", written, warnings, err)
	}
	removedNames, removed, err := removeMCPEntries("cartographer", cfg.KnownKBs, names[1:], dir, nil, false, "", false)
	if err != nil || len(removedNames) != 0 || len(removed) != 0 {
		t.Fatalf("removeMCPEntries = %v, %v, %v", removedNames, removed, err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("files were created: %v", ents)
	}
	// doctor does not look for an entry in a file that does not exist.
	if f := checkMCPEntries(dir, cfg, names[1:]); len(f) != 0 {
		t.Errorf("doctor findings for cli/none providers: %v", f)
	}
}

// The bundled cartographer-cli skill goes to a cli provider and to no other.
func TestCLISkillGoesToTheCLIProviderOnly(t *testing.T) {
	registerTestProviders(t, shellAndNone()...)
	m := provisioning.Manifest{Artifacts: []provisioning.Artifact{{Kind: "skill", Name: cliSkillName, Source: "bundle"}}}
	if got := len(withoutCLISkill("fork-shell", m).Artifacts); got != 1 {
		t.Errorf("the cli provider has %d artifacts, want the skill kept", got)
	}
	if got := len(withoutCLISkill("fork-none", m).Artifacts); got != 0 {
		t.Errorf("a none provider keeps the cli skill")
	}
	for _, p := range configurator.ProviderList() {
		if got := len(withoutCLISkill(string(p), m).Artifacts); got != 0 {
			t.Errorf("%s keeps the cli skill", p)
		}
	}
}

// A provider whose MCP file holds a `*` gets the entry in every installed
// version's file through connect/sync's own writer, reports it as changed, and
// disconnect takes it out of each; nothing is created where the client is absent.
func TestGlobMCPProviderIsWrittenAndRemovedThroughTheCommonPath(t *testing.T) {
	stubBridgeCommand(t)
	registerTestProviders(t, configurator.PluginSpec{
		Name: "fork-ext", Base: string(configurator.ProviderCrush), MCPMode: "native", Active: true,
		MCPConfig: []string{".fork", "ext-*", "mcp.json"},
	})
	home := t.TempDir()
	cfg := bridgeConfigFor(t, "fork-ext")
	entries := map[string][]mcpEntry{"fork-ext": {{Name: "cartographer", URL: cfg.ServerURL}}}

	written, warnings, err := applyMCPEntries(entries, []string{"fork-ext"}, home, nil, false, "", false)
	if err != nil || len(written) != 0 || !strings.Contains(strings.Join(warnings, "\n"), "open the client once") {
		t.Fatalf("no client installed: %v %v %v", written, warnings, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".fork")); !os.IsNotExist(err) {
		t.Fatal("the client's folder was created")
	}

	tpls := []string{filepath.Join(home, ".fork", "ext-1", "mcp.json"), filepath.Join(home, ".fork", "ext-2", "mcp.json")}
	for _, p := range tpls {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snap := snapshotMCPConfigFiles([]string{"fork-ext"}, home, nil)
	if len(snap) != 2 {
		t.Fatalf("snapshot = %v", snap)
	}
	written, _, err = applyMCPEntries(entries, []string{"fork-ext"}, home, nil, false, "", false)
	if err != nil || len(written) != 2 {
		t.Fatalf("applyMCPEntries = %v, %v", written, err)
	}
	if got := changedMCPEntries([]string{"fork-ext"}, entries, snap, home, nil, false, ""); len(got) != 1 {
		t.Errorf("changed entries = %v", got)
	}
	for _, p := range tpls {
		if b, _ := os.ReadFile(p); !strings.Contains(string(b), "cartographer") {
			t.Errorf("%s lacks the entry:\n%s", p, b)
		}
	}
	_, removed, err := removeMCPEntries("cartographer", cfg.KnownKBs, []string{"fork-ext"}, home, nil, false, "", false)
	if err != nil || !removed["fork-ext"] {
		t.Fatalf("removeMCPEntries = %v, %v", removed, err)
	}
	for _, p := range tpls {
		if b, _ := os.ReadFile(p); strings.Contains(string(b), "cartographer") {
			t.Errorf("%s still holds the entry:\n%s", p, b)
		}
	}
	// Doctor has no single file to read for a pattern and says nothing.
	if f := checkMCPEntries(home, cfg, []string{"fork-ext"}); len(f) != 0 {
		t.Errorf("doctor findings for a pattern provider: %v", f)
	}
}
