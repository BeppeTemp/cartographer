package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A run whose HOME is not the account's home never reaches the per-user
// scheduler (#683): the e2e suite's sandboxed `connect` replaced a user's real
// sync timer this way.
func TestSchedulerRefusedUnderSandboxedHome(t *testing.T) {
	defer func(a, e func() (string, error)) { accountHome, envHome = a, e }(accountHome, envHome)
	accountHome = func() (string, error) { return "/home/user", nil }

	envHome = func() (string, error) { return "/tmp/sandbox", nil }
	for _, cmd := range []string{"launchctl", "systemctl", "powershell.exe", "wsl.exe", "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"} {
		if _, err := execRun(cmd, "print"); !errors.Is(err, errSandboxHome) {
			t.Errorf("%s under a sandboxed HOME: err = %v, want errSandboxHome", cmd, err)
		}
	}
	// The text says what was refused, not what the caller was doing: a read
	// through interop (`doctor` reading .wslconfig) has no unit files.
	err := guardScheduler("/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe")
	if err == nil {
		t.Fatal("powershell.exe under a sandboxed HOME was not refused")
	}
	for _, want := range []string{"HOME=/tmp/sandbox", "account home /home/user", "powershell.exe was not run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	// Nor does it claim more than the guard knows: an install creates an
	// explicit --data directory (possibly outside HOME) before its first
	// scheduler command, so the text says the command was not run and nothing
	// about what else was or was not changed.
	for _, bad := range []string{"unit files", "written", "registered", "per-user scheduler", "nothing", "changed"} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("error %q is install-specific (%q)", err, bad)
		}
	}
	if err := guardScheduler("git"); err != nil {
		t.Errorf("a non-scheduler command is not guarded: %v", err)
	}

	envHome = func() (string, error) { return "/home/user/", nil }
	if err := guardScheduler("launchctl"); err != nil {
		t.Errorf("HOME is the account home: err = %v, want nil", err)
	}
}

// stubSandboxedExecutable points osExecutable at a real file, so an install
// gets as far as the scheduler.
func stubSandboxedExecutable(t *testing.T) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cartographer")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := osExecutable
	osExecutable = func() (string, error) { return bin, nil }
	t.Cleanup(func() { osExecutable = orig })
}

// The guard's own text is neutral (it also refuses reads), so the install is
// the one to say that it wrote the unit files and registered nothing -- and the
// wrap keeps errors.Is(err, ErrSandboxHome), which connect relies on.
func TestInstallUnderSandboxedHomeSaysTheFilesAreWritten(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform+"/service", func(t *testing.T) {
			home := withTestHome(t, platform)
			stubSandboxedExecutable(t)
			_, err := NewManager().Install(InstallOptions{DataDir: filepath.Join(home, "data"), HTTPAddr: "127.0.0.1:39273"})
			if !errors.Is(err, ErrSandboxHome) {
				t.Fatalf("Install under a sandboxed HOME: err = %v, want ErrSandboxHome", err)
			}
			if !strings.Contains(err.Error(), unitsWrittenNote) {
				t.Errorf("error %q does not say the unit files are written", err)
			}
			if def, derr := DefinitionPath(); derr != nil || !fileExists(def) {
				t.Errorf("the unit file the error talks about is missing (%q, %v)", def, derr)
			}
		})
		t.Run(platform+"/sync-timer", func(t *testing.T) {
			withTestHome(t, platform)
			stubSandboxedExecutable(t)
			err := NewManager().InstallSyncTimer(0)
			if !errors.Is(err, ErrSandboxHome) {
				t.Fatalf("InstallSyncTimer under a sandboxed HOME: err = %v, want ErrSandboxHome", err)
			}
			if !strings.Contains(err.Error(), unitsWrittenNote) {
				t.Errorf("error %q does not say the unit files are written", err)
			}
			path, perr := SyncLaunchdPlistPath()
			if platform == "linux" {
				path, perr = SyncSystemdTimerPath()
			}
			if perr != nil || !fileExists(path) {
				t.Errorf("the sync unit the error talks about is missing (%q, %v)", path, perr)
			}
		})
	}
}

// A refusal that is not an install's -- a probe, a read through interop -- says
// nothing about unit files, and sandboxRefusal leaves other errors alone.
func TestSandboxRefusalOnlyAddsToTheGuardsError(t *testing.T) {
	withTestHome(t, "linux")
	_, err := execRun("powershell.exe", "-NoProfile")
	if !errors.Is(err, errSandboxHome) || strings.Contains(err.Error(), "unit files") {
		t.Errorf("a bare refusal = %v, want errSandboxHome without a word on unit files", err)
	}
	other := errors.New("boom")
	if got := sandboxRefusal(other, unitsWrittenNote); got != other {
		t.Errorf("a non-guard error was rewrapped: %v", got)
	}
	if sandboxRefusal(nil, unitsWrittenNote) != nil {
		t.Error("nil became an error")
	}
}

// The Windows wording of the refusal, through the helper itself and with a
// fake guard error, so it is pinned on every host: a Windows install registers
// its task from a side file, so what it can promise is that no task was
// registered and the installed definition is as it was.
func TestSandboxRefusalWindowsWording(t *testing.T) {
	guard := fmt.Errorf("%w (HOME=/tmp/sandbox, account home /home/user): powershell.exe was not run", errSandboxHome)
	got := sandboxRefusal(guard, taskKeptNote)
	if !errors.Is(got, ErrSandboxHome) {
		t.Fatalf("the wrapped refusal lost errors.Is(ErrSandboxHome): %v", got)
	}
	for _, want := range []string{"powershell.exe was not run", "no task was registered with the scheduler", "the installed task definition is unchanged"} {
		if !strings.Contains(got.Error(), want) {
			t.Errorf("error %q lacks %q", got, want)
		}
	}
	if strings.Contains(got.Error(), "unit files") {
		t.Errorf("a Windows refusal talks about unit files: %q", got)
	}
}

// sandboxedWindows makes goos "windows" on any host, the account's home not
// the HOME of the run, and no host command reachable: the real Manager's
// execRun refuses powershell.exe before anything is spawned.
func sandboxedWindows(t *testing.T) string {
	t.Helper()
	home := withTestHome(t, "windows")
	stubSandboxedExecutable(t)
	origEnv, origUser, origSID, origElev := envHome, windowsTaskUser, windowsTaskSID, windowsElevated
	envHome = func() (string, error) { return home, nil }
	windowsTaskUser = func() string { return `HOST\user` }
	windowsTaskSID = func() string { return "" }
	windowsElevated = func() bool { return false }
	t.Cleanup(func() {
		envHome, windowsTaskUser, windowsTaskSID, windowsElevated = origEnv, origUser, origSID, origElev
	})
	return home
}

// writeOldDefinition plants an installed task definition and returns a check
// that it is byte-for-byte what it was.
func writeOldDefinition(t *testing.T, path string) func() {
	t.Helper()
	const old = "<old definition/>"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if got, err := os.ReadFile(path); err != nil || string(got) != old {
			t.Errorf("the installed definition %s changed: %q, %v", path, got, err)
		}
		if fileExists(path + ".new") {
			t.Errorf("the side file %s.new was left behind", path)
		}
	}
}

// The three Windows installs wrap the refusal with taskKeptNote, and the claim
// is true: nothing was registered and the installed definition is unchanged.
func TestWindowsInstallsUnderSandboxedHomeSayNoTaskWasRegistered(t *testing.T) {
	check := func(t *testing.T, err error) {
		t.Helper()
		if !errors.Is(err, ErrSandboxHome) {
			t.Fatalf("err = %v, want ErrSandboxHome", err)
		}
		if !strings.Contains(err.Error(), taskKeptNote) {
			t.Errorf("error %q does not say %q", err, taskKeptNote)
		}
		if strings.Contains(err.Error(), "unit files") {
			t.Errorf("a Windows refusal talks about unit files: %q", err)
		}
	}
	t.Run("service", func(t *testing.T) {
		home := sandboxedWindows(t)
		path, err := WindowsTaskPath()
		if err != nil {
			t.Fatal(err)
		}
		unchanged := writeOldDefinition(t, path)
		_, err = NewManager().Install(InstallOptions{DataDir: filepath.Join(home, "data"), HTTPAddr: "127.0.0.1:39273"})
		check(t, err)
		unchanged()
	})
	t.Run("sync-timer", func(t *testing.T) {
		sandboxedWindows(t)
		path, err := SyncWindowsTaskPath()
		if err != nil {
			t.Fatal(err)
		}
		unchanged := writeOldDefinition(t, path)
		check(t, NewManager().InstallSyncTimer(0))
		unchanged()
	})
	t.Run("tray", func(t *testing.T) {
		sandboxedWindows(t)
		path, err := TrayWindowsTaskPath()
		if err != nil {
			t.Fatal(err)
		}
		unchanged := writeOldDefinition(t, path)
		check(t, NewManager().InstallTray())
		unchanged()
	})
}
