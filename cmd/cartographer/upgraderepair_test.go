package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// saveRepairFns snapshots every upgrade-repair collaborator var (plus the
// global `version`) and restores it on test cleanup, mirroring the
// statusServiceFn/statusHealthFn save/restore pattern in status_test.go.
// Individual tests only need to overwrite the vars relevant to the
// scenario under test.
func saveRepairFns(t *testing.T) {
	t.Helper()
	origVersion := version
	origEffective := repairEffectiveConfigFn
	origStatus := repairServiceStatusFn
	origHealth := repairProbeHealthFn
	origReplace := repairReplaceFn
	origTargetDir := repairTargetDirFn
	origLoadCfg := repairLoadClientConfigFn
	origRunSync := repairRunSyncFn
	t.Cleanup(func() {
		version = origVersion
		repairEffectiveConfigFn = origEffective
		repairServiceStatusFn = origStatus
		repairProbeHealthFn = origHealth
		repairReplaceFn = origReplace
		repairTargetDirFn = origTargetDir
		repairLoadClientConfigFn = origLoadCfg
		repairRunSyncFn = origRunSync
	})
}

// failIfReplaceCalled/failIfSyncCalled install collaborators that fail the
// test if invoked, for state-matrix rows where the mandate requires that no
// restart/no sync is attempted.
func failIfReplaceCalled(t *testing.T) {
	t.Helper()
	repairReplaceFn = func(opts service.ReplaceOptions) error {
		t.Fatalf("repairReplaceFn must not be called, got opts=%+v", opts)
		return nil
	}
}

func failIfSyncCalled(t *testing.T) {
	t.Helper()
	repairRunSyncFn = func(dir string, cfg *clientconfig.Config, opts syncOptions) (syncResult, error) {
		t.Fatalf("repairRunSyncFn must not be called, got dir=%q cfg=%+v opts=%+v", dir, cfg, opts)
		return syncResult{}, nil
	}
}

// writeRepairClientConfig saves a client config under dir, for tests that
// let repairLoadClientConfigFn fall through to the real clientconfig.Load
// against a temp directory returned by repairTargetDirFn.
func writeRepairClientConfig(t *testing.T, dir, serverURL string, agents []string) {
	t.Helper()
	if err := clientconfig.Save(dir, &clientconfig.Config{ServerURL: serverURL, Agents: agents, Trust: true}); err != nil {
		t.Fatalf("clientconfig.Save: %v", err)
	}
}

// --- Row 1: service not installed ---

func TestCmdUpgradeRepair_NotInstalled(t *testing.T) {
	saveRepairFns(t)
	repairEffectiveConfigFn = func(explicit string) (string, error) { return "/cfg.yaml", nil }
	repairServiceStatusFn = func(configPath string) (service.Status, error) {
		return service.Status{Installed: false}, nil
	}
	failIfReplaceCalled(t)
	failIfSyncCalled(t)

	out := withStdout(t, func() {
		if code := cmdUpgradeRepair(nil); code != exitRepairOK {
			t.Errorf("exit = %d, want %d", code, exitRepairOK)
		}
	})
	if !strings.Contains(out, "not installed") || !strings.Contains(out, "nothing to repair") {
		t.Errorf("output = %q, want it to report a no-op", out)
	}
}

// --- Row 2: service installed but stopped ---

func TestCmdUpgradeRepair_InstalledStopped(t *testing.T) {
	saveRepairFns(t)
	repairEffectiveConfigFn = func(explicit string) (string, error) { return "/cfg.yaml", nil }
	repairServiceStatusFn = func(configPath string) (service.Status, error) {
		return service.Status{Installed: true, Running: false}, nil
	}
	failIfReplaceCalled(t)
	failIfSyncCalled(t)

	out := withStdout(t, func() {
		if code := cmdUpgradeRepair(nil); code != exitRepairOK {
			t.Errorf("exit = %d, want %d", code, exitRepairOK)
		}
	})
	for _, want := range []string{"stopped", "cartographer service start", "cartographer sync"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want it to contain %q", out, want)
		}
	}
}

// --- Row 3: installed+running, /health already reports the installed
// version -> no restart, in-place sync ---

func TestCmdUpgradeRepair_RunningAlreadyCurrent_NoRestart(t *testing.T) {
	saveRepairFns(t)
	dir := t.TempDir()
	version = "v1.2.3"
	repairEffectiveConfigFn = func(explicit string) (string, error) { return "/cfg.yaml", nil }
	repairServiceStatusFn = func(configPath string) (service.Status, error) {
		return service.Status{Installed: true, Running: true, HTTPAddr: "127.0.0.1:39273"}, nil
	}
	repairProbeHealthFn = func(addr string, timeout time.Duration) (service.HealthStatus, error) {
		return service.HealthStatus{Status: "ok", Version: "v1.2.3"}, nil
	}
	failIfReplaceCalled(t)
	repairTargetDirFn = func() (string, error) { return dir, nil }
	writeRepairClientConfig(t, dir, "http://127.0.0.1:39273/mcp", []string{"claude"})

	var gotDir string
	var gotOpts syncOptions
	syncCalled := false
	repairRunSyncFn = func(d string, cfg *clientconfig.Config, opts syncOptions) (syncResult, error) {
		syncCalled = true
		gotDir, gotOpts = d, opts
		return syncResult{Revision: "rev1"}, nil
	}

	out := withStdout(t, func() {
		if code := cmdUpgradeRepair(nil); code != exitRepairOK {
			t.Errorf("exit = %d, want %d", code, exitRepairOK)
		}
	})
	if !syncCalled {
		t.Fatal("expected in-place sync to run")
	}
	if gotDir != dir {
		t.Errorf("runSync dir = %q, want %q", gotDir, dir)
	}
	// Policy options must be passed unchanged: AutoTrust=false, DryRun=false.
	if gotOpts != (syncOptions{}) {
		t.Errorf("runSync opts = %+v, want zero value (AutoTrust=false, DryRun=false)", gotOpts)
	}
	if !strings.Contains(out, "restart already-open provider sessions") {
		t.Errorf("output = %q, want the provider-session restart reminder", out)
	}
}

// --- Row 4: installed+running, /health absent or reports an old version ->
// repairReplaceFn is called with the correct ExpectedVersion and effective
// config path, then sync runs ---

func TestCmdUpgradeRepair_RunningOldVersion_ReplaceThenSync(t *testing.T) {
	cases := []struct {
		name   string
		health func(addr string, timeout time.Duration) (service.HealthStatus, error)
	}{
		{
			name: "health reports old version",
			health: func(addr string, timeout time.Duration) (service.HealthStatus, error) {
				return service.HealthStatus{Status: "ok", Version: "v1.2.2"}, nil
			},
		},
		{
			name: "health absent (connection error)",
			health: func(addr string, timeout time.Duration) (service.HealthStatus, error) {
				return service.HealthStatus{}, errors.New("connection refused")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			saveRepairFns(t)
			dir := t.TempDir()
			version = "v1.2.3"
			const effectiveConfigPath = "/effective/config.yaml"
			repairEffectiveConfigFn = func(explicit string) (string, error) { return effectiveConfigPath, nil }
			repairServiceStatusFn = func(configPath string) (service.Status, error) {
				return service.Status{Installed: true, Running: true, HTTPAddr: "127.0.0.1:39273"}, nil
			}
			repairProbeHealthFn = tc.health

			var gotOpts service.ReplaceOptions
			replaceCalled := false
			repairReplaceFn = func(opts service.ReplaceOptions) error {
				replaceCalled = true
				gotOpts = opts
				return nil
			}
			repairTargetDirFn = func() (string, error) { return dir, nil }
			writeRepairClientConfig(t, dir, "http://127.0.0.1:39273/mcp", []string{"claude"})
			syncCalled := false
			repairRunSyncFn = func(d string, cfg *clientconfig.Config, opts syncOptions) (syncResult, error) {
				syncCalled = true
				return syncResult{Revision: "rev1"}, nil
			}

			out := withStdout(t, func() {
				if code := cmdUpgradeRepair(nil); code != exitRepairOK {
					t.Errorf("exit = %d, want %d", code, exitRepairOK)
				}
			})
			if !replaceCalled {
				t.Fatal("expected repairReplaceFn to be called")
			}
			if gotOpts.ExpectedVersion != "v1.2.3" {
				t.Errorf("Replace ExpectedVersion = %q, want %q", gotOpts.ExpectedVersion, "v1.2.3")
			}
			if gotOpts.ConfigPath != effectiveConfigPath {
				t.Errorf("Replace ConfigPath = %q, want %q", gotOpts.ConfigPath, effectiveConfigPath)
			}
			if !syncCalled {
				t.Error("expected sync to run after a successful replace")
			}
			if !strings.Contains(out, "restart already-open provider sessions") {
				t.Errorf("output = %q, want the provider-session restart reminder", out)
			}
		})
	}
}

// --- repairReplaceFn failure (timeout on an old version) -> exit 2, and no
// sync is attempted ---

func TestCmdUpgradeRepair_ReplaceFails_ExitServiceFailure_NoSync(t *testing.T) {
	saveRepairFns(t)
	repairEffectiveConfigFn = func(explicit string) (string, error) { return "/cfg.yaml", nil }
	repairServiceStatusFn = func(configPath string) (service.Status, error) {
		return service.Status{Installed: true, Running: true, HTTPAddr: "127.0.0.1:39273"}, nil
	}
	repairProbeHealthFn = func(addr string, timeout time.Duration) (service.HealthStatus, error) {
		return service.HealthStatus{}, errors.New("connection refused")
	}
	repairReplaceFn = func(opts service.ReplaceOptions) error {
		return errors.New("timed out waiting for http://127.0.0.1:39273/health to serve version v1.2.3")
	}
	failIfSyncCalled(t)

	code := withExitCode(t, func() int { return cmdUpgradeRepair(nil) })
	if code != exitRepairServiceFailure {
		t.Errorf("exit = %d, want %d", code, exitRepairServiceFailure)
	}
}

// withExitCode runs f with stdout/stderr discarded via withStdout, returning
// only the exit code, for tests that don't need the printed text.
func withExitCode(t *testing.T, f func() int) int {
	t.Helper()
	var code int
	withStdout(t, func() { code = f() })
	return code
}

// --- effective-config / status discovery failures -> exit 2, no restart, no
// sync attempted ---

func TestCmdUpgradeRepair_EffectiveConfigError_ExitServiceFailure(t *testing.T) {
	saveRepairFns(t)
	repairEffectiveConfigFn = func(explicit string) (string, error) {
		return "", errors.New("installed plist does not declare a usable config")
	}
	failIfReplaceCalled(t)
	failIfSyncCalled(t)

	code := withExitCode(t, func() int { return cmdUpgradeRepair(nil) })
	if code != exitRepairServiceFailure {
		t.Errorf("exit = %d, want %d", code, exitRepairServiceFailure)
	}
}

func TestCmdUpgradeRepair_ServiceStatusError_ExitServiceFailure(t *testing.T) {
	saveRepairFns(t)
	repairEffectiveConfigFn = func(explicit string) (string, error) { return "/cfg.yaml", nil }
	repairServiceStatusFn = func(configPath string) (service.Status, error) {
		return service.Status{}, errors.New("service: unsupported platform")
	}
	failIfReplaceCalled(t)
	failIfSyncCalled(t)

	code := withExitCode(t, func() int { return cmdUpgradeRepair(nil) })
	if code != exitRepairServiceFailure {
		t.Errorf("exit = %d, want %d", code, exitRepairServiceFailure)
	}
}

// --- custom --config wins over discovery: the flag value must reach
// repairEffectiveConfigFn's `explicit` argument unchanged ---

func TestCmdUpgradeRepair_CustomConfigFlagWinsOverDiscovery(t *testing.T) {
	saveRepairFns(t)
	var gotExplicit string
	const customPath = "/custom/server-config.yaml"
	repairEffectiveConfigFn = func(explicit string) (string, error) {
		gotExplicit = explicit
		return explicit, nil
	}
	repairServiceStatusFn = func(configPath string) (service.Status, error) {
		return service.Status{Installed: false}, nil
	}
	failIfReplaceCalled(t)
	failIfSyncCalled(t)

	withStdout(t, func() { cmdUpgradeRepair([]string{"--config", customPath}) })
	if gotExplicit != customPath {
		t.Errorf("explicit config passed = %q, want %q", gotExplicit, customPath)
	}
}

// --- Row 5 + related: client eligibility variants, all installed+running
// with a health response that already matches the installed version (so the
// table isolates provider-sync eligibility from the restart decision) ---

func TestCmdUpgradeRepair_ProviderSyncEligibility(t *testing.T) {
	const serviceAddr = "127.0.0.1:39273"

	cases := []struct {
		name           string
		writeConfig    func(t *testing.T, dir string)
		wantExit       int
		wantContains   []string
		wantSyncCalled bool
	}{
		{
			name:         "missing client config",
			writeConfig:  nil,
			wantExit:     exitRepairOK,
			wantContains: []string{"no client configuration found"},
		},
		{
			name: "malformed client config",
			writeConfig: func(t *testing.T, dir string) {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				// Unclosed bracket: invalid YAML syntax, not just an
				// unreadable file, so clientconfig.Load returns a parse
				// error rather than os.ErrNotExist.
				if err := os.WriteFile(clientconfig.Path(dir), []byte("agents: [claude\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantExit:     exitRepairSyncPending,
			wantContains: []string{"cartographer sync"},
		},
		{
			name: "zero providers",
			writeConfig: func(t *testing.T, dir string) {
				writeRepairClientConfig(t, dir, "http://127.0.0.1:39273/mcp", nil)
			},
			wantExit:     exitRepairOK,
			wantContains: []string{"no agent connected"},
		},
		{
			name: "non-loopback host",
			writeConfig: func(t *testing.T, dir string) {
				writeRepairClientConfig(t, dir, "http://cartographer.example:39273/mcp", []string{"claude"})
			},
			wantExit:     exitRepairOK,
			wantContains: []string{"skipping provider sync", "not loopback"},
		},
		{
			name: "https scheme",
			writeConfig: func(t *testing.T, dir string) {
				writeRepairClientConfig(t, dir, "https://127.0.0.1:39273/mcp", []string{"claude"})
			},
			wantExit:     exitRepairOK,
			wantContains: []string{"skipping provider sync", "https"},
		},
		{
			name: "embedded credentials",
			writeConfig: func(t *testing.T, dir string) {
				writeRepairClientConfig(t, dir, "http://user:pass@127.0.0.1:39273/mcp", []string{"claude"})
			},
			wantExit:     exitRepairOK,
			wantContains: []string{"skipping provider sync", "embedded credentials"},
		},
		{
			name: "fragment",
			writeConfig: func(t *testing.T, dir string) {
				writeRepairClientConfig(t, dir, "http://127.0.0.1:39273/mcp#section", []string{"claude"})
			},
			wantExit:     exitRepairOK,
			wantContains: []string{"skipping provider sync", "fragment"},
		},
		{
			name: "malformed URL",
			writeConfig: func(t *testing.T, dir string) {
				writeRepairClientConfig(t, dir, "http://%zz", []string{"claude"})
			},
			wantExit:     exitRepairOK,
			wantContains: []string{"skipping provider sync", "malformed"},
		},
		{
			name: "port mismatch",
			writeConfig: func(t *testing.T, dir string) {
				writeRepairClientConfig(t, dir, "http://127.0.0.1:8080/mcp", []string{"claude"})
			},
			wantExit:     exitRepairOK,
			wantContains: []string{"skipping provider sync", "does not match the native service port"},
		},
		{
			name: "eligible loopback endpoint -> in-place sync",
			writeConfig: func(t *testing.T, dir string) {
				writeRepairClientConfig(t, dir, "http://127.0.0.1:39273/mcp", []string{"claude"})
			},
			wantExit:       exitRepairOK,
			wantContains:   []string{"provider sync complete", "restart already-open provider sessions"},
			wantSyncCalled: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			saveRepairFns(t)
			dir := t.TempDir()
			version = "v1.2.3"
			repairEffectiveConfigFn = func(explicit string) (string, error) { return "/cfg.yaml", nil }
			repairServiceStatusFn = func(configPath string) (service.Status, error) {
				return service.Status{Installed: true, Running: true, HTTPAddr: serviceAddr}, nil
			}
			repairProbeHealthFn = func(addr string, timeout time.Duration) (service.HealthStatus, error) {
				return service.HealthStatus{Status: "ok", Version: "v1.2.3"}, nil
			}
			failIfReplaceCalled(t)
			repairTargetDirFn = func() (string, error) { return dir, nil }
			if tc.writeConfig != nil {
				tc.writeConfig(t, dir)
			}
			syncCalled := false
			repairRunSyncFn = func(d string, cfg *clientconfig.Config, opts syncOptions) (syncResult, error) {
				syncCalled = true
				return syncResult{Revision: "rev1"}, nil
			}

			out := withStdout(t, func() {
				if code := cmdUpgradeRepair(nil); code != tc.wantExit {
					t.Errorf("exit = %d, want %d", code, tc.wantExit)
				}
			})
			for _, want := range tc.wantContains {
				if !strings.Contains(out, want) {
					t.Errorf("output = %q, want it to contain %q", out, want)
				}
			}
			if syncCalled != tc.wantSyncCalled {
				t.Errorf("sync called = %v, want %v", syncCalled, tc.wantSyncCalled)
			}
		})
	}
}

// --- repairTargetDirFn failure -> pending sync (exit 1), not a service
// failure, since the service was already verified ---

func TestCmdUpgradeRepair_TargetDirError_ExitSyncPending(t *testing.T) {
	saveRepairFns(t)
	repairEffectiveConfigFn = func(explicit string) (string, error) { return "/cfg.yaml", nil }
	repairServiceStatusFn = func(configPath string) (service.Status, error) {
		return service.Status{Installed: true, Running: true, HTTPAddr: "127.0.0.1:39273"}, nil
	}
	repairProbeHealthFn = func(addr string, timeout time.Duration) (service.HealthStatus, error) {
		return service.HealthStatus{Status: "ok", Version: "v1.2.3"}, nil
	}
	version = "v1.2.3"
	failIfReplaceCalled(t)
	repairTargetDirFn = func() (string, error) { return "", errors.New("resolve home dir failed") }
	failIfSyncCalled(t)

	out := withStdout(t, func() {
		if code := cmdUpgradeRepair(nil); code != exitRepairSyncPending {
			t.Errorf("exit = %d, want %d", code, exitRepairSyncPending)
		}
	})
	if !strings.Contains(out, "cartographer sync") {
		t.Errorf("output = %q, want the retry hint", out)
	}
}

// --- runSync failure -> exit 1 with the underlying error and the exact
// retry command ---

func TestCmdUpgradeRepair_SyncFails_ExitSyncPending(t *testing.T) {
	saveRepairFns(t)
	dir := t.TempDir()
	version = "v1.2.3"
	repairEffectiveConfigFn = func(explicit string) (string, error) { return "/cfg.yaml", nil }
	repairServiceStatusFn = func(configPath string) (service.Status, error) {
		return service.Status{Installed: true, Running: true, HTTPAddr: "127.0.0.1:39273"}, nil
	}
	repairProbeHealthFn = func(addr string, timeout time.Duration) (service.HealthStatus, error) {
		return service.HealthStatus{Status: "ok", Version: "v1.2.3"}, nil
	}
	failIfReplaceCalled(t)
	repairTargetDirFn = func() (string, error) { return dir, nil }
	writeRepairClientConfig(t, dir, "http://127.0.0.1:39273/mcp", []string{"claude"})
	repairRunSyncFn = func(d string, cfg *clientconfig.Config, opts syncOptions) (syncResult, error) {
		return syncResult{}, errors.New("sync_pull: server unreachable")
	}

	out := withStdout(t, func() {
		if code := cmdUpgradeRepair(nil); code != exitRepairSyncPending {
			t.Errorf("exit = %d, want %d", code, exitRepairSyncPending)
		}
	})
	if !strings.Contains(out, "provider sync pending") || !strings.Contains(out, "cartographer sync") {
		t.Errorf("output = %q, want the pending-sync retry hint with the exact command", out)
	}
}

// --- loopback host equivalence, port normalization, and every rejection
// reason of upgradeRepairSyncEligible, tested directly as a pure function
// (the command-level table above already proves the wiring). ---

func TestUpgradeRepairSyncEligible(t *testing.T) {
	cases := []struct {
		name        string
		clientURL   string
		serviceAddr string
		wantOK      bool
	}{
		{"localhost equals 127.0.0.1", "http://localhost:39273/mcp", "127.0.0.1:39273", true},
		{"127.0.0.1 explicit", "http://127.0.0.1:39273/mcp", "127.0.0.1:39273", true},
		{"[::1] equals 127.0.0.1", "http://[::1]:39273/mcp", "127.0.0.1:39273", true},
		{"bare-port service addr", "http://127.0.0.1:39273/mcp", ":39273", true},
		{"omitted client port normalizes to 80", "http://127.0.0.1/mcp", "127.0.0.1:80", true},
		{"port mismatch", "http://127.0.0.1:9999/mcp", "127.0.0.1:39273", false},
		{"https rejected", "https://127.0.0.1:39273/mcp", "127.0.0.1:39273", false},
		{"embedded credentials rejected", "http://user:pass@127.0.0.1:39273/mcp", "127.0.0.1:39273", false},
		{"fragment rejected", "http://127.0.0.1:39273/mcp#x", "127.0.0.1:39273", false},
		{"non-loopback host rejected", "http://example.com:39273/mcp", "127.0.0.1:39273", false},
		{"malformed client URL rejected", "http://%zz", "127.0.0.1:39273", false},
		{"unusable service address rejected", "http://127.0.0.1:39273/mcp", "not-an-addr", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := upgradeRepairSyncEligible(tc.clientURL, tc.serviceAddr)
			if ok != tc.wantOK {
				t.Errorf("upgradeRepairSyncEligible(%q, %q) = (%v, %q), want ok=%v", tc.clientURL, tc.serviceAddr, ok, reason, tc.wantOK)
			}
			if !tc.wantOK && reason == "" {
				t.Error("expected a non-empty rejection reason")
			}
		})
	}
}

// --- Negative safety requirements: no path may call connect/disconnect,
// treat configurator removal as a disconnect, or delete the client config.
// ---

// TestCmdUpgradeRepair_SourceDoesNotReferenceConnectDisconnect statically
// enforces the D121 invariant ("no path calls disconnect, deletes
// user-owned configuration, requires a new connect") against the actual
// production file, since upgrade-repair's collaborators are narrow enough
// (service status/health/replace, client-config load, the shared sync
// runner) that a call to any connect/disconnect/removal machinery would be
// a mandate violation regardless of which state-matrix row exercises it.
func TestCmdUpgradeRepair_SourceDoesNotReferenceConnectDisconnect(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(".", "upgraderepair.go"))
	if err != nil {
		t.Fatalf("read upgraderepair.go: %v", err)
	}
	forbidden := []string{
		"cmdConnect", "cmdDisconnect",
		"doConnect(", "doDisconnect(",
		"configurator.Remove", "removeMCPEntries",
		"os.Remove", "RemoveAll",
	}
	for _, sym := range forbidden {
		if strings.Contains(string(src), sym) {
			t.Errorf("upgraderepair.go must not reference %q", sym)
		}
	}
}

// TestCmdUpgradeRepair_ClientConfigNeverDeleted runs upgrade-repair across
// several no-op/skip/error branches and asserts the client config file on
// disk is left untouched (still present, byte-identical) — upgrade-repair
// itself must never remove or rewrite it; only the real (here stubbed)
// sync runner may do so, under ordinary sync policy.
func TestCmdUpgradeRepair_ClientConfigNeverDeleted(t *testing.T) {
	branches := []struct {
		name       string
		serverURL  string
		agents     []string
		runSyncErr error
		wantExit   int
	}{
		{name: "non-loopback skip", serverURL: "http://cartographer.example:39273/mcp", agents: []string{"claude"}, wantExit: exitRepairOK},
		{name: "eligible sync succeeds", serverURL: "http://127.0.0.1:39273/mcp", agents: []string{"claude"}, wantExit: exitRepairOK},
		{name: "eligible sync fails", serverURL: "http://127.0.0.1:39273/mcp", agents: []string{"claude"}, runSyncErr: errors.New("boom"), wantExit: exitRepairSyncPending},
	}

	for _, tc := range branches {
		t.Run(tc.name, func(t *testing.T) {
			saveRepairFns(t)
			dir := t.TempDir()
			version = "v1.2.3"
			repairEffectiveConfigFn = func(explicit string) (string, error) { return "/cfg.yaml", nil }
			repairServiceStatusFn = func(configPath string) (service.Status, error) {
				return service.Status{Installed: true, Running: true, HTTPAddr: "127.0.0.1:39273"}, nil
			}
			repairProbeHealthFn = func(addr string, timeout time.Duration) (service.HealthStatus, error) {
				return service.HealthStatus{Status: "ok", Version: "v1.2.3"}, nil
			}
			failIfReplaceCalled(t)
			repairTargetDirFn = func() (string, error) { return dir, nil }
			writeRepairClientConfig(t, dir, tc.serverURL, tc.agents)

			before, err := os.ReadFile(clientconfig.Path(dir))
			if err != nil {
				t.Fatalf("read config before: %v", err)
			}

			repairRunSyncFn = func(d string, cfg *clientconfig.Config, opts syncOptions) (syncResult, error) {
				if tc.runSyncErr != nil {
					return syncResult{}, tc.runSyncErr
				}
				return syncResult{Revision: "rev1"}, nil
			}

			withStdout(t, func() {
				if code := cmdUpgradeRepair(nil); code != tc.wantExit {
					t.Errorf("exit = %d, want %d", code, tc.wantExit)
				}
			})

			after, err := os.ReadFile(clientconfig.Path(dir))
			if err != nil {
				t.Fatalf("client config was removed: %v", err)
			}
			if string(after) != string(before) {
				t.Errorf("client config changed:\nbefore=%s\nafter=%s", before, after)
			}
		})
	}
}
