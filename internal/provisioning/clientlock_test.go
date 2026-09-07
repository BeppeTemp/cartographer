package provisioning_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// TestLockClientState_Serializes: the second acquisition fails with a message
// naming the file, rather than proceeding and losing the first writer's
// entries (D172).
func TestLockClientState_Serializes(t *testing.T) {
	dir := t.TempDir()
	release, err := provisioning.LockClientState(dir, time.Second)
	if err != nil {
		t.Fatalf("first acquisition: %v", err)
	}

	start := time.Now()
	_, err = provisioning.LockClientState(dir, 300*time.Millisecond)
	if err == nil {
		t.Fatal("the second acquisition should have failed while the lock is held")
	}
	if waited := time.Since(start); waited < 250*time.Millisecond {
		t.Errorf("it should wait for the timeout before giving up, waited %s", waited)
	}
	if !strings.Contains(err.Error(), provisioning.ClientLockFileName) {
		t.Errorf("the error must name the lock file: %v", err)
	}

	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	// Released: the next caller gets it.
	release2, err := provisioning.LockClientState(dir, time.Second)
	if err != nil {
		t.Fatalf("acquisition after release: %v", err)
	}
	if err := release2(); err != nil {
		t.Fatalf("second release: %v", err)
	}

	if _, err := filepath.Abs(filepath.Join(dir, provisioning.ClientLockFileName)); err != nil {
		t.Fatal(err)
	}
}

// TestLockClientState_SequentialAcquisitions: back-to-back acquisitions all
// succeed — the lock serializes, it does not accumulate state.
func TestLockClientState_SequentialAcquisitions(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		release, err := provisioning.LockClientState(dir, time.Second)
		if err != nil {
			t.Fatalf("acquisition %d: %v", i, err)
		}
		if err := release(); err != nil {
			t.Fatalf("release %d: %v", i, err)
		}
	}
}
