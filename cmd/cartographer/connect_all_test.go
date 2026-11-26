package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

const allHealth = `{"status":"ok","kbs":[{"name":"alpha"},{"name":"beta"}]}`

// treeOf lists the files under dir, relative, with the config and state files of
// the client left out: what connect delivered to the agent, not what it recorded.
func treeOf(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, p := range dirEntries(t, dir) {
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(filepath.Base(rel), ".cartographer") {
			continue
		}
		out = append(out, filepath.ToSlash(rel))
	}
	slices.Sort(out)
	return out
}

// AC-cad-4: `connect --kb all` records `kbs: all` (not the names) and writes the
// same artifacts and MCP entries as the list of the same names, and a KB mounted
// afterwards is not written into the file.
func TestConnectKBAllWritesMarker(t *testing.T) {
	srv := multiKBServer(t, allHealth)
	defer srv.Close()
	run := func(kbs []string) (string, connectResult) {
		dir := t.TempDir()
		res, err := doConnect(connectOptions{Providers: []string{"claude", "codex"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", Token: "${TOKEN}", KBs: kbs})
		if err != nil {
			t.Fatalf("doConnect %v: %v", kbs, err)
		}
		return dir, res
	}
	dir, res := run([]string{"all"})
	listDir, listRes := run([]string{"alpha", "beta"})

	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"claude", "codex"} {
		b := cfg.Clients[p]
		if !b.FollowsServer || b.Explicit() || len(b.KBs) != 0 {
			t.Errorf("%s binding = %+v, want the all marker and no names", p, b)
		}
	}
	raw, err := os.ReadFile(clientconfig.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "kbs: all") || strings.Contains(string(raw), "alpha") {
		t.Errorf("the file does not hold the marker alone:\n%s", raw)
	}
	if strings.Join(res.MCPEntries, ",") != strings.Join(listRes.MCPEntries, ",") {
		t.Errorf("MCP entries %v, the list gave %v", res.MCPEntries, listRes.MCPEntries)
	}
	if got, want := treeOf(t, dir), treeOf(t, listDir); !slices.Equal(got, want) {
		t.Errorf("artifacts differ from the list's:\n%v\n%v", got, want)
	}
	got, explicit := cfg.BoundKBs("claude")
	if explicit {
		t.Errorf("BoundKBs explicit = true for the marker")
	}
	cfg.KnownKBs = []string{"alpha", "beta", "gamma"}
	if got, _ = cfg.BoundKBs("claude"); strings.Join(got, ",") != "alpha,beta,gamma" {
		t.Errorf("a KB mounted later is not followed: %v", got)
	}
}

// AC-cad-5: `connect --kb all` over an explicit list replaces it, lineage keys
// included; a re-run without --kb leaves a list untouched (D190).
func TestConnectKBAllReplacesList(t *testing.T) {
	srv := multiKBServer(t, allHealth)
	defer srv.Close()
	dir := t.TempDir()
	base := connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", Token: "${TOKEN}"}

	list := base
	list.KBs, list.Exclude = []string{"alpha"}, []string{"beta"}
	if _, err := doConnect(list); err != nil {
		t.Fatal(err)
	}
	if _, err := doConnect(base); err != nil { // no --kb: the list stands
		t.Fatal(err)
	}
	cfg, _ := clientconfig.Load(dir)
	if b := cfg.Clients["claude"]; b.FollowsServer || strings.Join(b.KBs, ",") != "alpha" || !slices.Equal(b.Exclude, []string{"beta"}) {
		t.Fatalf("a connect without --kb changed the list: %+v", b)
	}

	all := base
	all.KBs = []string{"all"}
	if _, err := doConnect(all); err != nil {
		t.Fatal(err)
	}
	cfg, _ = clientconfig.Load(dir)
	if b := cfg.Clients["claude"]; !b.FollowsServer || len(b.KBs) != 0 || b.KBsSet || b.NoLineage || len(b.Exclude) != 0 {
		t.Errorf("the marker did not replace the list: %+v", b)
	}
	// A re-run without --kb neither refuses nor re-resolves the marker.
	if _, err := doConnect(base); err != nil {
		t.Fatalf("a re-run over the marker: %v", err)
	}
	if cfg, _ = clientconfig.Load(dir); !cfg.FollowsServer("claude") {
		t.Errorf("the re-run dropped the marker")
	}
}

// AC-cad-8: `connect --kb all --workspace <repo>` binds the folder to the names
// mounted now, says so, and leaves the provider's global binding as it was.
func TestConnectKBAllWorkspaceSnapshot(t *testing.T) {
	srv := multiKBServer(t, allHealth)
	defer srv.Close()
	setHome(t, t.TempDir())
	dir := t.TempDir()
	ws := t.TempDir()
	opts := connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", Token: "${TOKEN}", KBs: []string{"alpha"}}
	if _, err := doConnect(opts); err != nil {
		t.Fatal(err)
	}
	opts.KBs, opts.Workspace = []string{"all"}, ws
	var out strings.Builder
	opts.Out = &out
	if _, err := doConnect(opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "note: --kb all in a workspace binds the KBs mounted now; the folder does not follow later changes") {
		t.Errorf("no snapshot note:\n%s", out.String())
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b := cfg.Clients["claude"]; b.FollowsServer || strings.Join(b.KBs, ",") != "alpha" {
		t.Errorf("the global binding changed: %+v", b)
	}
	canonical, _ := clientconfig.CanonicalWorkspacePath(ws)
	found := false
	for _, w := range cfg.WorkspaceBindings("claude") {
		if w.Path == canonical {
			found = true
			if strings.Join(w.KBs, ",") != "alpha,beta" {
				t.Errorf("workspace binding = %+v, want the names mounted now", w)
			}
		}
	}
	if !found {
		t.Fatal("no workspace binding")
	}
}

// AC-cad-9: `connect --kb all` with --exclude or --no-lineage is a usage error
// (nothing to follow), and a marker provider holds no lineage entry of its own.
func TestConnectKBAllRejectsLineageFlags(t *testing.T) {
	setHome(t, t.TempDir())
	for _, args := range [][]string{
		{"claude", "--kb", "all", "--exclude", "core-kb"},
		{"claude", "--kb", "all", "--no-lineage"},
	} {
		var code int
		withStderr(t, func() { code = cmdConnect(args) })
		if code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	srv := multiKBServer(t, allHealth)
	defer srv.Close()
	dir := t.TempDir()
	if _, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", Token: "${TOKEN}", KBs: []string{"all"}, Exclude: []string{"core-kb"}}); err == nil {
		t.Error("doConnect accepted --exclude with the marker")
	}
}

// The form's extra row answers with the sentinel; ticking every KB still
// answers with the list, and the two never mix.
func TestKBSelectFormAllLaterRow(t *testing.T) {
	m := newKBSelectModel([]string{"alpha", "beta"})
	if got := m.result(); len(got) != 0 {
		t.Fatalf("nothing is pre-selected, got %v", got)
	}
	key := func(m kbSelectModel, k string) kbSelectModel {
		var msg tea.KeyMsg
		switch k {
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case " ":
			msg = tea.KeyMsg{Type: tea.KeySpace}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		next, _ := m.Update(msg)
		return next.(kbSelectModel)
	}
	m = key(m, " ") // alpha
	m = key(m, "down")
	m = key(m, "down") // the extra row
	m = key(m, " ")
	if got := m.result(); !slices.Equal(got, []string{kbSelectionAll}) || len(m.chosen()) != 0 {
		t.Errorf("the extra row gave %v (names %v)", got, m.chosen())
	}
	m = key(m, "a") // every name: the list again
	if got := m.result(); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Errorf("ticking every KB gave %v", got)
	}
}
