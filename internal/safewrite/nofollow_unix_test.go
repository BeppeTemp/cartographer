//go:build !windows

package safewrite

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func skipModeCheck() bool { return false }

func umask() os.FileMode {
	m := syscall.Umask(0)
	syscall.Umask(m)
	return os.FileMode(m)
}

func TestWriteRefusesAFifoSwappedInAndDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "leaf")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	swapFor(t, func() {
		_ = os.Remove(path)
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Fatal(err)
		}
		// A reader makes the open succeed: the fstat must refuse it.
		go func() {
			if f, err := os.OpenFile(path, os.O_RDONLY, 0); err == nil {
				time.Sleep(2 * time.Second)
				f.Close()
			}
		}()
		time.Sleep(100 * time.Millisecond)
	})
	done := make(chan error, 1)
	go func() { done <- WriteFileNoFollow(path, []byte("new"), 0o644) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrSymlinkDestination) {
			t.Fatalf("err = %v, want ErrSymlinkDestination", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the write blocked on a FIFO")
	}
}
