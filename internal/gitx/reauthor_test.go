package gitx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// A commit the old Surveyor authored as surveyor/<id> at surveyor.invalid
// keeps its attribution when a reauthor replaces the author: it moves to the
// Cartographer-Surveyor trailer, which the readers show as the same author.
func TestReauthorCommits_OldSurveyorAuthorBecomesATrailer(t *testing.T) {
	t.Parallel()
	dir := realignRepo(t)
	commitAs(t, dir, "a.txt", "one\n\nCartographer-Host: h\nCartographer-Token: tok", "surveyor/docs-night/docs-night-0123456789ab", "surveyor@surveyor.invalid", "surveyor/docs-night/docs-night-0123456789ab", "surveyor@surveyor.invalid")
	commitAs(t, dir, "b.txt", "two", "surveyor/p/r", "surveyor@surveyor.invalid", "surveyor/p/r", "surveyor@surveyor.invalid")
	commitAs(t, dir, "c.txt", "three", "Grace", "grace@example.test", "Grace", "grace@example.test")
	if _, err := ReauthorCommits(dir, "origin/main", "Ada", "ada@example.test"); err != nil {
		t.Fatalf("ReauthorCommits: %v", err)
	}
	got := mustGit(t, dir, "log", "--reverse", "--format=%an|%(trailers:key=Cartographer-Surveyor,valueonly,separator=%x2c)", "origin/main..HEAD")
	var authors []string
	for _, l := range strings.Split(got, "\n") {
		authors = append(authors, strings.TrimSpace(l))
	}
	want := []string{"Ada|docs-night/docs-night-0123456789ab", "Ada|p/r", "Ada|"}
	if strings.Join(authors, ",") != strings.Join(want, ",") {
		t.Errorf("authors and trailers = %q, want %q", authors, want)
	}
	if !strings.Contains(mustGit(t, dir, "log", "-3", "--format=%B"), "Cartographer-Token: tok\nCartographer-Surveyor: docs-night/") {
		t.Error("the trailer is not in the existing trailer paragraph")
	}
}

// The readers show a commit with the trailer as the Surveyor's author whatever
// its git author is, and an older Surveyor commit by its own name.
func TestSurveyorAuthor(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ author, id, want string }{
		{"Ada", "p/r", "surveyor/p/r"},
		{"surveyor/p/r", "", "surveyor/p/r"},
		{"Ada", "", "Ada"},
		{"Ada", "no-slash", "Ada"},
		{"Ada", "a/b c", "Ada"},
		{"Ada", "a/b\nCartographer-Token: x", "Ada"},
	} {
		if got := SurveyorAuthor(c.author, c.id); got != c.want {
			t.Errorf("SurveyorAuthor(%q, %q) = %q, want %q", c.author, c.id, got, c.want)
		}
	}
}

// LogNameStatus reads the trailer into the author (last value wins, like the
// Token) and leaves the Reason after it intact.
func TestLogNameStatusSurveyorTrailerIsTheAuthor(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second).Add(time.Minute)
	for i, msg := range []string{
		"concept_write: one\n\nReason: why\nCartographer-Surveyor: p/r-1\nCartographer-Token: tok",
		"concept_write: two\n\nCartographer-Surveyor: forged/x\nCartographer-Surveyor: p/r-2",
		"concept_write: three\n\nCartographer-Surveyor: not a valid id",
	} {
		if err := os.WriteFile(filepath.Join(dir, "f.md"), []byte(fmt.Sprintf("v%d\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		if err := Commit(dir, msg, "Ada", "ada@example.test", "GIT_AUTHOR_DATE="+at, "GIT_COMMITTER_DATE="+at); err != nil {
			t.Fatal(err)
		}
	}
	commits, err := LogNameStatus(dir, base.Add(-time.Second))
	if err != nil || len(commits) != 3 {
		t.Fatalf("LogNameStatus = %d commits, err %v", len(commits), err)
	}
	if commits[0].Author != "Ada" || commits[1].Author != "surveyor/p/r-2" || commits[2].Author != "surveyor/p/r-1" || commits[2].Reason != "why" || commits[2].Token != "tok" {
		t.Errorf("commits = %+v", commits)
	}
}
