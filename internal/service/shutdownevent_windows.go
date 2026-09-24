//go:build windows

package service

import (
	"fmt"

	"golang.org/x/sys/windows"

	"github.com/BeppeTemp/cartographer/internal/defaults"
)

// signalShutdownEvent asks a running `serve` for a graceful shutdown by setting
// the named event it waits on (defaults.WindowsShutdownEventName).
//
// This exists because there is no CLI-deliverable SIGTERM on Windows: Stop-
// ScheduledTask kills the action, and GenerateConsoleCtrlEvent cannot reach a
// process that runs without a console — which a Scheduled Task's action does.
// Without the event, Replace would have no drain at all, and D76 WP4's flush of
// pending pushes would be lost on every upgrade.
//
// A failure to open the event is returned, not swallowed: it means no server of
// this user is listening in this session — because none is running, because it
// runs in another logon session (Local\ is per session: an SSH session cannot
// see the desktop's), or because it predates this mechanism. The caller
// (stopServeAndWait) then stops the task outright and says why, rather than
// report a graceful drain it did not perform.
func signalShutdownEvent() error {
	name, err := windows.UTF16PtrFromString(defaults.WindowsShutdownEventName)
	if err != nil {
		return fmt.Errorf("service: shutdown event name: %w", err)
	}
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		return fmt.Errorf("service: open shutdown event %s: %w (no cartographer server of this user is listening in this logon session)", defaults.WindowsShutdownEventName, err)
	}
	defer windows.CloseHandle(h)
	if err := windows.SetEvent(h); err != nil {
		return fmt.Errorf("service: signal shutdown event: %w", err)
	}
	return nil
}
