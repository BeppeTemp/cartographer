package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// worktreesRepo makes a repository with two linked worktrees and a config that
// binds it, with or without the --worktrees opt-in.
func worktreesRepo(t *testing.T, optIn bool) (cfg *clientconfig.Config, repo string, wts []string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo = filepath.Join(root, "repo")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=u", "GIT_AUTHOR_EMAIL=u@example.com", "GIT_COMMITTER_NAME=u", "GIT_COMMITTER_EMAIL=u@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "init")
	for _, n := range []string{"a", "b"} {
		wt := filepath.Join(root, "wt-"+n)
		run("worktree", "add", "-q", "-b", n, wt)
		wts = append(wts, wt)
	}
	cfg = &clientconfig.Config{
		ServerName: "cartographer", ServerURL: "https://example.com/mcp",
		ServerRoutedPath: "/mcp/routed", KnownKBs: []string{"kb-a"},
	}
	if err := cfg.SetWorkspaceScope("claude", clientconfig.ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := cfg.BindWorkspaceWith("claude", repo, []string{"kb-a"}, clientconfig.BindOptions{Worktrees: optIn}); err != nil {
		t.Fatal(err)
	}
	return cfg, cfg.WorkspaceBindings("claude")[0].Path, wts
}

func workspaceDirs(ps []syncProjection) []string {
	var out []string
	for _, p := range ps {
		if p.Workspace != "" {
			out = append(out, p.Workspace)
		}
	}
	return out
}

func TestProjectionsForWorktreesOptIn(t *testing.T) {
	cfg, repo, wts := worktreesRepo(t, true)
	ps, err := projectionsFor(cfg, "claude", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := workspaceDirs(ps)
	if len(got) != 3 || got[0] != repo || got[1] != wts[0] || got[2] != wts[1] {
		t.Fatalf("workspaces = %v, want %s and %v", got, repo, wts)
	}
	for _, p := range ps[2:] {
		if p.BaseDir != p.Workspace || p.Scope != contract.ScopeProject || len(p.KBs) != 1 || p.KBs[0] != "kb-a" {
			t.Errorf("worktree projection = %+v", p)
		}
	}
	if st := workspaceStatuses(cfg, "claude", t.TempDir()); len(st) != 3 || st[1].Source != "worktree of "+repo || st[1].State != "ok" {
		t.Errorf("status = %+v, want the main folder and its two worktrees", st)
	}

	// A removed worktree is not listed and not an error.
	if out, err := exec.Command("git", "-C", repo, "worktree", "remove", "--force", wts[0]).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	ps, err = projectionsFor(cfg, "claude", t.TempDir())
	if err != nil || len(workspaceDirs(ps)) != 2 {
		t.Fatalf("after remove: %v, %v", workspaceDirs(ps), err)
	}
}

func TestProjectionsForWorktreesOffByDefault(t *testing.T) {
	cfg, repo, _ := worktreesRepo(t, false)
	ps, err := projectionsFor(cfg, "claude", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := workspaceDirs(ps); len(got) != 1 || got[0] != repo {
		t.Fatalf("workspaces = %v, want the bound folder only", got)
	}
	if st := workspaceStatuses(cfg, "claude", t.TempDir()); len(st) != 1 {
		t.Errorf("status = %+v", st)
	}
}

// A vanished worktree's record is dropped without a word; one that exists but
// is no longer declared takes the ordinary prune; a failed listing prunes none.
func TestPruneOrphanWorktrees(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	kept := t.TempDir()
	key := func(w string) provisioning.Projection {
		return provisioning.Projection{Provider: "claude", Workspace: w}
	}
	mk := func() *provisioning.LockFile {
		lf := &provisioning.LockFile{}
		lf.SetProjection(key(gone), provisioning.Lock{Worktree: true})
		lf.SetProjection(key(kept), provisioning.Lock{})
		return lf
	}
	declared := []syncProjection{{Provider: "claude", Workspace: "/elsewhere", Scope: contract.ScopeProject}}

	var out, errb bytes.Buffer
	lf := mk()
	if _, err := pruneOrphanProjections(syncStreams{Out: &out, Err: &errb}, lf, declared, nil, t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), gone) || strings.Contains(errb.String(), gone) {
		t.Errorf("vanished worktree not silent: %q %q", out.String(), errb.String())
	}
	if len(lf.ForProjection(key(gone)).Managed) != 0 || containsKey(lf, key(gone)) {
		t.Error("vanished worktree's record kept")
	}
	if containsKey(lf, key(kept)) || !strings.Contains(out.String(), kept) {
		t.Errorf("existing undeclared workspace not pruned the usual way: %q", out.String())
	}

	out.Reset()
	lf = mk()
	declared[0].WorktreesUnlisted = true
	pruned, err := pruneOrphanProjections(syncStreams{Out: &out, Err: &errb}, lf, declared, nil, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsKey(lf, key(kept)) || out.Len() != 0 || len(pruned) != 1 {
		t.Errorf("failed listing: kept record gone or output %q, pruned %v", out.String(), pruned)
	}
}

func containsKey(lf *provisioning.LockFile, k provisioning.Projection) bool {
	for _, p := range lf.Projections() {
		if p == k {
			return true
		}
	}
	return false
}

func TestProjectionsForWorktreesListingFailure(t *testing.T) {
	cfg, repo, _ := worktreesRepo(t, true)
	orig := linkedWorktrees
	linkedWorktrees = func(string) ([]string, error) { return nil, errors.New("boom") }
	t.Cleanup(func() { linkedWorktrees = orig })
	ps, err := projectionsFor(cfg, "claude", t.TempDir())
	if err != nil {
		t.Fatalf("a listing failure must not fail the main projection: %v", err)
	}
	got := workspaceDirs(ps)
	if len(got) != 1 || got[0] != repo {
		t.Fatalf("workspaces = %v", got)
	}
	if !ps[1].WorktreesUnlisted {
		t.Error("the unlisted flag is not set")
	}
}

// The main folder and two linked worktrees each keep their own block in the
// shared info/exclude.
func TestWorktreeExcludeBlocksDoNotOverwrite(t *testing.T) {
	_, repo, wts := worktreesRepo(t, true)
	for _, d := range append([]string{repo}, wts...) {
		if err := provisioning.EnsureWorkspaceExcluded(d, []string{".claude/agents"}); err != nil {
			t.Fatalf("%s: %v", d, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), ">>> cartographer (managed"); n != 3 {
		t.Fatalf("want 3 blocks , got %d:\n%s", n, b)
	}
}

// The silent drop of a missing directory is for a linked worktree of a
// --worktrees binding only: another undeclared key (an unmounted volume, a
// moved clone) takes the ordinary prune path and is not silently forgotten.
func TestPruneOrphanMissingDirOnlyDropsWorktrees(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(root, "wt-gone")
	other := filepath.Join(root, "moved-clone")
	key := func(w string) provisioning.Projection {
		return provisioning.Projection{Provider: "claude", Workspace: w}
	}
	lf := &provisioning.LockFile{}
	lf.SetProjection(key(wt), provisioning.Lock{Worktree: true})
	lf.SetProjection(key(other), provisioning.Lock{})
	declared := []syncProjection{{Provider: "claude", Workspace: "/elsewhere", Scope: contract.ScopeProject}}
	var out, errb bytes.Buffer
	if _, err := pruneOrphanProjections(syncStreams{Out: &out, Err: &errb}, lf, declared, nil, t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	if containsKey(lf, key(wt)) || strings.Contains(out.String()+errb.String(), wt) {
		t.Errorf("a vanished worktree was kept or not silent: %q %q", out.String(), errb.String())
	}
	// The other key is reported by the ordinary prune, not forgotten silently.
	if !strings.Contains(out.String(), "pruned the projection in "+other) {
		t.Errorf("an undeclared key with a missing directory was dropped silently: %q %q", out.String(), errb.String())
	}
}

// A real git that fails to list (an unparsable repository config) must prune
// nothing: the live worktree's files and record stay.
func TestWorktreesRealListingFailurePrunesNothing(t *testing.T) {
	cfg, repo, wts := worktreesRepo(t, true)
	if err := os.WriteFile(filepath.Join(repo, ".git", "config"), []byte("[core\nbroken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, err := projectionsFor(cfg, "claude", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := workspaceDirs(ps); len(got) != 1 || got[0] != repo {
		t.Fatalf("workspaces = %v, want the bound folder only", got)
	}
	if !ps[len(ps)-1].WorktreesUnlisted {
		t.Fatal("the unlisted flag is not set after a real git failure")
	}
	if st := workspaceStatuses(cfg, "claude", t.TempDir()); !strings.Contains(st[0].Detail, "linked worktrees not listed") {
		t.Errorf("status hides the failed listing: %+v", st)
	}

	live := filepath.Join(wts[0], ".claude", "agents")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(live, "a.md")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	k := provisioning.Projection{Provider: "claude", Workspace: wts[0]}
	lf := &provisioning.LockFile{}
	lf.SetProjection(k, provisioning.Lock{Worktree: true, Managed: []provisioning.ManagedFile{{Kind: "agent", Name: "a", Path: ".claude/agents/a.md"}}})
	var out, errb bytes.Buffer
	if _, err := pruneOrphanProjections(syncStreams{Out: &out, Err: &errb}, lf, ps, nil, t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	if !containsKey(lf, k) {
		t.Error("the live worktree's record was dropped")
	}
	if _, err := os.Stat(f); err != nil {
		t.Errorf("the live worktree's file was removed: %v", err)
	}
}

// An orphaned workspace section removes only what still hashes to what the
// sync wrote: a recorded path with no hash (or an edited file) is reported and
// kept, a file Cartographer wrote is cleaned, and the section goes either way.
func TestPruneOrphanProjectionsRemovesOnlyVerifiedFiles(t *testing.T) {
	ws := t.TempDir()
	skillDir := filepath.Join(ws, ".claude", "skills", "mine")
	victimDir := filepath.Join(ws, ".claude", "skills", "theirs")
	for _, d := range []string{skillDir, victimDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("---\nname: x\n---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h, err := provisioning.ContentHashDirOSForKind(skillDir, "skill")
	if err != nil {
		t.Fatal(err)
	}
	key := provisioning.Projection{Provider: "claude", Workspace: ws}
	lf := &provisioning.LockFile{}
	lf.SetProjection(key, provisioning.Lock{Managed: []provisioning.ManagedFile{
		{Kind: "skill", Name: "mine", Path: ".claude/skills/mine/SKILL.md", ContentHash: h, MaterializedHash: h},
		{Kind: "skill", Name: "theirs", Path: ".claude/skills/theirs/SKILL.md"},
	}})
	declared := []syncProjection{{Provider: "claude", Workspace: "/elsewhere", Scope: contract.ScopeProject}}
	var out, errb bytes.Buffer
	if _, err := pruneOrphanProjections(syncStreams{Out: &out, Err: &errb}, lf, declared, nil, t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("the verified file survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(victimDir, "SKILL.md")); err != nil {
		t.Errorf("an unverifiable file was removed: %v", err)
	}
	if containsKey(lf, key) {
		t.Error("the section stayed in the lockfile")
	}
	if !strings.Contains(errb.String(), "could not be verified") {
		t.Errorf("the kept file is not reported: %q", errb.String())
	}
}

// A prune that fails with a permission error (#2178) is a per-projection
// refusal: warning, the lock keeps the entry, the other projection is pruned
// and the workspace is reported for the exit code.
func TestPruneOrphanProjections_PermissionDeniedIsRefusal(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX directory permissions and a non-root user")
	}
	denied, ok := t.TempDir(), t.TempDir()
	for _, w := range []string{denied, ok} {
		dir := filepath.Join(w, ".claude", "skills", "a")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: a\n---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ro := filepath.Join(denied, ".claude", "skills", "a")
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })

	lf := &provisioning.LockFile{}
	for _, w := range []string{denied, ok} {
		// The entry passes the hash verification of the orphan prune, so the
		// removal itself is what fails (EPERM), not the verification.
		h, err := provisioning.ContentHashDirOSForKind(filepath.Join(w, ".claude", "skills", "a"), "skill")
		if err != nil {
			t.Fatal(err)
		}
		lf.SetProjection(provisioning.Projection{Provider: "claude", Workspace: w}, provisioning.Lock{
			BaseDir: w,
			Managed: []provisioning.ManagedFile{{Path: ".claude/skills/a/SKILL.md", Kind: "skill", Name: "a", ContentHash: h, MaterializedHash: h}},
		})
	}
	declared := []syncProjection{{Provider: "claude", Workspace: "/elsewhere", Scope: contract.ScopeProject}}
	var out, errb bytes.Buffer
	var den []string
	if _, err := pruneOrphanProjections(syncStreams{Out: &out, Err: &errb}, lf, declared, nil, t.TempDir(), &den); err != nil {
		t.Fatalf("a permission error aborted the run: %v", err)
	}
	if !containsKey(lf, provisioning.Projection{Provider: "claude", Workspace: denied}) {
		t.Error("the denied projection left the lockfile: the next run cannot retry")
	}
	if containsKey(lf, provisioning.Projection{Provider: "claude", Workspace: ok}) {
		t.Error("the other projection was not pruned")
	}
	if len(den) != 1 || den[0] != denied || !strings.Contains(errb.String(), "not pruned") {
		t.Errorf("denied = %v, stderr %q", den, errb.String())
	}
}
