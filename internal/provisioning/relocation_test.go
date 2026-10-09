package provisioning_test

// D360: an artifact whose lockfile paths are outside the provider's current
// destination is rewritten at the new one and its old files are pruned.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func TestApply_RelocatesArtifactWhoseDestinationMoved(t *testing.T) {
	baseDir := t.TempDir()
	bundleFS := makeBundleFS("Relocation body.")
	m, err := provisioning.BuildManifest(bundleFS, nil, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	opts := provisioning.ApplyOptions{BundleFS: bundleFS, Provider: configurator.ProviderOpenCode, BaseDir: baseDir}
	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	// Rebuild the state a pre-D360 sync left: files under .opencode/skills,
	// lock paths pointing there, and a hook directory next to them.
	oldRoot := filepath.Join(baseDir, ".opencode")
	if err := os.MkdirAll(oldRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(baseDir, ".config", "opencode", "skills"), filepath.Join(oldRoot, "skills")); err != nil {
		t.Fatal(err)
	}
	hookFile := filepath.Join(oldRoot, "hooks", "keep", "run.sh")
	if err := os.MkdirAll(filepath.Dir(hookFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookFile, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	lock := res.NewLock
	for i := range lock.Managed {
		lock.Managed[i].Path = strings.Replace(lock.Managed[i].Path, ".config/opencode/skills", ".opencode/skills", 1)
	}

	// Dry run reports the move and touches nothing.
	dry := opts
	dry.Lock, dry.DryRun = lock, true
	dr, err := provisioning.Apply(m, dry)
	if err != nil {
		t.Fatalf("dry Apply: %v", err)
	}
	if len(dr.Relocated) == 0 || len(dr.Pruned) == 0 {
		t.Errorf("dry run: Relocated=%v Pruned=%v, want both reported", dr.Relocated, dr.Pruned)
	}
	if _, err := os.Stat(filepath.Join(oldRoot, "skills", "kb-create", "SKILL.md")); err != nil {
		t.Errorf("dry run removed the old file: %v", err)
	}

	opts.Lock = lock
	res2, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("relocating Apply: %v", err)
	}
	if len(res2.Relocated) == 0 {
		t.Error("Relocated empty after the destination moved")
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".config", "opencode", "skills", "kb-create", "SKILL.md")); err != nil {
		t.Errorf("new destination not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(oldRoot, "skills")); !os.IsNotExist(err) {
		t.Errorf("old skills directory survived: %v", err)
	}
	if _, err := os.Stat(hookFile); err != nil {
		t.Errorf(".opencode/hooks must survive: %v", err)
	}
	for _, mf := range res2.NewLock.Managed {
		if strings.HasPrefix(mf.Path, ".opencode/skills") {
			t.Errorf("lock still records the old path %s", mf.Path)
		}
	}

	// Second Apply is a no-op.
	opts.Lock = res2.NewLock
	res3, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("third Apply: %v", err)
	}
	if len(res3.Relocated)+len(res3.Written)+len(res3.Pruned) != 0 {
		t.Errorf("second Apply not a no-op: relocated=%v written=%v pruned=%v", res3.Relocated, res3.Written, res3.Pruned)
	}
}

// #654: a workspace Claude Code MCP entry written to <ws>/.claude.json before
// D363 (the writer ignored the scope) is re-registered in the project cell,
// .mcp.json, and removed from the old file on the next Apply; a third Apply is
// a no-op.
func TestApply_RelocatesWorkspaceMCPEntryToProjectCell(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "tools", `{"type":"http","url":"https://tools.example.com/mcp"}`)
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, mcpAllow("tools", "https://tools.example.com/mcp"))
	if err != nil {
		t.Fatal(err)
	}
	a := findMCPArtifact(t, m, "tools")
	ws := t.TempDir()
	opts := provisioning.ApplyOptions{
		KBRoots: map[string]string{"kb": kbRoot}, Provider: configurator.ProviderClaudeCode,
		BaseDir: ws, Scope: provisioning.ScopeProject,
		ApprovedMCP: map[string]string{"kb:kb\x00tools": a.ContentHash},
	}
	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}

	// Rebuild the pre-D363 state: the entry in <ws>/.claude.json, next to an
	// entry of the user's, and the lock recording that file.
	old := filepath.Join(ws, ".claude.json")
	if err := os.WriteFile(old, []byte(`{"mcpServers":{"tools":{"type":"http","url":"https://tools.example.com/mcp"},"mine":{"type":"http","url":"https://example.com/mcp"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(ws, ".mcp.json")); err != nil {
		t.Fatal(err)
	}
	lock := res.NewLock
	for i := range lock.Managed {
		if lock.Managed[i].Kind == "mcp" {
			lock.Managed[i].Path = ".claude.json"
		}
	}

	opts.Lock = lock
	res, err = provisioning.Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Relocated) == 0 {
		t.Errorf("Relocated is empty, want the mcp entry reported as moved")
	}
	if servers, _ := readJSONFile(t, filepath.Join(ws, ".mcp.json"))["mcpServers"].(map[string]any); servers["tools"] == nil {
		t.Errorf(".mcp.json has no tools entry: %v", servers)
	}
	servers, _ := readJSONFile(t, old)["mcpServers"].(map[string]any)
	if servers["tools"] != nil || servers["mine"] == nil {
		t.Errorf(".claude.json = %v, want only the user's entry left", servers)
	}
	for _, mf := range res.NewLock.Managed {
		if mf.Kind == "mcp" && filepath.ToSlash(mf.Path) != ".mcp.json" {
			t.Errorf("lock still records %s", mf.Path)
		}
	}

	opts.Lock = res.NewLock
	res, err = provisioning.Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Relocated) != 0 || len(res.Pruned) != 0 {
		t.Errorf("third Apply moved again: Relocated=%v Pruned=%v", res.Relocated, res.Pruned)
	}
}
