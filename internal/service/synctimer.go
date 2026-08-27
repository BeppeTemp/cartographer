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
// from the server's (launchdLabel / systemdUnit).
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
	// Path is the plist (darwin) or timer unit (linux) backing it.
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
