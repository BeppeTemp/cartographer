package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// hasGitBinary skips the tests that shell out to git where it is absent.
func hasGitBinary() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func TestValidateKBName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"alpha", false},
		{"alpha-beta", false},
		{"alpha_beta", false},
		{"Alpha123", false},
		{"", true},
		{"alpha/beta", true},
		{"../escape", true},
		{"alpha.beta", true},
		{"alpha beta", true},
		{"alpha:beta", true},
	}
	for _, tc := range cases {
		err := validateKBName(tc.name)
		if tc.wantErr && err == nil {
			t.Errorf("validateKBName(%q) = nil, want error", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("validateKBName(%q) = %v, want nil", tc.name, err)
		}
	}
}

// withNoGuidance stubs printPostCreateGuidanceFn to a no-op for the
// duration of f, so cmdKBCreate tests never reach out over the network
// (real ~/.cartographer.yaml / service config on the test machine).
func withNoGuidance(t *testing.T, f func()) {
	t.Helper()
	orig := printPostCreateGuidanceFn
	printPostCreateGuidanceFn = func(string, bool) {}
	defer func() { printPostCreateGuidanceFn = orig }()
	f()
}

func TestCmdKBCreateScaffold(t *testing.T) {
	dataDir := t.TempDir()

	var code int
	out := withStdout(t, func() {
		withNoGuidance(t, func() {
			code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--no-remote"})
		})
	})
	if code != 0 {
		t.Fatalf("cmdKBCreate = %d, want 0 (output: %s)", code, out)
	}

	kbPath := filepath.Join(dataDir, "alpha")
	for _, rel := range []string{"data/index.md", "data/log.md", ".git"} {
		if _, err := os.Stat(filepath.Join(kbPath, rel)); err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
		}
	}
}

func TestCmdKBCreateSecondRunError(t *testing.T) {
	dataDir := t.TempDir()

	withNoGuidance(t, func() {
		if code := cmdKBCreate([]string{"alpha", "--data", dataDir, "--no-remote"}); code != 0 {
			t.Fatalf("first cmdKBCreate = %d, want 0", code)
		}
	})

	var code int
	withNoGuidance(t, func() {
		code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--no-remote"})
	})
	if code == 0 {
		t.Fatal("second cmdKBCreate = 0, want non-zero (already exists)")
	}
}

func TestCmdKBCreateInvalidName(t *testing.T) {
	dataDir := t.TempDir()

	var code int
	withNoGuidance(t, func() {
		code = cmdKBCreate([]string{"not/valid", "--data", dataDir, "--no-remote"})
	})
	if code == 0 {
		t.Fatal("cmdKBCreate with invalid name = 0, want non-zero")
	}
}

// TestCmdKBCreateRequiresRemoteChoice covers D134's gate: neither flag, and
// both flags, are usage errors (exit 2) that create nothing.
func TestCmdKBCreateRequiresRemoteChoice(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no choice", nil},
		{"both", []string{"--remote", "file:///tmp/x.git", "--no-remote"}},
	}
	for _, tc := range cases {
		dataDir := t.TempDir()
		var code int
		withNoGuidance(t, func() {
			code = cmdKBCreate(append([]string{"alpha", "--data", dataDir}, tc.args...))
		})
		if code != 2 {
			t.Errorf("%s: cmdKBCreate = %d, want 2", tc.name, code)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "alpha")); !os.IsNotExist(err) {
			t.Errorf("%s: KB created despite usage error: %v", tc.name, err)
		}
	}
}

// TestCmdKBCreateNoRemoteLeavesNoOrigin pins the opt-out: a KB is scaffolded,
// and it has no origin configured.
func TestCmdKBCreateNoRemoteLeavesNoOrigin(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping KB create remote test")
	}

	dataDir := t.TempDir()
	var code int
	withNoGuidance(t, func() {
		code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--no-remote"})
	})
	if code != 0 {
		t.Fatalf("cmdKBCreate --no-remote = %d, want 0", code)
	}
	if _, err := gitx.RemoteURL(filepath.Join(dataDir, "alpha"), "origin"); err == nil {
		t.Error("--no-remote KB has an origin remote, want none")
	}
}

// TestCmdKBCreateWithRemotePushes checks the happy path end to end: origin is
// attached and the initial commit is reachable from the remote's branch.
func TestCmdKBCreateWithRemotePushes(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping KB create remote test")
	}

	tmp := t.TempDir()
	bare := filepath.Join(tmp, "alpha.git")
	mustRunGit(t, "", "init", "--bare", bare)
	remote := fileURL(bare)

	dataDir := filepath.Join(tmp, "data")
	var code int
	withNoGuidance(t, func() {
		code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--remote", remote})
	})
	if code != 0 {
		t.Fatalf("cmdKBCreate --remote = %d, want 0", code)
	}

	kbPath := filepath.Join(dataDir, "alpha")
	got, err := gitx.RemoteURL(kbPath, "origin")
	if err != nil {
		t.Fatalf("origin not configured: %v", err)
	}
	if got != remote {
		t.Errorf("origin = %q, want %q", got, remote)
	}
	local, err := gitx.HeadSHA(kbPath)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	pushed, err := gitx.HeadSHAAt(bare, gitx.DefaultBranch)
	if err != nil {
		t.Fatalf("HeadSHAAt(bare, %s): %v", gitx.DefaultBranch, err)
	}
	if pushed != local {
		t.Errorf("remote %s = %s, want local HEAD %s", gitx.DefaultBranch, pushed, local)
	}
}

// TestCmdKBCreateRemoteFailureCleansScaffold pins the rollback invariant: an
// unreachable remote must not leave a mountable KB behind.
// A failed push must not delete the scaffold: the local KB is complete and
// valid, only the push failed, and re-doing it by hand was the reported cost
// (D156). The command still exits non-zero and prints how to finish.
func TestCmdKBCreateRemoteFailureKeepsScaffold(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping KB create remote test")
	}

	dataDir := t.TempDir()
	var code int
	out := withStderr(t, func() {
		withNoGuidance(t, func() {
			code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--remote", "file:///does-not-exist/missing.git"})
		})
	})
	if code == 0 {
		t.Fatal("cmdKBCreate with unreachable remote = 0, want non-zero")
	}
	scaffold := filepath.Join(dataDir, "alpha")
	if _, err := os.Stat(scaffold); err != nil {
		t.Fatalf("scaffold was removed after a failed push: %v", err)
	}
	// It must be a real KB, not a half-written directory.
	if _, err := kb.Open(scaffold); err != nil {
		t.Fatalf("kept scaffold is not a valid KB: %v", err)
	}
	for _, want := range []string{"only the push failed", "push -u origin", "Or remove it"} {
		if !strings.Contains(out, want) {
			t.Errorf("guidance missing %q\n---\n%s", want, out)
		}
	}
}

// seedForgeRemote builds a bare remote the way a forge does for a project
// created with "initialize with README": branch holds commits commits, the
// first writing files; the bare repo's HEAD points at it.
func seedForgeRemote(t *testing.T, tmp, branch string, files []string, commits int) string {
	t.Helper()
	content := map[string]string{}
	for _, f := range files {
		content[f] = "x\n"
	}
	return seedForgeRemoteContent(t, tmp, branch, content, commits)
}

func seedForgeRemoteContent(t *testing.T, tmp, branch string, files map[string]string, commits int) string {
	t.Helper()
	src := filepath.Join(tmp, "forge-src")
	mustRunGit(t, "", "init", "-b", branch, src)
	mustRunGit(t, src, "config", "user.email", "forge@example.com")
	mustRunGit(t, src, "config", "user.name", "Forge")
	for f, body := range files {
		if err := os.WriteFile(filepath.Join(src, f), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustRunGit(t, src, "add", ".")
	mustRunGit(t, src, "commit", "-m", "Initial commit")
	for i := 1; i < commits; i++ {
		mustRunGit(t, src, "commit", "--allow-empty", "-m", fmt.Sprintf("more %d", i))
	}
	bare := filepath.Join(tmp, "forge.git")
	mustRunGit(t, "", "init", "--bare", bare)
	mustRunGit(t, bare, "symbolic-ref", "HEAD", "refs/heads/"+branch)
	mustRunGit(t, src, "push", bare, branch)
	return bare
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func TestCmdKBCreateAdoptsForgeInitialCommit(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	for _, branch := range []string{"main", "master"} {
		t.Run(branch, func(t *testing.T) {
			tmp := t.TempDir()
			bare := seedForgeRemote(t, tmp, branch, []string{"README.md", "LICENSE"}, 1)
			forgeSHA := gitOut(t, bare, "rev-parse", branch)
			// A committer of the process environment must not leak into the
			// rebased commit: the committer is the server (D904, D265).
			t.Setenv("GIT_AUTHOR_NAME", "Kb User")
			t.Setenv("GIT_AUTHOR_EMAIL", "user@example.com")
			t.Setenv("GIT_COMMITTER_NAME", "Someone Else")
			t.Setenv("GIT_COMMITTER_EMAIL", "else@example.com")

			dataDir := filepath.Join(tmp, "data")
			var code int
			out := withStdout(t, func() {
				withNoGuidance(t, func() {
					code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--remote", fileURL(bare)})
				})
			})
			if code != 0 {
				t.Fatalf("cmdKBCreate = %d, want 0", code)
			}
			if !strings.Contains(out, "adopted the forge's initial commit") {
				t.Errorf("stdout lacks the adoption line:\n%s", out)
			}
			kbPath := filepath.Join(dataDir, "alpha")
			if got := gitOut(t, kbPath, "symbolic-ref", "--short", "HEAD"); got != branch {
				t.Errorf("local branch = %q, want %q", got, branch)
			}
			if got := gitOut(t, bare, "rev-list", "--count", branch); got != "2" {
				t.Errorf("remote %s has %s commits, want 2", branch, got)
			}
			if got := gitOut(t, bare, "rev-parse", branch+"^"); got != forgeSHA {
				t.Errorf("parent = %s, want the forge commit %s", got, forgeSHA)
			}
			tree := gitOut(t, bare, "ls-tree", "-r", "--name-only", branch)
			for _, f := range []string{"README.md", "LICENSE", "instructions.md"} {
				if !strings.Contains(tree, f) {
					t.Errorf("remote tree lacks %s:\n%s", f, tree)
				}
			}
			cm := gitOut(t, bare, "log", "-1", "--format=%cn <%ce>", branch)
			name, email := kb.ServerIdentity()
			if cm != name+" <"+email+">" {
				t.Errorf("committer %q, want the server", cm)
			}
			if branch == "master" && strings.Contains(gitOut(t, bare, "branch", "--list"), "main") {
				t.Error("a main branch was pushed beside the forge's master (D264)")
			}
		})
	}
}

func TestCmdKBCreateReadme(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	const desc = "A short project description."
	const handWritten = "# forge\n\nHow we run this repository.\n\n## Usage\n\nRead the notes.\n"
	gitlab, err := os.ReadFile(filepath.Join("..", "..", "internal", "gitx", "testdata", "forge-readmes", "gitlab.md"))
	if err != nil {
		t.Fatal(err)
	}
	gitlabReadme := strings.Replace(string(gitlab), "# kb-a", "# forge", 1)
	cases := []struct {
		name, readme, flag string
		replaced           bool
		subject            string
		wantDesc           bool
	}{
		{"auto github-shaped", "# forge\n\n" + desc + "\n", "", true, "docs: replace the forge's boilerplate README", true},
		{"auto gitlab template", gitlabReadme, "", true, "docs: replace the forge's boilerplate README", false},
		{"auto hand-written", handWritten, "", false, "", false},
		{"keep boilerplate", "# forge\n\n" + desc + "\n", "keep", false, "", false},
		{"generate hand-written", handWritten, "generate", true, "docs: generate the README", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmp := t.TempDir()
			bare := seedForgeRemoteContent(t, tmp, "main", map[string]string{"README.md": c.readme, "LICENSE": "x\n"}, 1)
			forgeSHA := gitOut(t, bare, "rev-parse", "main")
			dataDir := filepath.Join(tmp, "data")
			args := []string{"alpha", "--data", dataDir, "--remote", fileURL(bare)}
			if c.flag != "" {
				args = append(args, "--readme", c.flag)
			}
			var code int
			var stderr string
			stdout := withStdout(t, func() {
				stderr = withStderr(t, func() {
					withNoGuidance(t, func() { code = cmdKBCreate(args) })
				})
			})
			if code != 0 {
				t.Fatalf("cmdKBCreate = %d, stderr:\n%s", code, stderr)
			}
			kbPath := filepath.Join(dataDir, "alpha")
			got := gitOut(t, bare, "show", "main:README.md")
			if gitOut(t, kbPath, "status", "--porcelain") != "" {
				t.Errorf("KB working tree is not clean:\n%s", gitOut(t, kbPath, "status", "--porcelain"))
			}
			if !c.replaced {
				if got != strings.TrimSpace(c.readme) {
					t.Errorf("README changed:\n%s", got)
				}
				if n := gitOut(t, bare, "rev-list", "--count", "main"); n != "2" {
					t.Errorf("remote has %s commits, want 2", n)
				}
				if strings.Contains(stdout, "replaced README.md") {
					t.Errorf("stdout claims a replacement:\n%s", stdout)
				}
				if hint := strings.Contains(stderr, "--readme generate"); hint != (c.flag == "") {
					t.Errorf("hint printed = %v, stderr:\n%s", hint, stderr)
				}
				return
			}
			if !strings.Contains(stdout, "replaced README.md") {
				t.Errorf("stdout lacks the replacement:\n%s", stdout)
			}
			if !strings.HasPrefix(got, "# alpha\n") || !strings.Contains(got, "data/index.md") || strings.Contains(got, "GitLab") {
				t.Errorf("generated README:\n%s", got)
			}
			if strings.Contains(got, desc) != c.wantDesc {
				t.Errorf("description carried = %v, want %v:\n%s", !c.wantDesc, c.wantDesc, got)
			}
			if n := gitOut(t, bare, "rev-list", "--count", "main"); n != "3" {
				t.Errorf("remote has %s commits, want 3", n)
			}
			if gitOut(t, bare, "log", "-1", "--format=%s", "main") != c.subject {
				t.Errorf("subject = %q, want %q", gitOut(t, bare, "log", "-1", "--format=%s", "main"), c.subject)
			}
			// The push was a fast-forward: the forge commit is still an ancestor.
			mustRunGit(t, bare, "merge-base", "--is-ancestor", forgeSHA, "main")
			const idFmt = "--format=%an <%ae> %cn <%ce>"
			if a, b := gitOut(t, bare, "log", "-1", idFmt, "main"), gitOut(t, bare, "log", "-1", idFmt, "main~1"); a != b {
				t.Errorf("extra commit identity %q differs from the rebased commit's %q", a, b)
			}
			k, err := kb.Open(kbPath)
			if err != nil {
				t.Fatal(err)
			}
			findings, err := lint.Run(k, "", false)
			if err != nil || len(findings) != 0 {
				t.Errorf("lint of the new KB: %v, %v", findings, err)
			}
		})
	}
}

// A remote whose README is a symlink must not have the generated README
// written through it: the link may point anywhere on the operator's disk.
func TestCmdKBCreateReadmeRefusesSymlink(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	tmp := t.TempDir()
	victim := filepath.Join(tmp, "victim.txt")
	if err := os.WriteFile(victim, []byte("untouched\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bare := seedForgeRemoteContent(t, tmp, "main", map[string]string{"LICENSE": "x\n"}, 1)
	src := filepath.Join(tmp, "forge-src")
	if err := os.Symlink(victim, filepath.Join(src, "README.md")); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, src, "add", "README.md")
	mustRunGit(t, src, "commit", "--amend", "--no-edit")
	mustRunGit(t, src, "push", "--force", bare, "main")
	dataDir := filepath.Join(tmp, "data")
	var code int
	errOut := withStderr(t, func() {
		withStdout(t, func() {
			withNoGuidance(t, func() {
				code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--remote", fileURL(bare), "--readme", "generate"})
			})
		})
	})
	if code == 0 {
		t.Fatalf("cmdKBCreate = 0, want a refusal; stderr:\n%s", errOut)
	}
	if !strings.Contains(errOut, "not a regular file") {
		t.Errorf("stderr does not name the symlink:\n%s", errOut)
	}
	if got, _ := os.ReadFile(victim); string(got) != "untouched\n" {
		t.Errorf("the symlink target was written: %q", got)
	}
}

func TestCmdKBCreateReadmeUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"--readme", "bogus", "--remote", "file:///nonexistent/forge.git"},
		{"--readme", "generate", "--no-remote"},
	} {
		dataDir := t.TempDir()
		var code int
		errOut := withStderr(t, func() {
			code = cmdKBCreate(append([]string{"alpha", "--data", dataDir}, args...))
		})
		if code != 2 || !strings.Contains(errOut, "--readme") {
			t.Errorf("%v: code=%d stderr=%q, want 2 naming --readme", args, code, errOut)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "alpha")); !os.IsNotExist(err) {
			t.Errorf("%v: KB created: %v", args, err)
		}
	}
}

func TestCmdKBCreateReadmeGenerateOnEmptyRemote(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	tmp := t.TempDir()
	bare := filepath.Join(tmp, "forge.git")
	mustRunGit(t, "", "init", "--bare", bare)
	dataDir := filepath.Join(tmp, "data")
	var code int
	withStdout(t, func() {
		withNoGuidance(t, func() {
			code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--remote", fileURL(bare), "--readme", "generate"})
		})
	})
	if code != 0 {
		t.Fatalf("cmdKBCreate = %d, want 0", code)
	}
	if n := gitOut(t, bare, "rev-list", "--count", "--all"); n != "1" {
		t.Errorf("remote has %s commits, want the scaffold's only", n)
	}
	if tree := gitOut(t, bare, "ls-tree", "-r", "--name-only", "main"); strings.Contains(tree, "README") {
		t.Errorf("a README was written:\n%s", tree)
	}
}

func TestCmdKBCreateForgeCommitCollisionRefused(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	tmp := t.TempDir()
	bare := seedForgeRemote(t, tmp, "main", []string{"README.md", ".gitattributes"}, 1)
	before := gitOut(t, bare, "rev-parse", "main")
	dataDir := filepath.Join(tmp, "data")
	var code int
	errOut := withStderr(t, func() {
		withNoGuidance(t, func() {
			code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--remote", fileURL(bare)})
		})
	})
	if code != 1 || !strings.Contains(errOut, ".gitattributes") {
		t.Errorf("code=%d stderr=%q, want 1 naming .gitattributes", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "alpha")); !os.IsNotExist(err) {
		t.Errorf("local KB still exists: %v", err)
	}
	if got := gitOut(t, bare, "rev-parse", "main"); got != before {
		t.Error("remote changed")
	}
}

func TestCmdKBCreateNonEmptyRemoteRefused(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	tmp := t.TempDir()
	bare := seedForgeRemote(t, tmp, "main", []string{"README.md"}, 2)
	before := gitOut(t, bare, "rev-parse", "main")
	dataDir := filepath.Join(tmp, "data")
	var code int
	errOut := withStderr(t, func() {
		withNoGuidance(t, func() {
			code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--remote", fileURL(bare)})
		})
	})
	if code != 1 || !strings.Contains(errOut, "kb clone") {
		t.Errorf("code=%d stderr=%q, want 1 with a kb clone hint", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "alpha")); !os.IsNotExist(err) {
		t.Errorf("local KB still exists: %v", err)
	}
	if got := gitOut(t, bare, "rev-parse", "main"); got != before {
		t.Error("remote changed")
	}
}

func TestCmdKBCloneForgeInitializedPointsAtCreate(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	tmp := t.TempDir()
	bare := seedForgeRemote(t, tmp, "main", []string{"README.md", "LICENSE"}, 1)
	data := filepath.Join(tmp, "data")
	var code int
	errOut := withStderr(t, func() {
		withNoGuidance(t, func() {
			code = cmdKBClone([]string{fileURL(bare), "--data", data})
		})
	})
	if code != 1 || !strings.Contains(errOut, "kb create") || !strings.Contains(errOut, "--remote") {
		t.Errorf("code=%d stderr=%q, want 1 pointing at kb create --remote", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(data, "forge")); !os.IsNotExist(err) {
		t.Errorf("clone destination left behind: %v", err)
	}
}

func TestCmdKBClone(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping KB clone test")
	}

	tmp := t.TempDir()
	src := kbtest.New(t).Root
	branchOut, err := os.ReadFile(filepath.Join(src, ".git", "HEAD"))
	if err != nil {
		t.Fatalf("read source HEAD: %v", err)
	}
	branch := string(branchOut[len("ref: refs/heads/"):])
	branch = branch[:len(branch)-1]

	bare := filepath.Join(tmp, "wiki-kb.git")
	mustRunGit(t, "", "init", "--bare", bare)
	mustRunGit(t, src, "remote", "add", "origin", bare)
	mustRunGit(t, src, "push", "origin", branch+":"+branch)
	mustRunGit(t, bare, "symbolic-ref", "HEAD", "refs/heads/"+branch)

	data := filepath.Join(tmp, "data")
	remote := fileURL(bare)
	withNoGuidance(t, func() {
		if code := cmdKBClone([]string{remote, "--data", data}); code != 0 {
			t.Fatalf("cmdKBClone = %d, want 0", code)
		}
	})

	mounted := filepath.Join(data, "wiki-kb")
	if _, err := kb.Open(mounted); err != nil {
		t.Fatalf("kb.Open(mounted clone): %v", err)
	}
	for _, rel := range []string{"data/index.md", "data/log.md", ".git"} {
		if _, err := os.Stat(filepath.Join(mounted, rel)); err != nil {
			t.Errorf("mounted clone missing %s: %v", rel, err)
		}
	}
	if code := cmdKBClone([]string{remote, "--data", data}); code == 0 {
		t.Fatal("second cmdKBClone = 0, want already-exists error")
	}
}

func TestCmdKBCloneFailureCleansPartialDestination(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping KB clone test")
	}

	data := filepath.Join(t.TempDir(), "data")
	withNoGuidance(t, func() {
		if code := cmdKBClone([]string{"file:///does-not-exist/missing.git", "--data", data}); code == 0 {
			t.Fatal("cmdKBClone(missing remote) = 0, want error")
		}
	})
	if _, err := os.Stat(filepath.Join(data, "missing")); !os.IsNotExist(err) {
		t.Fatalf("failed clone destination still exists: %v", err)
	}
}

func TestCmdKBCloneRejectsNonOKFRemoteAndCleansDestination(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping KB clone test")
	}

	tmp := t.TempDir()
	src := filepath.Join(tmp, "source")
	mustRunGit(t, "", "init", src)
	mustRunGit(t, src, "config", "user.email", "test@wiki.local")
	mustRunGit(t, src, "config", "user.name", "Wiki Test")
	if err := os.WriteFile(filepath.Join(src, "stray.txt"), []byte("not an OKF KB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, src, "add", "stray.txt")
	mustRunGit(t, src, "commit", "-m", "stray")
	branchOut, err := os.ReadFile(filepath.Join(src, ".git", "HEAD"))
	if err != nil {
		t.Fatalf("read source HEAD: %v", err)
	}
	branch := string(branchOut[len("ref: refs/heads/"):])
	branch = branch[:len(branch)-1]

	bare := filepath.Join(tmp, "non-okf.git")
	mustRunGit(t, "", "init", "--bare", bare)
	mustRunGit(t, src, "remote", "add", "origin", bare)
	mustRunGit(t, src, "push", "origin", branch+":"+branch)
	mustRunGit(t, bare, "symbolic-ref", "HEAD", "refs/heads/"+branch)

	data := filepath.Join(tmp, "data")
	withNoGuidance(t, func() {
		if code := cmdKBClone([]string{fileURL(bare), "--data", data}); code == 0 {
			t.Fatal("cmdKBClone(non-OKF) = 0, want error")
		}
	})
	if _, err := os.Stat(filepath.Join(data, "non-okf")); !os.IsNotExist(err) {
		t.Fatalf("non-OKF clone still exists: %v", err)
	}
}

func TestCmdKBCloneNameDerivation(t *testing.T) {
	cases := []struct {
		remote  string
		want    string
		wantErr bool
	}{
		{"https://example.test/team/wiki.git", "wiki", false},
		{"ssh://git@example.test/team/wiki.git/", "wiki", false},
		{"git@example.test:team/wiki.git", "wiki", false},
		{"https://example.test/team/bad.name.git", "bad.name", true},
	}
	for _, tc := range cases {
		got := remoteKBName(tc.remote)
		if got != tc.want {
			t.Errorf("remoteKBName(%q) = %q, want %q", tc.remote, got, tc.want)
		}
		if err := validateKBName(got); (err != nil) != tc.wantErr {
			t.Errorf("validateKBName(remoteKBName(%q)) error = %v, wantErr %v", tc.remote, err, tc.wantErr)
		}
	}
}

func TestRunKBDispatch(t *testing.T) {
	origKBFn := kbFn
	defer func() { kbFn = origKBFn }()
	var gotArgs []string
	kbFn = func(args []string) int {
		gotArgs = args
		return 9
	}

	if code := run([]string{"kb", "create", "alpha"}); code != 9 {
		t.Errorf("run([kb create alpha]) = %d, want 9", code)
	}
	want := []string{"create", "alpha"}
	if len(gotArgs) != len(want) {
		t.Fatalf("kbFn args = %v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Errorf("kbFn args[%d] = %q, want %q", i, gotArgs[i], want[i])
		}
	}
}

// withClientServerURL puts a .cartographer.yaml with the given server_url in
// a temporary HOME, which is what clientconfig.TargetDir resolves to. An
// empty serverURL means "no client config on this machine".
func withClientServerURL(t *testing.T, serverURL string) {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	if serverURL == "" {
		return
	}
	body := "schema_version: 1\nserver_url: " + serverURL + "\n"
	if err := os.WriteFile(filepath.Join(home, clientconfig.FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCheckLocalTarget: `kb create`/`kb clone` act on the LOCAL server's data
// dir. On a machine whose client points elsewhere, reporting "mounted" about a
// directory nothing reads is worse than failing, so it fails (D173).
func TestCheckLocalTarget(t *testing.T) {
	cases := []struct {
		name      string
		serverURL string
		dataFlag  string
		local     bool
		want      int
	}{
		{"client points at a remote server", "https://cartographer.example.com/mcp", "", false, 2},
		{"remote server with the explicit opt-out", "https://cartographer.example.com/mcp", "", true, 0},
		{"remote server but --data names the target", "https://cartographer.example.com/mcp", "/tmp/kbs", false, 0},
		{"loopback client", "http://127.0.0.1:39273/mcp", "", false, 0},
		{"no client config at all", "", "", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withClientServerURL(t, tc.serverURL)
			var code int
			out := withStderr(t, func() { code = checkLocalTarget(tc.dataFlag, tc.local) })
			if code != tc.want {
				t.Fatalf("checkLocalTarget = %d, want %d (stderr: %s)", code, tc.want, out)
			}
			if tc.want != 0 && !strings.Contains(out, "--local") {
				t.Errorf("the error must name its opt-out: %s", out)
			}
		})
	}
}

// TestResolveServerDataDir_CustomConfig: a service installed with `--config
// <path>` was invisible to the resolver, which read the standard path only —
// so `kb create` silently used ~/cartographer-data instead of the directory
// the running server serves.
func TestResolveServerDataDir_CustomConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "custom.yaml")
	dataDir := filepath.Join(dir, "kbs")
	if err := os.WriteFile(cfgPath, []byte("data: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveServerDataDir(cfgPath); got != dataDir {
		t.Errorf("resolveServerDataDir(%q) = %q, want %q", cfgPath, got, dataDir)
	}
	// An unreadable path falls back to the default rather than failing: the
	// caller can still pass --data.
	if got := resolveServerDataDir(filepath.Join(dir, "absent.yaml")); got != defaultDataDir() {
		t.Errorf("missing config = %q, want the default data dir", got)
	}
}

// seedKBListDir builds a data dir with one valid KB, one git repo that is not
// a KB, and one plain directory.
func seedKBListDir(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	if _, err := kb.Init(filepath.Join(dataDir, "wiki")); err != nil {
		t.Fatal(err)
	}
	plainRepo := filepath.Join(dataDir, "notes")
	if err := os.MkdirAll(plainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := gitx.Init(plainRepo); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

func TestScanDataDir(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	dataDir := seedKBListDir(t)
	rows, err := scanDataDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]kbRow{}
	for _, r := range rows {
		got[r.Name] = r
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3: %+v", len(rows), rows)
	}
	if !got["wiki"].IsKB || !got["wiki"].IsRepo {
		t.Errorf("wiki should be a git repo and an OKF KB: %+v", got["wiki"])
	}
	if got["notes"].IsKB || !got["notes"].IsRepo {
		t.Errorf("notes is a repo but not a KB: %+v", got["notes"])
	}
	if got["scratch"].IsKB || got["scratch"].IsRepo {
		t.Errorf("scratch is neither: %+v", got["scratch"])
	}

	// A missing data dir is reported, never created: `kb list` writes nothing.
	absent := filepath.Join(t.TempDir(), "nope")
	if rows, err := scanDataDir(absent); err != nil || len(rows) != 0 {
		t.Errorf("a missing data dir is an empty listing (#1304): %v, %v", rows, err)
	}
	if _, err := os.Stat(absent); err == nil {
		t.Error("kb list must not create the data dir")
	}
}

// TestCmdKBList_IsReadOnly is the point of the command's design: kb.Open
// self-migrates the local git-exclude entry, so a listing that used it would
// write into every repository it scanned.
func TestCmdKBList_IsReadOnly(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	dataDir := seedKBListDir(t)
	before := treeFingerprint(t, dataDir)

	var code int
	out := withStdout(t, func() { code = cmdKBList([]string{"--data", dataDir}) })
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"wiki", "notes", "scratch", "data dir:"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing is missing %q:\n%s", want, out)
		}
	}
	if after := treeFingerprint(t, dataDir); after != before {
		t.Errorf("kb list modified the scanned tree:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	out = withStdout(t, func() { code = cmdKBList([]string{"--data", dataDir, "--json"}) })
	if code != 0 {
		t.Fatalf("--json exit = %d, want 0", code)
	}
	if after := treeFingerprint(t, dataDir); after != before {
		t.Errorf("kb list --json modified the scanned tree:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestCmdKBList_JSON: with --json stdout is exactly one JSON document, and an
// unreachable server reads as mounted:null plus the reason, not as false.
func TestCmdKBList_JSON(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	withClientServerURL(t, closed.URL)
	dataDir := seedKBListDir(t)

	var code int
	out := withStdout(t, func() {
		code = cmdKBList([]string{"--data", dataDir, "--config", filepath.Join(t.TempDir(), "none.yaml"), "--json"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var doc struct {
		DataDir      string `json:"data_dir"`
		MountedError string `json:"mounted_error"`
		KBs          []struct {
			Name    string `json:"name"`
			OKF     bool   `json:"okf"`
			Git     bool   `json:"git"`
			Mounted *bool  `json:"mounted"`
		} `json:"kbs"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not a single JSON document: %v\n%s", err, out)
	}
	if doc.DataDir != dataDir || doc.MountedError == "" {
		t.Errorf("data_dir = %q, mounted_error = %q", doc.DataDir, doc.MountedError)
	}
	flags := map[string][2]bool{"notes": {false, true}, "scratch": {false, false}, "wiki": {true, true}}
	if len(doc.KBs) != len(flags) {
		t.Fatalf("kbs = %+v, want %d entries", doc.KBs, len(flags))
	}
	for _, r := range doc.KBs {
		if want, ok := flags[r.Name]; !ok || r.OKF != want[0] || r.Git != want[1] {
			t.Errorf("unexpected row %+v", r)
		}
		if r.Mounted != nil {
			t.Errorf("%s: mounted = %v, want null when the server could not be asked", r.Name, *r.Mounted)
		}
	}
	if !strings.Contains(out, `"mounted": null`) {
		t.Errorf("mounted must be an explicit null:\n%s", out)
	}
}

// treeFingerprint records every file path, size and mtime under root,
// including .git internals: the point is to catch a write nobody intended.
func treeFingerprint(t *testing.T, root string) string {
	t.Helper()
	var sb strings.Builder
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Directories contribute their name only. What the caller asserts is
		// that no file was created, removed or rewritten, and a directory whose
		// entries are all unchanged is unchanged — while its own mtime is not a
		// reliable witness of that: NTFS updates a directory's timestamp lazily,
		// so it can still be settling a millisecond after the tree was built.
		if d.IsDir() {
			fmt.Fprintf(&sb, "%s/\n", path)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(&sb, "%s %d %v\n", path, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

// TestPrintKBRows_UnreachableServer: absence of the signal is not evidence
// that nothing is mounted, so the column disappears and the reason is stated.
func TestPrintKBRows_UnreachableServer(t *testing.T) {
	var sb strings.Builder
	rows := []kbRow{{Name: "wiki", IsRepo: true, IsKB: true, Origin: "git@forge:team/wiki.git"}}
	printKBRows(&sb, "/data", rows, errors.New("connection refused"))
	out := sb.String()
	if strings.Contains(out, "MOUNTED") {
		t.Errorf("no MOUNTED column when the server could not be asked:\n%s", out)
	}
	if !strings.Contains(out, "could not be asked") {
		t.Errorf("the reason must be stated:\n%s", out)
	}

	sb.Reset()
	yes := true
	rows[0].Mounted = &yes
	printKBRows(&sb, "/data", rows, nil)
	if out := sb.String(); !strings.Contains(out, "MOUNTED") {
		t.Errorf("MOUNTED column expected when the server answered:\n%s", out)
	}
}

// TestPrintKBRowsJSON: the document carries Mounted as true/false when the
// server answered, and an empty data dir is `[]`, never null.
func TestPrintKBRowsJSON(t *testing.T) {
	yes, no := true, false
	rows := []kbRow{
		{Name: "kb-a", IsRepo: true, IsKB: true, Origin: "git@example.com:team/kb-a.git", Mounted: &yes},
		{Name: "kb-b", Mounted: &no},
	}
	var sb strings.Builder
	if err := printKBRowsJSON(&sb, "/data", rows, nil); err != nil {
		t.Fatal(err)
	}
	var doc kbListJSON
	if err := json.Unmarshal([]byte(sb.String()), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.KBs) != 2 || doc.KBs[0].Mounted == nil || !*doc.KBs[0].Mounted || doc.KBs[1].Mounted == nil || *doc.KBs[1].Mounted {
		t.Errorf("mounted should be true then false: %+v", doc.KBs)
	}
	if doc.KBs[0].Origin != "git@example.com:team/kb-a.git" || doc.MountedError != "" || strings.Contains(sb.String(), "mounted_error") {
		t.Errorf("unexpected document:\n%s", sb.String())
	}

	sb.Reset()
	if err := printKBRowsJSON(&sb, "/data", nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), `"kbs": []`) {
		t.Errorf("an empty data dir must give kbs: []:\n%s", sb.String())
	}
}

// TestCmdKBClone_Cleanup: a failed clone leaves nothing behind, and a
// pre-existing directory is never removed — the cleanup targets only what
// this command created.
func TestCmdKBClone_Cleanup(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	withClientServerURL(t, "")
	dataDir := t.TempDir()

	// A clone that fails: the partial directory must not survive.
	var code int
	withStderr(t, func() {
		withNoGuidance(t, func() {
			code = cmdKBClone([]string{filepath.Join(dataDir, "no-such-repo.git"), "alpha", "--data", dataDir, "--timeout", "30s"})
		})
	})
	if code == 0 {
		t.Fatal("cloning a non-existent remote should fail")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "alpha")); !os.IsNotExist(err) {
		t.Errorf("a failed clone left %s behind", filepath.Join(dataDir, "alpha"))
	}

	// A pre-existing directory is refused, and left exactly as it was.
	existing := filepath.Join(dataDir, "beta")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(existing, "keep.txt")
	if err := os.WriteFile(marker, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withStderr(t, func() {
		withNoGuidance(t, func() {
			code = cmdKBClone([]string{"https://forge.invalid/team/beta.git", "beta", "--data", dataDir})
		})
	})
	if code != 1 {
		t.Errorf("exit = %d, want 1 for an existing destination", code)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("a pre-existing directory was removed: %v", err)
	}
}

// #415: the recovery text names a removal command the operator's shell runs.
func TestUnpushedScaffoldGuidanceRemovalCommand(t *testing.T) {
	cases := []struct{ goos, path, want string }{
		{"linux", "/home/user/kbs/kb-a", "Or remove it: rm -rf /home/user/kbs/kb-a "},
		{"darwin", "/Users/user/kbs/kb-a", "Or remove it: rm -rf /Users/user/kbs/kb-a "},
		{"windows", `C:\Users\user\kbs\kb-a`, `Or remove it: Remove-Item -Recurse -Force 'C:\Users\user\kbs\kb-a' `},
		{"windows", `C:\Users\o'user\kb-a`, `Remove-Item -Recurse -Force 'C:\Users\o''user\kb-a'`},
	}
	for _, c := range cases {
		var b strings.Builder
		writeUnpushedScaffoldGuidance(&b, c.goos, c.path, gitx.DefaultBranch, "user", "user@example.com", "data")
		if !strings.Contains(b.String(), c.want) {
			t.Errorf("%s: guidance lacks %q:\n%s", c.goos, c.want, b.String())
		}
		if c.goos == "windows" && strings.Contains(b.String(), "rm -rf") {
			t.Errorf("windows guidance still says rm -rf:\n%s", b.String())
		}
	}
}

// D266: after a restart the old process may still answer /health for a moment
// before it exits. waitHealthy must hold out for a different started_at rather
// than report the service healthy on the strength of the process being
// replaced (#411: "service healthy", then connection refused).
func TestWaitHealthy_WaitsForANewProcess(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := "2026-01-01T00:00:00Z"
		if atomic.AddInt32(&n, 1) > 2 {
			startedAt = "2026-01-01T00:05:00Z"
		}
		fmt.Fprintf(w, `{"status":"ok","started_at":%q}`, startedAt)
	}))
	defer srv.Close()

	if !waitHealthy(srv.URL, "2026-01-01T00:00:00Z") {
		t.Fatal("waitHealthy = false, want true once a new process answers")
	}
	if got := atomic.LoadInt32(&n); got < 3 {
		t.Errorf("waitHealthy accepted the old process after %d probes", got)
	}

	// A server predating started_at (and so an empty old value) still counts.
	atomic.StoreInt32(&n, 0)
	if !waitHealthy(srv.URL, "") {
		t.Error("waitHealthy with no previous start time = false, want true")
	}
}

// D348: a bootstrapping answer carries no KB list and is not a restarted
// service yet; waitHealthy keeps waiting and fetchHealth reports an error.
func TestWaitHealthy_DoesNotAcceptBootstrapping(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) <= 2 {
			fmt.Fprint(w, `{"status":"ok","started_at":"new","kbs":[],"bootstrapping":true}`)
			return
		}
		fmt.Fprint(w, `{"status":"ok","started_at":"new","kbs":[]}`)
	}))
	defer srv.Close()

	if _, err := fetchHealth(srv.URL); err == nil {
		t.Fatal("fetchHealth accepted a bootstrapping answer")
	}
	atomic.StoreInt32(&n, 0)
	if !waitHealthy(srv.URL, "old") {
		t.Fatal("waitHealthy = false, want true once bootstrap ends")
	}
	if got := atomic.LoadInt32(&n); got < 3 {
		t.Errorf("waitHealthy returned after %d probes, during bootstrap", got)
	}
}

// D1096: a tokenless /health of a server with authentication on carries no
// kbs. kb list's MOUNTED column then says why, and the no-KB hint asks the
// data directory instead of concluding anything from the missing field.
func TestMountedKBNames_AnonymousHealthHasNoKBs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"ok","version":"v","started_at":"s","ready":true}`)
	}))
	defer srv.Close()

	_, err := mountedKBNames(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "does not list its KBs to a request without a token") {
		t.Fatalf("mountedKBNames error = %v, want the reworded reason", err)
	}
	h, err := fetchHealth(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !h.hasNoKBs(t.TempDir()) {
		t.Error("hasNoKBs on an empty data dir with kbs absent = false, want true (falls back to the data directory)")
	}
}

// kbCreateEnv returns a server config whose data: is a temp dir, and that dir,
// so kb create tests never read the developer's real config.
func kbCreateEnv(t *testing.T) (cfg, data string) {
	t.Helper()
	tmp := t.TempDir()
	data = filepath.Join(tmp, "data")
	cfg = filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(cfg, []byte("data: "+data+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg, data
}

func createKB(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var code int
	out := withStderr(t, func() {
		withStdout(t, func() {
			withNoGuidance(t, func() { code = cmdKBCreate(args) })
		})
	})
	return code, out
}

func TestCmdKBCreateLeavesConfigUntouched(t *testing.T) {
	cfg, data := kbCreateEnv(t)
	before, _ := os.ReadFile(cfg)
	if code, out := createKB(t, "alpha", "--config", cfg, "--local", "--no-remote"); code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(data, "alpha")); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(cfg)
	if string(before) != string(after) {
		t.Errorf("config changed:\n%s", after)
	}
}

// TestPrintPostCreateGuidance_NamesOpenSessions: the no-restart guidance tells
// the operator that agent sessions already open need a new session (D712).
func TestPrintPostCreateGuidance_NamesOpenSessions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","started_at":"2026-01-01T00:00:00Z"}`))
	}))
	defer srv.Close()
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("http: "+strings.TrimPrefix(srv.URL, "http://")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := withStdout(t, func() { printPostCreateGuidance(cfg, false) })
	for _, want := range []string{"cartographer service restart", "On connected client machines, run: cartographer sync", openSessionsLine} {
		if strings.Count(out, want) != 1 {
			t.Errorf("want %q exactly once in:\n%s", want, out)
		}
	}
}

// TestKBRemoteHelperRefused: an option-like or transport-helper remote is
// refused by kb create and kb clone, and no KB directory is left behind.
func TestKBRemoteHelperRefused(t *testing.T) {
	for _, remote := range []string{"ext::sh -c id", "--upload-pack=x"} {
		dataDir := filepath.Join(t.TempDir(), "data")
		var code int
		withNoGuidance(t, func() {
			code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--remote", remote})
		})
		if code == 0 {
			t.Errorf("kb create --remote %q = 0, want non-zero", remote)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "alpha")); err == nil {
			t.Errorf("kb create --remote %q left a directory", remote)
		}
		if strings.HasPrefix(remote, "-") {
			continue // the flag parser takes it before gitx sees it
		}
		var cloneCode int
		stderr := withStderr(t, func() {
			cloneCode = cmdKBClone([]string{remote, "beta", "--data", dataDir})
		})
		if cloneCode == 0 {
			t.Errorf("kb clone %q = 0, want non-zero", remote)
		}
		if !strings.Contains(stderr, "transport>::<address> helper syntax") {
			t.Errorf("kb clone %q: validation message missing from stderr: %q", remote, stderr)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "beta")); err == nil {
			t.Errorf("kb clone %q left a directory", remote)
		}
	}
}
