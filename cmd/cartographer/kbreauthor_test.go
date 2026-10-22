package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

const reauthorAda = "Ada Example <ada@example.test>"

func reauthorGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.test", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.test")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitInKB(t *testing.T, root, file, name, email string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "data", file), []byte(file+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reauthorGit(t, root, "add", "-A")
	cmd := exec.Command("git", "-C", root, "commit", "-q", "-m", "write "+file+"\n\nCartographer-Token: tok-1")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+email, "GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL="+email)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
}

type reauthorEnv struct {
	data, config, root, bare string
}

// newReauthorEnv is a data dir with one KB clone "kb-a" whose first commit is
// pushed to a bare remote guarded by a membership rule that only lets ada in,
// and a server file carrying git.identity (withIdentity).
func newReauthorEnv(t *testing.T, withIdentity bool) reauthorEnv {
	t.Helper()
	base := t.TempDir()
	e := reauthorEnv{data: filepath.Join(base, "data"), config: filepath.Join(base, "config.yaml"), bare: filepath.Join(base, "remote.git")}
	e.root = filepath.Join(e.data, "kb-a")
	if _, err := kb.Init(e.root); err != nil {
		t.Fatal(err)
	}
	reauthorGit(t, base, "init", "-q", "--bare", e.bare)
	reauthorGit(t, e.root, "remote", "add", "origin", e.bare)
	branch := reauthorGit(t, e.root, "branch", "--show-current")
	reauthorGit(t, e.root, "push", "-q", "-u", "origin", branch)
	hook, err := os.ReadFile(filepath.Join("..", "..", "test", "forge", "pre-receive.sh"))
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(e.bare, "hooks"), 0o755))
	must(os.WriteFile(filepath.Join(e.bare, "hooks", "pre-receive"), hook, 0o755))
	must(os.WriteFile(filepath.Join(e.bare, "hooks", "allowed-identities"), []byte("ada@example.test\n"), 0o644))
	yaml := "http: \"127.0.0.1:0\"\n"
	if withIdentity {
		yaml += "git:\n  identity: \"" + reauthorAda + "\"\n"
	}
	must(os.WriteFile(e.config, []byte(yaml), 0o644))
	return e
}

func (e reauthorEnv) run(t *testing.T, apply, asJSON bool) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = runKBReauthor("kb-a", e.data, e.config, apply, asJSON, &out, &errOut)
	return out.String(), errOut.String(), code
}

func (e reauthorEnv) remoteHead(t *testing.T) string {
	t.Helper()
	return reauthorGit(t, e.bare, "log", "-1", "--format=%an <%ae> | %cn <%ce>", reauthorGit(t, e.root, "branch", "--show-current"))
}

// The preview lists the commits and changes nothing; --json is the same data.
func TestKBReauthorPreview(t *testing.T) {
	e := newReauthorEnv(t, true)
	commitInKB(t, e.root, "a.md", "tok-1", "cartographer@host.example.test")
	commitInKB(t, e.root, "b.md", "Ada Example", "ada@example.test")
	head := reauthorGit(t, e.root, "rev-parse", "HEAD")

	out, errOut, code := e.run(t, false, false)
	if code != 0 {
		t.Fatalf("code = %d, stderr %s", code, errOut)
	}
	if !strings.Contains(out, "tok-1 <cartographer@host.example.test>") || !strings.Contains(out, "would be re-authored") {
		t.Errorf("preview = %q", out)
	}
	if reauthorGit(t, e.root, "rev-parse", "HEAD") != head {
		t.Error("the preview moved HEAD")
	}

	out, _, code = e.run(t, false, true)
	var rep reauthorReport
	if code != 0 || json.Unmarshal([]byte(out), &rep) != nil {
		t.Fatalf("json code=%d out=%s", code, out)
	}
	if rep.KB != "kb-a" || rep.Identity != reauthorAda || len(rep.Commits) != 2 || !rep.Commits[0].Rewrite || !rep.Commits[1].Rewrite == false || rep.Applied {
		t.Errorf("report = %+v", rep)
	}
}

// --apply rewrites, pushes, and the remote head is by the identity; the trailers stay.
func TestKBReauthorApplyPublishes(t *testing.T) {
	e := newReauthorEnv(t, true)
	commitInKB(t, e.root, "a.md", "tok-1", "cartographer@host.example.test")
	_, errOut, code := e.run(t, true, false)
	if code != 0 {
		t.Fatalf("code = %d, stderr %s", code, errOut)
	}
	if got := e.remoteHead(t); got != reauthorAda+" | "+reauthorAda {
		t.Errorf("remote head = %s", got)
	}
	branch := reauthorGit(t, e.root, "branch", "--show-current")
	if msg := reauthorGit(t, e.bare, "log", "-1", "--format=%B", branch); !strings.Contains(msg, "Cartographer-Token: tok-1") {
		t.Errorf("the trailer was lost: %q", msg)
	}
	if reauthorGit(t, e.root, "status", "--porcelain") != "" {
		t.Error("the working tree changed")
	}
	if out, _, code := e.run(t, true, false); code != 0 || !strings.Contains(out, "nothing to re-author") {
		t.Errorf("second run: code=%d out=%s", code, out)
	}
}

func TestKBReauthorRefusals(t *testing.T) {
	refused := func(t *testing.T, e reauthorEnv, want string) {
		t.Helper()
		before := reauthorGit(t, e.root, "rev-parse", "HEAD")
		out, errOut, code := e.run(t, true, false)
		if code != 2 || !strings.Contains(errOut, want) {
			t.Errorf("code=%d stderr=%q stdout=%q, want exit 2 saying %q", code, errOut, out, want)
		}
		if reauthorGit(t, e.root, "rev-parse", "HEAD") != before {
			t.Error("a refused run moved HEAD")
		}
	}
	t.Run("no identity", func(t *testing.T) {
		e := newReauthorEnv(t, false)
		commitInKB(t, e.root, "a.md", "tok-1", "cartographer@host.example.test")
		refused(t, e, "git.identity")
	})
	t.Run("no upstream", func(t *testing.T) {
		e := newReauthorEnv(t, true)
		reauthorGit(t, e.root, "checkout", "-q", "-b", "other")
		commitInKB(t, e.root, "a.md", "tok-1", "cartographer@host.example.test")
		refused(t, e, "never pushed")
	})
	t.Run("detached", func(t *testing.T) {
		e := newReauthorEnv(t, true)
		reauthorGit(t, e.root, "checkout", "-q", "--detach")
		refused(t, e, "detached")
	})
	t.Run("diverged", func(t *testing.T) {
		e := newReauthorEnv(t, true)
		branch := reauthorGit(t, e.root, "branch", "--show-current")
		other := filepath.Join(t.TempDir(), "other")
		reauthorGit(t, filepath.Dir(other), "clone", "-q", "-b", branch, e.bare, other)
		commitInKB(t, other, "x.md", "Ada Example", "ada@example.test")
		reauthorGit(t, other, "push", "-q", "origin", branch)
		commitInKB(t, e.root, "a.md", "tok-1", "cartographer@host.example.test")
		refused(t, e, "diverged")
	})
	t.Run("merge in range", func(t *testing.T) {
		e := newReauthorEnv(t, true)
		branch := reauthorGit(t, e.root, "branch", "--show-current")
		reauthorGit(t, e.root, "checkout", "-q", "-b", "side")
		commitInKB(t, e.root, "s.md", "tok-1", "cartographer@host.example.test")
		reauthorGit(t, e.root, "checkout", "-q", branch)
		commitInKB(t, e.root, "m.md", "tok-1", "cartographer@host.example.test")
		reauthorGit(t, e.root, "merge", "-q", "--no-ff", "-m", "merge", "side")
		refused(t, e, "merge or a root")
	})
	t.Run("already on another remote ref", func(t *testing.T) {
		e := newReauthorEnv(t, true)
		commitInKB(t, e.root, "a.md", "tok-1", "cartographer@host.example.test")
		reauthorGit(t, e.root, "update-ref", "refs/remotes/other/x", "HEAD")
		refused(t, e, "published history")
	})
	t.Run("rebase in progress", func(t *testing.T) {
		e := newReauthorEnv(t, true)
		commitInKB(t, e.root, "a.md", "tok-1", "cartographer@host.example.test")
		if err := os.MkdirAll(filepath.Join(e.root, ".git", "rebase-merge"), 0o755); err != nil {
			t.Fatal(err)
		}
		refused(t, e, "in progress")
	})
	t.Run("lock held", func(t *testing.T) {
		e := newReauthorEnv(t, true)
		commitInKB(t, e.root, "a.md", "tok-1", "cartographer@host.example.test")
		k, err := kb.Open(e.root)
		if err != nil {
			t.Fatal(err)
		}
		release, err := k.AcquireProcessLock(0, "test holder")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		// The same process holds it through another handle: flock is per open
		// file, so the command's attempt is refused as a held lock.
		saved := reauthorLockTimeout
		reauthorLockTimeout = 0
		defer func() { reauthorLockTimeout = saved }()
		refused(t, e, "locked")
	})
}

// Commits that already carry the identity are not rewritten, but --apply still
// pushes them; the preview says so instead of "nothing to re-author".
func TestKBReauthorPushesCommitsThatAlreadyCarryTheIdentity(t *testing.T) {
	e := newReauthorEnv(t, true)
	commitInKB(t, e.root, "a.md", "Ada Example", "ada@example.test")
	out, _, code := e.run(t, false, false)
	if code != 0 || strings.Contains(out, "nothing to re-author") || !strings.Contains(out, "already carry the configured identity") {
		t.Errorf("preview code=%d out=%q", code, out)
	}
	if _, errOut, code := e.run(t, true, false); code != 0 {
		t.Fatalf("apply code=%d stderr=%s", code, errOut)
	}
	if got := e.remoteHead(t); got != reauthorAda+" | "+reauthorAda {
		t.Errorf("remote head = %s", got)
	}
}

// --apply --json with a push that fails still reports the rewrite on stdout,
// with pushed false and the error.
func TestKBReauthorJSONReportsRewriteWhenPushFails(t *testing.T) {
	e := newReauthorEnv(t, true)
	commitInKB(t, e.root, "a.md", "tok-1", "cartographer@host.example.test")
	// A rule that refuses ada too: the rewrite works, the push cannot.
	if err := os.WriteFile(filepath.Join(e.bare, "hooks", "allowed-identities"), []byte("nobody@example.test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code := e.run(t, true, true)
	var rep map[string]any
	if code != 2 || json.Unmarshal([]byte(out), &rep) != nil {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if rep["rewritten"] != float64(1) || rep["pushed"] != false || rep["error"] == "" || rep["error"] == nil {
		t.Errorf("report = %v", rep)
	}
}
