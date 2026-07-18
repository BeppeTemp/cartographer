package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	out := DefaultServerYAML("/home/x/cartographer-data", "127.0.0.1:8080")
	for _, want := range []string{
		`http: "127.0.0.1:8080"`,
		`data: "/home/x/cartographer-data"`,
		"init: true",
		"cartographer service install",
		"config.example.yaml",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("yaml missing %q\n---\n%s", want, out)
		}
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

	m, stub := newTestManager()
	warnings, err := m.Install(InstallOptions{DataDir: "/data", HTTPAddr: "127.0.0.1:8080"})
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
	if !strings.Contains(string(data), `data: "/data"`) {
		t.Errorf("config content = %q, want data: \"/data\"", data)
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
	existing := "http: \":9999\"\ndata: /custom\n"
	if err := os.WriteFile(configPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := newTestManager()
	warnings, err := m.Install(InstallOptions{DataDir: "/data", HTTPAddr: "127.0.0.1:8080", DataExplicit: true})
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
}

func TestInstall_Idempotent(t *testing.T) {
	withTestHome(t, "darwin")
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "cartographer")
	os.WriteFile(binPath, []byte("x"), 0o755)
	origExecutable := osExecutable
	osExecutable = func() (string, error) { return binPath, nil }
	t.Cleanup(func() { osExecutable = origExecutable })

	m, _ := newTestManager()
	opts := InstallOptions{DataDir: "/data", HTTPAddr: "127.0.0.1:8080"}
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
		t.Errorf("HTTPAddr = %q, want 127.0.0.1:8080", st.HTTPAddr)
	}
}

func TestHealthURL(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		":8080":             "http://127.0.0.1:8080/health",
		"127.0.0.1:8080":    "http://127.0.0.1:8080/health",
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
