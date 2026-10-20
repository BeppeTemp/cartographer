package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// D737: a workspace projection writes no MCP entry, and removes the one a
// pre-D737 client (D717) left in the repository.

func entryTestConfig(t *testing.T, provider string, kbs []string) (*clientconfig.Config, string) {
	t.Helper()
	ws := t.TempDir()
	cfg := &clientconfig.Config{
		ServerName: "cartographer", ServerURL: "https://example.com/mcp",
		ServerRoutedPath: "/mcp/routed", KnownKBs: []string{"kb-a", "kb-b"},
	}
	if err := cfg.SetWorkspaceScope(provider, clientconfig.ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := cfg.BindWorkspace(provider, ws, kbs); err != nil {
		t.Fatal(err)
	}
	return cfg, cfg.WorkspaceBindings(provider)[0].Path
}

func TestWorkspaceSyncPrunesD717Entry(t *testing.T) {
	cfg, ws := entryTestConfig(t, "claude", []string{"kb-a"})
	target := t.TempDir()
	t.Setenv("HOME", target)
	cell := filepath.Join(ws, ".mcp.json")
	if err := os.WriteFile(cell, []byte(`{"mcpServers":{"cartographer":{"url":"https://example.com/mcp/routed?kbs=kb-a"},"other":{"url":"y"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	projs, err := projectionsFor(cfg, "claude", target)
	if err != nil {
		t.Fatal(err)
	}
	manifests := map[string]provisioning.Manifest{}
	for _, p := range projs {
		manifests[projectionKey(p)] = provisioning.Manifest{}
	}
	// The previous run recorded the entry as client-owned in the lock.
	lf := provisioning.LockFile{}
	lf.SetProjection(projs[1].Key(), provisioning.Lock{Managed: []provisioning.ManagedFile{
		{Kind: "mcp", Name: "cartographer", Path: ".mcp.json", Source: provisioning.SourceClient},
	}}, projs[1].Remote)
	if err := provisioning.WriteLockFile(lockFilePath(target), lf); err != nil {
		t.Fatal(err)
	}
	if _, err := materializeForProviders(manifests, projs, target, "", false, false, portabilityOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cell)
	if got := string(b); strings.Contains(got, `"cartographer"`) || !strings.Contains(got, `"other"`) {
		t.Fatalf("after sync: %s", got)
	}
}
