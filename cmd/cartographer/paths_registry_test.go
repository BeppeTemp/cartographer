package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// registryServer is placeholderServer whose KB, alpha, also serves a
// paths.yaml declaring claude-home with a ~-default (D263).
func registryServer(t *testing.T) *httptest.Server {
	t.Helper()
	pull := `{"revision":"test","artifacts":[],"placeholders":["path:claude-home"],` +
		`"path_registry":{"paths":{"claude-home":{"description":"Claude Code's directory","default":"~/.claude"}}}}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok","kbs":[{"name":"alpha"}]}`))
			return
		}
		var req struct {
			ID int `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
			"content": []map[string]string{{"type": "text", "text": pull}},
		}})
	}))
}

// The acceptance of D263 WP2: a fresh client resolves a declared default with
// no `paths:` entry, and never writes one.
func TestDoConnect_DeclaredDefaultResolvesWithoutPaths(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := registryServer(t)
	defer srv.Close()
	dir := t.TempDir()

	asked := false
	old := runPathsForm
	runPathsForm = func(rows []placeholderRow) (map[string]string, error) {
		asked = true
		return nil, nil
	}
	defer func() { runPathsForm = old }()

	res, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", Trust: true, KBs: []string{"alpha"}, PromptPaths: true})
	if err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	lock := res.Applied["claude"].NewLock
	if got := lock.ResolvedPlaceholders["path:claude-home"]; got != filepath.Join(home, ".claude") {
		t.Fatalf("resolved = %q (unresolved %v)", got, lock.UnresolvedPlaceholders)
	}
	if asked {
		t.Error("a key resolved by its default must not be asked for")
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Paths) != 0 {
		t.Errorf("a default must never be written into paths: %v", cfg.Paths)
	}
}

// A declared default that does not exist here is unresolved: the connect
// step shows the description and pre-fills the default.
func TestDoConnect_MissingDefaultIsOfferedPrefilled(t *testing.T) {
	setHome(t, t.TempDir())
	srv := registryServer(t)
	defer srv.Close()

	var asked []placeholderRow
	old := runPathsForm
	runPathsForm = func(rows []placeholderRow) (map[string]string, error) {
		asked = rows
		return nil, nil
	}
	defer func() { runPathsForm = old }()

	if _, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: t.TempDir(), ServerURL: srv.URL + "/mcp", Name: "cartographer", Trust: true, KBs: []string{"alpha"}, PromptPaths: true}); err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	if len(asked) != 1 || asked[0].Description != "Claude Code's directory" || asked[0].Default != "~/.claude" {
		t.Fatalf("rows = %+v", asked)
	}
	m := newPathsFormModel(asked)
	if got := m.inputs[0].Value(); got != "~/.claude" {
		t.Errorf("input pre-filled with %q, want the declared default", got)
	}
	if view := m.View(); !strings.Contains(view, "Claude Code's directory") {
		t.Errorf("the form must show the description:\n%s", view)
	}
}

func TestCmdPathsList_ShowsDescriptionAndDefault(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	lf := provisioning.LockFile{Providers: map[string]provisioning.Lock{
		"claude": {
			Provider:               "claude",
			UnresolvedPlaceholders: map[string]string{"path:claude-home": "missing"},
			PlaceholderSources:     map[string][]string{"path:claude-home": {"kb-a"}},
			PlaceholderDecls:       map[string]provisioning.PathDecl{"path:claude-home": {Description: "Claude Code's directory", Default: "~/.claude"}},
		},
	}}
	if err := provisioning.WriteLockFile(lockFilePath(home), lf); err != nil {
		t.Fatal(err)
	}
	out := withStdout(t, func() { cmdPaths([]string{"list"}) })
	if !strings.Contains(out, "Claude Code's directory (default ~/.claude)") {
		t.Errorf("list must show the declaration:\n%s", out)
	}
	out = withStdout(t, func() { cmdPaths([]string{"list", "--json"}) })
	if !strings.Contains(out, `"description": "Claude Code's directory"`) || !strings.Contains(out, `"default": "~/.claude"`) {
		t.Errorf("json must carry the declaration:\n%s", out)
	}
}

// Registries follow the binding exactly like the keys do.
func TestPathRegistriesForProjection(t *testing.T) {
	a := provisioning.PathRegistry{Paths: map[string]provisioning.PathDecl{"x": {Description: "a"}}}
	w := provisioning.PathRegistry{Paths: map[string]provisioning.PathDecl{"y": {Description: "w"}}}
	cs := candidateSet{PathRegistries: map[string]provisioning.PathRegistry{"kb-a": a, "work-kb": w}}
	cfg := &clientconfig.Config{KnownKBs: []string{"kb-a", "work-kb"}, Clients: map[string]clientconfig.ClientBinding{"codex": {KBs: []string{"kb-a"}}}}

	if got := cs.pathRegistriesForProjection(cfg, syncProjection{Provider: "claude"}); !reflect.DeepEqual(got, map[string]provisioning.PathRegistry{"kb-a": a, "work-kb": w}) {
		t.Errorf("default binding = %v", got)
	}
	if got := cs.pathRegistriesForProjection(cfg, syncProjection{Provider: "codex"}); !reflect.DeepEqual(got, map[string]provisioning.PathRegistry{"kb-a": a}) {
		t.Errorf("explicit binding = %v", got)
	}
	if got := cs.pathRegistriesForProjection(cfg, syncProjection{Provider: "claude", Workspace: "/w", KBs: []string{"work-kb"}}); !reflect.DeepEqual(got, map[string]provisioning.PathRegistry{"work-kb": w}) {
		t.Errorf("workspace = %v", got)
	}
	if got := cs.pathRegistriesForProjection(cfg, syncProjection{Provider: "claude", BundleOnly: true}); got != nil {
		t.Errorf("bundle-only = %v", got)
	}
}

func TestRegistryWarningsAcross_OncePerSync(t *testing.T) {
	results := map[string]provisioning.AppliedResult{
		"claude": {RegistryWarnings: []string{"conflict on path:k"}},
		"codex":  {RegistryWarnings: []string{"conflict on path:k"}},
	}
	if got := registryWarningsAcross(results); !reflect.DeepEqual(got, []string{"conflict on path:k"}) {
		t.Errorf("got %v", got)
	}
}
