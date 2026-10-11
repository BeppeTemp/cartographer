package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// D717: the workspace's own Cartographer entry.

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

func syncEntry(t *testing.T, cfg *clientconfig.Config, provider, target string) []syncProjection {
	t.Helper()
	projs, err := projectionsFor(cfg, provider, target)
	if err != nil {
		t.Fatal(err)
	}
	manifests := map[string]provisioning.Manifest{}
	for _, p := range projs {
		manifests[projectionKey(p)] = provisioning.Manifest{}
	}
	if _, err := materializeForProviders(manifests, projs, target, "", true, false, false, portabilityOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	return projs
}

func claudeServers(t *testing.T, ws string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(ws, ".mcp.json"))
	if err != nil {
		return nil
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	s, _ := doc["mcpServers"].(map[string]any)
	return s
}

func TestWorkspaceEntryRestrictedToWorkspaceKBs(t *testing.T) {
	cfg, ws := entryTestConfig(t, "claude", []string{"kb-a"})
	target := t.TempDir()
	t.Setenv("HOME", target)
	syncEntry(t, cfg, "claude", target)
	got, _ := claudeServers(t, ws)["cartographer"].(map[string]any)
	if got == nil || got["url"] != "https://example.com/mcp/routed?kbs=kb-a" {
		t.Fatalf("workspace entry = %v", got)
	}
	// Idempotent, and a rebind to two KBs rewrites the URL.
	syncEntry(t, cfg, "claude", target)
	if err := cfg.BindWorkspace("claude", ws, []string{"kb-a", "kb-b"}); err != nil {
		t.Fatal(err)
	}
	syncEntry(t, cfg, "claude", target)
	got, _ = claudeServers(t, ws)["cartographer"].(map[string]any)
	if got["url"] != "https://example.com/mcp/routed?kbs=kb-a,kb-b" {
		t.Fatalf("after rebind url = %v", got["url"])
	}
}

// An entry without kbs= would widen to every KB: no entry, a note, and a
// previously written one is removed.
func TestWorkspaceEntryNeverWidens(t *testing.T) {
	cfg, ws := entryTestConfig(t, "claude", []string{"kb-a"})
	target := t.TempDir()
	t.Setenv("HOME", target)
	syncEntry(t, cfg, "claude", target)
	if err := os.WriteFile(filepath.Join(ws, ".mcp.json"), []byte(`{"mcpServers":{"cartographer":{"url":"x"},"other":{"url":"y"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(){
		"no-kb":     func() { _ = cfg.BindWorkspace("claude", ws, nil) },
		"unmounted": func() { _ = cfg.BindWorkspace("claude", ws, []string{"gone"}) },
		"pre-D288":  func() { _ = cfg.BindWorkspace("claude", ws, []string{"kb-a"}); cfg.ServerRoutedPath = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			mutate()
			projs := syncEntry(t, cfg, "claude", target)
			if projs[1].ServerEntry != nil || projs[1].EntryNote == "" {
				t.Fatalf("entry=%v note=%q", projs[1].ServerEntry, projs[1].EntryNote)
			}
			s := claudeServers(t, ws)
			if _, has := s["cartographer"]; has || s["other"] == nil {
				t.Fatalf("servers = %v", s)
			}
		})
	}
}

func TestWorkspaceEntryUnbindRemovesOnlyItself(t *testing.T) {
	cfg, ws := entryTestConfig(t, "claude", []string{"kb-a"})
	target := t.TempDir()
	t.Setenv("HOME", target)
	if err := os.WriteFile(filepath.Join(ws, ".mcp.json"), []byte(`{"mcpServers":{"other":{"url":"y"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	syncEntry(t, cfg, "claude", target)
	if claudeServers(t, ws)["cartographer"] == nil {
		t.Fatal("entry not written")
	}
	cfg.Workspaces["claude"] = nil
	syncEntry(t, cfg, "claude", target)
	s := claudeServers(t, ws)
	if s["cartographer"] != nil || s["other"] == nil {
		t.Fatalf("after unbind servers = %v", s)
	}
}

// Only a client whose docs state entry-level replacement gets an entry (WP0).
func TestProjectMCPShadowsGlobalPerProvider(t *testing.T) {
	want := map[string]bool{"claude": true}
	for _, d := range configurator.Providers() {
		if got := configurator.ProjectMCPShadowsGlobal(d.Provider); got != want[string(d.Provider)] {
			t.Errorf("%s: shadows = %v, want %v", d.Provider, got, want[string(d.Provider)])
		}
	}
	cfg, _ := entryTestConfig(t, "codex", []string{"kb-a"})
	projs, err := projectionsFor(cfg, "codex", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if projs[1].ServerEntry != nil || !strings.Contains(projs[1].EntryNote, "does not let a project server replace") {
		t.Fatalf("codex entry=%v note=%q", projs[1].ServerEntry, projs[1].EntryNote)
	}
}
