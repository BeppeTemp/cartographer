package service

// The scheduled sync trigger (D140): a first-class trigger for clients that
// cannot be hooked at session start — Kiro's IDE surface, and any provider
// whose configuration Cartographer does not own. It reuses the same
// launchd/systemd machinery as the server unit, with its own label and files
// so the two never collide.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Labels and unit names of the client-side sync timer, deliberately distinct
// from the server's (launchdLabel / systemdUnit / windowsServeTaskName).
const (
	syncLaunchdLabel   = "com.cartographer.sync"
	syncSystemdService = "cartographer-sync.service"
	syncSystemdTimer   = "cartographer-sync.timer"

	// DefaultSyncInterval is long enough to stay invisible, short enough that
	// a skill published in the morning is present by mid-morning.
	DefaultSyncInterval = 30 * time.Minute
)

// SyncLaunchdPlistPath returns ~/Library/LaunchAgents/com.cartographer.sync.plist.
func SyncLaunchdPlistPath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", syncLaunchdLabel+".plist"), nil
}

// SyncLaunchdLogPath returns ~/Library/Logs/cartographer/sync.log — the same
// split the server unit uses (launchd logs to a file, systemd to the journal).
func SyncLaunchdLogPath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Logs", "cartographer", "sync.log"), nil
}

// SyncSystemdServicePath returns ~/.config/systemd/user/cartographer-sync.service.
func SyncSystemdServicePath() (string, error) {
	return systemdUserPath(syncSystemdService)
}

// SyncSystemdTimerPath returns ~/.config/systemd/user/cartographer-sync.timer.
func SyncSystemdTimerPath() (string, error) {
	return systemdUserPath(syncSystemdTimer)
}

func systemdUserPath(name string) (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", name), nil
}

// SyncWindowsTaskPath returns %LOCALAPPDATA%\cartographer\tasks\sync.xml — the
// sync task's definition, in the same directory as the server task's and
// deliberately a different file (see TestSyncTimerFilesAreDistinctFromServerService).
func SyncWindowsTaskPath() (string, error) {
	return windowsTaskFile("sync.xml")
}

// SyncWindowsLogPath returns %LOCALAPPDATA%\cartographer\Logs\sync.log, the
// same split the server task uses.
func SyncWindowsLogPath() (string, error) {
	return windowsLogFile("sync.log")
}

// RenderWindowsSyncTaskXML renders the Scheduled Task that runs
// `<binPath> sync --log-file <logPath>` every interval.
//
// The command carries no --auto-trust, for the same reason the plist and the
// timer unit do not: an unattended background job must not grant a trust the
// user never gave. The persisted `trust` setting in .cartographer.yaml still
// applies, which is the correct authorization boundary (D54).
//
// StartWhenAvailable is the analogue of systemd's Persistent=true: a run missed
// because the machine was off happens as soon as it is on again. The
// StartBoundary is a fixed date in the past rather than the time of
// registration, so the renderer stays pure and the definition is byte-stable
// across two installs — with StartWhenAvailable that means "due now", which is
// the intent anyway.
//
// The log file is what makes a failing scheduled sync diagnosable here. On
// linux the same output goes to the journal; on Windows a task's own history
// records exit codes only, and is disabled by default on many machines.
func RenderWindowsSyncTaskXML(binPath, logPath string, interval time.Duration) string {
	if interval <= 0 {
		interval = DefaultSyncInterval
	}
	args := fmt.Sprintf("sync --log-file %s", quotePath(logPath))
	return fmt.Sprintf(windowsTaskXMLDeclaration+`
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Cartographer client sync</Description>
    <URI>\%s\%s</URI>
  </RegistrationInfo>
  <Triggers>
    <TimeTrigger>
      <Repetition>
        <Interval>%s</Interval>
        <StopAtDurationEnd>false</StopAtDurationEnd>
      </Repetition>
      <StartBoundary>2000-01-01T00:00:00</StartBoundary>
      <Enabled>true</Enabled>
    </TimeTrigger>
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
    <ExecutionTimeLimit>PT1H</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
    </Exec>
  </Actions>
</Task>
`, xmlEscape(windowsTaskFolder), xmlEscape(windowsSyncTaskName), formatISO8601Duration(interval), xmlEscape(binPath), xmlEscape(args))
}

// formatISO8601Duration renders a duration the way Task Scheduler expresses
// one: PT30M, PT1H, PT1M30S. Sub-second precision is dropped — the shortest
// interval that means anything to a scheduler is a minute, and the value is
// read back by intervalFromTaskXML, which must round-trip it.
func formatISO8601Duration(d time.Duration) string {
	if d <= 0 {
		d = DefaultSyncInterval
	}
	total := int(d.Round(time.Second).Seconds())
	h, m, s := total/3600, (total%3600)/60, total%60
	var b strings.Builder
	b.WriteString("PT")
	if h > 0 {
		fmt.Fprintf(&b, "%dH", h)
	}
	if m > 0 {
		fmt.Fprintf(&b, "%dM", m)
	}
	if s > 0 || (h == 0 && m == 0) {
		fmt.Fprintf(&b, "%dS", s)
	}
	return b.String()
}

// parseISO8601Duration parses the subset formatISO8601Duration produces, plus
// the day component Task Scheduler may write (P1D). Zero on anything it does
// not understand — a definition edited by hand into something unreadable makes
// status report no interval, never a wrong one.
func parseISO8601Duration(s string) time.Duration {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "P") {
		return 0
	}
	rest := s[1:]
	var total time.Duration
	inTime := false
	num := ""
	for _, r := range rest {
		switch {
		case r == 'T':
			inTime = true
			num = ""
		case r >= '0' && r <= '9':
			num += string(r)
		default:
			if num == "" {
				return 0
			}
			n, err := strconv.Atoi(num)
			if err != nil {
				return 0
			}
			num = ""
			switch {
			case r == 'D' && !inTime:
				total += time.Duration(n) * 24 * time.Hour
			case r == 'H' && inTime:
				total += time.Duration(n) * time.Hour
			case r == 'M' && inTime:
				total += time.Duration(n) * time.Minute
			case r == 'S' && inTime:
				total += time.Duration(n) * time.Second
			default:
				// A month or year component (P1M outside T, P1Y) has no fixed
				// length in seconds and no business in a sync interval.
				return 0
			}
		}
	}
	if num != "" {
		return 0
	}
	return total
}

// RenderSyncLaunchdPlist renders the launchd agent that runs
// `<binPath> sync` every interval, logging to logPath.
//
// The command carries no --auto-trust: an unattended background job must not
// grant a trust the user never gave. The persisted `trust` setting in
// .cartographer.yaml still applies, which is the correct authorization
// boundary (D54).
func RenderSyncLaunchdPlist(binPath, logPath string, interval time.Duration) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>sync</string>
	</array>
	<key>StartInterval</key>
	<integer>%d</integer>
	<key>RunAtLoad</key>
	<false/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, syncLaunchdLabel, binPath, int(interval.Seconds()), logPath, logPath)
}

// RenderSyncSystemdService renders the oneshot unit the timer activates.
func RenderSyncSystemdService(binPath string) string {
	return fmt.Sprintf(`[Unit]
Description=Cartographer client sync

[Service]
Type=oneshot
ExecStart=%s sync
`, binPath)
}

// RenderSyncSystemdTimer renders the timer that activates the unit above.
// Persistent catches up a run missed while the machine was off.
func RenderSyncSystemdTimer(interval time.Duration) string {
	return fmt.Sprintf(`[Unit]
Description=Cartographer client sync timer

[Timer]
OnBootSec=%ds
OnUnitActiveSec=%ds
Persistent=true

[Install]
WantedBy=timers.target
`, int(interval.Seconds()), int(interval.Seconds()))
}

// SyncTimerStatus reports the installed state of the scheduled sync trigger.
type SyncTimerStatus struct {
	Installed bool
	Active    bool
	// Path is the plist (darwin), timer unit (linux) or task definition
	// (windows) backing it.
	Path string
	// Interval is the configured period, when it could be read back.
	Interval time.Duration
}

// InstallSyncTimer writes the platform unit(s) and registers them. Idempotent:
// re-running it overwrites the definition and re-registers.
func (m *Manager) InstallSyncTimer(interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultSyncInterval
	}
	binPath, err := osExecutable()
	if err != nil {
		return fmt.Errorf("service: resolve binary path: %w", err)
	}
	binPath = resolveStableBinPath(binPath)

	switch goos {
	case "darwin":
		logPath, err := SyncLaunchdLogPath()
		if err != nil {
			return fmt.Errorf("service: resolve sync log path: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			return fmt.Errorf("service: create log dir: %w", err)
		}
		plistPath, err := SyncLaunchdPlistPath()
		if err != nil {
			return fmt.Errorf("service: resolve sync plist path: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
			return fmt.Errorf("service: create LaunchAgents dir: %w", err)
		}
		if err := os.WriteFile(plistPath, []byte(RenderSyncLaunchdPlist(binPath, logPath, interval)), 0o644); err != nil {
			return fmt.Errorf("service: write sync plist: %w", err)
		}
		uid := os.Getuid()
		// Best-effort: bootout fails when the job was not loaded yet.
		m.run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", uid, syncLaunchdLabel))
		if _, err := m.run("launchctl", "bootstrap", fmt.Sprintf("gui/%d", uid), plistPath); err != nil {
			return fmt.Errorf("service: launchctl bootstrap sync timer: %w", err)
		}
		return nil
	case "linux":
		servicePath, err := SyncSystemdServicePath()
		if err != nil {
			return fmt.Errorf("service: resolve sync unit path: %w", err)
		}
		timerPath, err := SyncSystemdTimerPath()
		if err != nil {
			return fmt.Errorf("service: resolve sync timer path: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(timerPath), 0o755); err != nil {
			return fmt.Errorf("service: create systemd user dir: %w", err)
		}
		if err := os.WriteFile(servicePath, []byte(RenderSyncSystemdService(binPath)), 0o644); err != nil {
			return fmt.Errorf("service: write sync unit: %w", err)
		}
		if err := os.WriteFile(timerPath, []byte(RenderSyncSystemdTimer(interval)), 0o644); err != nil {
			// Leave no half-installed state behind.
			os.Remove(servicePath)
			return fmt.Errorf("service: write sync timer: %w", err)
		}
		if _, err := m.run("systemctl", "--user", "daemon-reload"); err != nil {
			return fmt.Errorf("service: systemctl daemon-reload: %w", err)
		}
		if _, err := m.run("systemctl", "--user", "enable", "--now", syncSystemdTimer); err != nil {
			return fmt.Errorf("service: systemctl enable --now %s: %w", syncSystemdTimer, err)
		}
		return nil
	case "windows":
		logPath, err := SyncWindowsLogPath()
		if err != nil {
			return fmt.Errorf("service: resolve sync log path: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			return fmt.Errorf("service: create log dir: %w", err)
		}
		taskPath, err := SyncWindowsTaskPath()
		if err != nil {
			return fmt.Errorf("service: resolve sync task path: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
			return fmt.Errorf("service: create tasks dir: %w", err)
		}
		if err := os.WriteFile(taskPath, []byte(RenderWindowsSyncTaskXML(binPath, logPath, interval)), 0o644); err != nil {
			return fmt.Errorf("service: write sync task definition: %w", err)
		}
		return m.registerWindowsTask(windowsSyncTaskName, taskPath)
	default:
		return fmt.Errorf("service: unsupported platform %q", goos)
	}
}

// UninstallSyncTimer unregisters and removes the unit(s). Uninstalling a timer
// that is not installed is a success, not an error.
func (m *Manager) UninstallSyncTimer() error {
	switch goos {
	case "darwin":
		plistPath, err := SyncLaunchdPlistPath()
		if err != nil {
			return fmt.Errorf("service: resolve sync plist path: %w", err)
		}
		m.run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), syncLaunchdLabel))
		if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("service: remove sync plist: %w", err)
		}
		return nil
	case "linux":
		servicePath, err := SyncSystemdServicePath()
		if err != nil {
			return fmt.Errorf("service: resolve sync unit path: %w", err)
		}
		timerPath, err := SyncSystemdTimerPath()
		if err != nil {
			return fmt.Errorf("service: resolve sync timer path: %w", err)
		}
		m.run("systemctl", "--user", "disable", "--now", syncSystemdTimer)
		for _, p := range []string{timerPath, servicePath} {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("service: remove %s: %w", filepath.Base(p), err)
			}
		}
		_, err = m.run("systemctl", "--user", "daemon-reload")
		return err
	case "windows":
		taskPath, err := SyncWindowsTaskPath()
		if err != nil {
			return fmt.Errorf("service: resolve sync task path: %w", err)
		}
		m.unregisterWindowsTask(windowsSyncTaskName)
		if err := os.Remove(taskPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("service: remove sync task definition: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("service: unsupported platform %q", goos)
	}
}

// SyncTimerStatus reports whether the scheduled trigger is installed and
// active. A missing launchctl/systemctl leaves Active false rather than
// failing: the file on disk is the authority on "installed".
func (m *Manager) SyncTimerStatus() (SyncTimerStatus, error) {
	var st SyncTimerStatus
	switch goos {
	case "darwin":
		plistPath, err := SyncLaunchdPlistPath()
		if err != nil {
			return st, fmt.Errorf("service: resolve sync plist path: %w", err)
		}
		st.Path = plistPath
		if data, err := os.ReadFile(plistPath); err == nil {
			st.Installed = true
			st.Interval = intervalFromPlist(string(data))
		}
		if _, err := m.run("launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), syncLaunchdLabel)); err == nil {
			st.Active = true
		}
		return st, nil
	case "linux":
		timerPath, err := SyncSystemdTimerPath()
		if err != nil {
			return st, fmt.Errorf("service: resolve sync timer path: %w", err)
		}
		st.Path = timerPath
		if data, err := os.ReadFile(timerPath); err == nil {
			st.Installed = true
			st.Interval = intervalFromTimerUnit(string(data))
		}
		if out, err := m.run("systemctl", "--user", "is-active", syncSystemdTimer); err == nil && len(out) > 0 {
			st.Active = true
		}
		return st, nil
	case "windows":
		taskPath, err := SyncWindowsTaskPath()
		if err != nil {
			return st, fmt.Errorf("service: resolve sync task path: %w", err)
		}
		st.Path = taskPath
		if data, err := os.ReadFile(taskPath); err == nil {
			st.Installed = true
			st.Interval = intervalFromTaskXML(string(data))
		}
		st.Active = m.windowsTaskRegistered(windowsSyncTaskName)
		return st, nil
	default:
		return st, fmt.Errorf("service: unsupported platform %q", goos)
	}
}

// intervalFromPlist recovers StartInterval from a rendered plist, so status
// can report the period without a second source of truth. Zero when absent.
func intervalFromPlist(plist string) time.Duration {
	return durationFromMarker(plist, "<key>StartInterval</key>", "<integer>", "</integer>")
}

// intervalFromTimerUnit recovers OnUnitActiveSec from a rendered timer unit.
func intervalFromTimerUnit(unit string) time.Duration {
	return durationFromMarker(unit, "OnUnitActiveSec=", "", "s")
}

func durationFromMarker(content, key, open, close string) time.Duration {
	idx := strings.Index(content, key)
	if idx == -1 {
		return 0
	}
	rest := content[idx+len(key):]
	if open != "" {
		start := strings.Index(rest, open)
		if start == -1 {
			return 0
		}
		rest = rest[start+len(open):]
	}
	end := strings.Index(rest, close)
	if end == -1 {
		return 0
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(rest[:end]))
	if err != nil {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
