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

	"github.com/BeppeTemp/cartographer/internal/service"
	"github.com/BeppeTemp/cartographer/internal/tray"
)

// trayGOOS is the platform cmdTray checks; a package var so a test can ask for
// an unsupported one without cross-compiling.
var trayGOOS = runtime.GOOS

// detachConsoleFn releases the console window a Scheduled Task gives a
// console-subsystem binary (D1479, `detachOwnConsole`). That function lives on
// a branch that has not merged, so this is a no-op until it lands; the line
// that replaces it is `detachConsoleFn = detachOwnConsole`.
var detachConsoleFn = func() {}

// cmdTray runs the status icon: a separate process from `serve` (DPRNUM), so it
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
		detachConsoleFn()
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
	updates, emit := tray.Latest()
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		tray.Run(ctx, deps, *interval, func(s tray.Snapshot) {
			actions.Observe(s)
			emit(s)
		})
	}()

	err = ui.Run(tray.DefaultMenu(), updates, actions.Handle)
	stop()
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
