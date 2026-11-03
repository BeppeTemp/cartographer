//go:build darwin

package tray

import (
	"fmt"
	"os"
)

// SingleInstance is called by cmdTray before anything polls or logs: a second
// tray in the same session (a hand-typed command next to the LaunchAgent) gets
// second == true and exits with status 0, which the LaunchAgent's KeepAlive on
// unsuccessful exit does not respawn. release is never nil.
func SingleInstance() (release func(), second bool) {
	warn := func(msg string) { fmt.Fprintln(os.Stderr, msg) }
	path, err := trayLockPath()
	if err != nil {
		warn("tray: single-instance lock unavailable, running unguarded: " + err.Error())
		return func() {}, false
	}
	return singleInstanceAt(path, warn)
}
