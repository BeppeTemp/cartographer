package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// stubTray replaces the three tray collaborators with counters and isolates
// the client config in a temp HOME. Nothing here may reach launchctl or
// PowerShell.
func stubTray(t *testing.T) (home string, installs *int) {
	t.Helper()
	oi, ou, oq := trayInstallFn, trayUninstallFn, trayInstalledFn
	t.Cleanup(func() { trayInstallFn, trayUninstallFn, trayInstalledFn = oi, ou, oq })
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	n := 0
	trayInstallFn = func() error { n++; return nil }
	trayUninstallFn = func() error { return nil }
	trayInstalledFn = func() bool { return true }
	return home, &n
}

func TestServiceInstallTray_DefaultOnNoTrayRemembered(t *testing.T) {
	home, installs := stubTray(t)

	out := withStdout(t, func() { installTrayWithService(false) })
	if *installs != 1 || !strings.Contains(out, "tray installed") {
		t.Fatalf("a plain install must install the tray: installs=%d out=%q", *installs, out)
	}

	withStdout(t, func() { installTrayWithService(true) })
	if *installs != 1 {
		t.Fatal("--no-tray must not install the tray")
	}
	if cfg, err := clientconfig.Load(home); err != nil || !cfg.TrayOptOut {
		t.Fatalf("--no-tray must remember the opt-out: cfg=%+v err=%v", cfg, err)
	}

	withStdout(t, func() { installTrayWithService(false) })
	if *installs != 1 {
		t.Fatal("a remembered opt-out must be honoured by a plain install")
	}
}

func TestServiceInstallTray_FailureIsAWarning(t *testing.T) {
	stubTray(t)
	trayInstallFn = func() error { return errors.New("no gui session") }
	var stderr string
	withStdout(t, func() { stderr = withStderr(t, func() { installTrayWithService(false) }) })
	if !strings.Contains(stderr, "Warning:") || !strings.Contains(stderr, "no gui session") {
		t.Errorf("stderr = %q, want a warning", stderr)
	}
}

func TestCmdServiceTray_UninstallSetsOptOutInstallClearsIt(t *testing.T) {
	home, installs := stubTray(t)

	withStdout(t, func() { cmdService([]string{"tray", "uninstall"}) })
	if cfg, err := clientconfig.Load(home); err != nil || !cfg.TrayOptOut {
		t.Fatalf("uninstall must remember the opt-out: cfg=%+v err=%v", cfg, err)
	}
	withStdout(t, func() { cmdService([]string{"tray", "install"}) })
	if *installs != 1 {
		t.Errorf("tray install must install, installs=%d", *installs)
	}
	if cfg, err := clientconfig.Load(home); err != nil || cfg.TrayOptOut {
		t.Fatalf("install must clear the opt-out: cfg=%+v err=%v", cfg, err)
	}
	trayInstalledFn = func() bool { return false }
	if code := withExitCode(t, func() int { return cmdService([]string{"tray", "status"}) }); code != exitStatusNotInstalled {
		t.Errorf("status exit = %d, want %d", code, exitStatusNotInstalled)
	}
}

func TestRepairEnsureTray_InstallsRestartsAndHonoursOptOut(t *testing.T) {
	home, installs := stubTray(t)
	if err := repairEnsureTrayFn(); err != nil || *installs != 1 {
		t.Fatalf("absent tray must be installed: err=%v installs=%d", err, *installs)
	}
	if err := repairEnsureTrayFn(); err != nil || *installs != 2 {
		t.Fatalf("an installed tray is re-rendered and restarted every run: installs=%d", *installs)
	}
	cfg := clientconfig.Default()
	cfg.TrayOptOut = true
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	if err := repairEnsureTrayFn(); err != nil || *installs != 2 {
		t.Fatalf("opted out: the tray must be left alone, installs=%d", *installs)
	}
}

func TestCmdUpgradeRepair_TrayStepPlacementAndExitCode(t *testing.T) {
	cases := []struct {
		name      string
		installed bool
		running   bool
		wantTray  int
	}{
		{"not installed skips the tray", false, false, 0},
		{"stopped service still gets the tray", true, false, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			saveRepairFns(t)
			calls := 0
			repairEnsureTrayFn = func() error { calls++; return errors.New("boom") }
			repairEffectiveConfigFn = func(string) (string, error) { return "/cfg.yaml", nil }
			repairServiceStatusFn = func(string) (service.Status, error) {
				return service.Status{Installed: tc.installed, Running: tc.running}, nil
			}
			var code int
			withStdout(t, func() { withStderr(t, func() { code = cmdUpgradeRepair(nil) }) })
			if code != exitRepairOK {
				t.Errorf("a tray failure must not change the exit code: %d", code)
			}
			if calls != tc.wantTray {
				t.Errorf("tray step calls = %d, want %d", calls, tc.wantTray)
			}
		})
	}
}
