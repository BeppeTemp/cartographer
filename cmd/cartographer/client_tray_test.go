package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// trayCounters stubs the tray collaborators and counts the calls; installed
// is the starting state of the definition (an install flips it).
type trayCounters struct{ installs, uninstalls int }

func stubTrayState(t *testing.T, installed bool) (home string, c *trayCounters) {
	t.Helper()
	home, _ = stubTray(t)
	c = &trayCounters{}
	trayInstallFn = func() error { c.installs++; installed = true; return nil }
	oldStable := trayStableBinaryFn
	trayStableBinaryFn = func() error { return nil }
	t.Cleanup(func() { trayStableBinaryFn = oldStable })
	trayUninstallFn = func() error { c.uninstalls++; installed = false; return nil }
	trayInstalledFn = func() bool { return installed }
	oldSvc := localServiceInstalledFn
	localServiceInstalledFn = func() bool { return false }
	t.Cleanup(func() { localServiceInstalledFn = oldSvc })
	return home, c
}

func TestEnsureClientTray(t *testing.T) {
	claude := []string{"claude"}
	tests := []struct {
		name         string
		goos         string
		installed    bool
		optedOut     bool
		opts         connectOptions
		res          connectResult
		wantInstalls int
		wantRemoves  int
		wantOptOut   bool
		wantOut      string
	}{
		{name: "default installs on darwin", goos: "darwin", wantInstalls: 1, wantOut: "tray installed"},
		{name: "default installs on windows", goos: "windows", wantInstalls: 1, wantOut: "tray installed"},
		{name: "unsupported platform does nothing", goos: "linux"},
		{name: "an installed tray is not reinstalled", goos: "darwin", installed: true},
		{name: "a remembered opt-out installs nothing", goos: "darwin", optedOut: true},
		{name: "--no-tray records the opt-out and installs nothing", goos: "darwin", opts: connectOptions{NoTray: true}, wantOptOut: true},
		{name: "--no-tray removes an installed tray", goos: "darwin", installed: true, opts: connectOptions{NoTray: true}, wantRemoves: 1, wantOptOut: true},
		{name: "every provider skipped by the write guard", goos: "darwin", res: connectResult{Skipped: map[string][]string{"claude": {"x"}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home, c := stubTrayState(t, tc.installed)
			trayGOOS = tc.goos
			if tc.optedOut {
				if err := clientconfig.Save(home, &clientconfig.Config{TrayOptOut: true}); err != nil {
					t.Fatal(err)
				}
			}
			tc.opts.Providers = claude
			out := withStdout(t, func() { ensureClientTray(tc.opts, tc.res) })
			if c.installs != tc.wantInstalls || c.uninstalls != tc.wantRemoves {
				t.Fatalf("installs=%d removes=%d, want %d/%d", c.installs, c.uninstalls, tc.wantInstalls, tc.wantRemoves)
			}
			if tc.wantOut != "" && !strings.Contains(out, tc.wantOut) {
				t.Fatalf("out = %q, want %q", out, tc.wantOut)
			}
			if tc.wantOut == "" && out != "" {
				t.Fatalf("unexpected output %q", out)
			}
			if tc.wantOptOut {
				if cfg, err := clientconfig.Load(home); err != nil || !cfg.TrayOptOut {
					t.Fatalf("opt-out not remembered: %+v %v", cfg, err)
				}
			}
		})
	}
}

func TestEnsureClientTray_FailureIsAWarning(t *testing.T) {
	_, _ = stubTrayState(t, false)
	trayInstallFn = func() error { return errors.New("no GUI session") }
	var out string
	errOut := withStderr(t, func() {
		out = withStdout(t, func() { ensureClientTray(connectOptions{Providers: []string{"claude"}}, connectResult{}) })
	})
	if !strings.Contains(errOut, "Warning: the status icon was not installed") || !strings.Contains(errOut, "no GUI session") {
		t.Fatalf("stderr = %q", errOut)
	}
	if strings.Contains(out, "tray installed") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestPrintConnectResult_TrayDryRunAndRun(t *testing.T) {
	_, c := stubTrayState(t, false)
	claude := []string{"claude"}
	out := withStdout(t, func() {
		printConnectResult(t.TempDir(), claude, connectOptions{Providers: claude, DryRun: true}, connectResult{Providers: claude})
	})
	if !strings.Contains(out, "[dry-run] would install the status icon") || c.installs != 0 {
		t.Fatalf("dry run: installs=%d out=%q", c.installs, out)
	}
	withStdout(t, func() {
		printConnectResult(t.TempDir(), claude, connectOptions{Providers: claude}, connectResult{Providers: claude})
	})
	if c.installs != 1 {
		t.Fatalf("connect must install the tray once, got %d", c.installs)
	}
}

func TestDoDisconnect_Tray(t *testing.T) {
	tests := []struct {
		name        string
		providers   []string // connected before
		disconnect  []string
		service     bool
		installed   bool
		dryRun      bool
		keepTray    bool
		wantRemoved bool
	}{
		{name: "last agent, no service, tray installed", providers: []string{"claude"}, disconnect: []string{"claude"}, installed: true, wantRemoved: true},
		{name: "an agent remains", providers: []string{"claude", "codex"}, disconnect: []string{"claude"}, installed: true},
		{name: "a local service is installed", providers: []string{"claude"}, disconnect: []string{"claude"}, installed: true, service: true},
		{name: "dry run", providers: []string{"claude"}, disconnect: []string{"claude"}, installed: true, dryRun: true},
		{name: "reconnect keeps it", providers: []string{"claude"}, disconnect: []string{"claude"}, installed: true, keepTray: true},
		{name: "no tray installed", providers: []string{"claude"}, disconnect: []string{"claude"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, c := stubTrayState(t, tc.installed)
			localServiceInstalledFn = func() bool { return tc.service }
			dir := setupDisconnectFixture(t, tc.providers...)
			var res disconnectResult
			withStdout(t, func() {
				var err error
				res, err = doDisconnect(disconnectOptions{Providers: tc.disconnect, Dir: dir, DryRun: tc.dryRun, KeepTray: tc.keepTray})
				if err != nil {
					t.Fatal(err)
				}
			})
			if res.TrayUninstalled != tc.wantRemoved || (c.uninstalls == 1) != tc.wantRemoved {
				t.Fatalf("TrayUninstalled=%v uninstalls=%d, want removed=%v", res.TrayUninstalled, c.uninstalls, tc.wantRemoved)
			}
			if cfg, err := clientconfig.Load(dir); err != nil || cfg.TrayOptOut {
				t.Fatalf("a disconnect must not set the opt-out: %+v %v", cfg, err)
			}
			out := withStdout(t, func() { printDisconnectSummary(res, false) })
			if tc.wantRemoved != strings.Contains(out, "removed the status icon: no agent is connected") {
				t.Fatalf("summary = %q", out)
			}
		})
	}
}

func TestServiceUninstall_KeepsTrayAClientUses(t *testing.T) {
	for _, connected := range []bool{true, false} {
		home, _ := stubTrayState(t, true)
		var agents []string
		if connected {
			agents = []string{"claude"}
		}
		if err := clientconfig.Save(home, &clientconfig.Config{Agents: agents}); err != nil {
			t.Fatal(err)
		}
		if got := trayInUseByClient(); got != connected {
			t.Fatalf("trayInUseByClient with agents=%v = %v", agents, got)
		}
	}
}

func TestUpgradeRepair_TrayWithNoService(t *testing.T) {
	tests := []struct {
		name      string
		installed bool
		optedOut  bool
		wantCalls int
		wantInst  int
	}{
		{name: "tray installed is refreshed", installed: true, wantCalls: 1, wantInst: 1},
		{name: "no tray is not created", wantCalls: 0},
		{name: "opted out is not reinstalled", installed: true, optedOut: true, wantCalls: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			saveRepairFns(t)
			home, c := stubTrayState(t, tc.installed)
			if tc.optedOut {
				if err := clientconfig.Save(home, &clientconfig.Config{TrayOptOut: true}); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			// The real step (opt-out check, then trayInstallFn), counted.
			repairEnsureTrayFn = func() error {
				calls++
				if trayOptedOut() {
					return nil
				}
				return trayInstallFn()
			}
			repairEffectiveConfigFn = func(string) (string, error) { return "/cfg.yaml", nil }
			repairServiceStatusFn = func(string) (service.Status, error) { return service.Status{Installed: false}, nil }
			withStdout(t, func() {
				if code := cmdUpgradeRepair(nil); code != exitRepairOK {
					t.Errorf("exit = %d", code)
				}
			})
			if calls != tc.wantCalls || c.installs != tc.wantInst {
				t.Fatalf("calls=%d installs=%d, want %d/%d", calls, c.installs, tc.wantCalls, tc.wantInst)
			}
		})
	}
}

func TestServiceTrayInstall_NoteWithoutServerOrClient(t *testing.T) {
	_, _ = stubTrayState(t, false)
	out := withStdout(t, func() { cmdServiceTray([]string{"install"}) })
	if !strings.Contains(out, "no server to show yet") {
		t.Fatalf("out = %q", out)
	}
	if err := clientconfig.Save(mustTargetDir(t), &clientconfig.Config{}); err != nil {
		t.Fatal(err)
	}
	out = withStdout(t, func() { cmdServiceTray([]string{"install"}) })
	if strings.Contains(out, "no server to show yet") {
		t.Fatalf("a client config exists, out = %q", out)
	}
}

func mustTargetDir(t *testing.T) string {
	t.Helper()
	dir, err := clientconfig.TargetDir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPrintTrayHint(t *testing.T) {
	tests := []struct {
		name      string
		goos      string
		installed bool
		optedOut  bool
		connected bool
		want      bool
	}{
		{name: "shown", goos: "darwin", connected: true, want: true},
		{name: "installed", goos: "darwin", installed: true, connected: true},
		{name: "opted out", goos: "darwin", optedOut: true, connected: true},
		{name: "unsupported platform", goos: "linux", connected: true},
		{name: "no agent", goos: "darwin"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home, _ := stubTrayState(t, tc.installed)
			trayGOOS = tc.goos
			if tc.optedOut {
				if err := clientconfig.Save(home, &clientconfig.Config{TrayOptOut: true}); err != nil {
					t.Fatal(err)
				}
			}
			out := withStdout(t, func() { printTrayHint(tc.connected) })
			if got := strings.Contains(out, "cartographer service tray install"); got != tc.want {
				t.Fatalf("hint shown=%v want %v (out %q)", got, tc.want, out)
			}
		})
	}
}

func TestRenderSetupPlan_StatusIconLine(t *testing.T) {
	for _, tc := range []struct {
		installed, optedOut, want bool
	}{{false, false, true}, {true, false, false}, {false, true, false}} {
		_, _ = stubTrayState(t, tc.installed)
		f := baseSetupFacts()
		f.TrayOptOut = tc.optedOut
		var b strings.Builder
		renderSetupPlan(&b, setupPlan{Agents: []string{"claude"}, ServerURL: "http://127.0.0.1:39273/mcp"}, f)
		if got := strings.Contains(b.String(), "status icon  install"); got != tc.want {
			t.Fatalf("%+v: line shown=%v\n%s", tc, got, b.String())
		}
	}
}

func TestEnsureClientTray_UnstableBinaryIsNotALoginItem(t *testing.T) {
	_, c := stubTrayState(t, false)
	trayStableBinaryFn = func() error { return errors.New("/tmp/go-build/x is a go-build artifact") }
	opts := connectOptions{Providers: []string{"claude"}}
	errOut := withStderr(t, func() { ensureClientTray(opts, connectResult{}) })
	if c.installs != 0 || !strings.Contains(errOut, "go-build artifact") || !strings.Contains(errOut, "cartographer service tray install") {
		t.Fatalf("installs=%d stderr=%q", c.installs, errOut)
	}
	if wouldInstallClientTray(opts, connectResult{}) {
		t.Fatal("a dry run must not promise an install the real run refuses")
	}
	trayStableBinaryFn = func() error { return nil }
	withStdout(t, func() { ensureClientTray(opts, connectResult{}) })
	if c.installs != 1 {
		t.Fatalf("a stable binary installs, got %d", c.installs)
	}
}

func TestWouldInstallClientTray_SharesTheSkippedGuard(t *testing.T) {
	_, _ = stubTrayState(t, false)
	opts := connectOptions{Providers: []string{"claude"}}
	if !wouldInstallClientTray(opts, connectResult{}) {
		t.Fatal("expected the dry run to promise an install")
	}
	if wouldInstallClientTray(opts, connectResult{Skipped: map[string][]string{"claude": {"x"}}}) {
		t.Fatal("every provider skipped: the real run installs nothing, so must the dry run")
	}
}

func TestDoDisconnect_NoopKeepsAHandInstalledTray(t *testing.T) {
	_, c := stubTrayState(t, true)
	dir := setupDisconnectFixture(t)
	if err := clientconfig.Save(dir, &clientconfig.Config{ServerURL: "http://localhost:8080/mcp", ServerName: "wiki"}); err != nil {
		t.Fatal(err)
	}
	var res disconnectResult
	withStdout(t, func() {
		var err error
		res, err = doDisconnect(disconnectOptions{Providers: []string{"claude"}, Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
	})
	if res.TrayUninstalled || c.uninstalls != 0 {
		t.Fatalf("a disconnect that removed no agent must keep the tray: %+v uninstalls=%d", res, c.uninstalls)
	}
}

func TestCmdServiceUninstall_KeepTrayWiring(t *testing.T) {
	for _, connected := range []bool{true, false} {
		home, _ := stubTrayState(t, true)
		var agents []string
		if connected {
			agents = []string{"claude"}
		}
		if err := clientconfig.Save(home, &clientconfig.Config{Agents: agents}); err != nil {
			t.Fatal(err)
		}
		old := serviceUninstallFn
		var gotKeep *bool
		serviceUninstallFn = func(keep bool) error { gotKeep = &keep; return nil }
		out := withStdout(t, func() { cmdServiceUninstall(nil) })
		serviceUninstallFn = old
		if gotKeep == nil || *gotKeep != connected {
			t.Fatalf("connected=%v: Uninstall keepTray = %v", connected, gotKeep)
		}
		if got := strings.Contains(out, "status icon kept"); got != connected {
			t.Fatalf("connected=%v: out=%q", connected, out)
		}
	}
}
