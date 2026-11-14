package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/service"
)

// The seams of connect's WSL awareness, beside servicewsl.go's.
var (
	wslServerRunsFn = func(d string) error { return service.NewManager().WslServerRuns(d) }
)

// refuseIPv6LoopbackForWsl refuses http://[::1]:<port> on Windows when the
// server is the WSL one: WSL forwards Windows' 127.0.0.1 (and localhost over
// IPv4) to the distro, never [::1], so that URL would reach nothing.
func refuseIPv6LoopbackForWsl(serverURL string) error {
	if serviceGOOS != "windows" {
		return nil
	}
	u, err := url.Parse(serverURL)
	if err != nil || u.Hostname() != "::1" {
		return nil
	}
	if d, _ := readWslMarkerFn(); d == "" {
		return nil
	}
	return fmt.Errorf("use 127.0.0.1: [::1] is not forwarded to WSL, where the server runs")
}

// recordWslServerOwner runs on Windows after a connect to a loopback server
// that answered: with no marker and no native server task, the server must be
// in a WSL distro, and the one whose `cartographer service status` exits 0 is
// recorded, so the service verbs act on it. Only running distros are asked
// (never start one). A machine without WSL, one with no running distro, a
// marker already set and a non-Windows host hear nothing; every other exit
// that records nothing prints one line to errw saying why (#2093).
func recordWslServerOwner(w, errw io.Writer) {
	if serviceGOOS != "windows" {
		return
	}
	if d, err := readWslMarkerFn(); err != nil || d != "" {
		return
	}
	running, err := wslListFn(true)
	if errors.Is(err, service.ErrNoWsl) {
		return
	}
	if err != nil {
		wslOwnerNote(errw, "wsl listing failed ("+err.Error()+")")
		return
	}
	if len(running) == 0 {
		return
	}
	if serveTaskInstalledFn() {
		wslOwnerNote(errw, "native Serve task registered")
		return
	}
	var failed []string
	for _, d := range running {
		if err := wslServerRunsFn(d); err != nil {
			failed = append(failed, d+": service status failed ("+err.Error()+")")
			continue
		}
		if err := writeWslMarkerFn(d); err != nil {
			wslOwnerNote(errw, "marker not written ("+err.Error()+")")
			return
		}
		fmt.Fprintf(w, "server: WSL distro %s\n", d)
		return
	}
	wslOwnerNote(errw, strings.Join(failed, "; "))
}

func wslOwnerNote(w io.Writer, reason string) {
	fmt.Fprintf(w, "note: WSL server owner not recorded: %s\n", reason)
}

// printWslServerHint runs on Windows when the loopback server does not
// answer, before a native install is offered: when a WSL distro hosts (or can
// host) the server, a native one would compete for the port, so the hint
// names the WSL command instead and it reports true. Off Windows, or with no
// distro carrying cartographer, it reports false and the native path stays.
func printWslServerHint(w io.Writer) bool {
	if serviceGOOS != "windows" {
		return false
	}
	if d, err := readWslMarkerFn(); err == nil && d != "" {
		fmt.Fprintf(w, "hint: the server runs in WSL distro %s: `cartographer service start` starts it there\n", d)
		return true
	}
	all, err := wslListFn(false)
	if err != nil {
		return false
	}
	for _, d := range all {
		if _, err := wslVersionFn(d); err == nil {
			fmt.Fprintf(w, "hint: cartographer is installed in WSL distro %s: run the server there with `cartographer service install --wsl %s` (the distro must be running)\n", d, d)
			return true
		}
	}
	return false
}

// printWslWindowsHint is the WSL side's one line after a connect: the
// Windows agents read %USERPROFILE%, which this binary never writes.
func printWslWindowsHint(w io.Writer) {
	if wslDetectFn().Interop {
		fmt.Fprintln(w, "Windows agents (Codex app, Claude desktop, VS Code) need `cartographer connect` run from PowerShell with the Windows binary.")
	}
}
