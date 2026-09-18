//go:build !windows

package main

// watchShutdownEvent has nothing to watch off Windows: launchd and systemd
// deliver a real SIGTERM, which signal.Notify already handles.
//
// It returns a nil channel on purpose rather than being compiled out of the
// select: a receive on a nil channel blocks forever, so the arm is present and
// unreachable, and the graceful-shutdown code stays one shape on every platform
// instead of two build-tagged copies of the same select.
func watchShutdownEvent() <-chan struct{} { return nil }
