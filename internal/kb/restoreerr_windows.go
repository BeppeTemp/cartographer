//go:build windows

package kb

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows"
)

// restoreMayHeal reports whether a failed restore is a file held open by
// another process, which clears by itself: the leftover stays recorded (#2254).
// Git reports it as text, the Go calls as ERROR_SHARING_VIOLATION (#2077).
func restoreMayHeal(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		strings.Contains(err.Error(), "being used by another process")
}
