//go:build !windows

package service

// processElevated has no meaning off Windows; the Windows branch is reached from
// a unix host only through the windowsElevated seam.
func processElevated() bool { return false }
