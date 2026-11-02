//go:build darwin

package tray

import "errors"

// NewUI is a placeholder until the macOS menu-bar UI lands (the Windows one is
// tray_windows.go): it replaces this file. Nothing runs the tray on macOS yet.
func NewUI() (UI, error) {
	return nil, errors.New("the tray UI is not implemented yet on this build")
}
