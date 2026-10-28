package clientconfig

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// workspaceCfg is a configuration with claude in workspace scope and the given
// derived bindings.
func workspaceCfg(t *testing.T, derived ...DerivedBinding) *Config {
	t.Helper()
	cfg := &Config{}
	if err := cfg.SetWorkspaceScope("claude", ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	cfg.SetDerived(DerivedState{Bindings: map[string][]DerivedBinding{"claude": derived}})
	return cfg
}

func canonDir(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(parts...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	c, err := CanonicalWorkspacePath(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDeriveBindings_MergesByPathAndGuardsByRemote(t *testing.T) {
	root := t.TempDir()
	shared := canonDir(t, root, "shared")
	other := canonDir(t, root, "other")
	moved := canonDir(t, root, "moved")
	withFakeRemote(t, map[string]string{
		shared: "example.com/owner/shared",
		other:  "example.com/owner/other",
		moved:  "example.com/owner/somewhere-else",
	})
	paths := map[string]string{"a-key": shared, "b-key": shared, "other": other, "moved": moved, "gone": filepath.Join(root, "nowhere")}
	got, bad := DeriveBindings([]DeclaredRepo{
		{KB: "kb-a", Key: "a-key", Remote: "example.com/owner/shared"},
		{KB: "kb-b", Key: "b-key", Remote: "example.com/owner/shared"},
		{KB: "kb-a", Key: "other", Remote: "example.com:22/Owner/other"},
		{KB: "kb-a", Key: "moved", Remote: "example.com/owner/moved"},
		{KB: "kb-a", Key: "gone", Remote: "example.com/owner/gone"},
	}, func(d DeclaredRepo) (string, error) { return paths[d.Key], nil })

	if len(got) != 2 {
		t.Fatalf("bindings = %+v, want shared (two KBs) and other", got)
	}
	var union DerivedBinding
	for _, b := range got {
		if b.Path == shared {
			union = b
		}
	}
	if !reflect.DeepEqual(union.KBs, []string{"kb-a", "kb-b"}) || len(union.Sources) != 2 {
		t.Errorf("one repository declared by two KBs binds to both: %+v", union)
	}
	if union.Remote != "example.com/owner/shared" {
		t.Errorf("the guard is the remote found at the path, got %q", union.Remote)
	}
	if len(bad) != 2 {
		t.Fatalf("unresolved = %+v, want the moved clone and the missing one", bad)
	}
}

func TestDeriveBindings_ResolverErrorLeavesItUnbound(t *testing.T) {
	got, bad := DeriveBindings([]DeclaredRepo{{KB: "kb-a", Key: "x", Remote: "example.com/o/x"}},
		func(DeclaredRepo) (string, error) { return "", errors.New("no clone") })
	if len(got) != 0 || len(bad) != 1 || bad[0].Reason != "declared, no local clone" {
		t.Errorf("got %+v / %+v", got, bad)
	}
}

func TestWorkspaceBindings_DerivedOnlyInWorkspaceScope(t *testing.T) {
	root := t.TempDir()
	repo := canonDir(t, root, "repo")
	d := DerivedBinding{Path: repo, KBs: []string{"kb-a"}, Sources: []DerivedSource{{KB: "kb-a", Key: "r"}}}

	cfg := workspaceCfg(t, d)
	got := cfg.WorkspaceBindings("claude")
	if len(got) != 1 || !got[0].Derived || got[0].Sources[0] != "kb-a" {
		t.Fatalf("bindings = %+v", got)
	}
	if !cfg.UsesBridge("claude", "/routed") {
		t.Error("a provider whose only bindings are derived uses the bridge")
	}

	// A provider in provider scope is unchanged: no existing installation moves.
	cfg.SetDerived(DerivedState{Bindings: map[string][]DerivedBinding{"codex": {d}}})
	if n := len(cfg.WorkspaceBindings("codex")); n != 0 {
		t.Errorf("a provider-scope provider got %d derived bindings", n)
	}
	if cfg.UsesBridge("codex", "/routed") {
		t.Error("a provider-scope provider must not switch to the bridge")
	}
}

func TestWorkspaceBindings_ExplicitReplacesDerivedOnItsPath(t *testing.T) {
	root := t.TempDir()
	repo := canonDir(t, root, "repo")
	withFakeRemote(t, nil)
	cfg := workspaceCfg(t, DerivedBinding{Path: repo, KBs: []string{"kb-a"}})
	// `--no-kb` is the local opt-out: it replaces the derived binding entirely.
	if err := cfg.BindWorkspace("claude", repo, nil); err != nil {
		t.Fatal(err)
	}
	got := cfg.WorkspaceBindings("claude")
	if len(got) != 1 || got[0].Derived || len(got[0].KBs) != 0 {
		t.Fatalf("bindings = %+v, want the explicit no-KB binding alone", got)
	}
	w, bound, err := cfg.ResolveWorkspace("claude", repo)
	if err != nil || !bound || len(w.KBs) != 0 {
		t.Errorf("resolve = %+v %v %v", w, bound, err)
	}
}

func TestResolveWorkspace_DeepestWinsAcrossExplicitAndDerived(t *testing.T) {
	root := t.TempDir()
	outer := canonDir(t, root, "folder")
	inner := canonDir(t, root, "folder", "repo")
	withFakeRemote(t, map[string]string{inner: "example.com/owner/repo"})
	cfg := workspaceCfg(t, DerivedBinding{Path: inner, Remote: "example.com/owner/repo", KBs: []string{"kb-a"}})
	if err := cfg.BindWorkspace("claude", outer, []string{"kb-b"}); err != nil {
		t.Fatal(err)
	}
	w, bound, err := cfg.ResolveWorkspace("claude", inner)
	if err != nil || !bound || !w.Derived || w.KBs[0] != "kb-a" {
		t.Fatalf("a declared repository inside a bound folder gets its own set: %+v %v %v", w, bound, err)
	}
	w, _, _ = cfg.ResolveWorkspace("claude", outer)
	if w.Derived || w.KBs[0] != "kb-b" {
		t.Errorf("the folder keeps its explicit binding: %+v", w)
	}
}

func TestResolveWorkspace_DerivedKeepsTheRemoteGuard(t *testing.T) {
	root := t.TempDir()
	repo := canonDir(t, root, "repo")
	withFakeRemote(t, map[string]string{repo: "example.com/owner/someone-else"})
	cfg := workspaceCfg(t, DerivedBinding{Path: repo, Remote: "example.com/owner/repo", KBs: []string{"kb-a"}})
	if _, _, err := cfg.ResolveWorkspace("claude", repo); err == nil {
		t.Error("a clone whose remote changed since it was derived must be refused")
	}
}

func TestDerivedState_RoundTripIsPerConfigDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	s := DerivedState{Bindings: map[string][]DerivedBinding{"claude": {{Path: "/r", KBs: []string{"kb-a"}}}}}
	if err := SaveDerived(dir, s); err != nil {
		t.Fatal(err)
	}
	if got := LoadDerived(dir); len(got.Bindings["claude"]) != 1 {
		t.Errorf("round trip lost the bindings: %+v", got)
	}
	// Derived for another configuration directory is not this one's.
	if got := LoadDerived(t.TempDir()); len(got.Bindings) != 0 {
		t.Errorf("a foreign config dir read %+v", got)
	}
}

// paths.yaml is unsigned KB content: the declared remote, matched by the
// clone's origin, is what makes binding a clone safe.
func TestDeriveBindings_RemoteIsRequiredAndMustMatch(t *testing.T) {
	root := t.TempDir()
	repo := canonDir(t, root, "repo")
	noOrigin := canonDir(t, root, "no-origin")
	withFakeRemote(t, map[string]string{repo: "example.com/owner/repo"})
	paths := map[string]string{"no-remote": repo, "no-origin": noOrigin}
	got, bad := DeriveBindings([]DeclaredRepo{
		{KB: "kb-a", Key: "no-remote"},
		{KB: "kb-a", Key: "no-origin", Remote: "example.com/owner/repo"},
	}, func(d DeclaredRepo) (string, error) { return paths[d.Key], nil })
	if len(got) != 0 || len(bad) != 2 {
		t.Fatalf("bound %+v, unresolved %+v: want both left unbound", got, bad)
	}
	if bad[0].Reason != "declared without a remote: not bound (the remote is the guard)" {
		t.Errorf("reason = %q", bad[0].Reason)
	}
}

func TestRenameDerivedKB(t *testing.T) {
	cfg := workspaceCfg(t,
		DerivedBinding{Path: "/a", KBs: []string{"kb-a"}, Sources: []DerivedSource{{KB: "kb-a", Key: "r"}}},
		DerivedBinding{Path: "/b", KBs: []string{"kb-a", "kb-b"}, Sources: []DerivedSource{{KB: "kb-a", Key: "r"}, {KB: "kb-b", Key: "r"}}})
	cfg.derived.Unresolved = map[string][]DerivedUnresolved{"claude": {{KB: "kb-a", Key: "x"}}}

	if !cfg.RenameDerivedKB("kb-a", "kb-z") {
		t.Fatal("rename reported no change")
	}
	got := cfg.derived.Bindings["claude"]
	if got[0].KBs[0] != "kb-z" || got[0].Sources[0].KB != "kb-z" || got[1].KBs[0] != "kb-z" || cfg.derived.Unresolved["claude"][0].KB != "kb-z" {
		t.Errorf("rename incomplete: %+v %+v", got, cfg.derived.Unresolved)
	}

	cfg.RenameDerivedKB("kb-z", "")
	got = cfg.derived.Bindings["claude"]
	if len(got) != 1 || got[0].Path != "/b" || len(got[0].KBs) != 1 || got[0].KBs[0] != "kb-b" || len(got[0].Sources) != 1 {
		t.Errorf("removal must drop the KB, and a binding left with none: %+v", got)
	}
	if len(cfg.derived.Unresolved) != 0 {
		t.Errorf("unresolved kept: %+v", cfg.derived.Unresolved)
	}
}

// #1268: a dry run asks whether the file would change, and writes nothing.
func TestDerivedWouldChange_NamesCreateAndUpdateOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	s := DerivedState{Bindings: map[string][]DerivedBinding{"claude": {{Path: "/r", KBs: []string{"kb-a"}}}}}

	path, changed, err := DerivedWouldChange(dir, s)
	if err != nil || !changed {
		t.Fatalf("fresh: changed=%v err=%v, want a creation", changed, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the preview wrote the file")
	}
	if err := SaveDerived(dir, s); err != nil {
		t.Fatal(err)
	}
	if _, changed, _ := DerivedWouldChange(dir, s); changed {
		t.Error("identical state reported as a change")
	}
	s.Bindings["claude"][0].KBs = []string{"kb-b"}
	if _, changed, _ := DerivedWouldChange(dir, s); !changed {
		t.Error("a different state was not reported")
	}
}

func TestSaveWouldRewrite_OnlyWhenBytesChange(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{ServerURL: "http://localhost:8080/mcp", ServerName: "wiki"}
	if changed, err := SaveWouldRewrite(dir, cfg); err != nil || !changed {
		t.Fatalf("no file yet: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(Path(dir)); !os.IsNotExist(err) {
		t.Fatal("the preview wrote the file")
	}
	if err := Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if changed, err := SaveWouldRewrite(dir, cfg); err != nil || changed {
		t.Errorf("after Save: changed=%v err=%v, want none", changed, err)
	}
	cfg.ServerName = "other"
	if changed, _ := SaveWouldRewrite(dir, cfg); !changed {
		t.Error("a changed setting was not reported")
	}
}

func TestRemoveSigningKeys(t *testing.T) {
	c := &Config{SigningKeys: map[string][]string{"kb-a": {"k"}}}
	if c.RemoveSigningKeys("kb-b") {
		t.Error("an unpinned KB reported as removed")
	}
	if !c.RemoveSigningKeys("kb-a") || len(c.SigningKeys) != 0 {
		t.Errorf("kb-a not removed: %v", c.SigningKeys)
	}
}
