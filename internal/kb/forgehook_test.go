package kb

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// installForgeHook puts the shared forge-rule hook (test/forge/pre-receive.sh,
// D996) on a bare remote. With allowed emails it also writes the allow-list the
// hook reads: GitLab's member check. internal/mcpserver carries a copy of this
// helper (two small copies are fine until the fixtures are consolidated).
func installForgeHook(t *testing.T, bare string, allowed ...string) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "test", "forge", "pre-receive.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bare, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bare, "hooks", "pre-receive"), src, 0o755); err != nil {
		t.Fatal(err)
	}
	if len(allowed) > 0 {
		list := "# who the forge counts as a member\n" + strings.Join(allowed, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(bare, "hooks", "allowed-identities"), []byte(list), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// protectBranch makes the bare remote refuse every push the way a protected
// branch does, with the wording of message.
func protectBranch(t *testing.T, bare, message string) {
	t.Helper()
	installForgeHook(t, bare)
	if err := os.WriteFile(filepath.Join(bare, "hooks", "protected-branch"), []byte("*\n"+message+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitAs runs git in dir with the given author and committer emails.
func gitAs(t *testing.T, dir, author, committer string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=A", "GIT_AUTHOR_EMAIL="+author,
		"GIT_COMMITTER_NAME=C", "GIT_COMMITTER_EMAIL="+committer,
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// The hook is the one place the rule lives, so it is tested itself: it checks
// the author and the committer separately, every commit of a merge and of a
// branch that is new to the remote, and it runs under the bundled sh on Windows.
func TestForgeHook(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	const ada, bob = "ada@example.test", "bob@example.test"
	bare := filepath.Join(t.TempDir(), "remote.git")
	gitHere(t, filepath.Dir(bare), "init", "--bare", bare)
	installForgeHook(t, bare, ada)
	work := filepath.Join(t.TempDir(), "work")
	gitHere(t, filepath.Dir(work), "init", "-b", "main", work)

	commit := func(author, committer, file string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(work, file), []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := gitAs(t, work, author, committer, "add", file); err != nil {
			t.Fatalf("add: %v\n%s", err, out)
		}
		if out, err := gitAs(t, work, author, committer, "commit", "-m", file); err != nil {
			t.Fatalf("commit: %v\n%s", err, out)
		}
	}
	push := func(refspec string) (string, error) { return gitAs(t, work, ada, ada, "push", bare, refspec) }
	refused := func(what, out string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(out, "GitLab: ") || !strings.Contains(out, "is not a member of team") {
			t.Errorf("%s: err=%v out=%q, want the member refusal", what, err, out)
		}
	}

	commit(ada, ada, "a")
	if out, err := push("main"); err != nil { // a new branch: the old id is all zeros
		t.Fatalf("a member's first push: %v\n%s", err, out)
	}
	commit(ada, bob, "b") // the author is a member, the committer is not
	out, err := push("main")
	refused("committer outside the list", out, err)
	gitHere(t, work, "reset", "--hard", "HEAD~1")
	commit(bob, ada, "c") // and the other way round
	out, err = push("main")
	refused("author outside the list", out, err)
	gitHere(t, work, "reset", "--hard", "HEAD~1")

	// A merge commit: the foreign commit comes in through the merged side.
	gitHere(t, work, "checkout", "-b", "side")
	commit(bob, bob, "d")
	gitHere(t, work, "checkout", "main")
	commit(ada, ada, "e")
	if out, err := gitAs(t, work, ada, ada, "merge", "--no-ff", "-m", "merge", "side"); err != nil {
		t.Fatalf("merge: %v\n%s", err, out)
	}
	out, err = push("main")
	refused("merge bringing in a non-member", out, err)

	// A new branch whose only new commit is a non-member's.
	gitHere(t, work, "checkout", "side")
	out, err = push("side")
	refused("new branch with a non-member commit", out, err)

	// Everything allowed once the list says so; no list at all allows all.
	installForgeHook(t, bare, ada, bob)
	if out, err := push("main"); err != nil {
		t.Fatalf("push with both members listed: %v\n%s", err, out)
	}
	if err := os.Remove(filepath.Join(bare, "hooks", "allowed-identities")); err != nil {
		t.Fatal(err)
	}
	if out, err := push("side"); err != nil {
		t.Fatalf("push with no allow-list: %v\n%s", err, out)
	}
}
