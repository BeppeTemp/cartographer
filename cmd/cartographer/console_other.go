//go:build !windows

package main

// detachOwnConsole has nothing to do off Windows: launchd and systemd start the
// service with no terminal attached (D1479).
func detachOwnConsole() {}
