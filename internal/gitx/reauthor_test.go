package gitx

import (
	"errors"
	"strings"
	"testing"
)

// Every commit above the upstream becomes the configured identity's, author and
// committer, token-authored ones included; message, tree and both dates stay
// (D1063).
func TestReauthorCommits_RewritesEveryCommitAhead(t *testing.T) {
	t.Parallel()
	dir := realignRepo(t)
	commitAs(t, dir, "a.txt", "one\n\nCartographer-Token: tok", "tok-1", "cartographer@host", "tok-1", "cartographer@host")
	commitAs(t, dir, "b.txt", "two", "Cartographer", "cartographer@host", "Cartographer", "cartographer@host")
	commitAs(t, dir, "c.txt", "three", "Ada", "ada@example.test", "Ada", "ada@example.test")
	treeBefore := mustGit(t, dir, "rev-parse", "HEAD^{tree}")
	datesBefore := mustGit(t, dir, "log", "--reverse", "--format=%ad|%cd", "--date=raw", "origin/main..HEAD")
	msgBefore := mustGit(t, dir, "log", "--reverse", "--format=%B", "origin/main..HEAD")
	base := mustGit(t, dir, "rev-parse", "origin/main")

	done, err := ReauthorCommits(dir, "origin/main", "Ada", "ada@example.test")
	if err != nil {
		t.Fatalf("ReauthorCommits: %v", err)
	}
	if len(done) != 3 {
		t.Fatalf("rewrote %d commits, want 3 (the last was already right but follows moved parents): %v", len(done), done)
	}
	got := identities(t, dir, "origin/main..HEAD")
	want := "one\n\nCartographer-Token: tok: Ada <ada@example.test> | Ada <ada@example.test>"
	if !strings.Contains(got, "Ada <ada@example.test> | Ada <ada@example.test>") || strings.Contains(got, "cartographer@host") {
		t.Errorf("identities = %q, want every commit by Ada (e.g. %q)", got, want)
	}
	if mustGit(t, dir, "rev-parse", "HEAD^{tree}") != treeBefore {
		t.Error("the tree changed")
	}
	if mustGit(t, dir, "log", "--reverse", "--format=%ad|%cd", "--date=raw", "origin/main..HEAD") != datesBefore {
		t.Error("a date changed")
	}
	if mustGit(t, dir, "log", "--reverse", "--format=%B", "origin/main..HEAD") != msgBefore {
		t.Error("a message changed (the trailers are the audit of the real writer)")
	}
	if mustGit(t, dir, "rev-parse", "origin/main") != base {
		t.Error("the upstream moved")
	}
	if n := mustGit(t, dir, "rev-list", "--count", "origin/main..HEAD"); n != "3" {
		t.Errorf("%s commits ahead, want 3", n)
	}
}

// Commits that already carry the identity are left alone.
func TestReauthorCommits_AlreadyRightIsNoOp(t *testing.T) {
	t.Parallel()
	dir := realignRepo(t)
	commitAs(t, dir, "a.txt", "one", "Ada", "ada@example.test", "Ada", "ada@example.test")
	before := mustGit(t, dir, "rev-parse", "HEAD")
	done, err := ReauthorCommits(dir, "origin/main", "Ada", "ada@example.test")
	if err != nil || len(done) != 0 {
		t.Fatalf("done=%v err=%v, want nothing", done, err)
	}
	if mustGit(t, dir, "rev-parse", "HEAD") != before {
		t.Error("HEAD moved")
	}
}

// A merge in the range is refused and nothing changes.
func TestReauthorCommits_MergeIsNotLinear(t *testing.T) {
	t.Parallel()
	dir := realignRepo(t)
	mustGit(t, dir, "checkout", "-q", "-b", "side")
	commitAs(t, dir, "s.txt", "side", "Bob", "bob@example.test", "Bob", "bob@example.test")
	mustGit(t, dir, "checkout", "-q", "main")
	commitAs(t, dir, "m.txt", "main", "Bob", "bob@example.test", "Bob", "bob@example.test")
	mustGit(t, dir, "-c", "user.name=Bob", "-c", "user.email=bob@example.test", "merge", "-q", "--no-ff", "-m", "merge", "side")
	before := mustGit(t, dir, "rev-parse", "HEAD")
	done, err := ReauthorCommits(dir, "origin/main", "Ada", "ada@example.test")
	if !errors.Is(err, ErrNotLinear) || len(done) != 0 {
		t.Fatalf("done=%v err=%v, want ErrNotLinear", done, err)
	}
	if mustGit(t, dir, "rev-parse", "HEAD") != before {
		t.Error("HEAD moved")
	}
}

// The branch moves with a compare-and-swap update-ref whose old value is the
// HEAD read first: this pins the primitive it relies on, a swap against a HEAD
// that has moved fails instead of dropping the newer commit.
func TestReauthorCommits_CompareAndSwap(t *testing.T) {
	t.Parallel()
	dir := realignRepo(t)
	commitAs(t, dir, "a.txt", "one", "Bob", "bob@example.test", "Bob", "bob@example.test")
	old := mustGit(t, dir, "rev-parse", "HEAD")
	// A wrong expected value is what a racing writer produces.
	commitAs(t, dir, "b.txt", "two", "Bob", "bob@example.test", "Bob", "bob@example.test")
	if out, err := runGit(dir, "update-ref", "HEAD", old, "0000000000000000000000000000000000000001"); err == nil {
		t.Fatalf("update-ref with a stale old value succeeded: %s", out)
	}
	head := mustGit(t, dir, "rev-parse", "HEAD")
	if head == old {
		t.Fatal("HEAD moved back")
	}
}

func TestReauthorCommits_NeedsAnIdentity(t *testing.T) {
	t.Parallel()
	if _, err := ReauthorCommits(realignRepo(t), "origin/main", "", ""); err == nil {
		t.Error("an empty identity was accepted")
	}
}

func TestLocalCommitsAndUnpublishedCount(t *testing.T) {
	t.Parallel()
	dir := realignRepo(t)
	commitAs(t, dir, "a.txt", "one", "Bob", "bob@example.test", "Cat", "cat@example.test")
	cs, err := LocalCommits(dir, "origin/main")
	if err != nil || len(cs) != 1 || cs[0].Author != "Bob" || cs[0].CommitterEmail != "cat@example.test" || cs[0].Subject != "one" {
		t.Fatalf("LocalCommits = %+v, %v", cs, err)
	}
	if in, un, err := UnpublishedCommitCount(dir, "origin/main"); err != nil || in != 1 || un != 1 {
		t.Errorf("count = %d/%d, %v, want 1/1", in, un, err)
	}
	// The commit reachable from another remote ref is published there.
	mustGit(t, dir, "update-ref", "refs/remotes/other/x", "HEAD")
	if in, un, _ := UnpublishedCommitCount(dir, "origin/main"); in != 1 || un != 0 {
		t.Errorf("count = %d/%d, want 1/0", in, un)
	}
}

func TestIsIdentityRejection(t *testing.T) {
	t.Parallel()
	yes := []string{
		"remote: GitLab: ada@example.test is not a member of team\n ! [remote rejected] main -> main (pre-receive hook declined)",
		"remote: GitLab: Author 'x@example.test' is not a member of team",
		"remote: GitLab: Committer 'x@example.test' is not a member of team",
		"remote: error: GH007: Your push would publish a private email address.",
	}
	no := []string{
		"remote: error: GH006: Protected branch update failed for refs/heads/main.",
		"remote: GitLab: You are not allowed to push code to protected branches on this project.",
		"! [remote rejected] main -> main (pre-receive hook declined)",
		"! [rejected] main -> main (non-fast-forward)",
		"Permission denied (publickey).",
		"remote: You are not a member of the project",
		"remote: error: you are not allowed to push to this repository (committer email check disabled)",
		// A protected-branch refusal that happens to mention a member.
		"remote: error: GH006: Protected branch update failed. author is not a member of team",
	}
	for _, s := range yes {
		if !IsIdentityRejection(s) {
			t.Errorf("%q is an identity refusal", s)
		}
	}
	for _, s := range no {
		if IsIdentityRejection(s) {
			t.Errorf("%q is not an identity refusal", s)
		}
	}
}
