package main

// Tests for the `service sync-timer` dispatch and the hook-less provider hint
// (D140).

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
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

// stubTimer replaces the three timer functions for one test and returns what
// was called. installed is the status the timer reports.
type timerCalls struct{ installs, uninstalls int }

func stubTimer(t *testing.T, installed bool) *timerCalls {
	t.Helper()
	oldI, oldU, oldS := syncTimerInstallFn, syncTimerUninstallFn, syncTimerStatusFn
	t.Cleanup(func() { syncTimerInstallFn, syncTimerUninstallFn, syncTimerStatusFn = oldI, oldU, oldS })
	c := &timerCalls{}
	syncTimerInstallFn = func(time.Duration) error { c.installs++; return nil }
	syncTimerUninstallFn = func() error { c.uninstalls++; return nil }
	syncTimerStatusFn = func() (service.SyncTimerStatus, error) {
		return service.SyncTimerStatus{Installed: installed}, nil
	}
	return c
}

// writeOptOut writes a client config under dir with the given opt-out.
func writeOptOut(t *testing.T, dir string, optOut bool) {
	t.Helper()
	cfg := clientconfig.Default()
	cfg.SyncTimerOptOut = optOut
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureSyncTimer_InstallsForKiro(t *testing.T) {
	c := stubTimer(t, false)
	dir := t.TempDir()
	writeOptOut(t, dir, false)
	out := withStdout(t, func() { ensureSyncTimer(dir, []string{"claude", "kiro"}) })
	if c.installs != 1 {
		t.Fatalf("installs = %d, want 1 (output %q)", c.installs, out)
	}
	if !strings.Contains(out, "sync-timer uninstall") {
		t.Errorf("the escape hatch must be named: %q", out)
	}
}

func TestEnsureSyncTimer_RespectsOptOut(t *testing.T) {
	c := stubTimer(t, false)
	dir := t.TempDir()
	writeOptOut(t, dir, true)
	out := withStdout(t, func() { ensureSyncTimer(dir, []string{"kiro"}) })
	if c.installs != 0 {
		t.Errorf("an explicit opt-out was overridden (output %q)", out)
	}
	if !strings.Contains(out, "explicitly uninstalled") {
		t.Errorf("the opt-out must be named: %q", out)
	}
}

// A client config that cannot be parsed is not evidence the operator did not
// opt out: nothing is installed.
func TestEnsureSyncTimer_CorruptConfigInstallsNothing(t *testing.T) {
	c := stubTimer(t, false)
	dir := t.TempDir()
	if err := os.WriteFile(clientconfig.Path(dir), []byte("agents: [unclosed"), 0o644); err != nil {
		t.Fatal(err)
	}
	withStdout(t, func() { ensureSyncTimer(dir, []string{"kiro"}) })
	if c.installs != 0 {
		t.Error("a corrupt client config must not trigger an install")
	}
}

func TestEnsureSyncTimer_NoTimerWhenAllHaveHooks(t *testing.T) {
	c := stubTimer(t, false)
	dir := t.TempDir()
	withStdout(t, func() { ensureSyncTimer(dir, []string{"claude", "codex"}) })
	if c.installs != 0 {
		t.Error("no provider needs the timer, none must be installed")
	}
}

func TestEnsureSyncTimer_AlreadyInstalled(t *testing.T) {
	c := stubTimer(t, true)
	withStdout(t, func() { ensureSyncTimer(t.TempDir(), []string{"kiro"}) })
	if c.installs != 0 {
		t.Error("an installed timer must not be installed again")
	}
}

func TestSyncTimerUninstall_SetsOptOutAndInstallClearsIt(t *testing.T) {
	stubTimer(t, true)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	withStdout(t, func() { cmdService([]string{"sync-timer", "uninstall"}) })
	cfg, err := clientconfig.Load(home)
	if err != nil || !cfg.SyncTimerOptOut {
		t.Fatalf("uninstall must remember the opt-out: cfg=%+v err=%v", cfg, err)
	}
	data, _ := os.ReadFile(clientconfig.Path(home))
	if !strings.Contains(string(data), "sync_timer_opt_out: true") {
		t.Errorf("opt-out not persisted: %s", data)
	}

	withStdout(t, func() { cmdService([]string{"sync-timer", "install"}) })
	cfg, err = clientconfig.Load(home)
	if err != nil || cfg.SyncTimerOptOut {
		t.Fatalf("install must clear the opt-out: cfg=%+v err=%v", cfg, err)
	}
}

func TestRenderSetupPlan_ShowsSyncTimer(t *testing.T) {
	stubTimer(t, false)
	plan := setupPlan{Service: serviceKeep, KB: kbKeepExisting, Existing: []string{"kb-a"}, Agents: []string{"claude", "kiro"}}

	var b strings.Builder
	renderSetupPlan(&b, plan, setupFacts{})
	if !strings.Contains(b.String(), "sync timer") || !strings.Contains(b.String(), "kiro") {
		t.Errorf("the plan must show the timer before it runs:\n%s", b.String())
	}

	b.Reset()
	renderSetupPlan(&b, plan, setupFacts{TimerOptOut: true})
	if strings.Contains(b.String(), "sync timer") {
		t.Errorf("an opted-out timer must not be planned:\n%s", b.String())
	}

	b.Reset()
	renderSetupPlan(&b, setupPlan{Service: serviceKeep, KB: kbKeepExisting, Existing: []string{"kb-a"}, Agents: []string{"claude"}}, setupFacts{})
	if strings.Contains(b.String(), "sync timer") {
		t.Errorf("no client needs the timer:\n%s", b.String())
	}
}

func TestCheckTriggerCoverage_OptOutIsInfo(t *testing.T) {
	stubTimer(t, false)
	dir := t.TempDir()
	writeOptOut(t, dir, true)
	got := checkTriggerCoverage(dir, []string{"kiro"})
	if len(got) != 1 || got[0].Severity != doctorInfo {
		t.Fatalf("want one info finding, got %+v", got)
	}
	writeOptOut(t, dir, false)
	got = checkTriggerCoverage(dir, []string{"kiro"})
	if len(got) != 1 || got[0].Severity != doctorWarning {
		t.Fatalf("want one warning, got %+v", got)
	}
}
