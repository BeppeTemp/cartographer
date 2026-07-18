package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

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

// cmdService manages the cartographer MCP server as a native per-user
// service: launchd on macOS, a systemd user unit on Linux.
func cmdService(args []string) int {
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
	default:
		fmt.Fprintln(os.Stderr, "Error: usage: cartographer service install|uninstall|start|stop|restart|status")
		return exitStatusError
	}
}

func cmdServiceInstall(args []string) int {
	fs := flag.NewFlagSet("service install", flag.ExitOnError)
	defaultConfig, _ := service.ConfigPath()
	configFlag := fs.String("config", defaultConfig, "Path to the generated server config YAML")
	dataFlag := fs.String("data", defaultDataDir(), "KB data directory (only used when generating a new config)")
	httpFlag := fs.String("http", "127.0.0.1:8080", "HTTP listen address (only used when generating a new config)")
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
	fs.Parse(args)

	if err := service.NewManager().Restart(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError
	}
	fmt.Println("service restarted")
	return exitStatusRunning
}

func cmdServiceStatus(args []string) int {
	fs := flag.NewFlagSet("service status", flag.ExitOnError)
	configFlag := fs.String("config", "", "Server config YAML to read the http address from (default: the standard path)")
	fs.Parse(args)

	st, err := service.NewManager().Status(*configFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError
	}

	fmt.Printf("binary:  %s\n", st.BinPath)
	fmt.Printf("config:  %s\n", st.ConfigPath)
	fmt.Printf("installed: %v\n", st.Installed)
	fmt.Printf("running:   %v\n", st.Running)
	fmt.Printf("healthy:   %v (http %s)\n", st.Healthy, st.HTTPAddr)

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
