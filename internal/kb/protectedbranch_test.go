package kb

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// D903: Cartographer never opens a pull request, so a push the remote refuses
// because the branch is protected is reported as that, with what to do about it,
// and is not retried like a network error. The commit stays safe in the clone.
func TestSyncOut_ProtectedBranchRejectionIsReportedAsSuch(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	for name, message := range map[string]string{
		"github":  "remote: error: GH006: Protected branch update failed for refs/heads/main.",
		"gitlab":  "remote: GitLab: You are not allowed to push code to protected branches on this project.",
		"generic": "remote: push rules declined by the pre-receive hook",
	} {
		message := message
		t.Run(name, func(t *testing.T) {
			k, bare := setupKBWithRemote(t)
			// An operator identity is configured: a refusal with no identity
			// wording is then not blamed on the identity (D1063).
			k.SetDefaultIdentity("Ada Example", "ada@example.test")
			if err := k.SyncOut(); err != nil { // the seed push, before the branch is protected
				t.Fatalf("seed SyncOut: %v", err)
			}
			protectBranch(t, bare, message)
			if err := k.WriteFileAtomic("data/a.md", []byte("a\n")); err != nil {
				t.Fatal(err)
			}
			if _, err := k.CommitOp("write a"); err != nil {
				t.Fatalf("CommitOp: %v", err)
			}

			err := k.SyncOut()
			var pb *ErrProtectedBranch
			if !errors.As(err, &pb) {
				t.Fatalf("SyncOut err = %v, want *ErrProtectedBranch", err)
			}
			branch, _ := gitx.Branch(k.Root)
			if pb.Branch != branch {
				t.Errorf("Branch = %q, want %q", pb.Branch, branch)
			}
			for _, want := range []string{"refused the push", "protected", "unprotect the branch", "`branch`", "never opens a pull request", "committed and safe"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not say %q", err, want)
				}
			}
			s := k.GitStatusSnapshot()
			if s.State != "failed" || s.Attempts != 1 || !strings.Contains(s.LastError, "unprotect the branch") {
				t.Errorf("status = %+v, want failed after one attempt (not retried) naming the way out", s)
			}
			if n := s.UnpushedCommits; n == nil || *n != 1 {
				t.Errorf("unpushed = %v, want the one local commit kept", n)
			}
		})
	}
}

// A rejection that is not about protection stays the generic push failure.
func TestProtectedBranchRejectionIsMatchedOnItsWording(t *testing.T) {
	t.Parallel()
	if !gitx.IsProtectedBranchRejection("remote: error: GH006: Protected branch update failed") ||
		!gitx.IsProtectedBranchRejection("! [remote rejected] main -> main (pre-receive hook declined)") ||
		!gitx.IsProtectedBranchRejection("You are not allowed to push code to protected branches") {
		t.Error("a protected-branch rejection is not recognised")
	}
	for _, other := range []string{
		"fatal: unable to access 'https://git.example.com/': Could not resolve host",
		"! [rejected] main -> main (non-fast-forward)",
		"Permission denied (publickey).",
	} {
		if gitx.IsProtectedBranchRejection(other) {
			t.Errorf("%q was taken for a protected branch", other)
		}
	}
}

// The two timings of the sync are constants, with Cloud Run the one exception.
func TestSyncOutDebounceForCloudRun(t *testing.T) {
	t.Parallel()
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := SyncOutDebounceFor(env(nil)); got != 3*time.Second {
		t.Errorf("default debounce = %v, want 3s", got)
	}
	if DefaultSyncInWindow != 30*time.Second {
		t.Errorf("DefaultSyncInWindow = %v, want 30s", DefaultSyncInWindow)
	}
	if got := SyncOutDebounceFor(env(map[string]string{"K_SERVICE": "cartographer-demo"})); got != 0 {
		t.Errorf("on Cloud Run the debounce = %v, want 0: the push is made before the response returns", got)
	}
}
