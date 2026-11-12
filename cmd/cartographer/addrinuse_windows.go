//go:build windows

package main

import (
	"errors"
	"syscall"
)

// wsaeAddrInUse is WSAEADDRINUSE: Winsock's own number, not the invented
// syscall.EADDRINUSE of this package, which no socket call returns.
const wsaeAddrInUse = syscall.Errno(10048)

// isAddrInUse reports whether err is a bind that failed because the address
// is taken (WSAEADDRINUSE).
func isAddrInUse(err error) bool { return errors.Is(err, wsaeAddrInUse) }
