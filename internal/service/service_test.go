package service

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/defaults"
)

func TestRenderLaunchdPlist(t *testing.T) {
	out := RenderLaunchdPlist("/usr/local/bin/cartographer", "/home/x/.config/cartographer/server.yaml", "/home/x/Library/Logs/cartographer/server.log")
	for _, want := range []string{
		"<key>Label</key>",
		"<string>com.cartographer.serve</string>",
		"<string>/usr/local/bin/cartographer</string>",
		"<string>serve</string>",
		"<string>--config</string>",
		"<string>/home/x/.config/cartographer/server.yaml</string>",
		"<key>RunAtLoad</key>",
		"<true/>",
		"<key>KeepAlive</key>",
		"<key>StandardOutPath</key>",
		"<key>StandardErrorPath</key>",
		"<string>/home/x/Library/Logs/cartographer/server.log</string>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plist missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderSystemdUnit(t *testing.T) {
	out := RenderSystemdUnit("/usr/local/bin/cartographer", "/home/x/.config/cartographer/server.yaml")
	for _, want := range []string{
		"[Unit]",
		"Description=Cartographer MCP server",
		"[Service]",
		"ExecStart=/usr/local/bin/cartographer serve --config /home/x/.config/cartographer/server.yaml",
		"Restart=on-failure",
		"[Install]",
		"WantedBy=default.target",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("unit missing %q\n---\n%s", want, out)
		}
	}
}

func TestDefaultServerYAML(t *testing.T) {
	out := DefaultServerYAML("/home/x/cartographer-data", defaults.DefaultListenAddress)
	for _, want := range []string{
		`http: "127.0.0.1:39273"`,
		`data: "/home/x/cartographer-data"`,
		"init: true",
		"cartographer service install",
		"config.example.yaml",
		"git:",
		"# author_name:",
		"# author_email:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("yaml missing %q\n---\n%s", want, out)
		}
	}
	path := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load generated YAML: %v", err)
	}
	if cfg.HTTP != defaults.DefaultListenAddress || cfg.Data != "/home/x/cartographer-data" || !cfg.Init {
		t.Fatalf("loaded generated YAML = %+v", cfg)
	}
}

// withTestHome redirects userHomeDir/goos for the duration of the test.
func withTestHome(t *testing.T, os_ string) string {
	t.Helper()
	home := t.TempDir()
	origHome, origGOOS := userHomeDir, goos
	userHomeDir = func() (string, error) { return home, nil }
	goos = os_
	t.Cleanup(func() { userHomeDir, goos = origHome, origGOOS })
	return home
}

func TestConfigPath(t *testing.T) {
	home := withTestHome(t, "darwin")
	got, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	want := filepath.Join(home, ".config", "cartographer", "server.yaml")
	if got != want {
		t.Errorf("ConfigPath() = %q, want %q", got, want)
	}
}

func TestLaunchdPaths(t *testing.T) {
	home := withTestHome(t, "darwin")
	plist, err := LaunchdPlistPath()
	if err != nil {
		t.Fatalf("LaunchdPlistPath: %v", err)
	}
	if want := filepath.Join(home, "Library", "LaunchAgents", "com.cartographer.serve.plist"); plist != want {
		t.Errorf("LaunchdPlistPath() = %q, want %q", plist, want)
	}
	logPath, err := LaunchdLogPath()
	if err != nil {
		t.Fatalf("LaunchdLogPath: %v", err)
	}
	if want := filepath.Join(home, "Library", "Logs", "cartographer", "server.log"); logPath != want {
		t.Errorf("LaunchdLogPath() = %q, want %q", logPath, want)
	}
}

func TestSystemdUnitPath(t *testing.T) {
	home := withTestHome(t, "linux")
	got, err := SystemdUnitPath()
	if err != nil {
		t.Fatalf("SystemdUnitPath: %v", err)
	}
	want := filepath.Join(home, ".config", "systemd", "user", "cartographer.service")
	if got != want {
		t.Errorf("SystemdUnitPath() = %q, want %q", got, want)
	}
}

// stubRunner records every invocation and returns "" with a nil error,
// simulating a successful launchctl/systemctl call.
type stubRunner struct {
	calls [][]string
	fail  map[string]bool // command name (joined with " ") -> force error
}

func (s *stubRunner) run(name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	s.calls = append(s.calls, call)
	key := strings.Join(call, " ")
	for pat := range s.fail {
		if strings.Contains(key, pat) {
			return "", errNotLoaded
		}
	}
	return "", nil
}

var errNotLoaded = &stubError{"not loaded"}

type stubError struct{ msg string }

func (e *stubError) Error() string { return e.msg }

func newTestManager() (*Manager, *stubRunner) {
	s := &stubRunner{}
	return &Manager{run: s.run}, s
}

func TestInstall_GeneratesConfigAndPlist_Darwin(t *testing.T) {
	home := withTestHome(t, "darwin")
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "cartographer")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	origExecutable := osExecutable
	osExecutable = func() (string, error) { return binPath, nil }
	t.Cleanup(func() { osExecutable = origExecutable })

	dataDir := filepath.Join(home, "cartographer-data")
	m, stub := newTestManager()
	warnings, err := m.Install(InstallOptions{DataDir: dataDir, HTTPAddr: "127.0.0.1:39273"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings on fresh install: %v", warnings)
	}

	configPath := filepath.Join(home, ".config", "cartographer", "server.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(data), `data: `+strconv.Quote(dataDir)) {
		t.Errorf("config content = %q, want data: %s", data, strconv.Quote(dataDir))
	}
	if _, err := os.Stat(dataDir); err != nil {
		t.Errorf("data dir not created: %v", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.cartographer.serve.plist")
	if _, err := os.Stat(plistPath); err != nil {
		t.Fatalf("plist not written: %v", err)
	}

	var gotBootstrap bool
	for _, c := range stub.calls {
		if len(c) > 1 && c[0] == "launchctl" && c[1] == "bootstrap" {
			gotBootstrap = true
		}
	}
	if !gotBootstrap {
		t.Errorf("expected launchctl bootstrap call, got calls: %v", stub.calls)
	}
}

// TestInstall_PrefersStableCaskroomSymlink covers the D83 fix: the plist
// must record the stable Homebrew symlink, not the versioned Caskroom path
// os.Executable() resolves to (which brew upgrade removes on every version
// bump, breaking the service until `service install` is re-run).
func TestInstall_PrefersStableCaskroomSymlink(t *testing.T) {
	home := withTestHome(t, "darwin")

	// Fake Caskroom layout: the real binary lives under a versioned dir,
	// and a stable symlink (mirroring /opt/homebrew/bin/cartographer)
	// points at it.
	caskDir := filepath.Join(home, "Caskroom", "cartographer", "0.1.0")
	if err := os.MkdirAll(caskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realBin := filepath.Join(caskDir, "cartographer")
	if err := os.WriteFile(realBin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	stableSymlink := filepath.Join(home, "bin", "cartographer")
	if err := os.MkdirAll(filepath.Dir(stableSymlink), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realBin, stableSymlink); err != nil {
		t.Fatal(err)
	}

	origSymlinks := stableBinSymlinks
	stableBinSymlinks = []string{stableSymlink}
	t.Cleanup(func() { stableBinSymlinks = origSymlinks })

	origExecutable := osExecutable
	// os.Executable() as invoked returns the versioned Caskroom path.
	osExecutable = func() (string, error) { return realBin, nil }
	t.Cleanup(func() { osExecutable = origExecutable })

	m, _ := newTestManager()
	if _, err := m.Install(InstallOptions{DataDir: filepath.Join(home, "data"), HTTPAddr: "127.0.0.1:39273"}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.cartographer.serve.plist")
	plist, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("plist not written: %v", err)
	}
	if !strings.Contains(string(plist), stableSymlink) {
		t.Errorf("plist = %q, want it to contain the stable symlink %q", plist, stableSymlink)
	}
	if strings.Contains(string(plist), realBin) {
		t.Errorf("plist = %q, must not contain the versioned Caskroom path %q", plist, realBin)
	}
}

func TestInstall_ExistingConfigNotOverwritten_WarnsOnExplicitFlags(t *testing.T) {
	home := withTestHome(t, "darwin")
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "cartographer")
	os.WriteFile(binPath, []byte("x"), 0o755)
	origExecutable := osExecutable
	osExecutable = func() (string, error) { return binPath, nil }
	t.Cleanup(func() { osExecutable = origExecutable })

	configPath := filepath.Join(home, ".config", "cartographer", "server.yaml")
	os.MkdirAll(filepath.Dir(configPath), 0o755)
	customDataDir := filepath.Join(home, "custom")
	// A generated service must never migrate this persisted pre-D112 endpoint.
	existing := "http: \"127.0.0.1:8080\"\ndata: " + customDataDir + "\n"
	if err := os.WriteFile(configPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := newTestManager()
	warnings, err := m.Install(InstallOptions{DataDir: filepath.Join(home, "data"), HTTPAddr: defaults.DefaultListenAddress, DataExplicit: true, HTTPExplicit: true})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(warnings) == 0 {
		t.Error("expected a warning when --data is explicit but config already exists")
	}

	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != existing {
		t.Errorf("existing config was modified: %q", got)
	}
	if _, err := os.Stat(customDataDir); err != nil {
		t.Errorf("existing config's data dir not created: %v", err)
	}
}

func TestInstall_Idempotent(t *testing.T) {
	home := withTestHome(t, "darwin")
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "cartographer")
	os.WriteFile(binPath, []byte("x"), 0o755)
	origExecutable := osExecutable
	osExecutable = func() (string, error) { return binPath, nil }
	t.Cleanup(func() { osExecutable = origExecutable })

	m, _ := newTestManager()
	opts := InstallOptions{DataDir: filepath.Join(home, "data"), HTTPAddr: "127.0.0.1:39273"}
	if _, err := m.Install(opts); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	if _, err := m.Install(opts); err != nil {
		t.Fatalf("second Install: %v", err)
	}
}

func TestUninstall_RemovesPlist(t *testing.T) {
	home := withTestHome(t, "darwin")
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.cartographer.serve.plist")
	os.MkdirAll(filepath.Dir(plistPath), 0o755)
	os.WriteFile(plistPath, []byte("<plist/>"), 0o644)

	m, stub := newTestManager()
	if err := m.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Errorf("plist still present after uninstall")
	}
	var gotBootout bool
	for _, c := range stub.calls {
		if len(c) > 1 && c[0] == "launchctl" && c[1] == "bootout" {
			gotBootout = true
		}
	}
	if !gotBootout {
		t.Errorf("expected launchctl bootout call, got %v", stub.calls)
	}
}

func TestUninstall_ConfigNotTouched(t *testing.T) {
	home := withTestHome(t, "darwin")
	configPath := filepath.Join(home, ".config", "cartographer", "server.yaml")
	os.MkdirAll(filepath.Dir(configPath), 0o755)
	os.WriteFile(configPath, []byte("data: /data\n"), 0o644)

	m, _ := newTestManager()
	if err := m.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("config was removed by Uninstall: %v", err)
	}
}

func TestStatus_NotInstalled(t *testing.T) {
	withTestHome(t, "darwin")
	m, stub := newTestManager()
	stub.fail = map[string]bool{"launchctl print": true}

	st, err := m.Status("")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Installed {
		t.Error("Installed should be false with no plist on disk")
	}
	if st.Running {
		t.Error("Running should be false when launchctl print fails")
	}
}

func TestStatus_InstalledAndRunning(t *testing.T) {
	home := withTestHome(t, "darwin")
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.cartographer.serve.plist")
	os.MkdirAll(filepath.Dir(plistPath), 0o755)
	os.WriteFile(plistPath, []byte("<plist/>"), 0o644)

	configPath := filepath.Join(home, ".config", "cartographer", "server.yaml")
	os.MkdirAll(filepath.Dir(configPath), 0o755)
	// A service installed before D112 may still listen on 8080; status reports
	// the persisted value rather than substituting the new default.
	os.WriteFile(configPath, []byte("http: \"127.0.0.1:8080\"\n"), 0o644)

	m, _ := newTestManager() // stub run() succeeds unconditionally
	st, err := m.Status(configPath)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed {
		t.Error("Installed should be true, plist is on disk")
	}
	if !st.Running {
		t.Error("Running should be true, stub run() succeeds")
	}
	if st.HTTPAddr != "127.0.0.1:8080" {
		t.Errorf("HTTPAddr = %q, want preserved 127.0.0.1:8080", st.HTTPAddr)
	}
}

func TestHealthURL(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		":39273":            "http://127.0.0.1:39273/health",
		"127.0.0.1:39273":   "http://127.0.0.1:39273/health",
		"0.0.0.0:9090":      "http://0.0.0.0:9090/health",
		"not-a-valid-value": "",
	}
	for addr, want := range cases {
		if got := healthURL(addr); got != want {
			t.Errorf("healthURL(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestUnsupportedPlatform(t *testing.T) {
	withTestHome(t, "windows")
	m, _ := newTestManager()
	if _, err := m.Install(InstallOptions{}); err == nil {
		t.Error("Install on unsupported platform should error")
	}
	if err := m.Uninstall(); err == nil {
		t.Error("Uninstall on unsupported platform should error")
	}
	if err := m.Start(); err == nil {
		t.Error("Start on unsupported platform should error")
	}
	if err := m.Stop(); err == nil {
		t.Error("Stop on unsupported platform should error")
	}
	if err := m.Restart(); err == nil {
		t.Error("Restart on unsupported platform should error")
	}
	if _, err := m.Status(""); err == nil {
		t.Error("Status on unsupported platform should error")
	}
}

// --- WP1: EffectiveConfigPath, Replace ---------------------------------

func TestEffectiveConfigPath_ExplicitOverrideWins(t *testing.T) {
	withTestHome(t, "darwin")
	m, _ := newTestManager()
	got, err := m.EffectiveConfigPath("/explicit/server.yaml")
	if err != nil {
		t.Fatalf("EffectiveConfigPath: %v", err)
	}
	if got != "/explicit/server.yaml" {
		t.Errorf("EffectiveConfigPath = %q, want the explicit override", got)
	}
}

func TestEffectiveConfigPath_NoInstalledDefinitionUsesStandardPath(t *testing.T) {
	home := withTestHome(t, "darwin")
	m, _ := newTestManager()
	got, err := m.EffectiveConfigPath("")
	if err != nil {
		t.Fatalf("EffectiveConfigPath: %v", err)
	}
	want := filepath.Join(home, ".config", "cartographer", "server.yaml")
	if got != want {
		t.Errorf("EffectiveConfigPath = %q, want standard path %q", got, want)
	}
}

func TestEffectiveConfigPath_DiscoversGeneratedDarwinPlist(t *testing.T) {
	home := withTestHome(t, "darwin")
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.cartographer.serve.plist")
	os.MkdirAll(filepath.Dir(plistPath), 0o755)
	custom := filepath.Join(home, "custom", "server.yaml")
	plist := RenderLaunchdPlist("/usr/local/bin/cartographer", custom, filepath.Join(home, "log"))
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := newTestManager()
	got, err := m.EffectiveConfigPath("")
	if err != nil {
		t.Fatalf("EffectiveConfigPath: %v", err)
	}
	if got != custom {
		t.Errorf("EffectiveConfigPath = %q, want the plist's --config argument %q", got, custom)
	}
}

func TestEffectiveConfigPath_DiscoversGeneratedLinuxUnit(t *testing.T) {
	home := withTestHome(t, "linux")
	unitPath := filepath.Join(home, ".config", "systemd", "user", "cartographer.service")
	os.MkdirAll(filepath.Dir(unitPath), 0o755)
	custom := filepath.Join(home, "custom", "server.yaml")
	unit := RenderSystemdUnit("/usr/local/bin/cartographer", custom)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := newTestManager()
	got, err := m.EffectiveConfigPath("")
	if err != nil {
		t.Fatalf("EffectiveConfigPath: %v", err)
	}
	if got != custom {
		t.Errorf("EffectiveConfigPath = %q, want the unit's --config argument %q", got, custom)
	}
}

func TestEffectiveConfigPath_MalformedPlistFails(t *testing.T) {
	home := withTestHome(t, "darwin")
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.cartographer.serve.plist")
	os.MkdirAll(filepath.Dir(plistPath), 0o755)
	if err := os.WriteFile(plistPath, []byte("<plist><dict>not a real definition</dict></plist>"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := newTestManager()
	if _, err := m.EffectiveConfigPath(""); err == nil {
		t.Error("EffectiveConfigPath should fail on a malformed installed plist")
	}
}

func TestEffectiveConfigPath_MalformedUnitFails(t *testing.T) {
	home := withTestHome(t, "linux")
	unitPath := filepath.Join(home, ".config", "systemd", "user", "cartographer.service")
	os.MkdirAll(filepath.Dir(unitPath), 0o755)
	if err := os.WriteFile(unitPath, []byte("[Unit]\nDescription=broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := newTestManager()
	if _, err := m.EffectiveConfigPath(""); err == nil {
		t.Error("EffectiveConfigPath should fail on a malformed installed unit")
	}
}

// healthServer starts an httptest server whose /health handler returns
// statuses in sequence (repeating the last one once exhausted), and reports
// how many requests it received.
type healthServer struct {
	*httptest.Server
	requests int32
}

func newHealthServer(t *testing.T, responses ...func(w http.ResponseWriter)) *healthServer {
	t.Helper()
	hs := &healthServer{}
	var idx int32
	hs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hs.requests, 1)
		i := atomic.AddInt32(&idx, 1) - 1
		if int(i) >= len(responses) {
			i = int32(len(responses) - 1)
		}
		responses[i](w)
	}))
	t.Cleanup(hs.Close)
	return hs
}

func healthOK(version string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(HealthStatus{Status: "ok", Version: version})
	}
}

func healthNonOK() func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) }
}

func healthMalformed() func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not json"))
	}
}

// writeReplaceConfig writes a minimal config YAML pointing at srv's address
// and returns its path.
func writeReplaceConfig(t *testing.T, home, httpAddr string) string {
	t.Helper()
	configPath := filepath.Join(home, ".config", "cartographer", "server.yaml")
	os.MkdirAll(filepath.Dir(configPath), 0o755)
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("http: %q\n", httpAddr)), 0o644); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func serverAddr(srv *httptest.Server) string {
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestReplace_DarwinSendsLaunchctlKillSIGTERM(t *testing.T) {
	home := withTestHome(t, "darwin")
	srv := newHealthServer(t, healthOK("v1.2.3"))
	configPath := writeReplaceConfig(t, home, serverAddr(srv.Server))

	m, stub := newTestManager()
	if err := m.Replace(ReplaceOptions{ConfigPath: configPath, ExpectedVersion: "v1.2.3", Timeout: time.Second, PollInterval: 10 * time.Millisecond}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	var gotKill bool
	for _, c := range stub.calls {
		if len(c) >= 2 && c[0] == "launchctl" && c[1] == "kill" {
			gotKill = true
			if !(len(c) >= 3 && c[2] == "SIGTERM") {
				t.Errorf("launchctl kill call = %v, want SIGTERM", c)
			}
		}
		if len(c) >= 2 && c[0] == "launchctl" && c[1] == "kickstart" {
			t.Errorf("Replace must not use launchctl kickstart -k, got %v", c)
		}
	}
	if !gotKill {
		t.Errorf("expected a launchctl kill call, got %v", stub.calls)
	}
}

func TestReplace_LinuxSelectsSystemctlRestart(t *testing.T) {
	home := withTestHome(t, "linux")
	srv := newHealthServer(t, healthOK("v1.2.3"))
	configPath := writeReplaceConfig(t, home, serverAddr(srv.Server))

	m, stub := newTestManager()
	if err := m.Replace(ReplaceOptions{ConfigPath: configPath, ExpectedVersion: "v1.2.3", Timeout: time.Second, PollInterval: 10 * time.Millisecond}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	var gotRestart bool
	for _, c := range stub.calls {
		if len(c) >= 3 && c[0] == "systemctl" && c[1] == "--user" && c[2] == "restart" {
			gotRestart = true
		}
	}
	if !gotRestart {
		t.Errorf("expected a systemctl --user restart call, got %v", stub.calls)
	}
}

func TestReplace_RetriesConnectionRefusal(t *testing.T) {
	home := withTestHome(t, "linux")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens on addr from here on: connection refused

	configPath := writeReplaceConfig(t, home, addr)
	m, _ := newTestManager()
	start := time.Now()
	err = m.Replace(ReplaceOptions{ConfigPath: configPath, ExpectedVersion: "v1.2.3", Timeout: 100 * time.Millisecond, PollInterval: 20 * time.Millisecond})
	if err == nil {
		t.Fatal("Replace should fail when the server is never reachable")
	}
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Errorf("connection refusal should be retried until the timeout (~100ms), took %s", elapsed)
	}
}

func TestReplace_RetriesNonOKThenOldVersionThenMatches(t *testing.T) {
	home := withTestHome(t, "linux")
	srv := newHealthServer(t, healthNonOK(), healthOK("v1.0.0"), healthOK("v1.2.3"))
	configPath := writeReplaceConfig(t, home, serverAddr(srv.Server))

	m, _ := newTestManager()
	if err := m.Replace(ReplaceOptions{ConfigPath: configPath, ExpectedVersion: "v1.2.3", Timeout: time.Second, PollInterval: 5 * time.Millisecond}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if got := atomic.LoadInt32(&srv.requests); got < 3 {
		t.Errorf("expected at least 3 requests (non-200, old version, match), got %d", got)
	}
}

func TestReplace_MatchingVersionSucceeds(t *testing.T) {
	home := withTestHome(t, "linux")
	srv := newHealthServer(t, healthOK("v1.2.3"))
	configPath := writeReplaceConfig(t, home, serverAddr(srv.Server))

	m, _ := newTestManager()
	if err := m.Replace(ReplaceOptions{ConfigPath: configPath, ExpectedVersion: "v1.2.3", Timeout: time.Second, PollInterval: 10 * time.Millisecond}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
}

func TestReplace_DevVersionRequiresHealthOnly(t *testing.T) {
	home := withTestHome(t, "linux")
	srv := newHealthServer(t, healthOK("v9.9.9")) // different from "dev", must still succeed
	configPath := writeReplaceConfig(t, home, serverAddr(srv.Server))

	m, _ := newTestManager()
	if err := m.Replace(ReplaceOptions{ConfigPath: configPath, ExpectedVersion: "dev", Timeout: time.Second, PollInterval: 10 * time.Millisecond}); err != nil {
		t.Fatalf("Replace with dev expected version: %v", err)
	}
	if err := m.Replace(ReplaceOptions{ConfigPath: configPath, ExpectedVersion: "", Timeout: time.Second, PollInterval: 10 * time.Millisecond}); err != nil {
		t.Fatalf("Replace with empty expected version: %v", err)
	}
}

func TestReplace_MalformedHealthFailsFast(t *testing.T) {
	home := withTestHome(t, "linux")
	srv := newHealthServer(t, healthMalformed())
	configPath := writeReplaceConfig(t, home, serverAddr(srv.Server))

	m, _ := newTestManager()
	start := time.Now()
	err := m.Replace(ReplaceOptions{ConfigPath: configPath, ExpectedVersion: "v1.2.3", Timeout: 5 * time.Second, PollInterval: 10 * time.Millisecond})
	if err == nil {
		t.Fatal("Replace should fail on a malformed /health response")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("malformed /health should fail fast, took %s", elapsed)
	}
}

func TestReplace_TimeoutIncludesLastObserved(t *testing.T) {
	home := withTestHome(t, "linux")
	srv := newHealthServer(t, healthOK("v1.0.0")) // never matches expected
	configPath := writeReplaceConfig(t, home, serverAddr(srv.Server))

	m, _ := newTestManager()
	err := m.Replace(ReplaceOptions{ConfigPath: configPath, ExpectedVersion: "v1.2.3", Timeout: 50 * time.Millisecond, PollInterval: 10 * time.Millisecond})
	if err == nil {
		t.Fatal("Replace should time out when the version never matches")
	}
	for _, want := range []string{serverAddr(srv.Server), "v1.2.3", "v1.0.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("timeout error = %q, want it to contain %q", err, want)
		}
	}
}

func TestReplace_ZeroKBsMatchingHealthSucceeds(t *testing.T) {
	// /health is 200 status:"ok" regardless of mounted KBs (D84); Replace
	// must not require a kbs field or any KB-related content.
	home := withTestHome(t, "linux")
	srv := newHealthServer(t, healthOK("v1.2.3"))
	configPath := writeReplaceConfig(t, home, serverAddr(srv.Server))

	m, _ := newTestManager()
	if err := m.Replace(ReplaceOptions{ConfigPath: configPath, ExpectedVersion: "v1.2.3", Timeout: time.Second, PollInterval: 10 * time.Millisecond}); err != nil {
		t.Fatalf("Replace with zero mounted KBs: %v", err)
	}
}

func TestReplace_MalformedConfigFailsBeforeSignaling(t *testing.T) {
	home := withTestHome(t, "linux")
	configPath := filepath.Join(home, "server.yaml")
	if err := os.WriteFile(configPath, []byte("not: [valid yaml"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, stub := newTestManager()
	if err := m.Replace(ReplaceOptions{ConfigPath: configPath, ExpectedVersion: "v1.2.3"}); err == nil {
		t.Fatal("Replace should fail on a malformed config")
	}
	if len(stub.calls) != 0 {
		t.Errorf("Replace must not signal the process before the config is validated, got calls: %v", stub.calls)
	}
}

func TestReplace_MissingHTTPAddrFailsBeforeSignaling(t *testing.T) {
	home := withTestHome(t, "linux")
	configPath := filepath.Join(home, "server.yaml")
	if err := os.WriteFile(configPath, []byte("data: /tmp/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, stub := newTestManager()
	if err := m.Replace(ReplaceOptions{ConfigPath: configPath}); err == nil {
		t.Fatal("Replace should fail when the config has no http address")
	}
	if len(stub.calls) != 0 {
		t.Errorf("Replace must not signal the process without an http address, got calls: %v", stub.calls)
	}
}

func TestRestart_Darwin_UsesKickstartK(t *testing.T) {
	withTestHome(t, "darwin")
	m, stub := newTestManager()
	if err := m.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if len(stub.calls) != 1 || stub.calls[0][0] != "launchctl" || stub.calls[0][1] != "kickstart" || stub.calls[0][2] != "-k" {
		t.Errorf("plain Restart calls = %v, want launchctl kickstart -k", stub.calls)
	}
}

func TestRestart_Linux_UsesSystemctlRestart(t *testing.T) {
	withTestHome(t, "linux")
	m, stub := newTestManager()
	if err := m.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if len(stub.calls) != 1 || stub.calls[0][0] != "systemctl" || stub.calls[0][1] != "--user" || stub.calls[0][2] != "restart" {
		t.Errorf("plain Restart calls = %v, want systemctl --user restart", stub.calls)
	}
}
