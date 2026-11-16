package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A sync that changes nothing leaves ~/.codex/config.toml alone (#2134): the
// remove-then-write pair of a client whose KB list is longer than its entries
// stripped the shared block and put the same bytes back, a new inode and mtime
// at every run.
func TestCodexConfigUntouchedByANoOpMCPSync(t *testing.T) {
	dir := t.TempDir()
	entries := map[string][]mcpEntry{"codex": {{Name: "cartographer", URL: "https://mcp.example.test/mcp"}}}
	kbs := []string{"kb-a"}
	path := filepath.Join(dir, ".codex", "config.toml")
	run := func() {
		t.Helper()
		if _, _, err := removeMCPEntries("cartographer", kbs, []string{"codex"}, dir, nil, false, "", false, entries); err != nil {
			t.Fatal(err)
		}
		if _, _, err := applyMCPEntries(entries, []string{"codex"}, dir, nil, false, "", false); err != nil {
			t.Fatal(err)
		}
	}
	run()
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	run()
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || !after.ModTime().Equal(old) {
		t.Error("config.toml was rewritten by a sync that changed nothing")
	}
}

// A stale entry of the shared block is still reported as removed and dropped
// by the write that follows.
func TestCodexStaleEntryStillDroppedWithoutARemoveFirst(t *testing.T) {
	dir := t.TempDir()
	both := map[string][]mcpEntry{"codex": {
		{Name: "cartographer", URL: "https://mcp.example.test/mcp"},
		{Name: "cartographer-kb-a", URL: "https://mcp.example.test/mcp?kbs=kb-a"},
	}}
	if _, _, err := applyMCPEntries(both, []string{"codex"}, dir, nil, false, "", false); err != nil {
		t.Fatal(err)
	}
	one := map[string][]mcpEntry{"codex": both["codex"][:1]}
	names, _, err := removeMCPEntries("cartographer", []string{"kb-a"}, []string{"codex"}, dir, nil, false, "", false, one)
	if err != nil {
		t.Fatal(err)
	}
	if got := names["codex"]; len(got) != 1 || got[0] != "cartographer-kb-a" {
		t.Errorf("removed names = %v, want [cartographer-kb-a]", got)
	}
	if _, _, err := applyMCPEntries(one, []string{"codex"}, dir, nil, false, "", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".codex", "config.toml"))
	if s := string(data); !strings.Contains(s, "mcp_servers.cartographer]") || strings.Contains(s, "cartographer-kb-a") {
		t.Errorf("config.toml = %q", s)
	}
}
