//go:build windows || darwin

package tray

import "errors"

// NewUI is a placeholder until the platform implementations land (the
// notification-area UI for Windows, the menu-bar UI for macOS): each replaces
// this file's build constraint. Nothing starts `cartographer tray` yet.
func NewUI() (UI, error) {
	return nil, errors.New("the tray UI is not implemented yet on this build")
}
