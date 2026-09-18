//go:build !windows

package service

import "fmt"

// signalShutdownEvent has no meaning off Windows: launchd and systemd deliver a
// real SIGTERM, which is what signalGraceful uses there.
//
// It is not unreachable code, though — goos is a package var, so a test can
// drive the Windows branch of signalGraceful from a unix host. Those tests stub
// setShutdownEvent; this error is what an unstubbed one gets, and saying so is
// more useful than a panic.
func signalShutdownEvent() error {
	return fmt.Errorf("service: the Windows shutdown event does not exist on %s", goos)
}
