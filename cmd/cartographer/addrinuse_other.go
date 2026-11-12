//go:build !windows

package main

import (
	"errors"
	"syscall"
)

// isAddrInUse reports whether err is a bind that failed because the address
// is taken (EADDRINUSE).
func isAddrInUse(err error) bool { return errors.Is(err, syscall.EADDRINUSE) }
