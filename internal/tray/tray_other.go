//go:build !windows && !darwin

package tray

import "errors"

// NewUI reports that there is no tray on this platform: Linux has none.
func NewUI() (UI, error) {
	return nil, errors.New("the tray is supported on Windows and macOS only")
}
