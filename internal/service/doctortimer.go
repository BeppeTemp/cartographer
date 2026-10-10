package service

// The scheduled doctor session (D369, D690) is gone (D703): Cartographer never
// starts an agent by itself, a kb-doctor session is always one the operator
// launches. What remains removes the job that 0.22 and 0.23 installed, so an
// upgraded machine does not keep firing a `doctor run` that no longer exists.
// Label, unit names and task are those releases' own: change them and the
// leftover is never found.

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	doctorLaunchdLabel   = "com.cartographer.doctor"
	doctorSystemdService = "cartographer-doctor.service"
	doctorSystemdTimer   = "cartographer-doctor.timer"
	windowsDoctorTask    = "Doctor"
)

func DoctorLaunchdPlistPath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", doctorLaunchdLabel+".plist"), nil
}

func DoctorSystemdServicePath() (string, error) { return systemdUserPath(doctorSystemdService) }
func DoctorSystemdTimerPath() (string, error)   { return systemdUserPath(doctorSystemdTimer) }
func DoctorWindowsTaskPath() (string, error)    { return windowsTaskFile("doctor.xml") }

// legacyDoctorTimerInstalled reports whether any definition file of the old
// job is on disk: the file, not the scheduler, is what an install left behind.
func legacyDoctorTimerInstalled() bool {
	var paths []string
	switch goos {
	case "darwin":
		p, _ := DoctorLaunchdPlistPath()
		paths = append(paths, p)
	case "linux":
		s, _ := DoctorSystemdServicePath()
		t, _ := DoctorSystemdTimerPath()
		paths = append(paths, s, t)
	case "windows":
		p, _ := DoctorWindowsTaskPath()
		paths = append(paths, p)
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, err := os.Lstat(p); err == nil {
			return true
		}
	}
	return false
}

// RemoveLegacyDoctorTimer removes the scheduled doctor job of 0.22/0.23 when
// one is installed; removed says whether there was one.
func (m *Manager) RemoveLegacyDoctorTimer() (removed bool, err error) {
	if !legacyDoctorTimerInstalled() {
		return false, nil
	}
	return true, m.UninstallDoctorTimer()
}

// UninstallDoctorTimer unregisters and removes the definition(s). Removing a
// timer that is not installed is a success.
func (m *Manager) UninstallDoctorTimer() error {
	switch goos {
	case "darwin":
		plistPath, err := DoctorLaunchdPlistPath()
		if err != nil {
			return fmt.Errorf("service: resolve doctor plist path: %w", err)
		}
		m.run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), doctorLaunchdLabel))
		if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("service: remove doctor plist: %w", err)
		}
		return nil
	case "linux":
		servicePath, err := DoctorSystemdServicePath()
		if err != nil {
			return fmt.Errorf("service: resolve doctor unit path: %w", err)
		}
		timerPath, err := DoctorSystemdTimerPath()
		if err != nil {
			return fmt.Errorf("service: resolve doctor timer path: %w", err)
		}
		m.run("systemctl", "--user", "disable", "--now", doctorSystemdTimer)
		for _, p := range []string{timerPath, servicePath} {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("service: remove %s: %w", filepath.Base(p), err)
			}
		}
		_, err = m.run("systemctl", "--user", "daemon-reload")
		return err
	case "windows":
		taskPath, err := DoctorWindowsTaskPath()
		if err != nil {
			return fmt.Errorf("service: resolve doctor task path: %w", err)
		}
		m.unregisterWindowsTask(windowsDoctorTask)
		if err := os.Remove(taskPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("service: remove doctor task definition: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("service: unsupported platform %q", goos)
	}
}
