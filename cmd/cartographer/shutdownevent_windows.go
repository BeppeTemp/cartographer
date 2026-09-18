//go:build windows

package main

import (
	"log"

	"golang.org/x/sys/windows"

	"github.com/BeppeTemp/cartographer/internal/defaults"
)

// watchShutdownEvent creates the named event `internal/service` sets to ask for
// a graceful shutdown (D217) and returns a channel closed when it is signalled.
//
// It is the Windows stand-in for SIGTERM, and it exists because nothing else can
// deliver one here: Stop-ScheduledTask terminates the action, and
// GenerateConsoleCtrlEvent cannot reach a process that runs without a console,
// which a task's action does. Without it `service restart --wait` could only
// kill the server, losing the in-flight requests and the pending pushes that
// D76 WP4's drain exists to flush.
//
// A failure to create the event is logged and ignored: the server starts and
// behaves exactly as it did before this existed. Refusing to start because a
// shutdown channel could not be opened would trade a degraded stop for no
// service at all.
func watchShutdownEvent() <-chan struct{} {
	name, err := windows.UTF16PtrFromString(defaults.WindowsShutdownEventName)
	if err != nil {
		log.Printf("warning: shutdown event name %q invalid: %v", defaults.WindowsShutdownEventName, err)
		return nil
	}
	// Manual reset: the event stays signalled once set, so the wait below cannot
	// miss it and a second waiter (an upgrade racing a stop) sees it too.
	// ERROR_ALREADY_EXISTS is not a failure — CreateEvent still returns a usable
	// handle to the existing event, which is what a second server of the same
	// user in the same session would get.
	h, err := windows.CreateEvent(nil, 1, 0, name)
	if err != nil && err != windows.ERROR_ALREADY_EXISTS {
		log.Printf("warning: graceful shutdown event unavailable (%v): this server can only be stopped abruptly", err)
		return nil
	}
	ch := make(chan struct{})
	go func() {
		// One wait, unbounded: the goroutine's whole purpose is to outlive
		// everything until either the event fires or the process exits.
		if _, err := windows.WaitForSingleObject(h, windows.INFINITE); err != nil {
			log.Printf("warning: wait on shutdown event failed: %v", err)
			return
		}
		close(ch)
	}()
	return ch
}
