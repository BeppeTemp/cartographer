package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/service"
	"github.com/BeppeTemp/cartographer/internal/updatecheck"
)

// TestMain keeps every test in this package away from GitHub, the real user
// cache directory and a real package manager (D254): the update check is
// stubbed to "nothing known", the cache lives in a temporary directory, and
// the opt-out is set as a second line of defence for anything that reaches
// the real updatecheck.Check.
func TestMain(m *testing.M) {
	gitxtest.QuietGit() // a detached git gc would break t.TempDir cleanup
	os.Setenv(updatecheck.EnvDisable, "1")
	cache, err := os.MkdirTemp("", "cartographer-update-cache-")
	if err != nil {
		panic(err)
	}
	updateCacheDirFn = func() (string, error) { return cache, nil }
	updateCheckFn = func(_ context.Context, _ updatecheck.Options) (updatecheck.Result, error) {
		return updatecheck.Result{Current: version}, nil
	}
	updateSettingsFn = func() (bool, error) { return true, nil }
	serverUpdateCheckFn = func(context.Context, string, updatecheck.Options) (updatecheck.Result, error) {
		return updatecheck.Result{}, nil
	}
	// No test may touch a real launchd/systemd/Task Scheduler: connect, setup
	// and disconnect now install and remove the sync timer on their own (D325).
	syncTimerInstallFn = func(time.Duration) error { return nil }
	syncTimerUninstallFn = func() error { return nil }
	syncTimerStatusFn = func() (service.SyncTimerStatus, error) { return service.SyncTimerStatus{}, nil }
	// Same for the leftover doctor job connect and upgrade-repair remove (D704).
	removeLegacyDoctorTimerFn = func() (bool, error) { return false, nil }
	// Nor the developer's global git identity, which connect and sync would
	// write into HTTP entries (D904).
	globalIdentFn = func() (string, bool) { return "", false }
	code := m.Run()
	os.RemoveAll(cache)
	os.Exit(code)
}

// stubUpdate installs a check answer, a channel, settings and a private cache
// dir for one test.
func stubUpdate(t *testing.T, res updatecheck.Result, ch updatecheck.Channel, enabled bool) (cacheDir string, calls *[]updatecheck.Options) {
	t.Helper()
	cacheDir = t.TempDir()
	var seen []updatecheck.Options
	oldDir, oldCheck, oldCh, oldSettings := updateCacheDirFn, updateCheckFn, updateChannelFn, updateSettingsFn
	updateCacheDirFn = func() (string, error) { return cacheDir, nil }
	updateCheckFn = func(_ context.Context, o updatecheck.Options) (updatecheck.Result, error) {
		seen = append(seen, o)
		return res, nil
	}
	updateChannelFn = func() updatecheck.Channel { return ch }
	updateSettingsFn = func() (bool, error) { return enabled, nil }
	t.Cleanup(func() {
		updateCacheDirFn, updateCheckFn, updateChannelFn, updateSettingsFn = oldDir, oldCheck, oldCh, oldSettings
	})
	return cacheDir, &seen
}

var available = updatecheck.Result{Current: "v0.16.1", Latest: "v0.17.0", Available: true, Kind: updatecheck.KindMinor}

func TestUpdateNoticeSilentWhenUpToDate(t *testing.T) {
	stubUpdate(t, updatecheck.Result{Current: "v0.17.0", Latest: "v0.17.0"}, updatecheck.ChannelHomebrew, true)
	out := withStdout(t, func() {
		if code := cmdUpdate([]string{"notice"}); code != 0 {
			t.Errorf("exit %d", code)
		}
	})
	if out != "" {
		t.Fatalf("an up-to-date notice must print nothing, got %q", out)
	}
}

func TestUpdateNoticeOneParagraphWhenBehind(t *testing.T) {
	_, calls := stubUpdate(t, available, updatecheck.ChannelHomebrew, true)
	out := withStdout(t, func() {
		if code := cmdUpdate([]string{"notice"}); code != 0 {
			t.Errorf("exit %d", code)
		}
	})
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("want exactly one paragraph, got %q", out)
	}
	for _, want := range []string{
		"Cartographer update available: v0.17.0 (installed v0.16.1, minor).",
		"Upgrade with: brew upgrade --cask beppetemp/tap/cartographer.",
		"Tell the user once in this session", "run it only if they agree", "cartographer-ops skill (§Upgrade)",
		"releases/tag/v0.17.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("notice missing %q:\n%s", want, out)
		}
	}
	if len(*calls) != 1 || (*calls)[0].Force || (*calls)[0].CacheOnly {
		t.Errorf("the notice answers from a fresh cache and refreshes a stale one: %+v", *calls)
	}
}

func TestUpdateNoticeWithoutACommand(t *testing.T) {
	for ch, want := range map[updatecheck.Channel]string{
		updatecheck.ChannelContainer: "image tag",
		updatecheck.ChannelUnknown:   updatecheck.ReleasesURL,
	} {
		stubUpdate(t, available, ch, true)
		out := withStdout(t, func() { cmdUpdate([]string{"notice"}) })
		if !strings.Contains(out, want) || strings.Contains(out, "Upgrade with:") {
			t.Errorf("%s notice: %q", ch, out)
		}
	}
}

func TestUpdateNoticeAlwaysExitsZero(t *testing.T) {
	stubUpdate(t, available, updatecheck.ChannelHomebrew, true)
	updateChannelFn = func() updatecheck.Channel { panic("boom") }
	withStdout(t, func() {
		if code := cmdUpdate([]string{"notice"}); code != 0 {
			t.Errorf("a hook command must exit 0 even on an internal error, got %d", code)
		}
	})
}

func TestUpdateCheckJSONShapeAndForcedRefresh(t *testing.T) {
	_, calls := stubUpdate(t, available, updatecheck.ChannelInstallSh, true)
	out := withStdout(t, func() {
		if code := cmdUpdate([]string{"check", "--output", "json"}); code != 0 {
			t.Errorf("exit %d", code)
		}
	})
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v: %q", err, out)
	}
	var keys []string
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if want := []string{"available", "channel", "command", "current", "kind", "latest"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
	if got["channel"] != "install.sh" || !strings.HasSuffix(got["command"].(string), "sh -s -- update") || got["available"] != true {
		t.Errorf("json = %v", got)
	}
	if len(*calls) != 1 || !(*calls)[0].Force {
		t.Errorf("update check must force a refresh: %+v", *calls)
	}
	if code := cmdUpdate([]string{"check", "--output", "yaml"}); code != 2 {
		t.Errorf("usage error exit = %d, want 2", code)
	}
	if code := cmdUpdate([]string{"bogus"}); code != 2 {
		t.Errorf("unknown subcommand exit = %d, want 2", code)
	}
}

func TestUpdateCheckHonoursTheOptOut(t *testing.T) {
	_, calls := stubUpdate(t, available, updatecheck.ChannelHomebrew, false)
	withStdout(t, func() { cmdUpdate([]string{"check"}) })
	if len(*calls) != 1 || !(*calls)[0].Disabled {
		t.Errorf("update_check: false must reach the check even when forced: %+v", *calls)
	}
}

// D906: an existing `update.policy: auto-patch` is read as update_check and
// nothing else. The notice for a patch release is the manual one, no process is
// started, and the internal `update apply` command is gone.
func TestAutoPatchNeverSelfReplaces(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.WriteFile(clientconfig.Path(home), []byte("server_url: http://x/mcp\nupdate:\n  policy: auto-patch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	enabled, err := updateCheckEnabled()
	if err != nil || !enabled {
		t.Fatalf("update_check from a file with only update.policy = %v, %v, want true", enabled, err)
	}
	patch := updatecheck.Result{Current: "v0.16.1", Latest: "v0.16.2", Available: true, Kind: updatecheck.KindPatch}
	dir, _ := stubUpdate(t, patch, updatecheck.ChannelHomebrew, enabled)
	out := withStdout(t, func() { cmdUpdate([]string{"notice"}) })
	if !strings.Contains(out, "Upgrade with: brew upgrade --cask beppetemp/tap/cartographer.") {
		t.Errorf("a patch release gets the plain notice: %q", out)
	}
	withStderr(t, func() {
		if code := cmdUpdate([]string{"apply"}); code != 2 {
			t.Errorf("update apply must be gone, exit = %d", code)
		}
	})
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("nothing may be written to the update cache by a notice: %v", entries)
	}
}

// WP3: the snapshot carries `update` only when one is available, read from
// the cache only; the status line and the TUI follow it; exit codes do not.
func TestStatusSnapshotCarriesUpdateOnlyWhenAvailable(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	cfg := clientconfig.Default()
	cfg.ServerURL = "https://remote.example/mcp"
	cfg.Agents = []string{"claude"}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	oldVersion, oldHealth, oldManifest, oldService := version, statusHealthFn, statusManifestsFn, statusServiceFn
	version = "v0.16.1"
	statusHealthFn = func(*clientconfig.Config) (*client.Health, error) {
		return &client.Health{Version: "v0.16.1", LatestVersion: "v0.17.0"}, nil
	}
	statusManifestsFn = func(_ *clientconfig.Config, ps []syncProjection) (map[string]provisioning.Manifest, error) {
		return uniformManifests(provisioning.Manifest{Revision: "r1"}, projectionProviders(ps)), nil
	}
	statusServiceFn = func() (service.Status, error) { return service.Status{}, nil }
	t.Cleanup(func() {
		version, statusHealthFn, statusManifestsFn, statusServiceFn = oldVersion, oldHealth, oldManifest, oldService
	})

	_, calls := stubUpdate(t, updatecheck.Result{Current: "v0.16.1", Latest: "v0.16.1"}, updatecheck.ChannelHomebrew, true)
	s := snapshotForConfig(home, cfg, false)
	if s.Update != nil {
		t.Fatalf("up to date: update = %+v", s.Update)
	}
	if len(*calls) != 1 || !(*calls)[0].CacheOnly {
		t.Errorf("status must read the cache only: %+v", *calls)
	}
	if s.ServerLatest != "v0.17.0" {
		t.Errorf("a remote server's latest_version = %q", s.ServerLatest)
	}
	data, _ := json.Marshal(s)
	if strings.Contains(string(data), `"update"`) {
		t.Errorf("no update key when none is available: %s", data)
	}

	stubUpdate(t, available, updatecheck.ChannelHomebrew, true)
	s = snapshotForConfig(home, cfg, false)
	want := &updateSnapshot{Latest: "v0.17.0", Kind: "minor", Channel: "homebrew", Command: "brew upgrade --cask beppetemp/tap/cartographer"}
	if !reflect.DeepEqual(s.Update, want) {
		t.Fatalf("update = %+v", s.Update)
	}
	if s.Schema != statusSchema {
		t.Errorf("schema changed: %s", s.Schema)
	}
	out := withStdout(t, func() {
		if code := renderStatus("table", s, 0); code != 0 {
			t.Errorf("an update must not change the exit code, got %d", code)
		}
	})
	for _, line := range []string{
		"update available: v0.17.0 (installed v0.16.1) — brew upgrade --cask beppetemp/tap/cartographer",
		"server update available: v0.17.0",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("status missing %q:\n%s", line, out)
		}
	}

	// A loopback server is this machine's own binary: no server line.
	cfg.ServerURL = "http://127.0.0.1:39273/mcp"
	if s := snapshotForConfig(home, cfg, false); s.ServerLatest != "" {
		t.Errorf("loopback server reported ServerLatest %q", s.ServerLatest)
	}
}

func TestDoctorReportsUpdateAsInfo(t *testing.T) {
	stubUpdate(t, available, updatecheck.ChannelInstallPS1, true)
	got := checkUpdateAvailable()
	if len(got) != 1 || got[0].Check != "update_available" || got[0].Severity != doctorInfo || !strings.Contains(got[0].Fix, "install.ps1))) update") {
		t.Fatalf("finding = %+v", got)
	}
	if doctorExitCode(doctorReport{Infos: 1}) != 0 {
		t.Error("an info finding must not fail doctor")
	}
	stubUpdate(t, updatecheck.Result{Current: "v1", Latest: "v1"}, updatecheck.ChannelInstallPS1, true)
	if got := checkUpdateAvailable(); len(got) != 0 {
		t.Errorf("up to date: %+v", got)
	}
}

func TestViewServerPanelShowsAnAvailableUpdate(t *testing.T) {
	m := testModel()
	m.width = 100
	m.snapshot = &statusSnapshot{Schema: statusSchema, ServerURL: "http://h/mcp", Reachable: true, Client: "v0.16.1", Server: "v0.16.1", State: "in_sync"}
	if out := ansiRE.ReplaceAllString(m.viewServerPanel(), ""); strings.Contains(out, "available") {
		t.Errorf("no update, no suffix:\n%s", out)
	}
	m.snapshot.Update = &updateSnapshot{Latest: "v0.17.0", Kind: "minor", Channel: "homebrew"}
	raw := m.viewServerPanel()
	if !strings.Contains(ansiRE.ReplaceAllString(raw, ""), "version    client v0.16.1 · server v0.16.1 · v0.17.0 available") {
		t.Errorf("missing the update suffix:\n%s", raw)
	}
	if !strings.Contains(raw, styleDrift.Render(" · v0.17.0 available")) {
		t.Errorf("the suffix must use the drift style:\n%q", raw)
	}
}

// WP4: a dev build never starts the server checker; a release build does,
// and exposes what it found.
func TestServerUpdateCheck(t *testing.T) {
	var calls int
	oldFn, oldDelay := serverUpdateCheckFn, serverUpdateDelay
	serverUpdateCheckFn = func(_ context.Context, current string, _ updatecheck.Options) (updatecheck.Result, error) {
		calls++
		return updatecheck.Result{Current: current, Latest: "v0.17.0", Available: true, Kind: "minor"}, nil
	}
	serverUpdateDelay = 0
	t.Cleanup(func() { serverUpdateCheckFn, serverUpdateDelay = oldFn, oldDelay })
	t.Setenv(updatecheck.EnvDisable, "")

	cfg := config.Default()
	if src := startServerUpdateCheck(cfg, "dev"); src != nil {
		t.Fatal("a dev server must never start the checker")
	}
	cfg.UpdateCheck = false
	if src := startServerUpdateCheck(cfg, "v0.16.1"); src != nil {
		t.Fatal("update_check: false must not start the checker")
	}
	if calls != 0 {
		t.Fatalf("checker ran %d time(s)", calls)
	}
	cfg.UpdateCheck = true
	cfg.Data = t.TempDir()
	src := startServerUpdateCheck(cfg, "v0.16.1")
	if src == nil {
		t.Fatal("a release build with update_check on starts the checker")
	}
	deadline := time.Now().Add(5 * time.Second)
	for src() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if src() != "v0.17.0" {
		t.Fatalf("latest = %q", src())
	}
	if got := serverUpdateCacheFile(cfg); got != filepath.Join(cfg.Data, ".cartographer", updatecheck.CacheFileName) {
		t.Errorf("server cache = %s", got)
	}
}

// WP2 trap, on the path `sync` takes: an already-connected client whose
// bootstrap script predates the notice gets the new script from the next
// sync, without a reconnect (ensureBootstrapForProviders runs on every
// runSync, and EnsureBootstrapHook rewrites unconditionally).
func TestSyncBootstrapRewritesAnOutdatedScript(t *testing.T) {
	dir := t.TempDir()
	if err := ensureBootstrapForProviders([]string{string(configurator.ProviderClaudeCode)}, dir, false); err != nil {
		t.Fatal(err)
	}
	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var script string
	for _, mf := range lf.ForProvider(string(configurator.ProviderClaudeCode)).Managed {
		if mf.Name == provisioning.BootstrapHookName && strings.HasPrefix(filepath.Base(mf.Path), "bootstrap.") {
			script = filepath.Join(dir, mf.Path)
		}
	}
	if script == "" {
		t.Fatal("no bootstrap script recorded")
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncartographer sync --auto-trust >/dev/null 2>&1 || true\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureBootstrapForProviders([]string{string(configurator.ProviderClaudeCode)}, dir, false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(script)
	if !strings.Contains(string(got), "cartographer update notice") {
		t.Fatalf("the next sync left the outdated script in place:\n%s", got)
	}
}

func TestClientUpdateSetsCheck(t *testing.T) {
	home := writeClientCfg(t, []string{"claude"}, nil, nil)
	out := withStdout(t, func() {
		if code := cmdClient([]string{"update", "--check=false"}); code != 0 {
			t.Errorf("exit %d", code)
		}
	})
	if !strings.Contains(out, "update check   false") || strings.Contains(out, "policy") {
		t.Errorf("output: %q", out)
	}
	if cfg := loadClientCfg(t, home); cfg.UpdateCheckEnabled() {
		t.Errorf("saved: update check still on")
	}
	withStderr(t, func() {
		if code := cmdClient([]string{"update", "--policy", "auto-patch"}); code != 2 {
			t.Errorf("--policy is gone, exit = %d", code)
		}
	})
}

// #415: a failed check retries after serverUpdateRetry, not after the full
// interval, so a server started while GitHub was unreachable reports the newer
// release once it is reachable again.
func TestServerUpdateCheckRetriesSoonAfterAFailure(t *testing.T) {
	var calls atomic.Int32
	oldFn, oldDelay, oldInterval, oldRetry := serverUpdateCheckFn, serverUpdateDelay, serverUpdateInterval, serverUpdateRetry
	serverUpdateCheckFn = func(_ context.Context, current string, _ updatecheck.Options) (updatecheck.Result, error) {
		if calls.Add(1) == 1 {
			return updatecheck.Result{Current: current}, errors.New("rate limited")
		}
		return updatecheck.Result{Current: current, Latest: "v0.17.0", Available: true, Kind: "minor"}, nil
	}
	serverUpdateDelay, serverUpdateInterval, serverUpdateRetry = 0, time.Hour, 10*time.Millisecond
	t.Cleanup(func() {
		serverUpdateCheckFn, serverUpdateDelay, serverUpdateInterval, serverUpdateRetry = oldFn, oldDelay, oldInterval, oldRetry
	})
	t.Setenv(updatecheck.EnvDisable, "")

	cfg := config.Default()
	cfg.UpdateCheck = true
	cfg.Data = t.TempDir()
	src := startServerUpdateCheck(cfg, "v0.16.1")
	if src == nil {
		t.Fatal("checker not started")
	}
	deadline := time.Now().Add(5 * time.Second)
	for src() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if src() != "v0.17.0" {
		t.Fatalf("latest = %q after a failed first check: the retry waited the full interval", src())
	}
}

// D906: the write-findings hook is always installed, and the `write_findings_hook`
// key of an older file switches nothing off.
func TestEnsureBootstrapForProviders_WriteFindingsAlwaysInstalled(t *testing.T) {
	dir := t.TempDir()
	claude := []string{string(configurator.ProviderClaudeCode)}
	hookDir := filepath.Join(dir, ".claude", "hooks", provisioning.WriteFindingsHookName)
	if err := os.WriteFile(clientconfig.Path(dir), []byte("server_url: http://x/mcp\nwrite_findings_hook: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ensureBootstrapForProviders(claude, dir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(hookDir); err != nil {
		t.Fatalf("hook not installed: %v", err)
	}
	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var boot, findings bool
	for _, mf := range lf.ForProvider(claude[0]).Managed {
		findings = findings || mf.Name == provisioning.WriteFindingsHookName
		boot = boot || mf.Name == provisioning.BootstrapHookName
	}
	if !boot || !findings {
		t.Errorf("both client hooks must be recorded: bootstrap %v findings %v", boot, findings)
	}
}
