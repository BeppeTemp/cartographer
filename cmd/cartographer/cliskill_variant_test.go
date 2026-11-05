package main

import (
	"os"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func registerCLIVariant(t *testing.T) {
	t.Helper()
	spec := configurator.VariantSpec{Name: "fork-shell", Base: string(configurator.ProviderCrush), MCPMode: configurator.MCPModeCLI, Binaries: []string{"fork-shell"}}
	if err := configurator.ReplaceVariants([]configurator.VariantSpec{spec}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { configurator.ReplaceVariants(nil) })
}

// A cli variant gets no MCP entry on connect or sync, nothing to remove on
// disconnect, and no warning; the MCP provider next to it is unaffected.
func TestCLIVariantHasNoMCPEntry(t *testing.T) {
	registerCLIVariant(t)
	cfg := bridgeConfigFor(t, "claude", "fork-shell")
	entries, err := entriesByProviderForKBs(cfg, []string{"claude", "fork-shell"}, "cartographer", cfg.ServerURL, cfg.KnownKBs, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries["fork-shell"]) != 0 || len(entries["claude"]) == 0 {
		t.Fatalf("entries = %v", entries)
	}
	dir := t.TempDir()
	written, warnings, err := applyMCPEntries(map[string][]mcpEntry{"fork-shell": {{Name: "cartographer", URL: cfg.ServerURL}}}, []string{"fork-shell"}, dir, nil, false, "", false)
	if err != nil || len(written) != 0 || len(warnings) != 0 {
		t.Fatalf("applyMCPEntries = %v, %v, %v", written, warnings, err)
	}
	names, removed, err := removeMCPEntries("cartographer", cfg.KnownKBs, []string{"fork-shell"}, dir, nil, false, "", false)
	if err != nil || len(names) != 0 || len(removed) != 0 {
		t.Fatalf("removeMCPEntries = %v, %v, %v", names, removed, err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("files were created: %v", ents)
	}
}

// The bundled cartographer-cli skill goes to the cli variant and to no other provider.
func TestCLISkillGoesToTheCLIVariantOnly(t *testing.T) {
	registerCLIVariant(t)
	m := provisioning.Manifest{Artifacts: []provisioning.Artifact{{Kind: "skill", Name: cliSkillName, Source: "bundle"}}}
	if got := len(withoutCLISkill("fork-shell", m).Artifacts); got != 1 {
		t.Errorf("the cli variant has %d artifacts, want the skill kept", got)
	}
	for _, p := range configurator.ProviderList() {
		if got := len(withoutCLISkill(string(p), m).Artifacts); got != 0 {
			t.Errorf("%s keeps the cli skill", p)
		}
	}
}
