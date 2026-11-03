//go:build !windows

package service

import "fmt"

// requestTrayClose has no meaning off Windows: the macOS tray is stopped by
// launchd, which sends SIGTERM. Tests stub closeTrayWindow.
func requestTrayClose() error {
	return fmt.Errorf("service: no tray window to close on %s", goos)
}
