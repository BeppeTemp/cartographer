package service

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
)

// launchdLabel/serviceLabel identify the launchd job / systemd unit.
const (
	launchdLabel  = "com.cartographer.serve"
	systemdUnit   = "cartographer.service"
	healthTimeout = 2 * time.Second
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

// Start starts the service.
func (m *Manager) Start() error {
	switch goos {
	case "darwin":
		plistPath, err := LaunchdPlistPath()
		if err != nil {
			return fmt.Errorf("service: resolve plist path: %w", err)
		}
		_, err = m.run("launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plistPath)
		return err
	case "linux":
		_, err := m.run("systemctl", "--user", "start", systemdUnit)
		return err
	default:
		return fmt.Errorf("service: unsupported platform %q", goos)
	}
}

// Stop stops the service.
func (m *Manager) Stop() error {
	switch goos {
	case "darwin":
		_, err := m.run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel))
		return err
	case "linux":
		_, err := m.run("systemctl", "--user", "stop", systemdUnit)
		return err
	default:
		return fmt.Errorf("service: unsupported platform %q", goos)
	}
}

// Restart restarts the service.
func (m *Manager) Restart() error {
	switch goos {
	case "darwin":
		_, err := m.run("launchctl", "kickstart", "-k", fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel))
		return err
	case "linux":
		_, err := m.run("systemctl", "--user", "restart", systemdUnit)
		return err
	default:
		return fmt.Errorf("service: unsupported platform %q", goos)
	}
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
// healthTimeout. addr may be a bare port (":8080") or host:port
// ("127.0.0.1:8080"); a bare port is normalized to a 127.0.0.1 host.
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
// bare-port address (":8080") to a 127.0.0.1 host. Returns "" if addr is empty.
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
