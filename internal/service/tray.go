package service

// The tray's autostart (D1482): a third per-user definition next to the
// server's and the sync timer's, started at logon so the status icon is there
// without the user typing anything. Linux has no tray, so nothing is written.

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

const trayLaunchdLabel = "com.cartographer.tray"

// TrayLaunchdPlistPath returns ~/Library/LaunchAgents/com.cartographer.tray.plist.
func TrayLaunchdPlistPath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", trayLaunchdLabel+".plist"), nil
}

// TrayLaunchdLogPath returns ~/Library/Logs/cartographer/tray.log.
func TrayLaunchdLogPath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Logs", "cartographer", "tray.log"), nil
}

// TrayWindowsTaskPath returns %LOCALAPPDATA%\cartographer\tasks\tray.xml.
func TrayWindowsTaskPath() (string, error) { return windowsTaskFile("tray.xml") }

// TrayWindowsLogPath returns %LOCALAPPDATA%\cartographer\Logs\tray.log.
func TrayWindowsLogPath() (string, error) { return windowsLogFile("tray.log") }

// RenderWindowsTrayTaskXML renders the logon task that runs
// `<binPath> tray --log-file <logPath>`. It follows RenderWindowsTaskXML (D217:
// a logon trigger scoped to userID, InteractiveToken, IgnoreNew, no time
// limit) with two differences: no RestartOnFailure, because a tray that fails
// for lack of a shell must not loop, and no battery or idle conditions to
// override beyond the ones the server task already turns off.
func RenderWindowsTrayTaskXML(binPath, logPath, userID, sid string) string {
	args := fmt.Sprintf("tray --log-file %s", quotePath(logPath))
	userElem := taskUserElem(userID)
	return fmt.Sprintf(windowsTaskXMLDeclaration+`
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Cartographer status icon</Description>
    <URI>\%s\%s</URI>%s
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>%s
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">%s
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
    </Exec>
  </Actions>
</Task>
`, xmlEscape(windowsTaskFolder), xmlEscape(windowsTrayTaskName), taskSDDLElem(sid), userElem, userElem, xmlEscape(binPath), xmlEscape(args))
}

// RenderTrayLaunchdPlist renders the LaunchAgent that runs `<binPath> tray` in
// the user's GUI session. KeepAlive on an unsuccessful exit restarts a crash,
// while the user's Quit (exit 0) stays quit until the next login. No PATH: the
// tray runs no subprocess but /usr/bin/open.
func RenderTrayLaunchdPlist(binPath, logPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>tray</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, trayLaunchdLabel, xmlEscape(binPath), xmlEscape(logPath), xmlEscape(logPath))
}

// TrayInstalled reports whether the tray's definition is on disk (the file is
// the authority, as for the other units). Always false on Linux.
func (m *Manager) TrayInstalled() bool {
	var p string
	var err error
	switch goos {
	case "darwin":
		p, err = TrayLaunchdPlistPath()
	case "windows":
		p, err = TrayWindowsTaskPath()
	default:
		return false
	}
	if err != nil {
		return false
	}
	_, statErr := os.Stat(p)
	return statErr == nil
}

// InstallTray writes the tray definition, registers it and starts it now, so
// the icon appears without a logoff. Idempotent: a running tray is stopped
// first, because IgnoreNew (Windows) and a loaded job (launchd) would keep the
// old binary and definition otherwise, the reason stopServeForReinstall exists.
// On Linux it does nothing. An error from the registration (typically a
// session with no GUI, such as ssh) is the caller's to report as a warning.
func (m *Manager) InstallTray() error {
	if goos != "darwin" && goos != "windows" {
		return nil
	}
	binPath, err := osExecutable()
	if err != nil {
		return fmt.Errorf("service: resolve binary path: %w", err)
	}
	binPath = resolveStableBinPath(binPath)
	var logPath, defPath string
	if goos == "darwin" {
		if logPath, err = TrayLaunchdLogPath(); err == nil {
			defPath, err = TrayLaunchdPlistPath()
		}
	} else if logPath, err = TrayWindowsLogPath(); err == nil {
		defPath, err = TrayWindowsTaskPath()
	}
	if err != nil {
		return fmt.Errorf("service: resolve tray paths: %w", err)
	}
	// connect installs the tray on its own, so these writes refuse a symlink
	// planted at the destination or in a parent (internal/safewrite).
	base, err := userHomeDir()
	if err != nil {
		return fmt.Errorf("service: resolve home: %w", err)
	}
	for _, d := range []string{filepath.Dir(logPath), filepath.Dir(defPath)} {
		if err := safewrite.MkdirAllNoFollow(base, d, 0o755); err != nil {
			return fmt.Errorf("service: create %s: %w", d, err)
		}
	}
	if goos == "darwin" {
		if err := safewrite.WriteFile(base, defPath, []byte(RenderTrayLaunchdPlist(binPath, logPath)), 0o644); err != nil {
			return fmt.Errorf("service: write tray plist: %w", err)
		}
		if err := m.bootoutAndWait(trayLaunchdLabel, defPath); err != nil {
			return err
		}
		if _, err := m.run("launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), defPath); err != nil {
			return fmt.Errorf("service: launchctl bootstrap tray (needs a logged-in GUI session): %w", err)
		}
		return nil
	}
	// Register first: a refused registration (an old Administrators-owned
	// task) leaves the running tray alone instead of stopped, and the installed
	// definition as it was (#1961).
	guarded := func(path string, data []byte) error { return safewrite.WriteFile(base, path, data, 0o644) }
	if err := m.registerAndReplace(windowsTrayTaskName, defPath, []byte(RenderWindowsTrayTaskXML(binPath, logPath, windowsTaskUser(), windowsTaskSID())), guarded); err != nil {
		return err
	}
	// IgnoreNew turns a start on a running task into a no-op, so a tray that
	// survives the stop would keep the old binary and definition: an error.
	if err := m.stopTrayAndWait(); err != nil {
		return err
	}
	return m.startWindowsTask(windowsTrayTaskName)
}

// UninstallTray stops the tray and removes its definition. A tray that is not
// installed (no definition on disk: the file is the authority) is a success
// and touches nothing. On Windows the running process is stopped before the
// task is unregistered, so no cartographer.exe stays locked by it.
func (m *Manager) UninstallTray() error {
	switch goos {
	case "darwin":
		p, err := TrayLaunchdPlistPath()
		if err != nil {
			return fmt.Errorf("service: resolve tray plist path: %w", err)
		}
		if _, err := os.Stat(p); err != nil {
			return nil
		}
		if err := m.bootoutAndWait(trayLaunchdLabel, p); err != nil {
			return err
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("service: remove tray plist: %w", err)
		}
	case "windows":
		p, err := TrayWindowsTaskPath()
		if err != nil {
			return fmt.Errorf("service: resolve tray task path: %w", err)
		}
		if _, err := os.Stat(p); err != nil {
			return nil
		}
		// A tray still running would keep cartographer.exe locked, and once
		// tray.xml is gone a retry would be a no-op: on a survivor, keep the
		// definition and report, so the uninstall can be run again.
		if err := m.stopTrayAndWait(); err != nil {
			return err
		}
		if err := m.unregisterWindowsTask(windowsTrayTaskName); err != nil {
			return err
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("service: remove tray task definition: %w", err)
		}
	}
	return nil
}

// closeTrayWindow is requestTrayClose behind a test seam: it asks the tray to
// close its own window, nil when the request was posted.
var closeTrayWindow = requestTrayClose

// trayCloseTimeout bounds how long a tray gets to exit after the close request
// before Stop-ScheduledTask is used, counted in polls like the other waits.
var trayCloseTimeout = 3 * time.Second

// stopTrayAndWait ends a running tray task and returns only once the process
// is gone, as stopServeAndWait does for the server (the poll budget is
// windowsKillTimeout, counted in slept intervals). A task that is not
// registered or not running is already stopped, whatever the stop reported.
//
// The tray is asked to close its window first, so it removes its notification
// icon (Stop-ScheduledTask would leave a ghost until the mouse passes over
// it); the task is stopped only if no window answered or the process stayed.
func (m *Manager) stopTrayAndWait() error {
	if !m.windowsTaskRunning(windowsTrayTaskName) {
		return nil
	}
	if closeTrayWindow() == nil {
		for waited := time.Duration(0); waited <= trayCloseTimeout; waited += windowsDrainPoll {
			if !m.windowsTaskRunning(windowsTrayTaskName) {
				return nil
			}
			windowsSleep(windowsDrainPoll)
		}
	}
	stopErr := m.stopWindowsTask(windowsTrayTaskName)
	for waited := time.Duration(0); ; waited += windowsDrainPoll {
		if !m.windowsTaskRunning(windowsTrayTaskName) {
			return nil
		}
		if waited >= windowsKillTimeout {
			break
		}
		windowsSleep(windowsDrainPoll)
	}
	err := fmt.Errorf("service: the tray task (\\%s\\%s) is still running after Stop-ScheduledTask; end its cartographer.exe process (Stop-Process) and retry", windowsTaskFolder, windowsTrayTaskName)
	if stopErr != nil {
		err = fmt.Errorf("%w (Stop-ScheduledTask: %v)", err, stopErr)
	}
	return err
}
