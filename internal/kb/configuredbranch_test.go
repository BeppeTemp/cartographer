package kb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// commitSettingsOnDefault adds settings.yaml holding `branch: <branch>` to the
// bare remote's default branch (main), via a throwaway clone, so a KB that
// fetches it resolves that branch as canonical (#903 WP4).
func commitSettingsOnDefault(t *testing.T, bare, base, branch string) {
	t.Helper()
	wc := filepath.Join(base, "settingswc")
	gitIn(t, base, "clone", "-q", bare, wc)
	gitIn(t, wc, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(wc, "settings.yaml"), []byte("branch: "+branch+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wc, "add", "settings.yaml")
	gitIn(t, wc, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", "branch setting")
	gitIn(t, wc, "push", "-q", "origin", "main")
}

// #903 WP4: branch: committed on a NON-default branch has no effect — the KB
// keeps following the remote's default (D264), because resolution reads the
// default branch only.
func TestConfiguredBranch_OnlyOnNonDefaultHasNoEffect(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	base := t.TempDir()
	bare := filepath.Join(base, "remote.git")
	gitIn(t, base, "-c", "init.defaultBranch=main", "init", "-q", "--bare", bare)
	// Seed main with a commit (no branch: setting), and a side branch that
	// DOES carry branch: kb-data — but it is not the default.
	seed := filepath.Join(base, "seed")
	gitIn(t, base, "clone", "-q", bare, seed)
	gitIn(t, seed, "checkout", "-q", "-B", "main")
	if err := os.WriteFile(filepath.Join(seed, "seed.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, seed, "add", "seed.md")
	gitIn(t, seed, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", "seed")
	gitIn(t, seed, "push", "-q", "origin", "main")
	gitIn(t, seed, "checkout", "-q", "-b", "side")
	if err := os.WriteFile(filepath.Join(seed, "settings.yaml"), []byte("branch: kb-data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, seed, "add", "settings.yaml")
	gitIn(t, seed, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", "branch on side")
	gitIn(t, seed, "push", "-q", "origin", "side")

	root := filepath.Join(base, "kb")
	gitIn(t, base, "clone", "-q", bare, root)
	k, err := Init(root)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	k.gitattrsChecked = true
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	// The default branch (main) carries no branch:, so the KB follows it.
	if k.ConfiguredBranch != "" {
		t.Fatalf("ConfiguredBranch = %q, want empty (branch: is only on a non-default branch)", k.ConfiguredBranch)
	}
	if s := k.GitStatusSnapshot(); s.BranchSource != "remote" || s.RemoteDefaultBranch != "main" {
		t.Fatalf("status = %+v, want source remote / main", s)
	}
}

// #903 WP4: once branch: is committed ON THE DEFAULT BRANCH, a KB checked out
// on that branch has its writes go there, and the remote's default is left
// untouched.
func TestConfiguredBranch_OnDefaultRoutesWrites(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	base := filepath.Dir(k.Root)
	commitSettingsOnDefault(t, bare, base, "kb-data")
	mainBefore := gitHere(t, bare, "rev-parse", "main")
	gitHere(t, k.Root, "fetch", "-q", "origin")
	gitHere(t, k.Root, "checkout", "-q", "-b", "kb-data", "origin/main")

	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	if k.ConfiguredBranch != "kb-data" {
		t.Fatalf("ConfiguredBranch = %q, want kb-data", k.ConfiguredBranch)
	}
	if err := k.WriteFileAtomic("data/a.md", []byte("a\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("write a"); err != nil {
		t.Fatalf("CommitOp: %v", err)
	}
	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut: %v", err)
	}
	if got := gitHere(t, bare, "rev-parse", "kb-data"); got != gitHere(t, k.Root, "rev-parse", "HEAD") {
		t.Fatalf("remote kb-data %s is not the KB HEAD", got)
	}
	if got := gitHere(t, bare, "rev-parse", "main"); got != mainBefore {
		t.Fatal("the remote default branch moved")
	}
	s := k.GitStatusSnapshot()
	if s.State != "clean" || s.RemoteDefaultBranch != "kb-data" || s.BranchSource != "config" {
		t.Fatalf("status = %+v", s)
	}
}

// #903 WP4: a KB checked out on another branch than the branch: named on the
// default is refused, the message names the branch and the settings key, and
// nothing reaches the remote.
func TestConfiguredBranch_OtherBranchRefused(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	base := filepath.Dir(k.Root)
	commitSettingsOnDefault(t, bare, base, "kb-data")
	// The KB stays on main, which is not the configured branch kb-data.

	_, err := k.SyncIn()
	if !errors.Is(err, ErrBranchDiverged) {
		t.Fatalf("SyncIn err = %v, want ErrBranchDiverged", err)
	}
	for _, want := range []string{`"main"`, `"kb-data"`, "branch:", k.Root} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %s", err, want)
		}
	}
	if strings.Contains(err.Error(), "Merge") {
		t.Fatalf("error %q suggests a merge on the remote", err)
	}
	if err := k.WriteFileAtomic("data/b.md", []byte("b\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("write b"); err != nil {
		t.Fatalf("CommitOp: %v", err)
	}
	if err := k.SyncOut(); !errors.Is(err, ErrBranchDiverged) {
		t.Fatalf("SyncOut err = %v, want ErrBranchDiverged", err)
	}
	if heads := remoteHeads(t, bare); !strings.Contains(heads, "refs/heads/main") || strings.Contains(heads, "kb-data") {
		t.Fatalf("remote heads = %q, want main without kb-data", heads)
	}
	if s := k.GitStatusSnapshot(); s.State != "degraded" {
		t.Fatalf("status = %+v", s)
	}
}

// Without branch: on the default, the status says the branch comes from the
// remote (D264).
func TestConfiguredBranch_UnsetReportsRemoteSource(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, _ := remoteWithDefault(t)
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	if s := k.GitStatusSnapshot(); s.BranchSource != "remote" || s.RemoteDefaultBranch != "main" {
		t.Fatalf("status = %+v", s)
	}
}

// BranchFromSettings reads branch: and ignores everything else (#903 WP4).
func TestBranchFromSettings(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"branch: kb-data\n":                    "kb-data",
		"branch: team/kb\nwrite_gate: error\n": "team/kb",
		"write_gate: error\n":                  "",
		"":                                     "",
		"branch:\n":                            "",
		"not: a mapping\n- x\n":                "",
	}
	for in, want := range cases {
		if got := BranchFromSettings([]byte(in)); got != want {
			t.Errorf("BranchFromSettings(%q) = %q, want %q", in, got, want)
		}
	}
}
