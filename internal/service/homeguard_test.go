package service

import (
	"errors"
	"testing"
)

// A run whose HOME is not the account's home never reaches the per-user
// scheduler (#683): the e2e suite's sandboxed `connect` replaced a user's real
// sync timer this way.
func TestSchedulerRefusedUnderSandboxedHome(t *testing.T) {
	defer func(a, e func() (string, error)) { accountHome, envHome = a, e }(accountHome, envHome)
	accountHome = func() (string, error) { return "/home/user", nil }

	envHome = func() (string, error) { return "/tmp/sandbox", nil }
	for _, cmd := range []string{"launchctl", "systemctl", "powershell.exe"} {
		if _, err := execRun(cmd, "print"); !errors.Is(err, errSandboxHome) {
			t.Errorf("%s under a sandboxed HOME: err = %v, want errSandboxHome", cmd, err)
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
