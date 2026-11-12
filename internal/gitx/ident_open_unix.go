//go:build unix

package gitx

import (
	"os"
	"syscall"
)

// openNoBlock opens p for reading without waiting for a writer: an O_RDONLY
// open of a FIFO blocks until one appears, O_NONBLOCK returns at once. The
// descriptor is close-on-exec (os.OpenFile sets it). It is only ever used to
// Stat, never to read, so the flag has no other effect.
func openNoBlock(p string) (*os.File, error) {
	return os.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
