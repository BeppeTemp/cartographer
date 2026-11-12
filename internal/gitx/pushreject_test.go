package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

func TestPermanentPushRejection(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct{ out, want string }{
		"403":    {"remote: Permission to org/kb.git denied to user.\nfatal: unable to access 'https://example.com/org/kb.git/': The requested URL returned error: 403", "this credential cannot push"},
		"gh013":  {"remote: error: GH013: Repository rule violations found for refs/heads/main.\n ! [remote rejected] main -> main (push declined due to repository rule violations)", "the remote's rules refuse this push"},
		"signed": {"remote: error: GH013: Repository rule violations found for refs/heads/main.\nremote: - Commits must have verified signatures.\n ! [remote rejected] main -> main (push declined due to repository rule violations)", "the remote requires signed commits: Cartographer does not sign them"},
		"size":   {"remote: error: GH001: Large files detected. You may want to try Git Large File Storage\nremote: error: File big.bin is 150 MB; this exceeds GitHub's file size limit of 100 MB", "file over the remote's size limit"},
		"hook":   {" ! [remote rejected] main -> main (hook declined)", "push rules declined the push"},
	} {
		remedy, ok := PermanentPushRejection(c.out)
		if !ok || !strings.Contains(remedy, c.want) || !strings.Contains(remedy, "the remote said:") && name != "hook" {
			t.Errorf("%s: PermanentPushRejection = %q, %v; want %q quoting the remote's line", name, remedy, ok, c.want)
		}
	}
	// Retried: races and server faults behind a "[remote rejected]", a bare 403
	// (GitHub's secondary rate limit is one), the size warning of a push that
	// succeeds, a non-fast-forward, network errors.
	for name, out := range map[string]string{
		"failed to update ref": " ! [remote rejected] main -> main (failed to update ref)",
		"cannot lock ref":      "error: cannot lock ref 'refs/heads/main': is at abc but expected def\n ! [remote rejected] main -> main (failed to update ref)",
		"internal error":       " ! [remote rejected] main -> main (internal error)",
		"unpacker error":       " ! [remote rejected] main -> main (unpacker error)",
		"bare 403":             "fatal: unable to access 'https://example.com/kb.git/': The requested URL returned error: 403",
		"rate limit":           "remote: You have exceeded a secondary rate limit.\nfatal: unable to access 'https://example.com/kb.git/': The requested URL returned error: 403",
		"size warning":         "remote: warning: File notes.bin is 60.00 MB; this is larger than GitHub's recommended maximum file size of 50.00 MB\nremote: warning: GH001: Large files detected. You may want to try Git Large File Storage - https://git-lfs.github.com.",
		"non-fast-forward":     " ! [rejected]        main -> main (non-fast-forward)",
		"dns":                  "fatal: unable to access 'https://example.com/kb.git/': Could not resolve host: example.com",
		"ssh":                  "Permission denied (publickey).",
		"empty":                "",
	} {
		if remedy, ok := PermanentPushRejection(out); ok {
			t.Errorf("%s is retried, got permanent: %q", name, remedy)
		}
	}
}

func TestRemoteRemedy_TLSNamesTheCABundle(t *testing.T) {
	t.Parallel()
	for _, out := range []string{
		"fatal: unable to access 'https://git.example.com/kb.git/': SSL certificate problem: unable to get local issuer certificate",
		"Get \"https://git.example.com\": tls: failed to verify certificate: x509: certificate signed by unknown authority",
	} {
		if r := remoteRemedy(out); !strings.Contains(r, "SSL_CERT_FILE") || !strings.Contains(r, "http.sslCAInfo") {
			t.Errorf("remoteRemedy(%q) = %q, want the CA bundle remedy", out, r)
		}
	}
}

func TestProbePushAccess_ReadOnlyCredentialIsRefusedAtOnce(t *testing.T) {
	t.Parallel()
	url, _ := gitxtest.ReadOnlyHTTPRemote(t, "token", httpTestToken)
	env := append(gitxtest.IsolatedEnv(), gitxtest.TokenEnv(writeToken(t, httpTestToken))...)
	err := ProbePushAccess(context.Background(), url, env...)
	if err == nil || !strings.Contains(err.Error(), "this credential cannot push") {
		t.Fatalf("ProbePushAccess on a read-only remote = %v, want the cannot-push remedy", err)
	}
}

func TestProbePushAccess_WritableRemotePassesAndLeavesNothing(t *testing.T) {
	t.Parallel()
	url, bare := seededHTTPRemote(t)
	env := append(gitxtest.IsolatedEnv(), gitxtest.TokenEnv(writeToken(t, httpTestToken))...)
	if err := ProbePushAccess(context.Background(), url, env...); err != nil {
		t.Fatalf("ProbePushAccess: %v", err)
	}
	if got := runIn(t, bare, "for-each-ref", "--format=%(refname)"); got != "refs/heads/main" {
		t.Errorf("the remote's refs after the probe = %q, want only main: a dry run writes nothing", got)
	}
}

// Server commits do not depend on the user's commit-time configuration: a global
// commit.gpgsign=true with a signer that fails, and a core.hooksPath whose
// pre-commit fails, must not stop a write (a service has no pinentry prompt).
func TestCommit_IgnoresTheUsersSigningAndHooks(t *testing.T) {
	home := t.TempDir()
	hooks := filepath.Join(home, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\necho user hook ran >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(home, "gitconfig")
	conf := "[commit]\n\tgpgsign = true\n[push]\n\tgpgSign = true\n[gpg]\n\tprogram = " + filepath.ToSlash(filepath.Join(home, "no-such-signer")) +
		"\n[core]\n\thooksPath = " + filepath.ToSlash(hooks) + "\n[user]\n\temail = u@example.test\n\tname = U\n"
	if err := os.WriteFile(cfg, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "a.md", "a\n")
	// A hook of the repository itself is the user's commit-time config too.
	if err := os.WriteFile(filepath.Join(dir, ".git", "hooks", "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Commit(dir, "write a", "", ""); err != nil {
		t.Fatalf("Commit under the user's gpgsign and failing hook: %v", err)
	}
	if got := runIn(t, dir, "rev-list", "--count", "HEAD"); got != "1" {
		t.Errorf("commits = %s, want 1", got)
	}
	// push.gpgSign=true would make the push a signed one, which a remote refuses
	// or a pinentry prompt blocks.
	bare := filepath.Join(t.TempDir(), "remote.git")
	runIn(t, filepath.Dir(bare), "init", "--bare", bare)
	if err := AddRemote(dir, "origin", bare); err != nil {
		t.Fatal(err)
	}
	br, _ := Branch(dir)
	if err := Push(dir, "origin", br); err != nil {
		t.Fatalf("Push under the user's push.gpgSign: %v", err)
	}
}

// stuckGit puts a wrapper first in PATH that, on "commit" only, runs script
// (it sees $GIT_DIR_LOCK, the index.lock path) and then sleeps: the stuck git.
func stuckGit(t *testing.T, beforeSleep string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stuck git is a POSIX shell wrapper")
	}
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not in PATH")
	}
	bin := t.TempDir()
	wrapper := "#!/bin/sh\ncase \" $* \" in *\" commit \"*) " + beforeSleep + "; sleep 30;; esac\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	prev := CommitTimeout
	CommitTimeout = 500 * time.Millisecond
	t.Cleanup(func() { CommitTimeout = prev })
}

// A commit that blocks (a hook the isolation does not cover, a stuck prompt) is
// stopped at CommitTimeout instead of holding the KB's lock.
func TestCommit_IsBounded(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := kbRepo(t)
	runIn(t, dir, "config", "user.email", "t@example.test")
	writeFile(t, dir, "b.md", "b\n")
	runIn(t, dir, "add", "b.md")
	stuckGit(t, "true")
	start := time.Now()
	err := Commit(dir, "stuck", "", "")
	if err == nil || !strings.Contains(err.Error(), "no answer after") {
		t.Fatalf("a stuck commit = %v, want it stopped with the timeout error", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("stopped after %s", d)
	}
}

// The SIGKILL leaves the index.lock the commit held; the next write would fail
// on it. A lock newer than the commit's start is ours and is removed; an older
// one is someone else's and stays, named in the error.
func TestCommit_TimeoutClearsTheLockItLeft(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := kbRepo(t)
	lock := filepath.Join(dir, ".git", "index.lock")
	writeFile(t, dir, "b.md", "b\n")
	runIn(t, dir, "add", "b.md")
	stuckGit(t, "touch '"+filepath.ToSlash(lock)+"'")
	if err := Commit(dir, "stuck", "", ""); err == nil || !strings.Contains(err.Error(), "no answer after") {
		t.Fatalf("a stuck commit = %v, want the timeout error", err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Errorf("the killed commit's index.lock is still there (%v)", err)
	}
	// And the next write goes through.
	t.Setenv("PATH", strings.SplitN(os.Getenv("PATH"), string(os.PathListSeparator), 2)[1])
	if err := Commit(dir, "after", "", ""); err != nil {
		t.Fatalf("Commit after the timeout: %v", err)
	}
}

func TestCommit_TimeoutKeepsAnOlderLock(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := kbRepo(t)
	lock := filepath.Join(dir, ".git", "index.lock")
	// The wrapper leaves a lock dated long before the commit's start: another
	// process's, as far as the commit can tell (a lock present before the commit
	// would stop its own git add).
	stuckGit(t, "touch -t 202001010000 '"+filepath.ToSlash(lock)+"'")
	err := Commit(dir, "stuck", "", "")
	if err == nil || !strings.Contains(err.Error(), "predates this commit and was left in place") {
		t.Fatalf("Commit = %v, want the older lock reported", err)
	}
	if _, serr := os.Stat(lock); serr != nil {
		t.Errorf("a lock older than the commit was removed: %v", serr)
	}
}
