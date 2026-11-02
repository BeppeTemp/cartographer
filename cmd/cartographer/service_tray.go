package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// trayInstallFn and friends are indirected so the dispatch is testable without
// launchctl or PowerShell.
var (
	trayInstallFn   = func() error { return service.NewManager().InstallTray() }
	trayUninstallFn = func() error { return service.NewManager().UninstallTray() }
	trayInstalledFn = func() bool { return service.NewManager().TrayInstalled() }
)

const trayUnsupportedMsg = "the status icon is supported on Windows and macOS only"

func traySupported() bool { return trayGOOS == "windows" || trayGOOS == "darwin" }

// trayOptedOut reads the remembered opt-out (D1482); an unreadable client
// config reads as "not opted out", the default of the feature.
func trayOptedOut() bool {
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return false
	}
	cfg, err := clientconfig.Load(dir)
	return err == nil && cfg.TrayOptOut
}

// installTrayWithService is the tray half of `service install`: on by default,
// skipped by --no-tray (which is remembered) or a remembered opt-out. A failure
// is a warning only: the server is installed, and a session with no GUI (ssh)
// cannot show an icon.
func installTrayWithService(noTray bool) {
	if !traySupported() {
		return
	}
	if noTray {
		setTrayOptOut(true)
		// A reinstall with --no-tray must not leave the earlier tray behind.
		if trayInstalledFn() {
			if err := trayUninstallFn(); err != nil {
				fmt.Fprintln(os.Stderr, "Warning: the existing status icon was not removed:", err)
			}
		}
		return
	}
	if trayOptedOut() {
		return
	}
	if err := trayInstallFn(); err != nil {
		fmt.Fprintln(os.Stderr, "Warning: the status icon was not installed:", err)
		return
	}
	if trayInstalledFn() {
		fmt.Println("tray installed (status icon at logon; remove it with `cartographer service tray uninstall`)")
	}
}

// cmdServiceTray manages the status icon's autostart (D1482). `uninstall` is
// the explicit opt-out, remembered so upgrade-repair does not bring it back;
// `install` clears it.
func cmdServiceTray(args []string) int {
	if hasHelpFlag(args) {
		printServiceUsage(os.Stdout)
		return 0
	}
	action, rest := splitPositional(args, "")
	fs := flag.NewFlagSet("service tray "+action, flag.ExitOnError)
	if action == "install" || action == "uninstall" || action == "status" {
		if code := noExtraArgs(fs, rest); code != 0 {
			return code
		}
	}
	if !traySupported() && (action == "install" || action == "uninstall" || action == "status") {
		switch action {
		case "install":
			fmt.Fprintln(os.Stderr, "Error:", trayUnsupportedMsg)
			return exitStatusError
		case "uninstall":
			// Nothing to remove here; the choice is still recorded, silently,
			// so a config shared with another machine keeps it.
			setTrayOptOut(true)
			return 0
		default:
			fmt.Println("tray not installed:", trayUnsupportedMsg)
			return exitStatusNotInstalled
		}
	}
	switch action {
	case "install":
		if err := trayInstallFn(); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return exitStatusError
		}
		setTrayOptOut(false)
		fmt.Println("tray installed")
		return 0
	case "uninstall":
		if err := trayUninstallFn(); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return exitStatusError
		}
		fmt.Println("tray uninstalled")
		if setTrayOptOut(true) {
			fmt.Println("opt-out remembered: upgrade-repair will not reinstall it (reset with `cartographer service tray install`)")
		}
		return 0
	case "status":
		if !trayInstalledFn() {
			fmt.Println("tray not installed (cartographer service tray install)")
			return exitStatusNotInstalled
		}
		fmt.Println("tray installed")
		return exitStatusRunning
	default:
		fmt.Fprintln(os.Stderr, "Error: tray action required (install, uninstall, status)")
		printServiceUsage(os.Stderr)
		return exitStatusError
	}
}

// repairEnsureTrayFn is the tray step of upgrade-repair (D1482). It re-renders
// and restarts the tray on every run, so a tray left running by the previous
// binary (an in-place update moves the exe aside) picks up the new one, and an
// install that predates the tray gets it. Skipped when the operator opted out.
var repairEnsureTrayFn = func() error {
	if trayOptedOut() {
		return nil
	}
	return trayInstallFn()
}
