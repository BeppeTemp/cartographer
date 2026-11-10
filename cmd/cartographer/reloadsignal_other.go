//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// watchReloadSignal returns a channel that receives on every SIGHUP, and the
// function that stops the delivery.
func watchReloadSignal() (<-chan struct{}, func()) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP)
	out := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-sigCh:
				select {
				case out <- struct{}{}:
				default: // one reload is already pending
				}
			case <-done:
				return
			}
		}
	}()
	return out, func() { signal.Stop(sigCh); close(done) }
}
