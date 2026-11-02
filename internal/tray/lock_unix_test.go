//go:build !windows

package tray

import (
	"os"
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

func TestSingleInstanceAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tray.lock")
	var warned []string
	warn := func(m string) { warned = append(warned, m) }

	release, second := singleInstanceAt(path, warn)
	if second || len(warned) != 0 {
		t.Fatalf("first: second=%v warned=%v", second, warned)
	}
	if _, second := singleInstanceAt(path, warn); !second {
		t.Fatal("second instance not detected")
	}
	release()

	// A lock that cannot be created runs unguarded and says so once.
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, second = singleInstanceAt(filepath.Join(blocked, "tray.lock"), warn)
	if second || len(warned) != 1 {
		t.Fatalf("unavailable lock: second=%v warned=%v", second, warned)
	}
}
