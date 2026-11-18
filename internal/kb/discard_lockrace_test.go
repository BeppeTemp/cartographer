package kb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A foreign index.lock that clears between the refused discard and the look at
// the lock (a slow git, as on Windows) is a lock that cleared within the wait:
// the discard is tried again, and the failed write leaves nothing (#2241).
func TestDiscardUncommitted_LockClearedBeforeTheLookIsRetried(t *testing.T) {
	k, _ := cleanRemoteKB(t)
	if err := k.WriteFileAtomic("data/left.md", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(k.Root, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	afterDiscardAttempt = func() { os.Remove(lock) }
	t.Cleanup(func() { afterDiscardAttempt = func() {} })
	if err := k.DiscardUncommitted(); err != nil {
		t.Fatalf("DiscardUncommitted: %v", err)
	}
	if st := strings.TrimSpace(gitHere(t, k.Root, "status", "--porcelain")); st != "" {
		t.Fatalf("the failed write left files: %s", st)
	}
}
