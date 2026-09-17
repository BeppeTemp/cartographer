package kb

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

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
	t.Run("serialises two acquirers", func(t *testing.T) {
		k := mustInitKB(t)
		release, err := k.AcquireProcessLock(time.Second, "first")
		if err != nil {
			t.Fatal(err)
		}
		// A second acquirer with no patience gets a typed error naming the holder.
		// The contender opens its own handle on the same file from this same
		// process, which is contention on both platforms — flock(2) locks an open
		// file description, LockFileEx a handle — so this is deliberate coverage,
		// not an accident of running single-process. On Windows it is also what
		// catches a lock taken over the metadata bytes: the range is mandatory
		// there, so readLockOwner would fail and PID would come back 0.
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
		// pid 0 is never a live process.
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

// Every error from the lock helper used to mean "wait", so an I/O failure cost a
// full timeout and then reported a LockHeldError naming a holder that never
// existed. Only contention is worth polling for.
func TestAcquireProcessLockFailsFastOnANonContentionError(t *testing.T) {
	if isLockBusy(errors.New("boom")) {
		t.Fatal("a generic error must not count as contention")
	}
	k := mustInitKB(t)
	orig := tryLockFileFn
	t.Cleanup(func() { tryLockFileFn = orig })
	tryLockFileFn = func(*os.File) error { return errors.New("boom") }

	start := time.Now()
	_, err := k.AcquireProcessLock(30*time.Second, "writer")
	if err == nil {
		t.Fatal("a failing lock must not report success")
	}
	var held *LockHeldError
	if errors.As(err, &held) {
		t.Errorf("an I/O failure reported as contention: %v", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("the error hides its cause: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waited %s before failing, which means the poll loop was entered", elapsed)
	}
}

// processAlive decides whether a lock is stale and may be reclaimed, so it is
// asserted on every platform: it must recognise this very process, and must not
// claim a pid no process can have. The reason it gives is platform-specific and
// deliberately not asserted.
func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("this process is running, so its own pid must be alive")
	}
	// Far above any pid either kernel hands out.
	if processAlive(1 << 30) {
		t.Error("a pid that cannot exist must not hold a KB locked forever")
	}
}

// An aborted rebase left a .git/rebase-merge holding only an autostash, and from
// then on every write failed with a message that named no way out.
func TestSyncRefusesWhileARebaseStateExists(t *testing.T) {
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
