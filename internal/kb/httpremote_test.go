package kb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

// SyncOut and SyncIn over an authenticated smart-HTTP remote, with the git
// environment serve builds for a KB that has a token file (gitEnvForKB, D905),
// and a pre-receive rejection reaching the caller as the "remote:" lines a forge
// sends back (D996).
func TestSync_OverAuthenticatedHTTP(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	url, bare := gitxtest.HTTPRemote(t, "token", "s3cret-token")
	tokenFile := filepath.Join(t.TempDir(), "default.token")
	if err := os.WriteFile(tokenFile, []byte("s3cret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	k, err := Init(filepath.Join(t.TempDir(), "kb"))
	if err != nil {
		t.Fatal(err)
	}
	if err := gitx.AddRemote(k.Root, "origin", url); err != nil {
		t.Fatal(err)
	}

	// Without the token the push is refused and says what is missing.
	k.GitEnv = gitxtest.IsolatedEnv()
	if err := k.SyncOut(); err == nil || !strings.Contains(err.Error(), "could not read Username") {
		t.Fatalf("SyncOut without a token: %v, want git's missing-credentials message", err)
	}

	k.GitEnv = append(gitxtest.IsolatedEnv(), gitxtest.TokenEnv(tokenFile)...)
	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut with the token: %v", err)
	}
	branch, _ := gitx.Branch(k.Root)
	if got := gitHere(t, bare, "rev-list", "--count", branch); got == "" || got == "0" {
		t.Fatalf("the remote received no commits (count=%q)", got)
	}

	// Another writer publishes; SyncIn brings it in over HTTP.
	pushRemoteCommit(t, url, branch, "data/from-elsewhere.md")
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn with the token: %v", err)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "data", "from-elsewhere.md")); err != nil {
		t.Errorf("SyncIn did not bring the remote's file in: %v", err)
	}

	// A hook declining the push reaches the caller through the HTTP transport.
	protectBranch(t, bare, "remote: error: GH006: Protected branch update failed")
	if err := k.WriteFileAtomic("data/a.md", []byte("a\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("write a"); err != nil {
		t.Fatal(err)
	}
	err = k.SyncOut()
	var pb *ErrProtectedBranch
	if !errors.As(err, &pb) {
		t.Fatalf("SyncOut = %v, want *ErrProtectedBranch", err)
	}
	if !strings.Contains(err.Error(), "GH006") {
		t.Errorf("error %q does not carry the forge's own message", err)
	}
}

// pushRemoteCommit makes a commit with file in a fresh clone of url (the
// token is the one the test server expects) and pushes it.
func pushRemoteCommit(t *testing.T, url, branch, file string) {
	t.Helper()
	tokenFile := filepath.Join(t.TempDir(), "t.token")
	if err := os.WriteFile(tokenFile, []byte("s3cret-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := append(gitxtest.IsolatedEnv(), gitxtest.TokenEnv(tokenFile)...)
	clone := filepath.Join(t.TempDir(), "clone")
	if err := gitx.Clone(t.Context(), url, clone, env...); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(clone, file)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, file), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := gitAs(t, clone, "o@example.test", "o@example.test", "add", "-A"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if out, err := gitAs(t, clone, "o@example.test", "o@example.test", "commit", "-m", "elsewhere"); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	if err := gitx.Push(clone, "origin", branch, env...); err != nil {
		t.Fatal(err)
	}
}
