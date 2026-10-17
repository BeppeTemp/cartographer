package provisioning_test

// What a pre-D737 client wrote into a workspace (D717) is pruned, nothing new is.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func TestPruneServerEntry_RemovesOnlyTheClientOwnedEntry(t *testing.T) {
	ws := t.TempDir()
	cell := filepath.Join(ws, ".mcp.json")
	if err := os.WriteFile(cell, []byte(`{"mcpServers":{"other":{"url":"https://example.com/other"},"cartographer":{"url":"https://example.com/mcp/routed?kbs=kb-a"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := provisioning.Lock{Managed: []provisioning.ManagedFile{
		{Kind: "mcp", Name: "cartographer", Path: ".mcp.json", Source: provisioning.SourceClient},
		{Kind: "skill", Name: "s", Path: ".claude/skills/s", Source: "kb:kb-a"},
	}}
	got, err := provisioning.PruneServerEntry(ws, configurator.ProviderClaudeCode, lock, false)
	if err != nil {
		t.Fatal(err)
	}
	servers := readJSONFile(t, cell)["mcpServers"].(map[string]any)
	if servers["cartographer"] != nil || servers["other"] == nil {
		t.Fatalf("servers = %v", servers)
	}
	if len(got.Managed) != 1 || got.Managed[0].Kind != "skill" {
		t.Fatalf("lock = %+v", got.Managed)
	}
	// Idempotent: a second run has nothing to remove.
	if again, err := provisioning.PruneServerEntry(ws, configurator.ProviderClaudeCode, got, false); err != nil || len(again.Managed) != 1 {
		t.Fatalf("second run: %v %+v", err, again.Managed)
	}
}

func TestPruneServerEntry_DryRunTouchesNothing(t *testing.T) {
	ws := t.TempDir()
	cell := filepath.Join(ws, ".mcp.json")
	body := `{"mcpServers":{"cartographer":{"url":"x"}}}`
	if err := os.WriteFile(cell, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := provisioning.Lock{Managed: []provisioning.ManagedFile{{Kind: "mcp", Name: "cartographer", Path: ".mcp.json", Source: provisioning.SourceClient}}}
	if _, err := provisioning.PruneServerEntry(ws, configurator.ProviderClaudeCode, lock, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(cell); string(b) != body {
		t.Fatalf("dry run changed the file: %s", b)
	}
}
