package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/repoindex"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=u@example.com", "-c", "user.name=user", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// cloneOf makes a git repository at a canonical path whose origin is remote
// and which tracks the given files.
func cloneOf(t *testing.T, remote string, tracked ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "remote", "add", "origin", "https://"+remote+".git")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range tracked {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), []byte("# team rules\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "initial")
	return dir
}

func workspaceScoped(provider string, kbs ...string) *clientconfig.Config {
	cfg := clientconfig.Default()
	cfg.KnownKBs = kbs
	_ = cfg.SetWorkspaceScope(provider, clientconfig.ScopeWorkspace)
	return cfg
}

func prefetchOf(regs map[string]provisioning.PathRegistry, failed ...string) *prefetched {
	pre := &prefetched{
		cs:  candidateSet{Named: map[string][]provisioning.Artifact{}, PathRegistries: regs},
		kbs: map[string]bool{},
	}
	for kb := range regs {
		pre.kbs[kb] = true
	}
	for _, kb := range failed {
		pre.kbs[kb] = true
		pre.failed = append(pre.failed, kbPullFailure{KB: kb, Err: os.ErrInvalid})
	}
	return pre
}

func derive(cfg *clientconfig.Config, pre *prefetched) clientconfig.DerivedState {
	providers, _ := derivationScope(cfg, []string{"claude"})
	r := repoindex.NewResolver(cfg.Paths, nil, repoindex.DefaultDepth)
	next := computeDerived(cfg, []string{"claude"}, providers, pre, r)
	cfg.SetDerived(next)
	return next
}

func boolPtr(b bool) *bool { return &b }

func TestComputeDerived_DeclaredReposAreBound(t *testing.T) {
	worked := cloneOf(t, "example.com/owner/worked-on")
	cited := cloneOf(t, "example.com/owner/cited")
	cfg := workspaceScoped("claude", "kb-a")
	cfg.Paths = map[string]string{"worked-on": worked, "cited": cited}
	pre := prefetchOf(map[string]provisioning.PathRegistry{"kb-a": {Repos: map[string]provisioning.PathDecl{
		"worked-on": {Description: "d", Remote: "example.com/owner/worked-on"},
		"cited":     {Description: "d", Remote: "example.com/owner/cited", Bind: boolPtr(false)},
		"missing":   {Description: "d", Remote: "example.com/owner/missing"},
	}}})

	st := derive(cfg, pre)
	got := st.Bindings["claude"]
	if len(got) != 1 || got[0].Path != worked || got[0].KBs[0] != "kb-a" {
		t.Fatalf("bindings = %+v, want only the worked-on repository (bind: false is cited, not bound)", got)
	}
	if un := st.Unresolved["claude"]; len(un) != 1 || un[0].Key != "missing" {
		t.Errorf("unresolved = %+v, want the key with no clone", un)
	}
	if ws := workspaceStatuses(cfg, "claude", t.TempDir()); len(ws) != 2 || ws[1].State != "unresolved" || !strings.Contains(ws[1].Detail, "no local clone") {
		t.Errorf("status = %+v, want the binding then `declared, no local clone`", ws)
	}
}

func TestComputeDerived_TwoKBsOnOneRepositoryUnion(t *testing.T) {
	repo := cloneOf(t, "example.com/owner/shared")
	cfg := workspaceScoped("claude", "kb-a", "kb-b")
	cfg.Paths = map[string]string{"shared": repo}
	decl := provisioning.PathRegistry{Repos: map[string]provisioning.PathDecl{"shared": {Description: "d", Remote: "example.com/owner/shared"}}}
	st := derive(cfg, prefetchOf(map[string]provisioning.PathRegistry{"kb-a": decl, "kb-b": decl}))
	got := st.Bindings["claude"]
	if len(got) != 1 || strings.Join(got[0].KBs, ",") != "kb-a,kb-b" {
		t.Fatalf("bindings = %+v, want one binding to both KBs", got)
	}
}

func TestComputeDerived_AnotherCloneOfTheRemoteIsNotBound(t *testing.T) {
	bound := cloneOf(t, "example.com/owner/shared")
	cloneOf(t, "example.com/owner/shared") // a second clone, never named by any path
	cfg := workspaceScoped("claude", "kb-a")
	cfg.Paths = map[string]string{"shared": bound}
	decl := provisioning.PathRegistry{Repos: map[string]provisioning.PathDecl{"shared": {Description: "d", Remote: "example.com/owner/shared"}}}
	got := derive(cfg, prefetchOf(map[string]provisioning.PathRegistry{"kb-a": decl})).Bindings["claude"]
	if len(got) != 1 || got[0].Path != bound {
		t.Errorf("bindings = %+v, want exactly the clone the placeholder resolves to", got)
	}
}

func TestComputeDerived_RemovedDeclarationIsDroppedAndAFailedPullKeepsTheList(t *testing.T) {
	repo := cloneOf(t, "example.com/owner/worked-on")
	cfg := workspaceScoped("claude", "kb-a")
	cfg.Paths = map[string]string{"worked-on": repo}
	decl := provisioning.PathRegistry{Repos: map[string]provisioning.PathDecl{"worked-on": {Description: "d", Remote: "example.com/owner/worked-on"}}}
	if n := len(derive(cfg, prefetchOf(map[string]provisioning.PathRegistry{"kb-a": decl})).Bindings["claude"]); n != 1 {
		t.Fatalf("setup: %d bindings", n)
	}

	// kb-a could not be pulled: not a declaration that the repository went away.
	if n := len(derive(cfg, prefetchOf(nil, "kb-a")).Bindings["claude"]); n != 1 {
		t.Errorf("a failed pull dropped the derived list (%d bindings)", n)
	}
	// kb-a answers, and no longer declares it: the binding goes.
	if st := derive(cfg, prefetchOf(map[string]provisioning.PathRegistry{"kb-a": {}})); len(st.Bindings) != 0 {
		t.Errorf("bindings = %+v, want none once the declaration is gone", st.Bindings)
	}
	// bind: false added: same.
	off := provisioning.PathRegistry{Repos: map[string]provisioning.PathDecl{"worked-on": {Description: "d", Remote: "example.com/owner/worked-on", Bind: boolPtr(false)}}}
	derive(cfg, prefetchOf(map[string]provisioning.PathRegistry{"kb-a": decl}))
	if st := derive(cfg, prefetchOf(map[string]provisioning.PathRegistry{"kb-a": off})); len(st.Bindings) != 0 {
		t.Errorf("bindings = %+v, want none after bind: false", st.Bindings)
	}
}

func TestComputeDerived_ProviderScopeAndUntouchedProviders(t *testing.T) {
	repo := cloneOf(t, "example.com/owner/worked-on")
	cfg := clientconfig.Default()
	cfg.KnownKBs = []string{"kb-a"}
	cfg.Paths = map[string]string{"worked-on": repo}
	decl := provisioning.PathRegistry{Repos: map[string]provisioning.PathDecl{"worked-on": {Description: "d", Remote: "example.com/owner/worked-on"}}}
	if st := derive(cfg, prefetchOf(map[string]provisioning.PathRegistry{"kb-a": decl})); len(st.Bindings) != 0 {
		t.Errorf("a provider in provider scope must not derive: %+v", st.Bindings)
	}
	// `sync --client codex` leaves claude's derived list as it was.
	cfg.SetDerived(clientconfig.DerivedState{Bindings: map[string][]clientconfig.DerivedBinding{"claude": {{Path: repo, KBs: []string{"kb-a"}}}}})
	next := computeDerived(cfg, []string{"codex"}, nil, nil, nil)
	if len(next.Bindings["claude"]) != 1 {
		t.Errorf("an untouched provider lost its derived bindings: %+v", next.Bindings)
	}
}

func TestDerivedInstructions(t *testing.T) {
	cases := []struct {
		name, provider string
		tracked        []string
		override, skip string
	}{
		{"tracked AGENTS.md is skipped", "codex", []string{"AGENTS.md"}, "", "AGENTS.md"},
		{"tracked CLAUDE.md goes to CLAUDE.local.md", "claude", []string{"CLAUDE.md"}, "CLAUDE.local.md", ""},
		{"tracked CLAUDE.local.md too means none", "claude", []string{"CLAUDE.md", "CLAUDE.local.md"}, "", "CLAUDE.md"},
		{"nothing tracked behaves as an explicit bind", "claude", nil, "", ""},
		{"an untracked AGENTS.md is the explicit bind's", "codex", nil, "", ""},
	}
	for _, c := range cases {
		dir := cloneOf(t, "example.com/owner/r", c.tracked...)
		override, skipped := derivedInstructions(c.provider, dir)
		if override != c.override || skipped != c.skip {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, override, skipped, c.override, c.skip)
		}
	}
}

// materializeDerived syncs one derived projection of provider into a clone and
// returns the clone.
func materializeDerived(t *testing.T, provider string, tracked ...string) string {
	t.Helper()
	repo := cloneOf(t, "example.com/owner/worked-on", tracked...)
	cfg := workspaceScoped(provider, "kb-a")
	cfg.SetDerived(clientconfig.DerivedState{Bindings: map[string][]clientconfig.DerivedBinding{
		provider: {{Path: repo, Remote: "example.com/owner/worked-on", KBs: []string{"kb-a"}, Sources: []clientconfig.DerivedSource{{KB: "kb-a", Key: "worked-on"}}}},
	}})
	target := t.TempDir()
	projections, err := allProjections(cfg, []string{provider}, target)
	if err != nil {
		t.Fatal(err)
	}
	manifests := map[string]provisioning.Manifest{}
	for _, p := range projections {
		if !p.BundleOnly {
			manifests[projectionKey(p)] = instructionsManifest("kb-a", "Rules of kb-a.\n")
		} else {
			manifests[projectionKey(p)] = provisioning.Manifest{}
		}
	}
	if _, err := materializeForProviders(manifests, projections, target, "", false, false, portabilityOptions{}, nil); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	return repo
}

func assertCleanTree(t *testing.T, repo string) {
	t.Helper()
	if out := gitIn(t, repo, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Errorf("the derived projection dirtied the repository:\n%s", out)
	}
}

func TestDerivedProjection_NeverEditsATrackedFile(t *testing.T) {
	t.Run("tracked CLAUDE.md gets its block in CLAUDE.local.md, excluded", func(t *testing.T) {
		repo := materializeDerived(t, "claude", "CLAUDE.md")
		local, err := os.ReadFile(filepath.Join(repo, "CLAUDE.local.md"))
		if err != nil || !strings.Contains(string(local), "cartographer:instructions:begin") || !strings.Contains(string(local), "Rules of kb-a.") {
			t.Fatalf("CLAUDE.local.md = %q, %v", local, err)
		}
		if tracked, _ := os.ReadFile(filepath.Join(repo, "CLAUDE.md")); string(tracked) != "# team rules\n" {
			t.Errorf("the tracked CLAUDE.md was edited: %q", tracked)
		}
		assertCleanTree(t, repo)
	})
	t.Run("tracked AGENTS.md gets no instructions", func(t *testing.T) {
		repo := materializeDerived(t, "codex", "AGENTS.md")
		if tracked, _ := os.ReadFile(filepath.Join(repo, "AGENTS.md")); string(tracked) != "# team rules\n" {
			t.Errorf("the tracked AGENTS.md was edited: %q", tracked)
		}
		assertCleanTree(t, repo)
		cfg := workspaceScoped("codex", "kb-a")
		cfg.SetDerived(clientconfig.DerivedState{Bindings: map[string][]clientconfig.DerivedBinding{"codex": {{Path: repo, KBs: []string{"kb-a"}}}}})
		ws := workspaceStatuses(cfg, "codex", t.TempDir())
		if len(ws) == 0 || !strings.Contains(ws[0].Detail, "AGENTS.md is tracked by git") || !strings.Contains(ws[0].Detail, "workspace bind") {
			t.Errorf("status must name the tracked file and the explicit-bind alternative: %+v", ws)
		}
	})
	t.Run("nothing tracked behaves as an explicit bind", func(t *testing.T) {
		repo := materializeDerived(t, "claude")
		if b, err := os.ReadFile(filepath.Join(repo, "CLAUDE.md")); err != nil || !strings.Contains(string(b), "Rules of kb-a.") {
			t.Fatalf("CLAUDE.md = %q, %v", b, err)
		}
		if _, err := os.Stat(filepath.Join(repo, "CLAUDE.local.md")); err == nil {
			t.Error("CLAUDE.local.md written although CLAUDE.md is not tracked")
		}
		assertCleanTree(t, repo)
	})
}

// A rename or an unmount must not leave the old KB name in the derived list
// until the next sync.
func TestRenameAndUnmountRewriteTheDerivedList(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	cfg := workspaceScoped("claude", "kb-a")
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	state := clientconfig.DerivedState{Bindings: map[string][]clientconfig.DerivedBinding{
		"claude": {{Path: "/r", KBs: []string{"kb-a"}, Sources: []clientconfig.DerivedSource{{KB: "kb-a", Key: "r"}}}},
	}}
	if err := clientconfig.SaveDerived(home, state); err != nil {
		t.Fatal(err)
	}
	if err := rewriteBindings("kb-a", "kb-z"); err != nil {
		t.Fatal(err)
	}
	if got := clientconfig.LoadDerived(home).Bindings["claude"]; len(got) != 1 || got[0].KBs[0] != "kb-z" {
		t.Fatalf("after rename: %+v", got)
	}
	if err := removeBindings("kb-z"); err != nil {
		t.Fatal(err)
	}
	if got := clientconfig.LoadDerived(home).Bindings; len(got) != 0 {
		t.Errorf("after unmount: %+v", got)
	}
}
