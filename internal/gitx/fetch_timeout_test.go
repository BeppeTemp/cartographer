package gitx

import (
	"errors"
	"net"
	"os/exec"
	"testing"
	"time"
)

// silentRemote accepts TCP connections and never answers: the shape of a
// remote behind a VPN that is down (#348), without waiting for the OS connect
// timeout.
func silentRemote(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
		}
	}()
	return "http://" + ln.Addr().String() + "/kb.git"
}

func TestFetch_SilentRemoteTimesOut(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
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
