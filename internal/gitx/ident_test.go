package gitx

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkTreeIdentFollowsTheRepository: the identity is the one git applies
// in that repository, an includeIf gitdir: one included; outside any work
// tree there is none, though a global identity exists, and GIT_DIR in the
// environment does not move the question to another repository.
func TestWorkTreeIdentFollowsTheRepository(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}
	home := isolateGitIdentity(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", root)
	work, personal, outside := filepath.Join(root, "work"), filepath.Join(root, "personal"), filepath.Join(root, "outside")
	for _, d := range []string{work, personal, filepath.Join(work, "docs"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{work, personal} {
		if err := Init(d); err != nil {
			t.Fatal(err)
		}
	}
	// The pattern is built from the path git reports, so it matches on every
	// platform the way git compares it.
	gitDir, err := runGit(work, "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatal(err)
	}
	workInclude := filepath.Join(home, "work.gitconfig")
	global := "[user]\n\tname = Home Person\n\temail = home@example.test\n" +
		"[includeIf \"gitdir:" + path.Dir(strings.TrimSpace(gitDir)) + "/\"]\n\tpath = " + filepath.ToSlash(workInclude) + "\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(global), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workInclude, []byte("[user]\n\tname = Work Person\n\temail = work@example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for dir, want := range map[string]string{
		work:                          "Work Person <work@example.test>",
		filepath.Join(work, "docs"):   "Work Person <work@example.test>",
		personal:                      "Home Person <home@example.test>",
		outside:                       "",
		filepath.Join(work, ".git"):   "",
		filepath.Join(root, "absent"): "",
	} {
		if got := WorkTreeIdent(dir); got != want {
			t.Errorf("WorkTreeIdent(%s) = %q, want %q", dir, got, want)
		}
	}

	t.Setenv("GIT_DIR", filepath.Join(personal, ".git"))
	if got := WorkTreeIdent(work); got != "Work Person <work@example.test>" {
		t.Errorf("with GIT_DIR naming another repository, WorkTreeIdent(work) = %q", got)
	}
}

// TestWorkTreeIdentNeverGuesses: a name with no email configured gives no
// identity, not the user@host git would otherwise make up or the EMAIL
// variable; the GIT_AUTHOR_* variables still apply, as they do to a commit.
func TestWorkTreeIdentNeverGuesses(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}
	home := isolateGitIdentity(t)
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = Name Only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := Init(repo); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EMAIL", "guess@example.test")
	if got := WorkTreeIdent(repo); got != "" {
		t.Errorf("WorkTreeIdent with no user.email = %q, want none", got)
	}
	t.Setenv("GIT_AUTHOR_EMAIL", "env@example.test")
	if got := WorkTreeIdent(repo); got != "Name Only <env@example.test>" {
		t.Errorf("WorkTreeIdent with GIT_AUTHOR_EMAIL = %q, want it applied", got)
	}
}
