//go:build !windows

package tray

import (
	"path/filepath"
	"testing"
)

func TestAcquireInstanceLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "tray.lock")
	held, release, err := acquireInstanceLock(path)
	if err != nil || !held {
		t.Fatalf("first lock = %v, %v, want held", held, err)
	}
	if again, _, err := acquireInstanceLock(path); err != nil || again {
		t.Fatalf("second lock = %v, %v, want already running (not held, no error)", again, err)
	}
	release()
	held, release, err = acquireInstanceLock(path)
	if err != nil || !held {
		t.Fatalf("lock after release = %v, %v, want held", held, err)
	}
	release()
}
