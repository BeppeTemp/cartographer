package service

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackend_PerPlatformAndInstalledUnit(t *testing.T) {
	home := withTestHome(t, "darwin")
	m := &Manager{run: func(string, ...string) (string, error) { return "", errors.New("no tools") }}
	if got := m.Backend(); got != BackendLaunchd {
		t.Errorf("darwin: %s", got)
	}
	goos = "windows"
	if got := m.Backend(); got != BackendTask {
		t.Errorf("windows: %s", got)
	}
	// On Linux an installed unit wins over the probe (nothing else could run it).
	goos = "linux"
	accountHome = func() (string, error) { return home, nil }
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if got := m.Backend(); got != BackendNone {
		t.Errorf("nothing installed, nothing to probe: %s", got)
	}
	unit, _ := SystemdUnitPath()
	os.MkdirAll(filepath.Dir(unit), 0o755)
	os.WriteFile(unit, []byte("x"), 0o644)
	if got := m.Backend(); got != BackendSystemd {
		t.Errorf("unit installed: %s", got)
	}
}

// Install must resolve the backend before its first write: no config and no data
// directory are left behind by a host that cannot run the server.
func TestInstall_NoServiceManagerWritesNothing(t *testing.T) {
	home := withTestHome(t, "linux")
	accountHome = func() (string, error) { return home, nil }
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	m := &Manager{run: func(string, ...string) (string, error) { return "", nil }}
	_, err := m.Install(InstallOptions{DataDir: filepath.Join(home, "data"), HTTPAddr: ":39999"})
	if !errors.Is(err, ErrNoServiceManager) {
		t.Fatalf("err = %v, want ErrNoServiceManager", err)
	}
	if ents, _ := os.ReadDir(home); len(ents) != 0 {
		t.Errorf("HOME is not empty: %v", ents)
	}
	if err := m.Uninstall(); err != nil {
		t.Errorf("uninstall with nothing installed and no service manager: %v", err)
	}
}

func TestRemoveLegacyDoctorTimer_NoSystemctl(t *testing.T) {
	withTestHome(t, "linux")
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	var calls []string
	m := &Manager{run: func(name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return "", errors.New("exec: \"systemctl\": executable file not found in $PATH")
	}}
	svc, _ := DoctorSystemdServicePath()
	os.MkdirAll(filepath.Dir(svc), 0o755)
	os.WriteFile(svc, []byte("x"), 0o644)
	removed, err := m.RemoveLegacyDoctorTimer()
	if !removed || err != nil {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
	if _, err := os.Stat(svc); !os.IsNotExist(err) {
		t.Error("the unit file is still there")
	}
	for _, c := range calls {
		if strings.Contains(c, "daemon-reload") {
			t.Errorf("daemon-reload ran without systemctl: %s", c)
		}
	}
}

func TestSchedulerGuardCoversCrontab(t *testing.T) {
	defer func(a, e func() (string, error)) { accountHome, envHome = a, e }(accountHome, envHome)
	accountHome = func() (string, error) { return "/home/user", nil }
	envHome = func() (string, error) { return "/tmp/sandbox", nil }
	if _, err := execRun("crontab", "-l"); !errors.Is(err, errSandboxHome) {
		t.Errorf("crontab under a sandboxed HOME: %v", err)
	}
}

// An installed unit selects systemd for uninstall even when the probe fails
// (no reachable user manager): the disable and the removal still happen.
func TestUninstall_InstalledUnitIsRemovedEvenIfProbeFails(t *testing.T) {
	home := withTestHome(t, "linux")
	accountHome = func() (string, error) { return home, nil }
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	unit, _ := SystemdUnitPath()
	os.MkdirAll(filepath.Dir(unit), 0o755)
	os.WriteFile(unit, []byte("x"), 0o644)
	var calls []string
	m := &Manager{run: func(name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return "", nil
	}}
	if err := m.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unit); !os.IsNotExist(err) {
		t.Error("unit file kept")
	}
	if !strings.Contains(strings.Join(calls, "\n"), "systemctl --user disable --now "+systemdUnit) {
		t.Errorf("unit not disabled: %v", calls)
	}
}
