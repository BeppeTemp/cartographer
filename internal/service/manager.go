package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
)

// launchdLabel/serviceLabel identify the launchd job / systemd unit.
const (
	launchdLabel  = "com.cartographer.serve"
	systemdUnit   = "cartographer.service"
	healthTimeout = 2 * time.Second

	// DefaultReplaceTimeout/DefaultReplacePollInterval bound Manager.Replace's
	// poll loop for callers (cmd/cartographer's `service restart --wait`) that
	// do not set ReplaceOptions.Timeout/PollInterval. Tests inject a shorter
	// window instead of waiting out these defaults.
	DefaultReplaceTimeout      = 30 * time.Second
	DefaultReplacePollInterval = 500 * time.Millisecond
)

// runFunc runs an external command and returns its combined output. Injected
// on Manager so tests can stub platform command execution (launchctl/
// systemctl) without running it for real.
type runFunc func(name string, args ...string) (string, error)

// osExecutable is os.Executable, indirected so tests can stub the resolved
// binary path without a real executable on disk.
var osExecutable = os.Executable

// Manager installs, starts, stops, and reports on the cartographer server
// native service (launchd on macOS, systemd user unit on Linux).
type Manager struct {
	run runFunc
}

// NewManager returns a Manager that runs real platform commands via os/exec.
func NewManager() *Manager {
	return &Manager{run: execRun}
}

func execRun(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w (output: %s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// InstallOptions parametrizes Manager.Install.
type InstallOptions struct {
	// ConfigPath overrides the standard config path (ConfigPath()) when non-empty.
	ConfigPath string
	// DataDir/HTTPAddr seed a newly generated config (DefaultServerYAML).
	// Ignored (with a warning) if the config file already exists.
	DataDir  string
	HTTPAddr string
	// DataExplicit/HTTPExplicit report whether the caller passed --data/--http
	// explicitly, so Install can warn only when they would otherwise be
	// silently ignored (config already present).
	DataExplicit bool
	HTTPExplicit bool
}

// resolvedConfigPath returns opts.ConfigPath if set, else the standard path.
func (o InstallOptions) resolvedConfigPath() (string, error) {
	if o.ConfigPath != "" {
		return o.ConfigPath, nil
	}
	return ConfigPath()
}

// Install generates the platform service definition (launchd plist or
// systemd unit) and (re)starts the service. It is idempotent: re-running it
// overwrites the plist/unit and restarts the service, but never touches an
// already-existing config YAML.
//
// Returns warnings to surface to the user (e.g. --data/--http ignored
// because a config file already existed) alongside any fatal error.
func (m *Manager) Install(opts InstallOptions) ([]string, error) {
	var warnings []string

	binPath, err := osExecutable()
	if err != nil {
		return nil, fmt.Errorf("service: resolve binary path: %w", err)
	}
	binPath = resolveStableBinPath(binPath)

	configPath, err := opts.resolvedConfigPath()
	if err != nil {
		return nil, fmt.Errorf("service: resolve config path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return nil, fmt.Errorf("service: create config dir: %w", err)
	}

	dataDir := opts.DataDir
	if _, err := os.Stat(configPath); err == nil {
		if opts.DataExplicit || opts.HTTPExplicit {
			warnings = append(warnings, fmt.Sprintf("config %s already exists; --data/--http are ignored (edit the file directly to change them)", configPath))
		}
		if cfg, cfgErr := config.Load(configPath); cfgErr == nil && cfg.Data != "" {
			dataDir = cfg.Data
		}
	} else if os.IsNotExist(err) {
		yamlData := DefaultServerYAML(opts.DataDir, opts.HTTPAddr)
		if err := os.WriteFile(configPath, []byte(yamlData), 0o644); err != nil {
			return nil, fmt.Errorf("service: write config: %w", err)
		}
	} else {
		return nil, fmt.Errorf("service: stat config: %w", err)
	}

	if dataDir != "" {
		if _, err := os.Stat(dataDir); os.IsNotExist(err) {
			if err := os.MkdirAll(dataDir, 0o755); err != nil {
				return nil, fmt.Errorf("service: create data dir: %w", err)
			}
			fmt.Fprintf(os.Stderr, "created %s\n", dataDir)
		}
	}

	switch goos {
	case "darwin":
		if err := m.installDarwin(binPath, configPath); err != nil {
			return warnings, err
		}
	case "linux":
		if err := m.installLinux(binPath, configPath); err != nil {
			return warnings, err
		}
	default:
		return warnings, fmt.Errorf("service: unsupported platform %q", goos)
	}

	return warnings, nil
}

func (m *Manager) installDarwin(binPath, configPath string) error {
	logPath, err := LaunchdLogPath()
	if err != nil {
		return fmt.Errorf("service: resolve log path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fmt.Errorf("service: create log dir: %w", err)
	}
	plistPath, err := LaunchdPlistPath()
	if err != nil {
		return fmt.Errorf("service: resolve plist path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return fmt.Errorf("service: create LaunchAgents dir: %w", err)
	}
	plist := RenderLaunchdPlist(binPath, configPath, logPath)
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("service: write plist: %w", err)
	}

	uid := os.Getuid()
	// Best-effort: bootout fails with a non-zero exit if the job wasn't
	// loaded yet, which is expected on a first install.
	m.run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", uid, launchdLabel))
	if _, err := m.run("launchctl", "bootstrap", fmt.Sprintf("gui/%d", uid), plistPath); err != nil {
		return fmt.Errorf("service: launchctl bootstrap: %w", err)
	}
	return nil
}

func (m *Manager) installLinux(binPath, configPath string) error {
	unitPath, err := SystemdUnitPath()
	if err != nil {
		return fmt.Errorf("service: resolve unit path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return fmt.Errorf("service: create systemd user dir: %w", err)
	}
	unit := RenderSystemdUnit(binPath, configPath)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("service: write unit: %w", err)
	}

	if _, err := m.run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("service: systemctl daemon-reload: %w", err)
	}
	if _, err := m.run("systemctl", "--user", "enable", "--now", systemdUnit); err != nil {
		return fmt.Errorf("service: systemctl enable --now: %w", err)
	}
	return nil
}

// Uninstall stops the service and removes the plist/unit. The config YAML
// and KB data are never touched.
func (m *Manager) Uninstall() error {
	switch goos {
	case "darwin":
		plistPath, err := LaunchdPlistPath()
		if err != nil {
			return fmt.Errorf("service: resolve plist path: %w", err)
		}
		uid := os.Getuid()
		m.run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", uid, launchdLabel))
		if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("service: remove plist: %w", err)
		}
		return nil
	case "linux":
		unitPath, err := SystemdUnitPath()
		if err != nil {
			return fmt.Errorf("service: resolve unit path: %w", err)
		}
		m.run("systemctl", "--user", "disable", "--now", systemdUnit)
		if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("service: remove unit: %w", err)
		}
		_, err = m.run("systemctl", "--user", "daemon-reload")
		return err
	default:
		return fmt.Errorf("service: unsupported platform %q", goos)
	}
}

// Start starts the service. On darwin it bootstraps the job into the GUI
// domain and re-enables it, because Stop disables it to defeat KeepAlive.
func (m *Manager) Start() error {
	switch goos {
	case "darwin":
		plistPath, err := LaunchdPlistPath()
		if err != nil {
			return fmt.Errorf("service: resolve plist path: %w", err)
		}
		target := fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel)
		// Best-effort: fails harmlessly when the job was never disabled.
		m.run("launchctl", "enable", target)
		_, err = m.run("launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plistPath)
		if err != nil && m.launchdJobLoaded() {
			// Already bootstrapped: enable + kickstart is the equivalent path.
			_, err = m.run("launchctl", "kickstart", target)
		}
		return err
	case "linux":
		_, err := m.run("systemctl", "--user", "start", systemdUnit)
		return err
	default:
		return fmt.Errorf("service: unsupported platform %q", goos)
	}
}

// Stop stops the service, leaving the job registered so Restart can find it.
// It used to `bootout`, which unregisters: `stop` then `restart` failed with
// "Could not find service ... in domain", and with the server down the natural
// reading was a broken installation. Disable comes first because the plist sets
// KeepAlive, so launchd would otherwise restart the process immediately;
// Uninstall keeps using bootout, which is what removing the definition means.
func (m *Manager) Stop() error {
	switch goos {
	case "darwin":
		target := fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel)
		if _, err := m.run("launchctl", "disable", target); err != nil {
			return err
		}
		_, err := m.run("launchctl", "kill", "SIGTERM", target)
		return err
	case "linux":
		_, err := m.run("systemctl", "--user", "stop", systemdUnit)
		return err
	default:
		return fmt.Errorf("service: unsupported platform %q", goos)
	}
}

// Restart restarts the service. On darwin it falls back to Start when the job
// is not registered: a job booted out by an older version's Stop must still be
// restartable after the upgrade, and kickstart cannot revive one.
func (m *Manager) Restart() error {
	switch goos {
	case "darwin":
		if !m.launchdJobLoaded() {
			return m.Start()
		}
		target := fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel)
		// Stop disables the job; a restart must undo that or kickstart is a no-op.
		m.run("launchctl", "enable", target)
		_, err := m.run("launchctl", "kickstart", "-k", target)
		return err
	case "linux":
		_, err := m.run("systemctl", "--user", "restart", systemdUnit)
		return err
	default:
		return fmt.Errorf("service: unsupported platform %q", goos)
	}
}

// launchdJobLoaded reports whether the job is registered in the user's GUI
// domain. It branches on launchctl's exit status rather than parsing its prose,
// which is localized.
func (m *Manager) launchdJobLoaded() bool {
	_, err := m.run("launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel))
	return err == nil
}

// EffectiveConfigPath resolves the server config path that governs the
// currently installed native service. An explicit override always wins. With
// no explicit path, the installed launchd plist / systemd unit is read and
// the argument following "serve --config" is extracted, preserving
// custom-config installations. If no service definition is installed, the
// standard ConfigPath() is used. An installed but unreadable or malformed
// definition is a contextual error, returned before any process is signaled.
func (m *Manager) EffectiveConfigPath(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	switch goos {
	case "darwin":
		plistPath, err := LaunchdPlistPath()
		if err != nil {
			return "", fmt.Errorf("service: resolve plist path: %w", err)
		}
		data, err := os.ReadFile(plistPath)
		if err != nil {
			if os.IsNotExist(err) {
				return ConfigPath()
			}
			return "", fmt.Errorf("service: read installed plist %s: %w", plistPath, err)
		}
		cfgPath, err := extractPlistConfigPath(data)
		if err != nil {
			return "", fmt.Errorf("service: installed plist %s does not declare a usable config: %w", plistPath, err)
		}
		return cfgPath, nil
	case "linux":
		unitPath, err := SystemdUnitPath()
		if err != nil {
			return "", fmt.Errorf("service: resolve unit path: %w", err)
		}
		data, err := os.ReadFile(unitPath)
		if err != nil {
			if os.IsNotExist(err) {
				return ConfigPath()
			}
			return "", fmt.Errorf("service: read installed unit %s: %w", unitPath, err)
		}
		cfgPath, err := extractUnitConfigPath(data)
		if err != nil {
			return "", fmt.Errorf("service: installed unit %s does not declare a usable config: %w", unitPath, err)
		}
		return cfgPath, nil
	default:
		return "", fmt.Errorf("service: unsupported platform %q", goos)
	}
}

var (
	plistProgramArgsRe = regexp.MustCompile(`(?s)<key>ProgramArguments</key>\s*<array>(.*?)</array>`)
	plistStringRe      = regexp.MustCompile(`<string>(.*?)</string>`)
)

// extractPlistConfigPath reads the argument following "serve --config" out
// of a generated launchd plist's ProgramArguments array (see
// RenderLaunchdPlist). Returns an error if the array or the --config
// argument is missing, which callers treat as a malformed definition.
func extractPlistConfigPath(data []byte) (string, error) {
	m := plistProgramArgsRe.FindSubmatch(data)
	if m == nil {
		return "", fmt.Errorf("no ProgramArguments array found")
	}
	matches := plistStringRe.FindAllSubmatch(m[1], -1)
	args := make([]string, len(matches))
	for i, s := range matches {
		args[i] = string(s[1])
	}
	return configArgAfter(args)
}

// extractUnitConfigPath reads the argument following "serve --config" out of
// a generated systemd unit's ExecStart line (see RenderSystemdUnit).
func extractUnitConfigPath(data []byte) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		return configArgAfter(strings.Fields(strings.TrimPrefix(line, "ExecStart=")))
	}
	return "", fmt.Errorf("no ExecStart line found")
}

// configArgAfter returns the value immediately following a "--config" token
// in args, erroring if the flag is absent or has no value.
func configArgAfter(args []string) (string, error) {
	for i, a := range args {
		if a == "--config" {
			if i+1 >= len(args) || args[i+1] == "" {
				return "", fmt.Errorf("--config has no value")
			}
			return args[i+1], nil
		}
	}
	return "", fmt.Errorf("no --config argument found")
}

// HealthStatus is the decoded subset of a server's /health response used to
// prove a version-gated replacement: JSON field names match the server's
// wire format (see internal/client.Health).
type HealthStatus struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// healthProtocolError marks a /health response that was received (HTTP 200)
// but is not well-formed proof of a healthy server: malformed JSON or a
// status other than "ok". Unlike connection failures and non-200 responses,
// this is not retried — it indicates the endpoint is not a cartographer
// server, so waiting out the timeout would not help.
type healthProtocolError struct{ err error }

func (e *healthProtocolError) Error() string { return e.err.Error() }
func (e *healthProtocolError) Unwrap() error { return e.err }

// ProbeHealth fetches and decodes GET http://<addr>/health, using the same
// addr normalization as healthURL (bare port or host:port). A connection
// failure or non-200 response is returned as a plain error (transient,
// callers may retry); malformed JSON or a status other than "ok" is wrapped
// in healthProtocolError (not transient — fail fast).
func ProbeHealth(addr string, timeout time.Duration) (HealthStatus, error) {
	url := healthURL(addr)
	if url == "" {
		return HealthStatus{}, fmt.Errorf("service: no http address configured")
	}
	c := http.Client{Timeout: timeout}
	resp, err := c.Get(url)
	if err != nil {
		return HealthStatus{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return HealthStatus{}, fmt.Errorf("service: %s returned %s", url, resp.Status)
	}
	var hs HealthStatus
	if err := json.NewDecoder(resp.Body).Decode(&hs); err != nil {
		return HealthStatus{}, &healthProtocolError{fmt.Errorf("service: decode %s response: %w", url, err)}
	}
	if hs.Status != "ok" {
		return hs, &healthProtocolError{fmt.Errorf("service: %s reported status %q, want \"ok\"", url, hs.Status)}
	}
	return hs, nil
}

// versionSatisfies reports whether observed proves expected is serving. An
// empty or "dev" expected version only requires a healthy response —
// development builds are not uniquely identifiable.
func versionSatisfies(observed, expected string) bool {
	return expected == "" || expected == "dev" || observed == expected
}

// signalGraceful sends the platform's graceful-stop signal so the service
// manager's supervisor (launchd KeepAlive / systemd Restart=on-failure)
// relaunches it: SIGTERM lets serve.go drain in-flight HTTP requests before
// exiting, unlike Restart's launchctl kickstart -k.
func (m *Manager) signalGraceful() error {
	switch goos {
	case "darwin":
		_, err := m.run("launchctl", "kill", "SIGTERM", fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel))
		return err
	case "linux":
		_, err := m.run("systemctl", "--user", "restart", systemdUnit)
		return err
	default:
		return fmt.Errorf("service: unsupported platform %q", goos)
	}
}

// ReplaceOptions parametrizes Manager.Replace.
type ReplaceOptions struct {
	// ConfigPath overrides EffectiveConfigPath's discovery when non-empty.
	ConfigPath string
	// ExpectedVersion is the binary version the caller expects to observe
	// after replacement. Empty or "dev" skips the equality check.
	ExpectedVersion string
	// Timeout bounds the poll loop; defaults to DefaultReplaceTimeout when <= 0.
	Timeout time.Duration
	// PollInterval paces retries; defaults to DefaultReplacePollInterval when <= 0.
	PollInterval time.Duration
}

// Replace gracefully replaces the already-running native service (SIGTERM,
// relaunched by the platform supervisor) and blocks until /health proves the
// expected version is serving, or returns a timeout error. Connection
// failures, non-200 responses, and an old version are retried; a malformed
// /health response fails immediately. Zero mounted KBs do not affect this —
// /health stays 200 regardless of readiness (D84).
func (m *Manager) Replace(opts ReplaceOptions) error {
	configPath, err := m.EffectiveConfigPath(opts.ConfigPath)
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("service: load config %s: %w", configPath, err)
	}
	if cfg.HTTP == "" {
		return fmt.Errorf("service: config %s has no http address configured", configPath)
	}

	if err := m.signalGraceful(); err != nil {
		return fmt.Errorf("service: graceful restart: %w", err)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultReplaceTimeout
	}
	interval := opts.PollInterval
	if interval <= 0 {
		interval = DefaultReplacePollInterval
	}

	endpoint := healthURL(cfg.HTTP)
	deadline := time.Now().Add(timeout)
	var lastStatus, lastVersion string
	var lastErr error
	for {
		hs, err := ProbeHealth(cfg.HTTP, healthTimeout)
		if err != nil {
			var protoErr *healthProtocolError
			if errors.As(err, &protoErr) {
				return fmt.Errorf("service: %s did not prove a healthy replacement (endpoint %s, expected version %s): %w", configPath, endpoint, displayVersion(opts.ExpectedVersion), err)
			}
			lastErr = err
		} else {
			lastStatus, lastVersion = hs.Status, hs.Version
			if versionSatisfies(hs.Version, opts.ExpectedVersion) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("service: timed out waiting for %s to serve version %s (last observed status=%q version=%q, last error=%v)", endpoint, displayVersion(opts.ExpectedVersion), lastStatus, lastVersion, lastErr)
}

// displayVersion renders an expected version for an error message, since an
// empty ExpectedVersion means "any" rather than the empty string literal.
func displayVersion(expected string) string {
	if expected == "" {
		return "(any)"
	}
	return expected
}

// Status reports the current state of the service.
type Status struct {
	BinPath    string
	ConfigPath string
	HTTPAddr   string
	Installed  bool
	Running    bool
	Healthy    bool
}

// Status inspects the service: whether its plist/unit is installed, whether
// it's currently running (launchctl print / systemctl is-active), and
// whether its /health endpoint responds (read from the config YAML's http
// address, best-effort — absent/unreadable config just leaves Healthy false
// and HTTPAddr empty).
func (m *Manager) Status(configPath string) (Status, error) {
	var st Status
	st.ConfigPath = configPath
	if configPath == "" {
		p, err := ConfigPath()
		if err != nil {
			return st, fmt.Errorf("service: resolve config path: %w", err)
		}
		st.ConfigPath = p
	}
	if bin, err := osExecutable(); err == nil {
		st.BinPath = resolveStableBinPath(bin)
	}

	switch goos {
	case "darwin":
		plistPath, err := LaunchdPlistPath()
		if err != nil {
			return st, fmt.Errorf("service: resolve plist path: %w", err)
		}
		if _, err := os.Stat(plistPath); err == nil {
			st.Installed = true
		}
		if _, err := m.run("launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel)); err == nil {
			st.Running = true
		}
	case "linux":
		unitPath, err := SystemdUnitPath()
		if err != nil {
			return st, fmt.Errorf("service: resolve unit path: %w", err)
		}
		if _, err := os.Stat(unitPath); err == nil {
			st.Installed = true
		}
		if _, err := m.run("systemctl", "--user", "is-active", systemdUnit); err == nil {
			st.Running = true
		}
	default:
		return st, fmt.Errorf("service: unsupported platform %q", goos)
	}

	if cfg, err := config.Load(st.ConfigPath); err == nil {
		st.HTTPAddr = cfg.HTTP
		st.Healthy = checkHealth(cfg.HTTP)
	}

	return st, nil
}

// checkHealth reports whether GET http://<addr>/health returns 200 within
// healthTimeout. addr may be a bare port (":39273") or host:port
// ("127.0.0.1:39273"); a bare port is normalized to a 127.0.0.1 host.
func checkHealth(addr string) bool {
	url := healthURL(addr)
	if url == "" {
		return false
	}
	client := http.Client{Timeout: healthTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// healthURL builds the /health URL from a server http address, normalizing a
// bare-port address (":39273") to a 127.0.0.1 host. Returns "" if addr is empty.
func healthURL(addr string) string {
	if addr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s/health", net.JoinHostPort(host, port))
}
