package service

// The tray's autostart (D1482): a third per-user definition next to the
// server's and the sync timer's, started at logon so the status icon is there
// without the user typing anything. Linux has no tray, so nothing is written.

import (
	"fmt"
	"os"
	"path/filepath"
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
func RenderWindowsTrayTaskXML(binPath, logPath, userID string) string {
	args := fmt.Sprintf("tray --log-file %s", quotePath(logPath))
	userElem := ""
	if userID != "" {
		userElem = "\n      <UserId>" + xmlEscape(userID) + "</UserId>"
	}
	return fmt.Sprintf(windowsTaskXMLDeclaration+`
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Cartographer status icon</Description>
    <URI>\%s\%s</URI>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>%s
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
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
`, xmlEscape(windowsTaskFolder), xmlEscape(windowsTrayTaskName), userElem, xmlEscape(binPath), xmlEscape(args))
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
	for _, d := range []string{filepath.Dir(logPath), filepath.Dir(defPath)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("service: create %s: %w", d, err)
		}
	}
	if goos == "darwin" {
		if err := os.WriteFile(defPath, []byte(RenderTrayLaunchdPlist(binPath, logPath)), 0o644); err != nil {
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
	if err := os.WriteFile(defPath, []byte(RenderWindowsTrayTaskXML(binPath, logPath, windowsTaskUser())), 0o644); err != nil {
		return fmt.Errorf("service: write tray task definition: %w", err)
	}
	_ = m.stopWindowsTask(windowsTrayTaskName) // best effort: not registered yet on a first install
	if err := m.registerWindowsTask(windowsTrayTaskName, defPath); err != nil {
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
		_ = m.stopWindowsTask(windowsTrayTaskName)
		m.unregisterWindowsTask(windowsTrayTaskName)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("service: remove tray task definition: %w", err)
		}
	}
	return nil
}
