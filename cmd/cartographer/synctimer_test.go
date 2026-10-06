package main

// Tests for the `service sync-timer` dispatch and the hook-less provider hint
// (D140).

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
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

// Kiro has a session-start hook that fires only in the V3 TUI (D300): the
// timer is still advised, and the reason given is the limit, never "no hook",
// which would be false of a client whose hook is installed.
func TestPrintSyncTimerHint_KiroNamesWhereItsHookFires(t *testing.T) {
	old := syncTimerStatusFn
	t.Cleanup(func() { syncTimerStatusFn = old })
	syncTimerStatusFn = func() (service.SyncTimerStatus, error) { return service.SyncTimerStatus{}, nil }

	out := withStdout(t, func() { printSyncTimerHint([]string{"hermes", "kiro"}) })
	if !strings.Contains(out, "hermes has no session-start hook") {
		t.Errorf("hermes must still be named as hookless: %q", out)
	}
	if !strings.Contains(out, "kiro's session-start hook fires only in `kiro-cli chat --v3 --tui`") {
		t.Errorf("kiro must be named with its limit: %q", out)
	}
	if strings.Contains(out, "kiro has no session-start hook") || strings.Count(out, "sync-timer install") != 1 {
		t.Errorf("unexpected hint: %q", out)
	}
}

// The hint names the scheduled trigger once per invocation, only for providers
// that genuinely have no session hook, and only when the trigger is not already
// installed (a status the hint used to ignore, advising the installation of
// something already running).
func TestPrintSyncTimerHint(t *testing.T) {
	installed := service.SyncTimerStatus{Installed: true, Path: "/tmp/com.cartographer.sync.plist"}
	cases := []struct {
		name      string
		providers []string
		status    func() (service.SyncTimerStatus, error)
		want      bool
		mentions  string
	}{
		{"hook-less provider", []string{"kiro"}, func() (service.SyncTimerStatus, error) { return service.SyncTimerStatus{}, nil }, true, "kiro"},
		{"hooked providers only", []string{"claude", "codex", "opencode"}, func() (service.SyncTimerStatus, error) { return service.SyncTimerStatus{}, nil }, false, ""},
		{"mixed", []string{"claude", "kiro"}, func() (service.SyncTimerStatus, error) { return service.SyncTimerStatus{}, nil }, true, "kiro"},
		{"hook-less provider but timer installed", []string{"kiro"}, func() (service.SyncTimerStatus, error) { return installed, nil }, false, ""},
		{"mixed and timer installed", []string{"claude", "kiro"}, func() (service.SyncTimerStatus, error) { return installed, nil }, false, ""},
		{"timer status unreadable still warns", []string{"kiro"}, func() (service.SyncTimerStatus, error) {
			return service.SyncTimerStatus{}, errors.New("launchctl unavailable")
		}, true, "kiro"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := syncTimerStatusFn
			t.Cleanup(func() { syncTimerStatusFn = old })
			syncTimerStatusFn = tc.status

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

// D320: connect shows Kiro's session-hook limit once and records it; every
// later sync keeps the acknowledgement and status stops repeating the hint,
// while a provider with no hook at all is still named.
func TestKiroHookLimitWarningPrintedOnce(t *testing.T) {
	old := syncTimerStatusFn
	t.Cleanup(func() { syncTimerStatusFn = old })
	syncTimerStatusFn = func() (service.SyncTimerStatus, error) { return service.SyncTimerStatus{}, nil }

	dir := doctorFixture(t, "kiro")
	if out := withStdout(t, func() { printSyncTimerHint([]string{"kiro"}) }); !strings.Contains(out, "kiro-cli chat --v3 --tui") {
		t.Fatalf("connect must show the limit: %q", out)
	}
	ackSessionHookLimit(dir, []string{"kiro"})

	// A sync rebuilds the lock: the acknowledgement must survive it.
	m := provisioning.Manifest{Revision: "r2"}
	if _, err := materializeForProviders(uniformManifests(m, []string{"kiro"}), globalProjections([]string{"kiro"}, dir), dir, "", true, false, false, portabilityOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil || !lf.ForProvider("kiro").SessionHookLimitAcked {
		t.Fatalf("acknowledgement lost across sync: %v %+v", err, lf.ForProvider("kiro"))
	}

	s := statusSnapshot{Schema: statusSchema, Reachable: true, State: "in_sync", Providers: []providerStatus{
		{Name: "kiro", Connected: true, HookLimitAcked: true},
		{Name: "hermes", Connected: true},
	}}
	out := withStdout(t, func() { renderStatus("table", s, 0) })
	if strings.Contains(out, "kiro-cli chat --v3 --tui") {
		t.Errorf("status repeated an acknowledged limit: %q", out)
	}
	if !strings.Contains(out, "hermes has no session-start hook") {
		t.Errorf("a hook-less provider must still be named: %q", out)
	}
}
