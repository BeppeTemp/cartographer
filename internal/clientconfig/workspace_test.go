package clientconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withFakeRemote(t *testing.T, remotes map[string]string) {
	t.Helper()
	prev := gitRemoteFn
	gitRemoteFn = func(dir string) string { return remotes[dir] }
	t.Cleanup(func() { gitRemoteFn = prev })
}

// TestWorkspaceScope_DefaultsToProvider is D193's migration promise: a
// configuration written before workspaces existed keeps the global catalogue it
// has. A new scope that switched under an existing installation would move every
// artifact on the next sync.
func TestWorkspaceScope_DefaultsToProvider(t *testing.T) {
	cfg := &Config{}
	if got := cfg.WorkspaceScope("claude"); got != ScopeProvider {
		t.Errorf("default scope = %q, want %q", got, ScopeProvider)
	}
	if err := cfg.SetWorkspaceScope("claude", ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	if got := cfg.WorkspaceScope("claude"); got != ScopeWorkspace {
		t.Errorf("scope = %q, want %q", got, ScopeWorkspace)
	}
	// Other providers are unaffected: the scope is per provider.
	if got := cfg.WorkspaceScope("codex"); got != ScopeProvider {
		t.Errorf("codex scope = %q, want %q", got, ScopeProvider)
	}
	// Setting it back removes the key rather than restating the default.
	if err := cfg.SetWorkspaceScope("claude", ScopeProvider); err != nil {
		t.Fatal(err)
	}
	if _, present := cfg.Scopes["claude"]; present {
		t.Error("setting the default scope left a key behind")
	}
	if err := cfg.SetWorkspaceScope("claude", "sideways"); err == nil {
		t.Error("an unknown scope was accepted")
	}
}

// TestBindWorkspace_CanonicalizesAndGuards: the path is resolved once, at bind
// time, and the remote is recorded as a guard beside it.
func TestBindWorkspace_CanonicalizesAndGuards(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "repo")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalWorkspacePath(ws)
	if err != nil {
		t.Fatal(err)
	}
	withFakeRemote(t, map[string]string{canonical: "git.example.com/team/repo"})

	cfg := &Config{}
	if err := cfg.BindWorkspace("claude", ws, []string{"homelab-kb"}); err != nil {
		t.Fatal(err)
	}
	bindings := cfg.WorkspaceBindings("claude")
	if len(bindings) != 1 {
		t.Fatalf("bindings = %+v, want 1", bindings)
	}
	if bindings[0].Path != canonical {
		t.Errorf("path = %q, want the canonical %q", bindings[0].Path, canonical)
	}
	if bindings[0].Remote != "git.example.com/team/repo" {
		t.Errorf("remote guard = %q", bindings[0].Remote)
	}

	// Re-binding the same path replaces rather than duplicating.
	if err := cfg.BindWorkspace("claude", ws, []string{"other-kb"}); err != nil {
		t.Fatal(err)
	}
	if bindings := cfg.WorkspaceBindings("claude"); len(bindings) != 1 || bindings[0].KBs[0] != "other-kb" {
		t.Errorf("rebind produced %+v", bindings)
	}

	// A path that does not exist cannot be bound: the guard would be
	// uncheckable from the moment it was written.
	if err := cfg.BindWorkspace("claude", filepath.Join(root, "nope"), nil); err == nil {
		t.Error("a non-existent directory was bound")
	}
}

// TestBindWorkspace_EmptyKBsIsADeclaration: decision 8 — zero KBs and several
// KBs are distinct explicit states, and neither is "every KB".
func TestBindWorkspace_EmptyKBsIsADeclaration(t *testing.T) {
	ws := t.TempDir()
	withFakeRemote(t, nil)
	cfg := &Config{KnownKBs: []string{"a", "b", "c"}}
	if err := cfg.BindWorkspace("claude", ws, nil); err != nil {
		t.Fatal(err)
	}
	got, ok, err := cfg.ResolveWorkspace("claude", ws)
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if len(got.KBs) != 0 {
		t.Errorf("a workspace bound to no KBs resolved to %v — an empty binding must never mean 'every KB'", got.KBs)
	}
}

// TestResolveWorkspace_LongestMatchWins: a repository bound inside another
// bound directory is its own perimeter, not the outer one's.
func TestResolveWorkspace_LongestMatchWins(t *testing.T) {
	root := t.TempDir()
	outer := filepath.Join(root, "work")
	inner := filepath.Join(outer, "work")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	withFakeRemote(t, nil)
	cfg := &Config{}
	if err := cfg.BindWorkspace("claude", outer, []string{"general-kb"}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.BindWorkspace("claude", inner, []string{"work-kb"}); err != nil {
		t.Fatal(err)
	}

	got, ok, err := cfg.ResolveWorkspace("claude", inner)
	if err != nil || !ok {
		t.Fatalf("resolve inner: ok=%v err=%v", ok, err)
	}
	if len(got.KBs) != 1 || got.KBs[0] != "work-kb" {
		t.Errorf("inner workspace resolved to %v, want the inner binding", got.KBs)
	}

	got, ok, err = cfg.ResolveWorkspace("claude", outer)
	if err != nil || !ok {
		t.Fatalf("resolve outer: ok=%v err=%v", ok, err)
	}
	if got.KBs[0] != "general-kb" {
		t.Errorf("outer workspace resolved to %v", got.KBs)
	}
}

// TestResolveWorkspace_UnboundIsNotAnError: an unbound directory is a legal,
// fail-closed state — it receives only the transversal bundle.
func TestResolveWorkspace_UnboundIsNotAnError(t *testing.T) {
	root := t.TempDir()
	bound := filepath.Join(root, "bound")
	elsewhere := filepath.Join(root, "elsewhere")
	for _, d := range []string{bound, elsewhere} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	withFakeRemote(t, nil)
	cfg := &Config{}
	if err := cfg.BindWorkspace("claude", bound, []string{"kb"}); err != nil {
		t.Fatal(err)
	}
	_, ok, err := cfg.ResolveWorkspace("claude", elsewhere)
	if err != nil {
		t.Fatalf("an unbound workspace is not an error: %v", err)
	}
	if ok {
		t.Error("an unbound directory resolved to a binding")
	}
}

// TestResolveWorkspace_RemoteGuardRefuses: the remote is a guard against
// accidental reuse of a path, and a mismatch is an error — never a fallback.
func TestResolveWorkspace_RemoteGuardRefuses(t *testing.T) {
	ws := t.TempDir()
	canonical, err := CanonicalWorkspacePath(ws)
	if err != nil {
		t.Fatal(err)
	}
	remotes := map[string]string{canonical: "git.example.com/team/work"}
	withFakeRemote(t, remotes)

	cfg := &Config{}
	if err := cfg.BindWorkspace("claude", ws, []string{"work-kb"}); err != nil {
		t.Fatal(err)
	}
	// The directory is now a different repository.
	remotes[canonical] = "git.example.com/team/homelab"

	_, ok, err := cfg.ResolveWorkspace("claude", ws)
	if err == nil {
		t.Fatal("a changed remote resolved successfully")
	}
	if ok {
		t.Error("ok was true alongside an error")
	}
	var lookupErr *WorkspaceLookupError
	if !asWorkspaceLookupError(err, &lookupErr) {
		t.Fatalf("error is not a WorkspaceLookupError: %T", err)
	}
}

func asWorkspaceLookupError(err error, target **WorkspaceLookupError) bool {
	if e, ok := err.(*WorkspaceLookupError); ok {
		*target = e
		return true
	}
	return false
}

// TestResolveWorkspace_GoneDirectoryRefuses: a binding whose directory is no
// longer there describes something that does not exist.
func TestResolveWorkspace_GoneDirectoryRefuses(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "repo")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	withFakeRemote(t, nil)
	cfg := &Config{}
	if err := cfg.BindWorkspace("claude", ws, []string{"kb"}); err != nil {
		t.Fatal(err)
	}
	// Resolving from a path that still exists, for a binding that does not.
	canonical := cfg.WorkspaceBindings("claude")[0].Path
	if err := os.RemoveAll(ws); err != nil {
		t.Fatal(err)
	}
	cfg.Workspaces["claude"][0].Path = canonical
	if _, _, err := cfg.ResolveWorkspace("claude", canonical); err == nil {
		t.Error("a binding whose directory is gone resolved successfully")
	}
}

func TestUnbindWorkspace(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	withFakeRemote(t, nil)
	cfg := &Config{}
	if err := cfg.BindWorkspace("claude", a, []string{"ka"}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.BindWorkspace("claude", b, []string{"kb"}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.UnbindWorkspace("claude", a); err != nil {
		t.Fatal(err)
	}
	if got := cfg.WorkspaceBindings("claude"); len(got) != 1 || got[0].KBs[0] != "kb" {
		t.Errorf("after unbind: %+v", got)
	}
	// Unbinding what is not bound is a silent no-op.
	if err := cfg.UnbindWorkspace("claude", a); err != nil {
		t.Errorf("unbinding an unbound path errored: %v", err)
	}
	// The last one removes the provider entry entirely.
	if err := cfg.UnbindWorkspace("claude", b); err != nil {
		t.Fatal(err)
	}
	if _, present := cfg.Workspaces["claude"]; present {
		t.Error("the provider entry survived its last binding")
	}
}

func TestNormalizeRemote(t *testing.T) {
	same := []string{
		"git@git.example.com:team/repo.git",
		"https://git.example.com/team/repo",
		"https://git.example.com/team/repo.git/",
		"ssh://git@git.example.com/team/repo.git",
		"  HTTPS://Git.Example.com/Team/Repo.git  ",
	}
	want := NormalizeRemote(same[0])
	if want == "" {
		t.Fatal("normalization produced an empty guard")
	}
	for _, raw := range same[1:] {
		if got := NormalizeRemote(raw); got != want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q — the same repository reached two ways must not read as a move", raw, got, want)
		}
	}
	if NormalizeRemote("") != "" {
		t.Error("an empty remote must normalize to empty, not to a guard")
	}
}

// TestWorkspacesRoundTrip: the bindings survive a Save/Load cycle, which is
// what makes them usable by a later `sync` in a different process.
func TestWorkspacesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "repo")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical, _ := CanonicalWorkspacePath(ws)
	withFakeRemote(t, map[string]string{canonical: "git.example.com/team/repo"})

	cfg := Default()
	if err := cfg.SetWorkspaceScope("claude", ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := cfg.BindWorkspace("claude", ws, []string{"homelab-kb"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.WorkspaceScope("claude") != ScopeWorkspace {
		t.Error("the scope did not survive a round trip")
	}
	got := reloaded.WorkspaceBindings("claude")
	if len(got) != 1 || got[0].Path != canonical || got[0].Remote != "git.example.com/team/repo" || got[0].KBs[0] != "homelab-kb" {
		t.Errorf("bindings did not survive a round trip: %+v", got)
	}
}

// #1356: the unbound_access resolution matrix.
func TestUnboundAccess(t *testing.T) {
	cases := []struct {
		name       string
		m          map[string]string
		provider   string
		mode, orig string
	}{
		{"unset", nil, "claude", "read", "default"},
		{"default write", map[string]string{"default": "write"}, "claude", "write", "unbound_access"},
		{"default write, kiro read", map[string]string{"default": "write", "kiro": "read"}, "kiro", "read", "unbound_access"},
		{"default write, kiro read, other", map[string]string{"default": "write", "kiro": "read"}, "claude", "write", "unbound_access"},
		{"default read, kiro write", map[string]string{"default": "read", "kiro": "write"}, "kiro", "write", "unbound_access"},
		{"default read, kiro write, other", map[string]string{"default": "read", "kiro": "write"}, "claude", "read", "unbound_access"},
		{"unknown value", map[string]string{"default": "yes"}, "claude", "read", "unbound_access"},
		{"unknown provider key", map[string]string{"nobody": "write"}, "claude", "read", "default"},
	}
	for _, c := range cases {
		cfg := &Config{UnboundModes: c.m}
		if mode, orig := cfg.UnboundAccess(c.provider); mode != c.mode || orig != c.orig {
			t.Errorf("%s: got %s/%s, want %s/%s", c.name, mode, orig, c.mode, c.orig)
		}
	}
}

// TRAP (#1356): unbound_access changes the session's tools, never what is
// projected. Artifacts stay tied to a binding (D193), so the projection code
// must not read the setting: sync output is then identical with it on or off.
func TestProjectionDoesNotReadUnboundAccess(t *testing.T) {
	err := filepath.WalkDir("../provisioning", func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".go" {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for _, id := range []string{"UnboundAccess", "UnboundModes", "unbound_access"} {
			if strings.Contains(string(b), id) {
				t.Errorf("%s reads %s: the projection must not depend on the unbound access mode", p, id)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
