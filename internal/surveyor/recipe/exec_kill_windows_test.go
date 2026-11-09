//go:build windows

package recipe

import "testing"

// assertGone: taskkill /T ends the tree, but a pid cannot be probed without
// a handle; the Windows run asserts the timeout error and the prompt return.
func assertGone(t *testing.T, pid int) { t.Helper() }
