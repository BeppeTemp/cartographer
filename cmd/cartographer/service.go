package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/defaults"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// Exit codes for `cartographer service status`, systemctl-like: 0 running,
// 3 installed but stopped, 4 not installed. Other subcommands (install,
// uninstall, start, stop, restart) use 0 on success, 2 on error.
const (
	exitStatusRunning      = 0
	exitStatusError        = 2
	exitStatusStopped      = 3
	exitStatusNotInstalled = 4
)

// serviceRestartFn/serviceReplaceFn are indirected through package-level vars
// so cmdServiceRestart's dispatch (plain restart vs --wait's graceful,
// version-gated replacement) is testable without a real launchctl/systemctl
// or a live /health endpoint.
var (
	serviceRestartFn = func() error { return service.NewManager().Restart() }
	serviceReplaceFn = func(opts service.ReplaceOptions) error { return service.NewManager().Replace(opts) }
)

// cmdService manages the cartographer MCP server as a native per-user
// service: launchd on macOS, a systemd user unit on Linux.
func cmdService(args []string) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		printServiceUsage(os.Stdout)
		return 0
	}
	target, rest := splitPositional(args, "")

	switch target {
	case "install":
		return cmdServiceInstall(rest)
	case "uninstall":
		return cmdServiceUninstall(rest)
	case "start":
		return cmdServiceStart(rest)
	case "stop":
		return cmdServiceStop(rest)
	case "restart":
		return cmdServiceRestart(rest)
	case "status":
		return cmdServiceStatus(rest)
	case "sync-timer":
		return cmdServiceSyncTimer(rest)
	default:
		fmt.Fprintln(os.Stderr, "Error: service action required")
		printServiceUsage(os.Stderr)
		return exitStatusError
	}
}

func printServiceUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: cartographer service <install|uninstall|start|stop|restart|status> [flags]")
	fmt.Fprintln(w, "       cartographer service sync-timer <install|uninstall|status> [--interval <duration>]")
}

// syncTimerFns are indirected so the dispatch is testable without a real
// launchctl/systemctl.
var (
	syncTimerInstallFn   = func(interval time.Duration) error { return service.NewManager().InstallSyncTimer(interval) }
	syncTimerUninstallFn = func() error { return service.NewManager().UninstallSyncTimer() }
	syncTimerStatusFn    = func() (service.SyncTimerStatus, error) { return service.NewManager().SyncTimerStatus() }
)

// cmdServiceSyncTimer manages the scheduled client sync (D140): the supported
// trigger for providers with no session hook. Opt-in and explicit —
// installing a launchd agent or a systemd user unit behind the user's back on
// `connect` would be out of proportion.
func cmdServiceSyncTimer(args []string) int {
	action, rest := splitPositional(args, "")
	switch action {
	case "install":
		fs := flag.NewFlagSet("service sync-timer install", flag.ExitOnError)
		interval := fs.Duration("interval", service.DefaultSyncInterval, "How often to run `cartographer sync`")
		fs.Parse(rest)
		if *interval <= 0 {
			fmt.Fprintln(os.Stderr, "Error: --interval must be positive")
			return exitStatusError
		}
		if err := syncTimerInstallFn(*interval); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return exitStatusError
		}
		fmt.Printf("sync timer installed (every %s)\n", *interval)
		// The timer never passes --auto-trust: an unattended job must not
		// grant a trust the user never gave (D54).
		fmt.Println("it runs `cartographer sync` without --auto-trust; the persisted `trust` setting still applies")
		return 0
	case "uninstall":
		if err := syncTimerUninstallFn(); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return exitStatusError
		}
		fmt.Println("sync timer uninstalled")
		return 0
	case "status":
		st, err := syncTimerStatusFn()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return exitStatusError
		}
		if !st.Installed {
			fmt.Println("sync timer not installed (cartographer service sync-timer install)")
			return exitStatusNotInstalled
		}
		state := "installed"
		if st.Active {
			state = "active"
		}
		if st.Interval > 0 {
			fmt.Printf("sync timer %s, every %s (%s)\n", state, st.Interval, st.Path)
		} else {
			fmt.Printf("sync timer %s (%s)\n", state, st.Path)
		}
		if !st.Active {
			return exitStatusStopped
		}
		return exitStatusRunning
	default:
		fmt.Fprintln(os.Stderr, "Error: sync-timer action required")
		printServiceUsage(os.Stderr)
		return exitStatusError
	}
}

func cmdServiceInstall(args []string) int {
	fs := flag.NewFlagSet("service install", flag.ExitOnError)
	defaultConfig, _ := service.ConfigPath()
	configFlag := fs.String("config", defaultConfig, "Path to the generated server config YAML")
	dataFlag := fs.String("data", defaultDataDir(), "KB data directory (only used when generating a new config)")
	httpFlag := fs.String("http", defaults.DefaultListenAddress, "HTTP listen address (only used when generating a new config)")
	fs.Parse(args)

	passed := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { passed[f.Name] = true })

	m := service.NewManager()
	warnings, err := m.Install(service.InstallOptions{
		ConfigPath:   *configFlag,
		DataDir:      *dataFlag,
		HTTPAddr:     *httpFlag,
		DataExplicit: passed["data"],
		HTTPExplicit: passed["http"],
	})
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "Warning:", w)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError
	}
	fmt.Println("service installed and started")

	dataDir := *dataFlag
	if cfg, cfgErr := config.Load(*configFlag); cfgErr == nil && cfg.Data != "" {
		dataDir = cfg.Data
	}
	printNoKBHintIfEmpty(dataDir)

	return exitStatusRunning
}

func cmdServiceUninstall(args []string) int {
	fs := flag.NewFlagSet("service uninstall", flag.ExitOnError)
	fs.Parse(args)

	if err := service.NewManager().Uninstall(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError
	}
	fmt.Println("service uninstalled")
	return exitStatusRunning
}

func cmdServiceStart(args []string) int {
	fs := flag.NewFlagSet("service start", flag.ExitOnError)
	fs.Parse(args)

	if err := service.NewManager().Start(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError
	}
	fmt.Println("service started")
	return exitStatusRunning
}

func cmdServiceStop(args []string) int {
	fs := flag.NewFlagSet("service stop", flag.ExitOnError)
	fs.Parse(args)

	if err := service.NewManager().Stop(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError
	}
	fmt.Println("service stopped")
	return exitStatusRunning
}

func cmdServiceRestart(args []string) int {
	fs := flag.NewFlagSet("service restart", flag.ExitOnError)
	wait := fs.Bool("wait", false, "Gracefully replace the running service (SIGTERM) and block until /health proves the installed version is serving")
	configFlag := fs.String("config", "", "Server config YAML used to verify the replacement (default: discovered from the installed service definition, else the standard path)")
	fs.Parse(args)

	if !*wait {
		if err := serviceRestartFn(); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return exitStatusError
		}
		fmt.Println("service restarted")
		return exitStatusRunning
	}

	if err := serviceReplaceFn(service.ReplaceOptions{ConfigPath: *configFlag, ExpectedVersion: version}); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError
	}
	fmt.Printf("service restarted and verified running version %s\n", version)
	return exitStatusRunning
}

func cmdServiceStatus(args []string) int {
	output, remaining, err := outputFlag(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError
	}
	fs := flag.NewFlagSet("service status", flag.ExitOnError)
	configFlag := fs.String("config", "", "Server config YAML to read the http address from (default: the standard path)")
	fs.Parse(remaining)

	st, err := service.NewManager().Status(*configFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError
	}

	s := statusSnapshot{Schema: statusSchema, Client: version, State: "running", Service: &serviceSnapshot{st.Installed, st.Running, st.Healthy, st.HTTPAddr}}
	if !st.Installed {
		s.State = "not_installed"
	} else if !st.Running {
		s.State = "stopped"
	}
	if output == "json" {
		_ = json.NewEncoder(os.Stdout).Encode(s)
	} else {
		fmt.Printf("binary:  %s\n", st.BinPath)
		fmt.Printf("config:  %s\n", st.ConfigPath)
		fmt.Printf("installed: %v\n", st.Installed)
		fmt.Printf("running:   %v\n", st.Running)
		fmt.Printf("healthy:   %v (http %s)\n", st.Healthy, st.HTTPAddr)
	}

	if !st.Installed {
		return exitStatusNotInstalled
	}
	if !st.Running {
		return exitStatusStopped
	}
	return exitStatusRunning
}

// defaultDataDir returns ~/cartographer-data, the default --data for
// `service install`, falling back to a relative path if the home directory
// cannot be resolved.
func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "cartographer-data"
	}
	return filepath.Join(home, "cartographer-data")
}
