package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

func registerSeedVariant(t *testing.T) {
	t.Helper()
	spec := configurator.VariantSpec{
		Name: "fork-ext", Base: string(configurator.ProviderCline), MCPMode: configurator.MCPModeSeed,
		SeedTemplate: []string{".fork", "ext-*", "template.json"}, SeedDerived: []string{".fork", "state", "servers.json"},
	}
	if err := configurator.ReplaceVariants([]configurator.VariantSpec{spec}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { configurator.ReplaceVariants(nil) })
}

// connect and sync seed the template with the stdio bridge (never the HTTP
// entry) and tell the operator the entry is read-only until the folder is bound;
// disconnect removes the entry from the template only.
func TestSeedVariantGetsTheBridgeInItsTemplate(t *testing.T) {
	registerSeedVariant(t)
	stubBridgeCommand(t)
	home := t.TempDir()
	tpl := filepath.Join(home, ".fork", "ext-1", "template.json")
	derived := filepath.Join(home, ".fork", "state", "servers.json")
	for p, c := range map[string]string{tpl: `{"mcpServers":{"other":{"command":"x"}}}`, derived: `{}`} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := bridgeConfigFor(t, "fork-ext")
	entries := map[string][]mcpEntry{"fork-ext": {{Name: "cartographer", URL: cfg.ServerURL}}}
	written, warnings, err := applyMCPEntries(entries, []string{"fork-ext"}, home, nil, false, "", false)
	if err != nil || len(written) != 0 {
		t.Fatalf("applyMCPEntries = %v, %v, %v", written, warnings, err)
	}
	got, _ := os.ReadFile(tpl)
	for _, want := range []string{`"other"`, `"cartographer"`, `"mcp"`, `/usr/local/bin/cartographer`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("template lacks %s:\n%s", want, got)
		}
	}
	if strings.Contains(string(got), "https://") {
		t.Errorf("the HTTP entry was seeded:\n%s", got)
	}
	if _, err := os.Stat(derived); err == nil {
		t.Error("derived file not reset")
	}
	all := strings.Join(warnings, "\n")
	for _, want := range []string{"restart the window", "read-only until the folder is bound", "unbound_access: {fork-ext: write}"} {
		if !strings.Contains(all, want) {
			t.Errorf("warnings lack %q:\n%s", want, all)
		}
	}
	if _, warnings, _ := applyMCPEntries(entries, []string{"fork-ext"}, home, nil, false, "", false); len(warnings) != 0 {
		t.Errorf("a second run warned: %v", warnings)
	}
	names, removed, err := removeMCPEntries("cartographer", cfg.KnownKBs, []string{"fork-ext"}, home, nil, false, "", false)
	left, _ := os.ReadFile(tpl)
	if err != nil || !removed["fork-ext"] || len(names["fork-ext"]) == 0 || strings.Contains(string(left), "cartographer") || !strings.Contains(string(left), "other") {
		t.Fatalf("remove = %v %v %v\n%s", names, removed, err, left)
	}
}

// Without `cartographer` on PATH nothing is seeded: a seed never widens to HTTP.
func TestSeedVariantWithoutTheBridgeWritesNothing(t *testing.T) {
	registerSeedVariant(t)
	old := bridgeCommand
	bridgeCommand = func() (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { bridgeCommand = old })
	home := t.TempDir()
	tpl := filepath.Join(home, ".fork", "ext-1", "template.json")
	os.MkdirAll(filepath.Dir(tpl), 0o755)
	os.WriteFile(tpl, []byte(`{}`), 0o644)
	_, warnings, err := applyMCPEntries(map[string][]mcpEntry{"fork-ext": {{Name: "cartographer", URL: "https://example.com/mcp"}}}, []string{"fork-ext"}, home, nil, false, "", false)
	got, _ := os.ReadFile(tpl)
	if err != nil || string(got) != `{}` || len(warnings) != 1 {
		t.Fatalf("%v %v %s", warnings, err, got)
	}
}

// The cli skill is requested from the server only while a cli variant is registered.
func TestSyncPullArgsAskForTheCLISkillOnlyWithACLIVariant(t *testing.T) {
	if len(syncPullArgs()) != 0 {
		t.Fatalf("args without a variant: %v", syncPullArgs())
	}
	registerCLIVariant(t)
	if syncPullArgs()["include_cli_skill"] != true {
		t.Fatalf("args with a cli variant: %v", syncPullArgs())
	}
}
