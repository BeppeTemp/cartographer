//go:build !windows

package kb

import "os"

// readFileRetrying is os.ReadFile: POSIX rename leaves an open of the
// destination either the old file or the new one, with no sharing violation.
func readFileRetrying(path string) ([]byte, error) { return os.ReadFile(path) }
