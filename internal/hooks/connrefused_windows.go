package hooks

import (
	"errors"
	"syscall"
)

// wsaeConnRefused is WSAECONNREFUSED; package syscall does not export it on Windows.
const wsaeConnRefused = syscall.Errno(10061)

// isConnRefused: Windows reports a refused dial as WSAECONNREFUSED, not ECONNREFUSED (D214).
func isConnRefused(err error) bool {
	return errors.Is(err, wsaeConnRefused) || errors.Is(err, syscall.ECONNREFUSED)
}
