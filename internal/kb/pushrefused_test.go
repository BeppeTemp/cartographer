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

func commitOne(t *testing.T, k *KB) {
	t.Helper()
	if err := k.WriteFileAtomic("data/a.md", []byte("a\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOpAs("write a", "", ""); err != nil {
		t.Fatalf("CommitOp: %v", err)
	}
}

func assertRefusedOnce(t *testing.T, k *KB, err error, wantRemedy string) {
	t.Helper()
	var pr *ErrPushRefused
	if !errors.As(err, &pr) {
		t.Fatalf("SyncOut err = %v, want *ErrPushRefused", err)
	}
	for _, want := range []string{wantRemedy, "not retried", "committed and safe"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
	s := k.GitStatusSnapshot()
	if s.State != "failed" || s.Attempts != 1 {
		t.Errorf("status = %+v, want failed after one attempt (not retried five times)", s)
	}
	if n := s.UnpushedCommits; n != nil && *n != 1 {
		t.Errorf("unpushed = %d, want the one local commit kept", *n)
	}
}

// A read-only credential (HTTP 403) is the remote's decision: not retried, and the
// operator is told what the credential lacks.
func TestSyncOut_ReadOnlyCredentialFailsAtOnce(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	url, _ := gitxtest.ReadOnlyHTTPRemote(t, "token", "s3cret")
	token := filepath.Join(t.TempDir(), "t")
	if err := os.WriteFile(token, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	k, err := Init(filepath.Join(t.TempDir(), "kb"))
	if err != nil {
		t.Fatal(err)
	}
	if err := gitx.AddRemote(k.Root, "origin", url); err != nil {
		t.Fatal(err)
	}
	k.GitEnv = append(gitxtest.IsolatedEnv(), gitxtest.TokenEnv(token)...)
	k.SetDefaultIdentity("Ada Example", "ada@example.test")
	commitOne(t, k)
	assertRefusedOnce(t, k, k.SyncOut(), "this credential cannot push")
}

// A repository rule (GitHub GH013) arrives as "[remote rejected] ... (hook declined)"
// from an update hook here, not as a protected branch: one attempt, the rule line quoted.
func TestSyncOut_RepositoryRuleFailsAtOnce(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	k.SetDefaultIdentity("Ada Example", "ada@example.test")
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	hook := "#!/bin/sh\necho 'remote: error: GH013: Repository rule violations found for refs/heads/main.' >&2\necho 'remote: - Commits must have verified signatures.' >&2\nexit 1\n"
	if err := os.MkdirAll(filepath.Join(bare, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bare, "hooks", "update"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	commitOne(t, k)
	assertRefusedOnce(t, k, k.SyncOut(), "the remote requires signed commits: Cartographer does not sign them")
	if s := k.GitStatusSnapshot(); !strings.Contains(s.LastError, "Commits must have verified signatures") {
		t.Errorf("last_error %q does not quote the remote's rule line", s.LastError)
	}
}
