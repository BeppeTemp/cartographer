//go:build !windows

package hooks

import (
	"errors"
	"syscall"
)

// isConnRefused: the per-OS split exists because Windows uses WSAECONNREFUSED (connrefused_windows.go) (D214).
func isConnRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
