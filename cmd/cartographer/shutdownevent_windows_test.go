//go:build windows

package main

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/BeppeTemp/cartographer/internal/defaults"
)

// The named event is the only graceful stop this platform can deliver (D217), so
// the thing worth asserting is the handshake itself: what `serve` creates, the
// Manager can open and set, and the waiter notices.
//
// This test runs on the `test-windows` CI leg only — there is no way to fake it
// from a POSIX host, which is also why `internal/service` keeps the
// setShutdownEvent seam for its own half of the same handshake.
func TestWatchShutdownEvent_FiresWhenTheEventIsSet(t *testing.T) {
	ch := watchShutdownEvent()
	if ch == nil {
		t.Fatal("watchShutdownEvent returned nil on Windows: the server would have no graceful stop")
	}

	select {
	case <-ch:
		t.Fatal("the channel fired before the event was set")
	case <-time.After(50 * time.Millisecond):
	}

	name, err := windows.UTF16PtrFromString(defaults.WindowsShutdownEventName)
	if err != nil {
		t.Fatal(err)
	}
	// Exactly what internal/service does, and from this process rather than from
	// another only because a test has no second process to spend: the Local\
	// namespace makes it reachable to any process of this user in this session.
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		t.Fatalf("OpenEvent on the event serve just created: %v", err)
	}
	defer windows.CloseHandle(h)
	if err := windows.SetEvent(h); err != nil {
		t.Fatalf("SetEvent: %v", err)
	}

	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("setting the event did not wake the waiter")
	}
}

// A second waiter must not fail: CreateEvent returns ERROR_ALREADY_EXISTS with a
// usable handle, which is what a second server of the same user would get, and
// treating that as a failure would leave it with no graceful stop.
func TestWatchShutdownEvent_SecondWaiterStillGetsAChannel(t *testing.T) {
	first := watchShutdownEvent()
	if first == nil {
		t.Fatal("first waiter got no channel")
	}
	if second := watchShutdownEvent(); second == nil {
		t.Fatal("a second waiter got no channel: ERROR_ALREADY_EXISTS was treated as a failure")
	}
}
