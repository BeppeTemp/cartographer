package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/service"
	"github.com/BeppeTemp/cartographer/internal/tray"
)

// trayGOOS is the platform cmdTray checks; a package var so a test can ask for
// an unsupported one without cross-compiling.
var trayGOOS = runtime.GOOS

// cmdTray runs the status icon: a separate process from `serve` (D1482), so it
// stays visible, shown stopped, when the server is down and can restart it. It reads
// /health and acts only through service.Manager and the OS "open".
func cmdTray(args []string) int {
	// Before anything touches a file: there is no tray to run elsewhere.
	if trayGOOS != "windows" && trayGOOS != "darwin" {
		fmt.Fprintln(os.Stderr, "Error: cartographer tray is supported on Windows and macOS only")
		return exitStatusError
	}
	fs := flag.NewFlagSet("tray", flag.ExitOnError)
	logFile := fs.String("log-file", "", "Append the tray's log to this file (a Scheduled Task has nowhere else to put it)")
	interval := fs.Duration("interval", tray.DefaultInterval, "How often the server is probed (minimum 1s)")
	configFlag := fs.String("config", "", "Local server config YAML (default: discovered from the installed service definition, else the standard path); passing it forces the local server, never the client config's")
	if code := noExtraArgs(fs, args); code != 0 {
		return code
	}
	if *interval < tray.MinInterval {
		fmt.Fprintf(os.Stderr, "Error: --interval must be at least %s\n", tray.MinInterval)
		return exitStatusError
	}

	// Before the log is opened and the first poll runs: a second tray on macOS
	// exits 0 without a line in the shared log.
	release, second := tray.SingleInstance()
	defer release()
	if second {
		return 0
	}

	if *logFile != "" {
		f, err := redirectServerLog(*logFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return exitStatusError
		}
		defer f.Close()
		detachOwnConsole() // the tray task's console window (D1479)
	}
	if exe, err := os.Executable(); err == nil {
		log.Printf("tray: started from %s", exe) // the first line of tray.log: the file exists once the tray ran
	}

	ui, err := tray.NewUI()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError
	}

	m := service.NewManager()
	statusFn := trayStatusFn(m, *configFlag)
	logf := log.Printf
	actions := tray.NewActions(m, logf)
	deps := tray.NewDeps(statusFn, trayServerLogPath())
	deps.Busy = actions.Busy
	deps.Agents = trayAgents
	deps.Client = trayClientTarget
	deps.ForceLocal = trayConfigPassed(fs) // a hand use: the local server, as before
	deps.Logf = logf

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runTray(ctx, ui, deps, *interval, actions)
}

// trayStatusFn is the tray's view of the service on every tick: the light
// Manager.TrayView, which runs no scheduler command (a PowerShell or launchctl
// start every 5 s is what it replaced) and no /health probe, the tray probes
// that itself.
func trayStatusFn(m *service.Manager, configFlag string) func() (service.Status, error) {
	return func() (service.Status, error) { return m.TrayView(configFlag) }
}

// runTray polls until ctx ends or the UI returns. A signal only cancels ctx, so
// once the poll loop has ended the updates channel is closed: that is what makes
// ui.Run return (the contract on tray.UI), and the icon goes away.
func runTray(ctx context.Context, ui tray.UI, deps tray.Deps, interval time.Duration, actions *tray.Actions) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	updates, emit, closeUpdates := tray.Latest()
	// The menu opening asks for fresh agent rows (the UI reports
	// tray.ActionMenuOpened); the send never blocks the UI thread, and a pending
	// request already says it.
	requests := make(chan struct{}, 1)
	deps.AgentRequests = requests
	onAction := func(a tray.Action) {
		if a == tray.ActionMenuOpened {
			select {
			case requests <- struct{}{}:
			default:
			}
			return
		}
		actions.Handle(a)
	}
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		tray.Run(ctx, deps, interval, func(s tray.Snapshot) {
			actions.Observe(s)
			emit(s)
		})
		closeUpdates()
	}()

	err := ui.Run(tray.DefaultMenu(), updates, onAction)
	cancel() // the user quit: stop polling
	<-pollDone
	actions.Wait()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError
	}
	return 0
}

// trayServerLogPath is the log "Open log" shows: the server's, per platform.
func trayServerLogPath() string {
	var p string
	var err error
	switch trayGOOS {
	case "windows":
		p, err = service.WindowsLogPath()
	case "darwin":
		p, err = service.LaunchdLogPath()
	}
	if err != nil {
		return ""
	}
	return p
}

// trayAgents is the agent-client rows of the menu: what `cartographer status`
// reports, read without the service part (the tray probes that itself). The
// poll loop calls it at start, every tray.AgentsInterval, when the server comes
// back and when the menu opens.
func trayAgents() tray.Agents {
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return tray.Agents{}
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil || len(cfg.Agents) == 0 {
		return tray.Agents{Known: err == nil}
	}
	return agentRows(snapshotForConfig(dir, cfg, false))
}

// agentRows is the one place a status snapshot becomes the menu's rows: the
// connected providers, in the snapshot's order, up to tray.MaxAgents.
func agentRows(s statusSnapshot) tray.Agents {
	a := tray.Agents{Known: true}
	for _, p := range s.Providers {
		if !p.Connected || a.N == tray.MaxAgents {
			continue
		}
		a.Rows[a.N] = trayAgentRow(p)
		a.N++
	}
	return a
}

// trayConfigPassed reports whether --config was given explicitly: the tray then
// follows that local server config and never the client config's server.
func trayConfigPassed(fs *flag.FlagSet) bool {
	passed := false
	fs.Visit(func(f *flag.Flag) { passed = passed || f.Name == "config" })
	return passed
}

// trayClientTarget is the server of the client config, read on every tick (two
// small files, so a connect to another server is followed without restarting
// the tray). No client config file → Present false and the tray stays local; a
// file that cannot be parsed is an error, for the poll loop to keep the last
// good target. The token is resolved as sync does (literal, ${NAME}, then the
// private token file, which is what a tray started by launchd or the Task
// Scheduler can read).
func trayClientTarget() (tray.ClientTarget, error) {
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return tray.ClientTarget{}, err
	}
	cfg, err := clientconfig.Load(dir)
	if errors.Is(err, os.ErrNotExist) {
		return tray.ClientTarget{}, nil
	}
	if err != nil {
		return tray.ClientTarget{}, err
	}
	return tray.ClientTarget{
		Present:           true,
		ServerURL:         cfg.ServerURL,
		Token:             resolveToken(cfg),
		AllowInsecureHTTP: cfg.InsecureHTTPAllowed(),
		Auth:              cfg.Auth,
	}, nil
}

// trayAgentRow maps one provider of the status snapshot. Only "in_sync" and
// "drift" are judgements; every other state ("unknown" when the server, the
// manifests or the lockfile could not be read) is Not checked.
func trayAgentRow(p providerStatus) tray.AgentRow {
	row := tray.AgentRow{Name: p.Name}
	if d, ok := configurator.Lookup(configurator.Provider(p.Name)); ok && d.DisplayName != "" {
		row.Name = d.DisplayName
	}
	switch p.State {
	case "in_sync":
		row.Sync = tray.SyncOK
	case "drift":
		row.Sync = tray.SyncDrift
	}
	return row
}
