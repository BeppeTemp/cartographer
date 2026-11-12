//go:build windows

package service

import "golang.org/x/sys/windows"

// processElevated reports whether this process holds an elevated token. A
// failure to ask reads as not elevated: the install then goes on as it did.
func processElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}
