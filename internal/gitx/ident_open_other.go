//go:build !unix

package gitx

import "os"

// openNoBlock opens p for reading. There is no O_NONBLOCK to ask for here
// (Windows has no FIFO node a plain open waits on), so it is a plain open.
func openNoBlock(p string) (*os.File, error) {
	return os.Open(p)
}
