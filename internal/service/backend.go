package service

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

// Backend names the mechanism that keeps the server running. The values are
// the words the setup plan prints.
type Backend string

const (
	BackendLaunchd Backend = "launchd"
	BackendSystemd Backend = "systemd"
	BackendCron    Backend = "cron"
	BackendTask    Backend = "task"
	BackendNone    Backend = "none"
)

// ErrNoServiceManager is Install's answer on a Linux host where neither a
// systemd user manager nor a usable crontab exists. It is returned before
// anything is written.
var ErrNoServiceManager = errors.New("service: no service manager on this machine: no reachable systemd user manager and no usable `crontab`; start `cartographer serve --config <config>` from your init system")

// lookPath and systemdRunDir are test seams for the backend probe.
var (
	lookPath      = exec.LookPath
	systemdRunDir = "/run/systemd/system"
)

// linuxInstalledBackend is the backend whose definition is on disk: the unit
// file wins over the cron definition (an install of one removes the other), ""
// when neither exists.
func linuxInstalledBackend() Backend {
	if p, err := SystemdUnitPath(); err == nil && fileExists(p) {
		return BackendSystemd
	}
	if p, err := CronServerDefinitionPath(); err == nil && fileExists(p) {
		return BackendCron
	}
	return ""
}

// Backend returns the backend the verbs act on: the platform's own on macOS and
// Windows; on Linux the installed one, else what DetectBackend finds.
func (m *Manager) Backend() Backend {
	switch goos {
	case "darwin":
		return BackendLaunchd
	case "windows":
		return BackendTask
	case "linux":
		if b := linuxInstalledBackend(); b != "" {
			return b
		}
		return m.DetectBackend()
	}
	return BackendNone
}

// DetectBackend probes a Linux host, in order:
//
//	a. $HOME is not the account's home: systemd, i.e. write the unit files and
//	   register nothing (the scheduler guard of homeguard.go, #683; e2e and the
//	   tests rely on it, so a sandboxed run must never reach crontab either).
//	b. systemctl is on PATH, /run/systemd/system is a directory (what
//	   sd_booted() checks, so a leftover binary on a non-systemd host does not
//	   count) and `systemctl --user show-environment` succeeds (the user manager
//	   is reachable over its bus: it fails in containers, in WSL without
//	   systemd and after a bare su, exactly where a unit would be written and
//	   never run): systemd.
//	c. crontab is on PATH and `crontab -l` succeeds or says there is no crontab
//	   (proves the account may use cron without writing anything): cron.
//	d. none.
//
// It never errors: a failing probe is "not this backend".
func (m *Manager) DetectBackend() Backend {
	if home, err := userHomeDir(); err == nil && home != "" {
		if acct, err := accountHome(); err == nil && acct != "" && !samePath(acct, home) {
			return BackendSystemd
		}
	} else {
		return BackendSystemd // unreadable $HOME: today's error surface
	}
	if _, err := lookPath("systemctl"); err == nil {
		if fi, err := os.Stat(systemdRunDir); err == nil && fi.IsDir() {
			if _, err := m.run("systemctl", "--user", "show-environment"); err == nil {
				return BackendSystemd
			}
		}
	}
	if _, err := lookPath("crontab"); err == nil {
		if _, err := m.readCrontab(); err == nil {
			return BackendCron
		}
	}
	return BackendNone
}

// isNoCrontab reports whether a `crontab -l` failure only says the user has no
// crontab yet, which is an empty crontab and not an error.
func isNoCrontab(out string, err error) bool {
	text := out
	if err != nil {
		text += " " + err.Error()
	}
	return strings.Contains(strings.ToLower(text), "no crontab")
}

// PrepareConfig is the half of Install that needs no service manager: it
// resolves the config path, writes a default config when none exists and
// creates the data directory. `service install --config-only` calls it on a
// host where Install itself would stop with ErrNoServiceManager.
func (m *Manager) PrepareConfig(opts InstallOptions) (warnings []string, configPath, dataDir string, err error) {
	return m.prepareConfig(opts)
}

// FailureHint is what setup prints after a failed install: only commands that
// work on the chosen backend (`journalctl` or `systemctl` would name a tool the
// host may not have), then where the log is.
func (m *Manager) FailureHint() string {
	const status = "`cartographer service status` shows the details"
	switch m.Backend() {
	case BackendSystemd:
		return status + "; the log is in `journalctl --user -u " + strings.TrimSuffix(systemdUnit, ".service") + "`"
	case BackendCron:
		server, err1 := statePath("server.log")
		supervisor, err2 := statePath("supervisor.log")
		if err1 == nil && err2 == nil {
			return status + "; the logs are " + server + " (the server) and " + supervisor + " (the cron entry)"
		}
	case BackendLaunchd:
		if p, err := LaunchdLogPath(); err == nil {
			return status + "; the log is " + p
		}
	case BackendTask:
		if p, err := WindowsLogPath(); err == nil {
			return status + "; the log is " + p
		}
	}
	return status
}
