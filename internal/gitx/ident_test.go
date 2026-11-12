package gitx

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

func TestGlobalIdentNeverCarriesAPerFolderIdentity(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping gitx tests")
	}
	home := isolateGitIdentity(t)
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	os.Unsetenv("GIT_CONFIG_GLOBAL")
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	slash := func(name string) string { return filepath.ToSlash(filepath.Join(home, name)) }
	const person = "[user]\n\tname = Home Person\n\temail = home@example.test\n"
	const homePerson = "Home Person <home@example.test>"
	included := slash("id.gitconfig")
	write(included, "[user]\n\tname = Included Person\n\temail = inc@example.test\n")
	// An included file that changes how git connects, never who it names.
	sshOnly := slash("ssh.gitconfig")
	write(sshOnly, "[core]\n\tsshCommand = ssh -i /key\n")
	authorOnly := slash("author.gitconfig")
	write(authorOnly, "[author]\n\temail = author@example.test\n")
	nestedCond := slash("nested-cond.gitconfig")
	write(nestedCond, "[includeIf \"gitdir:~/deeper/\"]\n\tpath = "+sshOnly+"\n")
	nestedUncond := slash("nested-uncond.gitconfig")
	write(nestedUncond, "[include]\n\tpath = "+included+"\n")
	cond := func(condition, path string) string {
		return "[includeIf \"" + condition + "\"]\n\tpath = " + path + "\n"
	}
	// A relative includeIf path lives in a file included from another directory
	// and resolves against that file's directory, never the global file's: the
	// same name sits in both, one harmless and one setting a user.
	write(filepath.Join(home, "rel.gitconfig"), "[user]\n\temail = decoy@example.test\n")
	write(filepath.Join(home, "harmless", "rel.gitconfig"), "[core]\n\tsshCommand = ssh -i /key\n")
	write(filepath.Join(home, "harmless", "inc.gitconfig"), cond("gitdir:~/work/", "rel.gitconfig"))
	write(filepath.Join(home, "setter", "rel.gitconfig"), "[user]\n\temail = inc@example.test\n")
	write(filepath.Join(home, "setter", "inc.gitconfig"), cond("gitdir:~/work/", "rel.gitconfig"))
	// The include path names a directory.
	if err := os.MkdirAll(filepath.Join(home, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, global, xdg, want string
		perFolder               bool
	}{
		{"plain", person, "", homePerson, false},
		{"unconditional include", "[include]\n\tpath = " + included + "\n", "", "Included Person <inc@example.test>", false},
		{"includeIf", person + cond("gitdir:~/work/", included), "", "", true},
		{"includeIf with no user set", person + cond("gitdir:~/work/", sshOnly), "", homePerson, false},
		{"includeIf with no user set, ~ path", person + cond("gitdir:~/work/", "~/ssh.gitconfig"), "", homePerson, false},
		{"includeIf with no user set, relative path", person + cond("gitdir:~/work/", "ssh.gitconfig"), "", homePerson, false},
		{"includeIf with no user set, space in the condition", person + cond("gitdir:~/my work/", sshOnly), "", homePerson, false},
		{"includeIf with a user set, space in the condition", person + cond("gitdir:~/my work/", included), "", "", true},
		{"includeIf setting only author.email", person + cond("gitdir:~/work/", authorOnly), "", "", true},
		{"includeIf relative path in a file included from another directory, harmless there",
			person + "[include]\n\tpath = " + slash("harmless/inc.gitconfig") + "\n", "", homePerson, false},
		{"includeIf relative path in a file included from another directory, sets a user there",
			person + "[include]\n\tpath = " + slash("setter/inc.gitconfig") + "\n", "", "", true},
		{"includeIf to a directory", person + cond("gitdir:~/work/", slash("adir")), "", "", true},
		{"includeIf to a missing file", person + cond("gitdir:~/work/", slash("missing.gitconfig")), "", "", true},
		{"includeIf to a file that includes a user", person + cond("gitdir:~/work/", nestedUncond), "", "", true},
		{"includeIf to a file with an includeIf", person + cond("gitdir:~/work/", nestedCond), "", "", true},
		{"includeIf key that is not a path", person + "[includeIf \"gitdir:~/work/\"]\n\tsomething = else\n", "", "", true},
		{"one harmless and one identity", person + cond("gitdir:~/a/", sshOnly) + cond("gitdir:~/b/", included), "", "", true},
		{"includeIf in the XDG file, no user set", "", person + cond("gitdir:~/work/", sshOnly), homePerson, false},
		{"includeIf in the XDG file, a user set", "", person + cond("gitdir:~/work/", included), "", true},
		{"name only", "[user]\n\tname = Home Person\n", "", "", false},
		{"invalid", "[user]\n\tname = Home <Person>\n\temail = home@example.test\n", "", "", false},
		{"none", "", "", "", false},
	}
	// A file git cannot read exits 1 as a file with nothing to find does.
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		unreadable := filepath.Join(home, "unreadable.gitconfig")
		write(unreadable, "[user]\n\temail = hidden@example.test\n")
		if err := os.Chmod(unreadable, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(unreadable, 0o600) })
		cases = append(cases, struct {
			name, global, xdg, want string
			perFolder               bool
		}{"includeIf to a file that cannot be read", person + cond("gitdir:~/work/", filepath.ToSlash(unreadable)), "", "", true})
	}
	for _, tc := range cases {
		// git config --global reads ~/.gitconfig, or the XDG file when there is
		// none: a test of the XDG file leaves ~/.gitconfig out.
		gitconfig, xdg := filepath.Join(home, ".gitconfig"), filepath.Join(home, "git", "config")
		os.Remove(gitconfig)
		os.Remove(xdg)
		if tc.xdg != "" {
			write(xdg, tc.xdg)
		} else {
			write(gitconfig, tc.global)
		}
		t.Setenv("GIT_AUTHOR_NAME", "Process Person")
		t.Setenv("GIT_AUTHOR_EMAIL", "process@example.test")
		if got, perFolder := GlobalIdent(); got != tc.want || perFolder != tc.perFolder {
			t.Errorf("%s: GlobalIdent() = %q, %v, want %q, %v", tc.name, got, perFolder, tc.want, tc.perFolder)
		}
	}
}

// TestResolveIncludePath: a relative include path is relative to the file that
// includes it; on Windows a rooted path with no drive is not resolved at all.
func TestResolveIncludePath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.ToSlash(filepath.Join(dir, "sub", "main.gitconfig"))
	if p, ok := resolveIncludePath(file, "rel.gitconfig"); !ok || p != filepath.Join(dir, "sub", "rel.gitconfig") {
		t.Errorf("relative: got %q, %v", p, ok)
	}
	abs := filepath.ToSlash(filepath.Join(dir, "abs.gitconfig"))
	if p, ok := resolveIncludePath(file, abs); !ok || p != filepath.Join(dir, "abs.gitconfig") {
		t.Errorf("absolute: got %q, %v", p, ok)
	}
	if runtime.GOOS == "windows" {
		for _, v := range []string{"/etc/work.gitconfig", `\x`, "/c/Users/user/work.gitconfig"} {
			if p, ok := resolveIncludePath(file, v); ok {
				t.Errorf("%q resolved to %q, want it unresolved", v, p)
			}
		}
	}
}

// TestGlobalIdentBudgetIsPerCommand: the includeIf files add a git command
// each, and a host where every command is slow must not run out of one shared
// budget half-way and report a per-folder identity that is not there.
func TestGlobalIdentBudgetIsPerCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell to stand in for git")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not in PATH")
	}
	dir := t.TempDir()
	const includes = 4
	var keys, origin strings.Builder
	for i := 0; i < includes; i++ {
		f := filepath.Join(dir, "inc"+string(rune('a'+i))+".gitconfig")
		if err := os.WriteFile(f, []byte("[core]\n\tsshCommand = ssh\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		rec := "includeif.gitdir:~/" + string(rune('a'+i)) + "/.path\n" + filepath.ToSlash(f) + "\x00"
		keys.WriteString(rec)
		origin.WriteString("file:" + filepath.ToSlash(filepath.Join(dir, "gitconfig")) + "\x00" + rec)
	}
	for name, content := range map[string]string{"keys": keys.String(), "origin": origin.String()} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\nsleep 0.3\ncase \"$*\" in\n" +
		"*--show-origin*) cat '" + dir + "/origin';;\n" +
		"*--file*) exit 1;;\n" +
		"*--get-regexp*) cat '" + dir + "/keys';;\n" +
		"*user.name*) echo Home Person;;\n" +
		"*user.email*) echo home@example.test;;\n" +
		"*) exit 1;;\nesac\n"
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// 4 includes take 8 commands of 0.3s (keys, origin, 4 files, name, email):
	// about 2.4s in all, more than the budget below if it is shared, while each
	// command alone stays far under it even when a fresh exec is slow under load.
	old := globalIdentCallTimeout
	globalIdentCallTimeout = 2 * time.Second
	t.Cleanup(func() { globalIdentCallTimeout = old })
	if got, perFolder := GlobalIdent(); got != "Home Person <home@example.test>" || perFolder {
		t.Errorf("GlobalIdent() = %q, %v, want the global identity", got, perFolder)
	}
}
