//go:build windows

package kb

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// lockExclusive opens path with no share mode, so any other open fails with
// ERROR_SHARING_VIOLATION until the handle is closed. The handle asks for write
// access too: a read-only, share-none handle does not refuse a read-only open.
func lockExclusive(t *testing.T, path string) windows.Handle {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestReadFileRetrying_SucceedsOnceTheHandleCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.md")
	if err := os.WriteFile(path, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := lockExclusive(t, path)
	defer windows.CloseHandle(h)
	orig := readRetrySleep
	t.Cleanup(func() { readRetrySleep = orig })
	sleeps := 0
	readRetrySleep = func(time.Duration) {
		sleeps++
		if sleeps == 1 {
			windows.CloseHandle(h)
		}
	}
	data, err := readFileRetrying(path)
	if err != nil {
		t.Fatalf("readFileRetrying: %v", err)
	}
	if string(data) != "body" || sleeps != 1 {
		t.Fatalf("data = %q, sleeps = %d; want body after one sleep", data, sleeps)
	}
}

func TestReadFileRetrying_GivesUpAfterTheLastAttempt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.md")
	if err := os.WriteFile(path, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := lockExclusive(t, path)
	defer windows.CloseHandle(h)
	orig := readRetrySleep
	t.Cleanup(func() { readRetrySleep = orig })
	sleeps := 0
	readRetrySleep = func(time.Duration) { sleeps++ }
	_, err := readFileRetrying(path)
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("err = %v, want a sharing violation", err)
	}
	if sleeps != 5 {
		t.Fatalf("sleeps = %d, want 5 (6 attempts)", sleeps)
	}
}

func TestReadFileRetrying_DoesNotRetryOtherErrors(t *testing.T) {
	orig := readRetrySleep
	t.Cleanup(func() { readRetrySleep = orig })
	readRetrySleep = func(time.Duration) { t.Error("slept on a missing file") }
	if _, err := readFileRetrying(filepath.Join(t.TempDir(), "missing.md")); !os.IsNotExist(err) {
		t.Fatalf("err = %v, want not-exist", err)
	}
}
