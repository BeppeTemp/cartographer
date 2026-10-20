package gitx

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// realignRepo is a repository with one upstream commit and a "origin/main"
// ref pointing at it, ready for local commits above it.
func realignRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q", "-b", "main")
	mustGit(t, dir, "config", "user.name", "Base")
	mustGit(t, dir, "config", "user.email", "base@example.test")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-q", "-m", "base")
	mustGit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	return dir
}

func commitAs(t *testing.T, dir, file, msg, authorName, authorEmail, committerName, committerEmail string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(msg+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "-A")
	cmd := exec.Command("git", "commit", "-q", "-m", msg)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+authorName, "GIT_AUTHOR_EMAIL="+authorEmail,
		"GIT_COMMITTER_NAME="+committerName, "GIT_COMMITTER_EMAIL="+committerEmail)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
}

func identities(t *testing.T, dir, rng string) string {
	t.Helper()
	return mustGit(t, dir, "log", "--reverse", "--format=%s: %an <%ae> | %cn <%ce>", rng)
}

// A local commit whose committer is not its author is re-committed by its
// author, keeping message, tree and author date, with the history above the
// upstream still linear (D950).
func TestRealignCommitters_CommitterBecomesAuthor(t *testing.T) {
	t.Parallel()
	dir := realignRepo(t)
	commitAs(t, dir, "a.txt", "one", "Ada", "ada@example.test", "Cartographer", "cartographer@host")
	commitAs(t, dir, "b.txt", "two", "Ada", "ada@example.test", "Ada", "ada@example.test")
	commitAs(t, dir, "c.txt", "three", "Bob", "bob@example.test", "Cartographer", "cartographer@host")
	dateBefore := mustGit(t, dir, "log", "-1", "--format=%ad", "--date=raw", "HEAD~2")
	treeBefore := mustGit(t, dir, "rev-parse", "HEAD^{tree}")
	n, err := RealignCommitters(dir, "origin/main", "Cartographer", "cartographer@host", "", "")
	// The middle commit is already right but sits on a rewritten parent, so
	// it is recreated too: all three get new SHAs.
	if err != nil || n != 3 {
		t.Fatalf("RealignCommitters = %d, %v, want 3 rewritten", n, err)
	}
	want := "one: Ada <ada@example.test> | Ada <ada@example.test>\n" +
		"two: Ada <ada@example.test> | Ada <ada@example.test>\n" +
		"three: Bob <bob@example.test> | Bob <bob@example.test>"
	if got := identities(t, dir, "origin/main..HEAD"); got != want {
		t.Errorf("identities =\n%s\nwant\n%s", got, want)
	}
	if got := mustGit(t, dir, "rev-parse", "HEAD^{tree}"); got != treeBefore {
		t.Error("the tree must not change")
	}
	if got := mustGit(t, dir, "log", "-1", "--format=%ad", "--date=raw", "HEAD~2"); got != dateBefore {
		t.Errorf("author date %q, want %q", got, dateBefore)
	}
	if got := mustGit(t, dir, "log", "-1", "--format=%P", "origin/main"); strings.Contains(got, "x") {
		t.Error("upstream untouched")
	}
	if st := mustGit(t, dir, "status", "--porcelain"); st != "" {
		t.Errorf("working tree touched: %q", st)
	}
}

// A commit authored by the server is re-authored as the default writer when
// one is given; nothing to do rewrites nothing.
func TestRealignCommitters_ServerAuthoredBecomesTheDefaultWriter(t *testing.T) {
	t.Parallel()
	dir := realignRepo(t)
	commitAs(t, dir, "a.txt", "by server", "Cartographer", "cartographer@host", "Cartographer", "cartographer@host")
	n, err := RealignCommitters(dir, "origin/main", "Cartographer", "cartographer@host", "Ada", "ada@example.test")
	if err != nil || n != 1 {
		t.Fatalf("RealignCommitters = %d, %v, want 1", n, err)
	}
	if got, want := identities(t, dir, "origin/main..HEAD"), "by server: Ada <ada@example.test> | Ada <ada@example.test>"; got != want {
		t.Errorf("identities = %q, want %q", got, want)
	}
	head := mustGit(t, dir, "rev-parse", "HEAD")
	if n, err := RealignCommitters(dir, "origin/main", "Cartographer", "cartographer@host", "Ada", "ada@example.test"); err != nil || n != 0 {
		t.Errorf("second run = %d, %v, want nothing to do", n, err)
	}
	if mustGit(t, dir, "rev-parse", "HEAD") != head {
		t.Error("a run with nothing to do must not move HEAD")
	}
	// Without a default writer a server-authored commit stays as it is.
	commitAs(t, dir, "b.txt", "still server", "Cartographer", "cartographer@host", "Cartographer", "cartographer@host")
	if n, _ := RealignCommitters(dir, "origin/main", "Cartographer", "cartographer@host", "", ""); n != 0 {
		t.Errorf("rewrote %d server commits with no writer to give them", n)
	}
}

// A commit authored "<token id> <server email>" names the token that wrote it:
// it keeps its author whoever is registered as the KB's writer, and only its
// committer is realigned (D950: a token must not be able to take over the
// commits of another).
func TestRealignCommitters_TokenAuthoredKeepsItsAuthor(t *testing.T) {
	t.Parallel()
	dir := realignRepo(t)
	commitAs(t, dir, "a.txt", "by token", "reviewer-bot", "cartographer@host", "Cartographer", "cartographer@host")
	n, err := RealignCommitters(dir, "origin/main", "Cartographer", "cartographer@host", "Ada", "ada@example.test")
	if err != nil || n != 1 {
		t.Fatalf("RealignCommitters = %d, %v, want 1", n, err)
	}
	want := "by token: reviewer-bot <cartographer@host> | reviewer-bot <cartographer@host>"
	if got := identities(t, dir, "origin/main..HEAD"); got != want {
		t.Errorf("identities = %q, want %q", got, want)
	}
}

// The server identity of another host (a pod that has been replaced, another
// instance) is still the server identity.
func TestRealignCommitters_ServerIdentityOfAnotherHost(t *testing.T) {
	t.Parallel()
	dir := realignRepo(t)
	commitAs(t, dir, "a.txt", "by an old pod", "Cartographer", "cartographer@old-pod-7", "Cartographer", "cartographer@old-pod-7")
	n, err := RealignCommitters(dir, "origin/main", "Cartographer", "cartographer@new-pod-9", "Ada", "ada@example.test")
	if err != nil || n != 1 {
		t.Fatalf("RealignCommitters = %d, %v, want 1", n, err)
	}
	if got, want := identities(t, dir, "origin/main..HEAD"), "by an old pod: Ada <ada@example.test> | Ada <ada@example.test>"; got != want {
		t.Errorf("identities = %q, want %q", got, want)
	}
}

// A range with a merge is left alone.
func TestRealignCommitters_MergeInRangeIsLeftAlone(t *testing.T) {
	t.Parallel()
	dir := realignRepo(t)
	mustGit(t, dir, "checkout", "-q", "-b", "side")
	commitAs(t, dir, "s.txt", "side", "Ada", "ada@example.test", "Cartographer", "cartographer@host")
	mustGit(t, dir, "checkout", "-q", "main")
	commitAs(t, dir, "m.txt", "main", "Ada", "ada@example.test", "Cartographer", "cartographer@host")
	cmd := exec.Command("git", "merge", "-q", "--no-ff", "-m", "merge", "side")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=A", "GIT_AUTHOR_EMAIL=a@x.test", "GIT_COMMITTER_NAME=A", "GIT_COMMITTER_EMAIL=a@x.test")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("merge: %v\n%s", err, out)
	}
	if n, err := RealignCommitters(dir, "origin/main", "Cartographer", "cartographer@host", "", ""); !errors.Is(err, ErrNotLinear) || n != 0 {
		t.Errorf("RealignCommitters over a merge = %d, %v, want 0 and ErrNotLinear", n, err)
	}
}
