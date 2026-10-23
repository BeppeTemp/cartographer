package gitx

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// silentRemote accepts TCP connections and never answers: the shape of a
// remote behind a VPN that is down (#348), without waiting for the OS connect
// timeout. Its cleanup closes the listener and every accepted connection.
func silentRemote(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	return "http://" + ln.Addr().String() + "/kb.git"
}

// removeWhenFree is t.TempDir for a directory a killed git may leave a child
// in: before the Windows tree kill (#1163), cancelling Fetch killed git.exe but
// not git-remote-http, which kept its working directory open until the silent
// remote's connection closed (#784). A child the kill tree misses still exits
// late on a loaded runner, so the removal still retries: registered before
// silentRemote, it runs after it and retries until the child is gone.
func removeWhenFree(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gitx-fetch-timeout-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// 30s, not 10s: a loaded Windows runner has kept the child past 10s (#902 CI).
		deadline := time.Now().Add(30 * time.Second)
		for {
			err := os.RemoveAll(dir)
			if err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("remove %s: %v", dir, err)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	return dir
}

func TestFetch_SilentRemoteTimesOut(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := removeWhenFree(t)
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", silentRemote(t)).CombinedOutput(); err != nil {
		t.Fatalf("remote add: %v\n%s", err, out)
	}
	defer func(d time.Duration) { FetchTimeout = d }(FetchTimeout)
	FetchTimeout = 500 * time.Millisecond

	start := time.Now()
	err := Fetch(dir, "origin")
	var rte *RemoteTimeoutError
	if !errors.As(err, &rte) {
		t.Fatalf("Fetch = %v, want a RemoteTimeoutError", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("Fetch took %s, want it bounded by FetchTimeout", took)
	}
}

func TestPush_SilentRemoteTimesOut(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := removeWhenFree(t)
	if out, err := exec.Command("git", "init", "-q", "-b", "main", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", silentRemote(t)).CombinedOutput(); err != nil {
		t.Fatalf("remote add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "-c", "user.name=T", "-c", "user.email=t@example.test",
		"commit", "--allow-empty", "-q", "-m", "x").CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	defer func(d time.Duration) { PushTimeout = d }(PushTimeout)
	PushTimeout = 500 * time.Millisecond

	start := time.Now()
	err := Push(dir, "origin", "main")
	var rte *RemoteTimeoutError
	if !errors.As(err, &rte) {
		t.Fatalf("Push = %v, want a RemoteTimeoutError", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("Push took %s, want it bounded by PushTimeout", took)
	}
}
