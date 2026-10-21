package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

// A forge's HTTPS endpoint is the transport the token helper exists for, and
// nothing met one: TestCloneEnv checks env strings. These tests run the real
// git-http-backend behind basic auth (D996).

const httpTestToken = "s3cret-token"

func writeToken(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "default.token")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func runIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.test",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.test")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// seededHTTPRemote is an authenticated remote already holding one commit on main.
func seededHTTPRemote(t *testing.T) (url, bare string) {
	t.Helper()
	url, bare = gitxtest.HTTPRemote(t, "token", httpTestToken)
	seed := filepath.Join(t.TempDir(), "seed")
	runIn(t, filepath.Dir(seed), "init", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "a.md"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runIn(t, seed, "add", "a.md")
	runIn(t, seed, "commit", "-m", "seed")
	runIn(t, seed, "push", bare, "main")
	return url, bare
}

func TestHTTPRemote_CloneFetchPushWithTheRightToken(t *testing.T) {
	t.Parallel()
	url, bare := seededHTTPRemote(t)
	env := append(gitxtest.IsolatedEnv(), gitxtest.TokenEnv(writeToken(t, httpTestToken+"\n"))...)
	dest := filepath.Join(t.TempDir(), "clone")
	if err := Clone(context.Background(), url, dest, env...); err != nil {
		t.Fatalf("Clone with the right token: %v", err)
	}
	// A second commit lands on the remote through another path; Fetch sees it.
	other := filepath.Join(t.TempDir(), "other")
	runIn(t, filepath.Dir(other), "clone", bare, other)
	runIn(t, other, "commit", "--allow-empty", "-m", "second")
	runIn(t, other, "push", "origin", "main")
	if err := Fetch(dest, "origin", env...); err != nil {
		t.Fatalf("Fetch with the right token: %v", err)
	}
	if got := runIn(t, dest, "rev-list", "--count", "origin/main"); got != "2" {
		t.Errorf("origin/main has %s commits after the fetch, want 2", got)
	}
	runIn(t, dest, "merge", "--ff-only", "origin/main")
	runIn(t, dest, "commit", "--allow-empty", "-m", "third")
	if err := Push(dest, "origin", "main", env...); err != nil {
		t.Fatalf("Push with the right token: %v", err)
	}
	if got := runIn(t, bare, "rev-list", "--count", "main"); got != "3" {
		t.Errorf("the remote has %s commits after the push, want 3", got)
	}
}

func TestHTTPRemote_WrongTokenSaysTheCredentialsWereRejected(t *testing.T) {
	t.Parallel()
	url, _ := seededHTTPRemote(t)
	env := append(gitxtest.IsolatedEnv(), gitxtest.TokenEnv(writeToken(t, "not-the-token"))...)
	err := Clone(context.Background(), url, filepath.Join(t.TempDir(), "clone"), env...)
	if err == nil || !strings.Contains(err.Error(), "the credentials were rejected") {
		t.Fatalf("Clone with a wrong token: %v, want the credentials-were-rejected remedy", err)
	}
}

func TestHTTPRemote_NoTokenNeverPromptsAndSaysSo(t *testing.T) {
	t.Parallel()
	url, _ := seededHTTPRemote(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err := Clone(ctx, url, filepath.Join(t.TempDir(), "clone"), gitxtest.IsolatedEnv()...)
	if err == nil || !strings.Contains(err.Error(), "git needed credentials and could not ask") {
		t.Fatalf("Clone with no token: %v, want the terminal-prompts-disabled remedy", err)
	}
	if ctx.Err() != nil {
		t.Fatal("the clone waited for a credential prompt")
	}
	// Fetch and Push carry no prompt guard of their own (only Clone sets
	// GIT_TERMINAL_PROMPT=0): the caller's env does, as IsolatedEnv's.
	good := append(gitxtest.IsolatedEnv(), gitxtest.TokenEnv(writeToken(t, httpTestToken))...)
	dest := filepath.Join(t.TempDir(), "clone")
	if err := Clone(context.Background(), url, dest, good...); err != nil {
		t.Fatal(err)
	}
	if err := Fetch(dest, "origin", gitxtest.IsolatedEnv()...); err == nil || !strings.Contains(err.Error(), "terminal prompts disabled") {
		t.Errorf("Fetch with no token: %v, want terminal prompts disabled", err)
	}
	runIn(t, dest, "commit", "--allow-empty", "-m", "x")
	if err := Push(dest, "origin", "main", gitxtest.IsolatedEnv()...); err == nil || !strings.Contains(err.Error(), "terminal prompts disabled") {
		t.Errorf("Push with no token: %v, want terminal prompts disabled", err)
	}
}
