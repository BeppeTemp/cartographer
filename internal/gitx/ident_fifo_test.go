//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly

package gitx

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestReadableFileNeverBlocksOnAFifo: an includeIf path naming a FIFO, or a
// symlink to one, is not a regular file. Opening it for reading waits for a
// writer forever, outside any timeout, so the check must refuse it before it
// opens anything. The call runs in a goroutine so a regression fails the test
// instead of hanging it.
func TestReadableFileNeverBlocksOnAFifo(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo.gitconfig")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no FIFO on this filesystem: %v", err)
	}
	link := filepath.Join(dir, "link.gitconfig")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	regular := filepath.Join(dir, "regular.gitconfig")
	if err := os.WriteFile(regular, []byte("[core]\n\tsshCommand = ssh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		path string
		want bool
	}{"fifo": {fifo, false}, "symlink to a fifo": {link, false}, "regular file": {regular, true}} {
		done := make(chan bool, 1)
		go func() { done <- readableFile(tc.path) }()
		select {
		case got := <-done:
			if got != tc.want {
				t.Errorf("%s: readableFile = %v, want %v", name, got, tc.want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: readableFile blocked: it opened the path before checking it is a regular file", name)
		}
	}
}

// TestIncludeIfFifoIsAPerFolderIdentity: the same FIFO named by the global
// config makes includeIfMaySetIdentity answer "may set one" promptly, which
// GlobalIdent reports as per-folder.
func TestIncludeIfFifoIsAPerFolderIdentity(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo.gitconfig")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no FIFO on this filesystem: %v", err)
	}
	cfg := filepath.Join(dir, "gitconfig")
	keys := "includeif.gitdir:~/a/.path\n" + filepath.ToSlash(fifo) + "\x00"
	origin := "file:" + filepath.ToSlash(cfg) + "\x00" + keys
	git := func(args ...string) ([]byte, error) {
		for _, a := range args {
			if a == "--show-origin" {
				return []byte(origin), nil
			}
		}
		t.Errorf("git ran for a FIFO include: %v", args)
		return nil, nil
	}
	done := make(chan bool, 1)
	go func() { done <- includeIfMaySetIdentity(keys, git) }()
	select {
	case got := <-done:
		if !got {
			t.Error("includeIfMaySetIdentity = false for a FIFO include, want true")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("includeIfMaySetIdentity blocked on a FIFO include")
	}
}

// TestOpenNoBlockReturnsOnAFifo pins the open itself, not only the check that
// follows it: a path that is a FIFO by the time it is opened (swapped in after
// any earlier Stat) must not wait for a writer. The descriptor then reports a
// non-regular file, which readableFile turns into false.
func TestOpenNoBlockReturnsOnAFifo(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo.gitconfig")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no FIFO on this filesystem: %v", err)
	}
	type result struct {
		regular bool
		err     error
	}
	done := make(chan result, 1)
	go func() {
		f, err := openNoBlock(fifo)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer f.Close()
		fi, err := f.Stat()
		done <- result{regular: err == nil && fi.Mode().IsRegular(), err: err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("openNoBlock on a FIFO: %v", r.err)
		}
		if r.regular {
			t.Error("the descriptor of a FIFO reports a regular file")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("openNoBlock blocked on a FIFO: it waits for a writer")
	}
}
