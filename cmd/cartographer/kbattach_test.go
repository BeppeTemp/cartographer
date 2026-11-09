package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

const attachSecret = "SECRETTOKEN"

type attachEnv struct {
	base, data, config, root, branch string
}

// newAttachEnv builds a KB with no origin, the state `kb create --no-remote`
// leaves, and a server config file to prove attach never writes it.
func newAttachEnv(t *testing.T) attachEnv {
	t.Helper()
	if !hasGitBinary() {
		t.Skip("git not in PATH")
	}
	base := t.TempDir()
	e := attachEnv{base: base, data: filepath.Join(base, "data"), config: filepath.Join(base, "config.yaml")}
	e.root = filepath.Join(e.data, "kb-a")
	if _, err := kb.Init(e.root); err != nil {
		t.Fatal(err)
	}
	e.branch = reauthorGit(t, e.root, "branch", "--show-current")
	if err := os.WriteFile(e.config, []byte("http: \"127.0.0.1:0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e attachEnv) bare(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(e.base, name)
	reauthorGit(t, e.base, "init", "-q", "--bare", p)
	return p
}

func (e attachEnv) run(remote string) (stdout, stderr string, code int) {
	var out, errOut bytes.Buffer
	code = runKBAttach("kb-a", e.data, e.config, remote, &out, &errOut)
	return out.String(), errOut.String(), code
}

// unchanged asserts the KB is as it was before a refused attach: no origin, no
// remote-tracking refs, no tracking configuration, and the server file intact.
func (e attachEnv) unchanged(t *testing.T, configBefore []byte) {
	t.Helper()
	if got := reauthorGit(t, e.root, "remote"); got != "" {
		t.Errorf("remotes left behind: %q", got)
	}
	if got := reauthorGit(t, e.root, "for-each-ref", "refs/remotes"); got != "" {
		t.Errorf("remote-tracking refs left behind: %q", got)
	}
	cfg, _ := os.ReadFile(e.config)
	if !bytes.Equal(cfg, configBefore) {
		t.Errorf("the server configuration changed: %q", cfg)
	}
}

func TestKBAttachPushesToAnEmptyRemote(t *testing.T) {
	e := newAttachEnv(t)
	bare := e.bare(t, "remote.git")
	cfgBefore, _ := os.ReadFile(e.config)
	out, errOut, code := e.run(bare)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "origin set to") || !strings.Contains(out, "needs no restart") {
		t.Errorf("out = %q", out)
	}
	if got := reauthorGit(t, e.root, "rev-parse", "HEAD"); got != reauthorGit(t, bare, "rev-parse", e.branch) {
		t.Errorf("remote %s is not at HEAD", e.branch)
	}
	if got := reauthorGit(t, e.root, "rev-parse", "--abbrev-ref", e.branch+"@{upstream}"); got != "origin/"+e.branch {
		t.Errorf("upstream = %q", got)
	}
	k, err := kb.Open(e.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := k.RemoteInfo(); !ok {
		t.Error("RemoteInfo does not report the new origin")
	}
	if cfg, _ := os.ReadFile(e.config); !bytes.Equal(cfg, cfgBefore) {
		t.Errorf("the server configuration changed: %q", cfg)
	}
}

func TestKBAttachFastForwardsAnAncestorRemote(t *testing.T) {
	e := newAttachEnv(t)
	bare := e.bare(t, "remote.git")
	reauthorGit(t, e.root, "push", "-q", bare, "HEAD:refs/heads/"+e.branch)
	t.Run("equal", func(t *testing.T) {
		if _, errOut, code := e.run(bare); code != 0 {
			t.Fatalf("code=%d stderr=%s", code, errOut)
		}
		if got := reauthorGit(t, e.root, "rev-parse", "--abbrev-ref", e.branch+"@{upstream}"); got != "origin/"+e.branch {
			t.Errorf("upstream = %q", got)
		}
		reauthorGit(t, e.root, "remote", "remove", "origin")
	})
	t.Run("ahead", func(t *testing.T) {
		commitInKB(t, e.root, "a.md", "Ada Example", "ada@example.test")
		if _, errOut, code := e.run(bare); code != 0 {
			t.Fatalf("code=%d stderr=%s", code, errOut)
		}
		if reauthorGit(t, e.root, "rev-parse", "HEAD") != reauthorGit(t, bare, "rev-parse", e.branch) {
			t.Error("the remote did not fast-forward")
		}
	})
}

func TestKBAttachRefusalsLeaveTheKBAsItWas(t *testing.T) {
	cases := map[string]struct {
		setup func(t *testing.T, e attachEnv) string // returns the remote
		want  string
	}{
		"another branch": {func(t *testing.T, e attachEnv) string {
			b := e.bare(t, "r.git")
			reauthorGit(t, e.root, "push", "-q", b, "HEAD:refs/heads/"+e.branch, "HEAD:refs/heads/extra")
			return b
		}, "several branches"},
		"different branch name": {func(t *testing.T, e attachEnv) string {
			b := e.bare(t, "r.git")
			reauthorGit(t, e.root, "push", "-q", b, "HEAD:refs/heads/other")
			return b
		}, `has the branch "other"`},
		"diverged": {func(t *testing.T, e attachEnv) string {
			b := e.bare(t, "r.git")
			reauthorGit(t, e.root, "checkout", "-q", "-b", "tmp")
			commitInKB(t, e.root, "x.md", "Ada Example", "ada@example.test")
			reauthorGit(t, e.root, "push", "-q", b, "tmp:refs/heads/"+e.branch)
			reauthorGit(t, e.root, "checkout", "-q", e.branch)
			reauthorGit(t, e.root, "branch", "-q", "-D", "tmp")
			commitInKB(t, e.root, "y.md", "Ada Example", "ada@example.test")
			return b
		}, "not part of this KB's"},
		"tags only": {func(t *testing.T, e attachEnv) string {
			b := e.bare(t, "r.git")
			reauthorGit(t, e.root, "tag", "v1")
			reauthorGit(t, e.root, "push", "-q", b, "refs/tags/v1")
			reauthorGit(t, e.root, "tag", "-d", "v1")
			return b
		}, "tags only"},
		"forge initial commit": {func(t *testing.T, e attachEnv) string {
			return seedForgeRemote(t, e.base, e.branch, []string{"README.md", "LICENSE"}, 1)
		}, "forge initial commit"},
		"unreachable": {func(t *testing.T, e attachEnv) string {
			return fileURL(filepath.Join(e.base, "missing.git"))
		}, "ls-remote"},
		"rejected push": {func(t *testing.T, e attachEnv) string {
			b := e.bare(t, "r.git")
			if err := os.MkdirAll(filepath.Join(b, "hooks"), 0o755); err != nil {
				t.Fatal(err)
			}
			hook := "#!/bin/sh\necho 'rejected by the test' >&2\nexit 1\n"
			if err := os.WriteFile(filepath.Join(b, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
				t.Fatal(err)
			}
			return b
		}, "re-author"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newAttachEnv(t)
			remote := tc.setup(t, e)
			cfgBefore, _ := os.ReadFile(e.config)
			_, errOut, code := e.run(remote)
			if code != 1 {
				t.Fatalf("code=%d, want 1; stderr=%s", code, errOut)
			}
			if !strings.Contains(errOut, tc.want) {
				t.Errorf("stderr lacks %q:\n%s", tc.want, errOut)
			}
			e.unchanged(t, cfgBefore)
		})
	}
}

func TestKBAttachExistingOrigin(t *testing.T) {
	e := newAttachEnv(t)
	dead := fileURL(filepath.Join(e.base, "dead.git"))
	reauthorGit(t, e.root, "remote", "add", "origin", dead)
	out, _, code := e.run(dead)
	if code != 0 || !strings.Contains(out, "nothing to do") {
		t.Fatalf("same origin: code=%d out=%q", code, out)
	}
	_, errOut, code := e.run(e.bare(t, "other.git"))
	if code != 1 || !strings.Contains(errOut, "git remote") {
		t.Fatalf("other origin: code=%d stderr=%s", code, errOut)
	}
	if got := reauthorGit(t, e.root, "remote", "get-url", "origin"); got != dead {
		t.Errorf("origin = %q, want it untouched", got)
	}
}

func TestKBAttachNeverPrintsASecret(t *testing.T) {
	e := newAttachEnv(t)
	out, errOut, code := e.run("https://user:" + attachSecret + "@example.com/x.git")
	if code != 2 {
		t.Fatalf("code=%d, want 2", code)
	}
	if strings.Contains(out+errOut, attachSecret) {
		t.Errorf("the secret was printed: %s%s", out, errOut)
	}
}

func TestRemoteCredentialProblem(t *testing.T) {
	for remote, refused := range map[string]bool{
		"https://user:" + attachSecret + "@example.com/x.git":   true,
		"https://" + attachSecret + "@example.com/org/repo.git": true,
		"https://user:p/ss@example.com/x.git":                   true, // does not parse
		"https://user:p#ss@example.com/x.git":                   true,
		"ssh://user:" + attachSecret + "@example.com/x.git":     true,
		"ssh://git@example.com/x.git":                           false,
		"ssh://git@example.com:2222/x.git":                      false,
		"git@example.com:x.git":                                 false,
		"https://example.com/org/repo.git":                      false,
		fileURL("/tmp/x.git"):                                   false,
	} {
		got := remoteCredentialProblem(remote)
		if (got != "") != refused {
			t.Errorf("%q: problem = %q, refused want %v", remote, got, refused)
		}
		if strings.Contains(got, attachSecret) {
			t.Errorf("the message echoes the secret: %q", got)
		}
	}
}

func TestKBAttachRefusesAnUnparseableURLWithAPassword(t *testing.T) {
	e := newAttachEnv(t)
	out, errOut, code := e.run("https://user:" + attachSecret + "/x@example.com/x.git")
	if code != 2 || strings.Contains(out+errOut, attachSecret) {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

// The fetch of a refused attach must not leave tags behind.
func TestKBAttachRefusedLeavesNoTag(t *testing.T) {
	e := newAttachEnv(t)
	bare := e.bare(t, "r.git")
	// The remote holds a branch tip that is not in the KB's history, and a tag.
	reauthorGit(t, e.root, "checkout", "-q", "-b", "other")
	commitInKB(t, e.root, "o.md", "Ada Example", "ada@example.test")
	reauthorGit(t, e.root, "tag", "v1")
	reauthorGit(t, e.root, "push", "-q", bare, "other:refs/heads/"+e.branch, "refs/tags/v1")
	reauthorGit(t, e.root, "tag", "-d", "v1")
	reauthorGit(t, e.root, "checkout", "-q", e.branch)
	reauthorGit(t, e.root, "branch", "-q", "-D", "other")
	cfgBefore, _ := os.ReadFile(e.config)
	_, errOut, code := e.run(bare)
	if code != 1 {
		t.Fatalf("code=%d stderr=%s", code, errOut)
	}
	e.unchanged(t, cfgBefore)
	if got := reauthorGit(t, e.root, "tag", "-l"); got != "" {
		t.Errorf("tags left behind: %q", got)
	}
}

func TestKBAttachUsageAndPreconditions(t *testing.T) {
	e := newAttachEnv(t)
	bare := e.bare(t, "r.git")
	var out, errOut bytes.Buffer
	if code := runKBAttach("nope", e.data, e.config, bare, &out, &errOut); code != 2 {
		t.Errorf("unknown KB: code=%d", code)
	}
	if code := runKBAttach("kb-a", e.data, e.config, "--upload-pack=x", &out, &errOut); code != 2 {
		t.Errorf("option-like remote: code=%d", code)
	}
	if code := cmdKBAttach([]string{"kb-a", "--data", e.data}); code != 2 {
		t.Errorf("missing --remote: code=%d", code)
	}
	t.Run("detached HEAD", func(t *testing.T) {
		reauthorGit(t, e.root, "checkout", "-q", "--detach")
		defer reauthorGit(t, e.root, "checkout", "-q", e.branch)
		if _, _, code := e.run(bare); code != 1 {
			t.Errorf("code=%d, want 1", code)
		}
		if got := reauthorGit(t, e.root, "remote"); got != "" {
			t.Errorf("remotes left: %q", got)
		}
	})
	t.Run("rebase in progress", func(t *testing.T) {
		d := filepath.Join(e.root, ".git", "rebase-merge")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(d)
		if _, errOut, code := e.run(bare); code != 1 || !strings.Contains(errOut, "in progress") {
			t.Errorf("code=%d stderr=%s", code, errOut)
		}
	})
	t.Run("not a repository", func(t *testing.T) {
		e := newAttachEnv(t)
		if err := os.RemoveAll(filepath.Join(e.root, ".git")); err != nil {
			t.Fatal(err)
		}
		if _, _, code := e.run(bare); code == 0 {
			t.Error("a directory without .git was accepted")
		}
	})
	t.Run("lock held", func(t *testing.T) {
		k, err := kb.Open(e.root)
		if err != nil {
			t.Fatal(err)
		}
		release, err := k.AcquireProcessLock(0, "test holder")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		saved := reauthorLockTimeout
		reauthorLockTimeout = 0
		defer func() { reauthorLockTimeout = saved }()
		if _, errOut, code := e.run(bare); code != 1 || !strings.Contains(errOut, "locked") {
			t.Errorf("code=%d stderr=%s", code, errOut)
		}
		if _, err := gitx.RemoteURL(e.root, "origin"); err == nil {
			t.Error("an origin was added while the lock was held")
		}
	})
}
