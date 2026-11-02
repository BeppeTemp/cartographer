package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/BeppeTemp/cartographer/internal/service"
	"github.com/BeppeTemp/cartographer/internal/tray"
)

// trayGOOS is the platform cmdTray checks; a package var so a test can ask for
// an unsupported one without cross-compiling.
var trayGOOS = runtime.GOOS

// cmdTray runs the status icon: a separate process from `serve` (D1482), so it
// stays visible, red, when the server is down and can restart it. It reads
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
	configFlag := fs.String("config", "", "Server config YAML (default: discovered from the installed service definition, else the standard path)")
	if code := noExtraArgs(fs, args); code != 0 {
		return code
	}
	if *interval < tray.MinInterval {
		fmt.Fprintf(os.Stderr, "Error: --interval must be at least %s\n", tray.MinInterval)
		return exitStatusError
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

	ui, err := tray.NewUI()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError
	}

	m := service.NewManager()
	statusFn := func() (service.Status, error) {
		cfg, err := m.EffectiveConfigPath(*configFlag)
		if err != nil {
			return service.Status{}, err
		}
		return m.Status(cfg)
	}
	logf := log.Printf
	actions := tray.NewActions(m, logf)
	deps := tray.NewDeps(statusFn, trayServerLogPath())
	deps.Busy = actions.Busy
	deps.Logf = logf

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runTray(ctx, ui, deps, *interval, actions)
}

// runTray polls until ctx ends or the UI returns. A signal only cancels ctx, so
// once the poll loop has ended the updates channel is closed: that is what makes
// ui.Run return (the contract on tray.UI), and the icon goes away.
func runTray(ctx context.Context, ui tray.UI, deps tray.Deps, interval time.Duration, actions *tray.Actions) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	updates, emit, closeUpdates := tray.Latest()
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		tray.Run(ctx, deps, interval, func(s tray.Snapshot) {
			actions.Observe(s)
			emit(s)
		})
		closeUpdates()
	}()

	err := ui.Run(tray.DefaultMenu(), updates, actions.Handle)
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
