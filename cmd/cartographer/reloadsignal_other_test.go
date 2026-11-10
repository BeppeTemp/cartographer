//go:build !windows

package main

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// SIGHUP reloads: the signal channel of watchReloadSignal fires on a real HUP.
func TestWatchReloadSignalFiresOnSIGHUP(t *testing.T) {
	ch, stop := watchReloadSignal()
	if ch == nil {
		t.Skip("no SIGHUP on this platform")
	}
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("SIGHUP did not reach the reload channel")
	}
}
