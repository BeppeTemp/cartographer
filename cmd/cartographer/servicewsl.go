package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/service"
)

// The seams of cartographer.exe's delegation to a server hosted in WSL.
var (
	serviceGOOS          = runtime.GOOS
	readWslMarkerFn      = service.ReadWslMarker
	writeWslMarkerFn     = service.WriteWslMarker
	removeWslMarkerFn    = service.RemoveWslMarker
	wslMarkerPathFn      = service.WslMarkerPath
	wslDelegateFn        = func(d string, a []string) (int, error) { return service.NewManager().DelegateToWsl(d, a) }
	wslVersionFn         = func(d string) (string, error) { return service.NewManager().WslCartographerVersion(d) }
	wslListFn            = func(running bool) ([]string, error) { return service.NewManager().ListWslDistros(running) }
	serveTaskInstalledFn = func() bool { return service.NewManager().ServeTaskRegistered() }
)

// wslDelegated are the verbs that act on the server, so on the WSL one when
// the marker says so. sync-timer and tray are client-side, ensure is cron's.
var wslDelegated = map[string]bool{"install": true, "uninstall": true, "start": true, "stop": true, "restart": true, "status": true}

const installShHint = "curl -fsSL https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh | sh"

// maybeDelegateToWsl runs a service verb inside WSL when this is Windows and
// the configured server is the WSL one: a marker exists, or `install` carries
// --wsl. It reports whether it handled the command. With no marker and no
// --wsl nothing changes: the native Scheduled Task is the service.
func maybeDelegateToWsl(target string, args []string) (int, bool) {
	if serviceGOOS != "windows" || !wslDelegated[target] {
		return 0, false
	}
	args, wslFlag, wslGiven := takeWslFlag(target, args)
	distro, err := readWslMarkerFn()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError, true
	}
	if wslGiven {
		if distro, err = pickWslDistro(wslFlag); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return exitStatusError, true
		}
	}
	if distro == "" {
		return 0, false
	}
	if p := windowsPathFlag(args); p != "" {
		fmt.Fprintf(os.Stderr, "Error: %s is a Windows path: --config and --data are paths inside WSL (for example $HOME/...)\n", p)
		return exitStatusError, true
	}
	if serveTaskInstalledFn() {
		marker, _ := wslMarkerPathFn()
		fmt.Fprintf(os.Stderr, "Error: a native server task (\\Cartographer\\Serve) is registered and the server is also configured in WSL distro %s: two servers would want one port. Keep one:\n", distro)
		fmt.Fprintf(os.Stderr, "  keep the native one: `wsl.exe -d %s -- cartographer service uninstall`, then delete %s\n", distro, marker)
		fmt.Fprintf(os.Stderr, "  keep the WSL one:    delete %s, run `cartographer service uninstall` here, then `cartographer service install --wsl %s`\n", marker, distro)
		return exitStatusError, true
	}
	v, err := wslVersionFn(distro)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cartographer is not installed in WSL distro %s (%v); install it there with:\n  wsl.exe -d %s\n  %s\n", distro, err, distro, installShHint)
		return exitStatusError, true
	}
	if v != version {
		fmt.Fprintf(os.Stderr, "Warning: cartographer in WSL distro %s is %s, this one is %s: update both (install.ps1 update, install.sh update)\n", distro, v, version)
	}
	code, err := wslDelegateFn(distro, append([]string{target}, args...))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return exitStatusError, true
	}
	if code == 0 {
		switch target {
		case "install":
			err = writeWslMarkerFn(distro)
		case "uninstall":
			err = removeWslMarkerFn()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "Warning: could not update the WSL server marker:", err)
		}
	}
	return code, true
}

// takeWslFlag removes `--wsl [<distro>]` (or `--wsl=<distro>`) from an
// install's arguments: cartographer.exe consumes it, the Linux side never
// sees it. A bare --wsl (followed by another flag, or last) means "the single
// running distro".
func takeWslFlag(target string, args []string) (rest []string, distro string, given bool) {
	if target != "install" {
		return args, "", false
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--wsl" || a == "-wsl":
			given = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				distro = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--wsl=") || strings.HasPrefix(a, "-wsl="):
			given = true
			_, distro, _ = strings.Cut(a, "=")
		default:
			rest = append(rest, a)
		}
	}
	return rest, distro, given
}

// pickWslDistro validates a named distro, or picks the single running one.
func pickWslDistro(named string) (string, error) {
	if named != "" {
		if !service.ValidWslDistro(named) {
			return "", fmt.Errorf("%q is not a WSL distro name", named)
		}
		return named, nil
	}
	running, err := wslListFn(true)
	if err != nil {
		return "", err
	}
	switch len(running) {
	case 1:
		return running[0], nil
	case 0:
		return "", fmt.Errorf("--wsl: no WSL distro is running; start one or name it (--wsl <distro>)")
	}
	return "", fmt.Errorf("--wsl: %d distros are running (%s); name one (--wsl <distro>)", len(running), strings.Join(running, ", "))
}

var windowsPathRe = regexp.MustCompile(`^([A-Za-z]:[\\/]|\\\\)`)

// windowsPathFlag returns the first --config/--data value that is a Windows
// path, "" when none is.
func windowsPathFlag(args []string) string {
	for i, a := range args {
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if name != "config" && name != "data" || !strings.HasPrefix(a, "-") {
			continue
		}
		if !hasVal {
			if i+1 >= len(args) {
				continue
			}
			val = args[i+1]
		}
		if windowsPathRe.MatchString(val) {
			return val
		}
	}
	return ""
}
