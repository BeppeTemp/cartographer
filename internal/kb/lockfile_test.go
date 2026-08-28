package kb

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("flock semantics differ on Windows")
	}
}

func mustInitKB(t *testing.T) *KB {
	t.Helper()
	dir, err := os.MkdirTemp("", "kb-lock-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	k, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func gitStatusPorcelain(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	return string(out), err
}

// Before D155 the only serialisation was an in-process mutex, blind to any other
// process: `cartographer import` wrote into the same directory the sync loop
// managed, and the two interleaving corrupted the git index.
func TestAcquireProcessLock(t *testing.T) {
	skipWindows(t)

	t.Run("serialises two acquirers", func(t *testing.T) {
		k := mustInitKB(t)
		release, err := k.AcquireProcessLock(time.Second, "first")
		if err != nil {
			t.Fatal(err)
		}
		// A second acquirer with no patience gets a typed error naming the holder.
		_, err = k.AcquireProcessLock(0, "second")
		var held *LockHeldError
		if !errors.As(err, &held) {
			t.Fatalf("second acquire = %v, want LockHeldError", err)
		}
		if held.PID != os.Getpid() || !strings.Contains(held.Error(), "first") {
			t.Errorf("error does not name the holder: %v", held)
		}
		release()
		// Released: available again.
		release2, err := k.AcquireProcessLock(time.Second, "third")
		if err != nil {
			t.Fatalf("acquire after release: %v", err)
		}
		release2()
	})

	t.Run("a lock whose pid is dead is reclaimed", func(t *testing.T) {
		k := mustInitKB(t)
		// pid 0 is never a live process for Kill(pid, 0).
		lockPath := filepath.Join(k.Root, LockFileName)
		if err := os.WriteFile(lockPath, []byte("0\n2020-01-01T00:00:00Z\nghost\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		release, err := k.AcquireProcessLock(0, "live")
		if err != nil {
			t.Fatalf("a stale lock must not block a KB forever: %v", err)
		}
		release()
	})

	t.Run("concurrent acquirers do not overlap", func(t *testing.T) {
		k := mustInitKB(t)
		var mu sync.Mutex
		inside, maxInside := 0, 0
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				release, err := k.AcquireProcessLock(5*time.Second, "worker")
				if err != nil {
					t.Errorf("acquire: %v", err)
					return
				}
				mu.Lock()
				inside++
				if inside > maxInside {
					maxInside = inside
				}
				mu.Unlock()
				time.Sleep(10 * time.Millisecond)
				mu.Lock()
				inside--
				mu.Unlock()
				release()
			}()
		}
		wg.Wait()
		if maxInside != 1 {
			t.Errorf("%d acquirers were inside the lock at once", maxInside)
		}
	})

	t.Run("the lock file never dirties the working tree", func(t *testing.T) {
		k := mustInitKB(t)
		release, err := k.AcquireProcessLock(time.Second, "writer")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		out, err := gitStatusPorcelain(k.Root)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, LockFileName) {
			t.Errorf("the lock file shows in git status, which blocks a server-profile PR reconciliation:\n%s", out)
		}
	})
}

// An aborted rebase left a .git/rebase-merge holding only an autostash, and from
// then on every write failed with a message that named no way out.
func TestSyncRefusesWhileARebaseStateExists(t *testing.T) {
	skipWindows(t)

	t.Run("orphan autostash", func(t *testing.T) {
		k := mustInitKB(t)
		dir := filepath.Join(k.Root, ".git", "rebase-merge")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "autostash"), []byte("deadbeef\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		kind, present := gitx.RebaseInProgress(k.Root)
		if !present || kind != gitx.RebaseStateOrphanAutostash {
			t.Fatalf("RebaseInProgress = %q, %v", kind, present)
		}
		err := k.SyncOut()
		var state *ErrRebaseStatePresent
		if !errors.As(err, &state) {
			t.Fatalf("SyncOut = %v, want ErrRebaseStatePresent", err)
		}
		// The message must name the way out, which is the whole point.
		for _, want := range []string{"only an autostash", "stash list", "remove the directory"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message %q does not mention %q", err, want)
			}
		}
	})

	t.Run("a real rebase in progress", func(t *testing.T) {
		k := mustInitKB(t)
		dir := filepath.Join(k.Root, ".git", "rebase-merge")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "head-name"), []byte("refs/heads/main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		kind, present := gitx.RebaseInProgress(k.Root)
		if !present || kind != gitx.RebaseStateNormal {
			t.Fatalf("RebaseInProgress = %q, %v", kind, present)
		}
		err := k.SyncOut()
		if err == nil || !strings.Contains(err.Error(), "rebase --continue") {
			t.Fatalf("SyncOut = %v, want the continue/abort remedy", err)
		}
	})

	t.Run("a clean repo syncs", func(t *testing.T) {
		k := mustInitKB(t)
		if _, present := gitx.RebaseInProgress(k.Root); present {
			t.Fatal("a fresh KB must have no rebase state")
		}
		if err := k.checkRebaseState(); err != nil {
			t.Errorf("checkRebaseState on a clean repo = %v", err)
		}
	})
}
