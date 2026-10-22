package kb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// writeAndCommit commits one new file on the checked-out branch, without pushing.
func writeAndCommit(t *testing.T, k *KB, rel string) {
	t.Helper()
	if err := k.WriteFileAtomic(rel, []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("write " + rel); err != nil {
		t.Fatalf("CommitOp: %v", err)
	}
}

// pushWrite commits a file and pushes it with SyncOut.
func pushWrite(t *testing.T, k *KB, rel string) {
	t.Helper()
	writeAndCommit(t, k, rel)
	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut: %v", err)
	}
}

// cleanRemoteKB is remoteWithDefault with what Init scaffolded committed and
// pushed, so the clone has a clean tree and nothing unpushed.
func cleanRemoteKB(t *testing.T) (*KB, string) {
	t.Helper()
	k, bare := remoteWithDefault(t)
	gitHere(t, k.Root, "add", "-A")
	gitHere(t, k.Root, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", "scaffold")
	gitHere(t, k.Root, "push", "-q", "origin", "main")
	return k, bare
}

// pushCommitToBareNew creates branch on the bare remote, from main, with one
// extra file, the way a teammate would.
func pushCommitToBareNew(t *testing.T, bare, branch string) {
	t.Helper()
	parent := t.TempDir()
	scratch := filepath.Join(parent, "clone")
	gitIn(t, parent, "clone", "-q", bare, scratch)
	gitIn(t, scratch, "checkout", "-q", "-b", branch, "origin/main")
	if err := os.WriteFile(filepath.Join(scratch, "from-remote.md"), []byte("r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, scratch, "add", "from-remote.md")
	gitIn(t, scratch, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", "remote branch")
	gitIn(t, scratch, "push", "-q", "origin", branch)
}

func currentBranch(t *testing.T, k *KB) string {
	t.Helper()
	b, _ := gitx.Branch(k.Root)
	return b
}

// D1093: the server's override beats the KB's own `branch`, which is not read:
// a malformed one cannot refuse the writes of a server that overrides it.
func TestBranchOverride_WinsOverTheKBSetting(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	for name, setting := range map[string]string{"another branch": "team-kb", "malformed": `"has space"`} {
		t.Run(name, func(t *testing.T) {
			k, bare := cleanRemoteKB(t)
			commitBranchSetting(t, bare, "main", setting)
			k.SetBranchOverride("mine")
			if got := k.ConfiguredBranch(); got != "mine" {
				t.Fatalf("ConfiguredBranch = %q before any sync, want mine", got)
			}
			gitHere(t, k.Root, "checkout", "-q", "-b", "mine")
			if _, err := k.SyncIn(); err != nil {
				t.Fatalf("SyncIn: %v", err)
			}
			pushWrite(t, k, "data/a.md")
			if got := gitHere(t, bare, "rev-parse", "mine"); got != gitHere(t, k.Root, "rev-parse", "HEAD") {
				t.Fatalf("remote mine %s is not the KB HEAD", got)
			}
			if heads := remoteHeads(t, bare); !strings.Contains(heads, "refs/heads/mine") || strings.Contains(heads, "team-kb") {
				t.Fatalf("remote heads = %q", heads)
			}
			if s := k.GitStatusSnapshot(); s.BranchSource != "server" || s.RemoteDefaultBranch != "mine" || s.State != "clean" {
				t.Fatalf("status = %+v", s)
			}
		})
	}
}

// Taking the override away hands the branch back to the KB's setting, then to
// the remote's default.
func TestBranchOverride_ClearedFallsBack(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := cleanRemoteKB(t)
	k.SetBranchOverride("mine")
	gitHere(t, k.Root, "checkout", "-q", "-b", "mine")
	pushWrite(t, k, "data/a.md")
	k.SetBranchOverride("")
	if got := k.BranchSource(); got != "remote" {
		t.Fatalf("BranchSource = %q, want remote", got)
	}
	commitBranchSetting(t, bare, "main", "team-kb")
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	if s := k.GitStatusSnapshot(); s.BranchSource != "settings" || s.RemoteDefaultBranch != "team-kb" || s.Branch != "team-kb" {
		t.Fatalf("status = %+v, want the clone moved to the KB's branch", s)
	}
}

// An empty remote: the override resolves at once and the first push creates it,
// not main. Without an override the first push is still main only.
func TestBranchOverride_EmptyRemote(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	k.SetBranchOverride("mine")
	gitHere(t, k.Root, "checkout", "-q", "-b", "mine")
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	pushWrite(t, k, "data/a.md")
	if heads := remoteHeads(t, bare); heads != "refs/heads/mine" {
		t.Fatalf("remote heads = %q, want mine only", heads)
	}
	if s := k.GitStatusSnapshot(); s.BranchSource != "server" || s.State != "clean" {
		t.Fatalf("status = %+v", s)
	}

	// On main, with an override naming another branch, the push is refused and
	// the message names the server key.
	k2, bare2 := setupKBWithRemote(t)
	k2.SetBranchOverride("mine")
	writeAndCommit(t, k2, "data/b.md")
	err := k2.SyncOut()
	if !errors.Is(err, ErrBranchDiverged) || !strings.Contains(err.Error(), "kbs[].branch") {
		t.Fatalf("SyncOut err = %v, want ErrBranchDiverged naming kbs[].branch", err)
	}
	if heads := remoteHeads(t, bare2); heads != "" {
		t.Fatalf("remote heads = %q, want none", heads)
	}

	// No override: main only (unchanged).
	k3, bare3 := setupKBWithRemote(t)
	if err := k3.SyncOut(); err != nil {
		t.Fatalf("SyncOut: %v", err)
	}
	if heads := remoteHeads(t, bare3); heads != "refs/heads/main" {
		t.Fatalf("remote heads = %q, want main only", heads)
	}
}

// D1093: a clean clone, fully pushed, follows a change of the effective branch.
func TestSyncIn_SwitchesACleanClone(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	t.Run("settings change", func(t *testing.T) {
		k, bare := cleanRemoteKB(t)
		if _, err := k.SyncIn(); err != nil {
			t.Fatal(err)
		}
		commitBranchSetting(t, bare, "main", "team-kb")
		if _, err := k.SyncIn(); err != nil {
			t.Fatalf("SyncIn: %v", err)
		}
		if got := currentBranch(t, k); got != "team-kb" {
			t.Fatalf("branch = %q, want team-kb (created from the remote default)", got)
		}
		pushWrite(t, k, "data/a.md")
		if s := k.GitStatusSnapshot(); s.State != "clean" || s.BranchSource != "settings" {
			t.Fatalf("status = %+v", s)
		}
		if heads := remoteHeads(t, bare); !strings.Contains(heads, "refs/heads/team-kb") {
			t.Fatalf("remote heads = %q", heads)
		}
	})
	t.Run("override added then removed", func(t *testing.T) {
		k, bare := cleanRemoteKB(t)
		k.SetBranchOverride("mine")
		if _, err := k.SyncIn(); err != nil {
			t.Fatalf("SyncIn: %v", err)
		}
		if got := currentBranch(t, k); got != "mine" {
			t.Fatalf("branch = %q, want mine", got)
		}
		pushWrite(t, k, "data/a.md")
		k.SetBranchOverride("")
		if _, err := k.SyncIn(); err != nil {
			t.Fatalf("SyncIn: %v", err)
		}
		if got := currentBranch(t, k); got != "main" {
			t.Fatalf("branch = %q, want main (the local branch exists)", got)
		}
		if heads := remoteHeads(t, bare); !strings.Contains(heads, "refs/heads/mine") {
			t.Fatalf("Cartographer deleted a branch: %q", heads)
		}
	})
	t.Run("target only on the remote", func(t *testing.T) {
		k, bare := cleanRemoteKB(t)
		pushCommitToBareNew(t, bare, "mine")
		k.SetBranchOverride("mine")
		if _, err := k.SyncIn(); err != nil {
			t.Fatalf("SyncIn: %v", err)
		}
		if got := currentBranch(t, k); got != "mine" {
			t.Fatalf("branch = %q, want mine", got)
		}
		if _, err := os.Stat(filepath.Join(k.Root, "from-remote.md")); err != nil {
			t.Fatalf("the clone does not carry the remote's mine: %v", err)
		}
	})
}

// D1093: anything that could be lost keeps the clone where it is, degraded, with
// the reason in the message; nothing is touched.
func TestSyncIn_SwitchRefusals(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, k *KB)
		want  string
	}{
		{"dirty tree", func(t *testing.T, k *KB) {
			if err := k.WriteFileAtomic("data/dirty.md", []byte("x\n")); err != nil {
				t.Fatal(err)
			}
		}, "uncommitted changes in "},
		{"unpushed commit", func(t *testing.T, k *KB) { writeAndCommit(t, k, "data/u.md") }, "1 commits unpushed on \"main\""},
		{"branch never pushed", func(t *testing.T, k *KB) { gitHere(t, k.Root, "checkout", "-q", "-b", "scratch") }, `"scratch" was never pushed`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k, bare := cleanRemoteKB(t)
			k.SetBranchOverride("mine")
			c.setup(t, k)
			headBefore := gitHere(t, k.Root, "rev-parse", "HEAD")
			branchBefore := currentBranch(t, k)
			_, err := k.SyncIn()
			if !errors.Is(err, ErrBranchDiverged) {
				t.Fatalf("SyncIn err = %v, want ErrBranchDiverged", err)
			}
			for _, want := range []string{c.want, "kbs[].branch", "not switched"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q lacks %q", err, want)
				}
			}
			if gitHere(t, k.Root, "rev-parse", "HEAD") != headBefore || currentBranch(t, k) != branchBefore {
				t.Fatal("the refused switch moved the clone")
			}
			if heads := remoteHeads(t, bare); heads != "refs/heads/main" {
				t.Fatalf("remote heads = %q", heads)
			}
			if s := k.GitStatusSnapshot(); s.State != "degraded" {
				t.Fatalf("status = %+v", s)
			}
		})
	}
}

// A detached HEAD is never switched (SyncIn does not even run on one, so the
// rule is pinned on the helper).
func TestSwitchToCanonical_DetachedHeadStays(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, _ := cleanRemoteKB(t)
	gitHere(t, k.Root, "checkout", "-q", "--detach")
	if switched, reason := k.switchToCanonical("origin", "", "mine", "main"); switched || reason != "HEAD is detached" {
		t.Fatalf("switchToCanonical = %v, %q", switched, reason)
	}
}

// A target the remote lacks, with no remote default to create it from, is not
// invented from the branch the clone is on.
func TestSwitchToCanonical_NothingToCreateItFrom(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, _ := cleanRemoteKB(t)
	switched, reason := k.switchToCanonical("origin", "main", "mine", "")
	if switched || !strings.Contains(reason, "to create it from") {
		t.Fatalf("switchToCanonical = %v, %q", switched, reason)
	}
	if got := currentBranch(t, k); got != "main" {
		t.Fatalf("branch = %q", got)
	}
}
