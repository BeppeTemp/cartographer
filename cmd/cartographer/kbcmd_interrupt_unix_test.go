//go:build !windows

package main

import (
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// onInterrupt reacts to the first signal only, stop may be called twice (kb
// clone calls it right after the clone and again on return), and once it has
// fired the handler is released so a second Ctrl-C is not swallowed.
func TestOnInterruptFiresOnceAndReleases(t *testing.T) {
	var fired int32
	stop := onInterrupt(func() { atomic.AddInt32(&fired, 1) })
	other := make(chan os.Signal, 4)
	signal.Notify(other, syscall.SIGINT) // keeps the test process alive
	defer signal.Stop(other)

	_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	for i := 0; atomic.LoadInt32(&fired) == 0 && i < 200; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&fired) != 1 {
		t.Fatal("fn did not run on SIGINT")
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	time.Sleep(100 * time.Millisecond)
	if atomic.LoadInt32(&fired) != 1 {
		t.Errorf("fn ran %d times, want once", fired)
	}
	stop()
	stop() // idempotent: must not panic on a closed channel
}
