package provisioning_test

// Cartographer's own workspace entry (D717).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

var testEntry = &provisioning.ServerEntry{Name: "cartographer", URL: "https://example.com/mcp/routed?kbs=kb-a", Auth: true, TokenEnv: "CARTOGRAPHER_TOKEN"}

func TestEnsureServerEntry_ClaudeLifecycle(t *testing.T) {
	ws := t.TempDir()
	cell := filepath.Join(ws, ".mcp.json")
	if err := os.WriteFile(cell, []byte(`{"mcpServers":{"other":{"url":"https://example.com/other"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := configurator.ProviderClaudeCode
	lock, _, err := provisioning.EnsureServerEntry(ws, p, provisioning.Lock{}, testEntry, false)
	if err != nil {
		t.Fatal(err)
	}
	servers := readJSONFile(t, cell)["mcpServers"].(map[string]any)
	if servers["other"] == nil || servers["cartographer"] == nil {
		t.Fatalf("servers = %v", servers)
	}
	if len(lock.Managed) != 1 || lock.Managed[0].Source != provisioning.SourceClient || lock.Managed[0].Path != ".mcp.json" {
		t.Fatalf("lock = %+v", lock.Managed)
	}
	again, _, err := provisioning.EnsureServerEntry(ws, p, lock, testEntry, false)
	if err != nil || len(again.Managed) != 1 || again.Managed[0].ContentHash != lock.Managed[0].ContentHash {
		t.Fatalf("not idempotent: %v %+v", err, again.Managed)
	}

	// The server manifest never lists it as removed, and Apply carries it.
	if d := provisioning.ComputeDiff(provisioning.Manifest{}, lock); len(d.Removed) != 0 {
		t.Fatalf("diff removes the client-owned entry: %+v", d.Removed)
	}
	res, err := provisioning.Apply(provisioning.Manifest{}, provisioning.ApplyOptions{Provider: p, BaseDir: ws, Scope: provisioning.ScopeProject, Lock: lock})
	if err != nil || len(res.NewLock.Managed) != 1 {
		t.Fatalf("apply dropped it: %v %+v", err, res.NewLock.Managed)
	}

	// Prune (unbind) strips only it.
	if _, err := provisioning.PruneManaged(lock.Managed, ws, false); err != nil {
		t.Fatal(err)
	}
	servers = readJSONFile(t, cell)["mcpServers"].(map[string]any)
	if servers["cartographer"] != nil || servers["other"] == nil {
		t.Fatalf("after prune = %v", servers)
	}

	// nil entry removes a previous one.
	lock, _, _ = provisioning.EnsureServerEntry(ws, p, provisioning.Lock{}, testEntry, false)
	lock, _, err = provisioning.EnsureServerEntry(ws, p, lock, nil, false)
	if err != nil || len(lock.Managed) != 0 {
		t.Fatalf("nil entry: %v %+v", err, lock.Managed)
	}
	if readJSONFile(t, cell)["mcpServers"].(map[string]any)["cartographer"] != nil {
		t.Fatal("entry still there")
	}
}

func TestEnsureServerEntry_NonShadowingAndCelllessAreNoOps(t *testing.T) {
	for _, p := range []configurator.Provider{configurator.ProviderCodex, configurator.ProviderKiro, configurator.ProviderOpenCode, configurator.ProviderHermes, configurator.ProviderCopilot} {
		ws := t.TempDir()
		lock, _, err := provisioning.EnsureServerEntry(ws, p, provisioning.Lock{}, testEntry, false)
		if err != nil || len(lock.Managed) != 0 {
			t.Errorf("%s: %v %+v", p, err, lock.Managed)
		}
		if entries, _ := os.ReadDir(ws); len(entries) != 0 {
			t.Errorf("%s wrote %d files", p, len(entries))
		}
	}
}

func TestApply_ReservedMCPNameIgnored(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "cartographer", `{"type":"http","url":"https://example.com/mcp"}`)
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, mcpAllow("cartographer", "https://example.com/mcp"))
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	mcp := findMCPArtifact(t, m, "cartographer")
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		AutoTrust: true, KBRoots: map[string]string{"kb": kbRoot}, ReservedMCPName: "cartographer",
		ApprovedMCP: map[string]string{mcp.Source + "\x00cartographer": mcp.ContentHash},
		Provider:    configurator.ProviderClaudeCode, BaseDir: ws, Scope: provisioning.ScopeProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".mcp.json")); err == nil {
		t.Error("reserved KB artifact was registered")
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "name reserved by Cartographer's own entry") {
		t.Errorf("warnings = %v", res.Warnings)
	}
}
