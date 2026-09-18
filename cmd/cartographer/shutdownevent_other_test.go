//go:build !windows

package main

import "testing"

// Off Windows the shutdown-event arm of serve's select must be unreachable, and a
// nil channel is what makes it so. If this ever returned a real channel, the arm
// would race with signal.Notify for the same shutdown — the platform where
// SIGTERM works is the platform that must keep using it.
func TestWatchShutdownEvent_IsNilOffWindows(t *testing.T) {
	if ch := watchShutdownEvent(); ch != nil {
		t.Error("watchShutdownEvent returned a non-nil channel off Windows")
	}
}
