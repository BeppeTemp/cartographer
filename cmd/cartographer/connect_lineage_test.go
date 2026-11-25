package main

import (
	"slices"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// AC-lineage-25
func TestConnectKBOptOutFlags(t *testing.T) {
	setHome(t, t.TempDir())
	dir := t.TempDir()
	seedAgentState(t, dir)
	base := connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: "http://127.0.0.1:1/mcp", Name: "cartographer", KBs: []string{"client-kb"}}

	with := base
	with.Exclude = []string{"core-kb"}
	if _, err := doConnect(with); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b := cfg.Clients["claude"]; !slices.Equal(b.Exclude, []string{"core-kb"}) || b.NoLineage {
		t.Fatalf("exclude not stored: %+v", b)
	}

	// The same connect without the flag replaces the binding whole.
	if _, err := doConnect(base); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = clientconfig.Load(dir); len(cfg.Clients["claude"].Exclude) != 0 {
		t.Fatalf("a connect without the flag keeps the exclusion: %+v", cfg.Clients["claude"])
	}

	for _, args := range [][]string{
		{"claude", "--exclude", "core-kb"},
		{"claude", "--no-lineage"},
		{"claude", "--kb", "all", "--no-lineage"},
		{"claude", "--kb", "client-kb", "--no-lineage", "--exclude", "core-kb"},
		{"claude", "--kb", "client-kb", "--exclude", "client-kb"},
	} {
		var code int
		withStderr(t, func() { code = cmdConnect(args) })
		if code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

// connect --workspace puts the lineage options on the workspace binding, not
// on the provider's.
func TestConnectWorkspaceCarriesLineageOptions(t *testing.T) {
	setHome(t, t.TempDir())
	dir := t.TempDir()
	seedAgentState(t, dir)
	ws := t.TempDir()
	opts := connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: "http://127.0.0.1:1/mcp", Name: "cartographer", KBs: []string{"client-kb"}, Workspace: ws, Exclude: []string{"core-kb"}}
	if _, err := doConnect(opts); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b := cfg.Clients["claude"]; len(b.Exclude) != 0 || b.NoLineage {
		t.Errorf("the provider binding got the options: %+v", b)
	}
	canonical, _ := clientconfig.CanonicalWorkspacePath(ws)
	found := false
	for _, w := range cfg.WorkspaceBindings("claude") {
		if w.Path == canonical {
			found = true
			if !slices.Equal(w.Exclude, []string{"core-kb"}) {
				t.Errorf("workspace binding = %+v", w)
			}
		}
	}
	if !found {
		t.Fatal("no workspace binding")
	}
}
