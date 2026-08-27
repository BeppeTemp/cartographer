package main

// Tests for the `service sync-timer` dispatch and the hook-less provider hint
// (D140).

import (
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/service"
)

func TestCmdServiceSyncTimer_Dispatch(t *testing.T) {
	oldInstall, oldUninstall, oldStatus := syncTimerInstallFn, syncTimerUninstallFn, syncTimerStatusFn
	t.Cleanup(func() {
		syncTimerInstallFn, syncTimerUninstallFn, syncTimerStatusFn = oldInstall, oldUninstall, oldStatus
	})

	t.Run("install passes the interval", func(t *testing.T) {
		var got time.Duration
		syncTimerInstallFn = func(interval time.Duration) error { got = interval; return nil }
		out := withStdout(t, func() {
			if code := cmdService([]string{"sync-timer", "install", "--interval", "5m"}); code != 0 {
				t.Errorf("exit = %d, want 0", code)
			}
		})
		if got != 5*time.Minute {
			t.Errorf("interval = %v, want 5m", got)
		}
		if !strings.Contains(out, "without --auto-trust") {
			t.Errorf("output must state the authorization boundary: %q", out)
		}
	})

	t.Run("install defaults to 30m", func(t *testing.T) {
		var got time.Duration
		syncTimerInstallFn = func(interval time.Duration) error { got = interval; return nil }
		withStdout(t, func() { cmdService([]string{"sync-timer", "install"}) })
		if got != service.DefaultSyncInterval {
			t.Errorf("interval = %v, want %v", got, service.DefaultSyncInterval)
		}
	})

	t.Run("status exit codes", func(t *testing.T) {
		syncTimerStatusFn = func() (service.SyncTimerStatus, error) { return service.SyncTimerStatus{}, nil }
		withStdout(t, func() {
			if code := cmdService([]string{"sync-timer", "status"}); code != exitStatusNotInstalled {
				t.Errorf("not installed: exit = %d, want %d", code, exitStatusNotInstalled)
			}
		})
		syncTimerStatusFn = func() (service.SyncTimerStatus, error) {
			return service.SyncTimerStatus{Installed: true, Path: "/tmp/x.plist", Interval: 30 * time.Minute}, nil
		}
		withStdout(t, func() {
			if code := cmdService([]string{"sync-timer", "status"}); code != exitStatusStopped {
				t.Errorf("installed but inactive: exit = %d, want %d", code, exitStatusStopped)
			}
		})
		syncTimerStatusFn = func() (service.SyncTimerStatus, error) {
			return service.SyncTimerStatus{Installed: true, Active: true, Path: "/tmp/x.plist", Interval: 30 * time.Minute}, nil
		}
		out := withStdout(t, func() {
			if code := cmdService([]string{"sync-timer", "status"}); code != exitStatusRunning {
				t.Errorf("active: exit = %d, want %d", code, exitStatusRunning)
			}
		})
		if !strings.Contains(out, "every 30m") {
			t.Errorf("status output = %q, want the interval", out)
		}
	})

	t.Run("uninstall", func(t *testing.T) {
		called := false
		syncTimerUninstallFn = func() error { called = true; return nil }
		withStdout(t, func() { cmdService([]string{"sync-timer", "uninstall"}) })
		if !called {
			t.Error("uninstall was not called")
		}
	})
}

// The hint names the scheduled trigger once per invocation, and only for
// providers that genuinely have no session hook.
func TestPrintSyncTimerHint(t *testing.T) {
	cases := []struct {
		name      string
		providers []string
		want      bool
		mentions  string
	}{
		{"hook-less provider", []string{"kiro"}, true, "kiro"},
		{"hooked providers only", []string{"claude", "codex", "opencode"}, false, ""},
		{"mixed", []string{"claude", "kiro"}, true, "kiro"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := withStdout(t, func() { printSyncTimerHint(tc.providers) })
			printed := strings.Contains(out, "sync-timer install")
			if printed != tc.want {
				t.Fatalf("hint printed=%v, want %v (output %q)", printed, tc.want, out)
			}
			if !tc.want {
				return
			}
			if strings.Count(out, "sync-timer install") != 1 {
				t.Errorf("the hint must appear exactly once: %q", out)
			}
			if !strings.Contains(out, tc.mentions) {
				t.Errorf("output = %q, want it to name %q", out, tc.mentions)
			}
			if strings.Contains(out, "claude") {
				t.Errorf("a hooked provider must not be named: %q", out)
			}
		})
	}
}
