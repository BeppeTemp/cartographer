package kb

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// D335: a configured branch missing on a non-empty remote is created by the
// first write; the remote's default branch is left untouched.
func TestConfiguredBranch_FirstWriteCreatesIt(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	mainBefore := gitHere(t, bare, "rev-parse", "main")
	gitHere(t, k.Root, "checkout", "-q", "-b", "kb-data")
	k.ConfiguredBranch = "kb-data"

	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
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
	if up := gitHere(t, k.Root, "rev-parse", "--abbrev-ref", "kb-data@{upstream}"); up != "origin/kb-data" {
		t.Fatalf("upstream = %q, want origin/kb-data", up)
	}
	s := k.GitStatusSnapshot()
	if s.State != "clean" || s.RemoteDefaultBranch != "kb-data" || s.BranchSource != "config" {
		t.Fatalf("status = %+v", s)
	}

	// Once on the remote, the branch is pulled and pushed like any other.
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn after creation: %v", err)
	}
}

// D335: on an empty remote the KB starts on the configured branch and its
// first push creates that branch, not main.
func TestConfiguredBranch_EmptyRemote(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	base := t.TempDir()
	bare := filepath.Join(base, "remote.git")
	gitIn(t, base, "-c", "init.defaultBranch=master", "init", "-q", "--bare", bare)
	root := filepath.Join(base, "kb")
	gitIn(t, base, "clone", "-q", bare, root)
	k, err := InitOnBranch(root, "kb-data")
	if err != nil {
		t.Fatalf("InitOnBranch: %v", err)
	}
	if b, _ := gitx.Branch(root); b != "kb-data" {
		t.Fatalf("branch = %q, want kb-data", b)
	}
	k.GitSync, k.ConfiguredBranch = true, "kb-data"
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut: %v", err)
	}
	if heads := remoteHeads(t, bare); heads != "refs/heads/kb-data" {
		t.Fatalf("remote heads = %q, want kb-data", heads)
	}
}

// D335: a clone on another branch is refused, the message names the
// configured branch and the key, and nothing reaches the remote.
func TestConfiguredBranch_OtherBranchRefused(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	k.ConfiguredBranch = "kb-data"

	_, err := k.SyncIn()
	if !errors.Is(err, ErrBranchDiverged) {
		t.Fatalf("SyncIn err = %v, want ErrBranchDiverged", err)
	}
	for _, want := range []string{`"main"`, `"kb-data"`, "git_branch", k.Root} {
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
	if heads := remoteHeads(t, bare); heads != "refs/heads/main" {
		t.Fatalf("remote heads = %q, want main only", heads)
	}
	if s := k.GitStatusSnapshot(); s.State != "degraded" {
		t.Fatalf("status = %+v", s)
	}
}

// Without the key the status says the branch comes from the remote (D264).
func TestConfiguredBranch_UnsetReportsRemoteSource(t *testing.T) {
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
