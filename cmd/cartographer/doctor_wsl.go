package main

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/service"
	"github.com/BeppeTemp/cartographer/internal/wsl"
)

// The seams of the WSL checks: every one that reaches Windows goes through the
// service Manager (and so the scheduler guard).
var (
	wslKeepAliveFn = func(i wsl.Info) (bool, error) { return service.NewManager().WslKeepAliveRegistered(i) }
	wslConfigFn    = func(i wsl.Info) (string, bool, error) { return service.NewManager().ReadWslConfig(i) }
	wslDrvfsFn     = wsl.IsDrvfs
	// wslDataDirFn is the data dir of the server config the service uses, ""
	// when there is none to read.
	wslDataDirFn = func() string {
		p, err := service.NewManager().EffectiveConfigPath("")
		if err != nil {
			return ""
		}
		cfg, err := config.Load(p)
		if err != nil {
			return ""
		}
		return cfg.Data
	}
)

// checkWSL names the WSL failure modes, each with its fix. Outside WSL it
// returns nothing and calls nothing.
func checkWSL() []doctorFinding {
	info := wslDetectFn()
	if !info.Is {
		return nil
	}
	var out []doctorFinding
	add := func(sev, msg, fix string) {
		out = append(out, doctorFinding{Check: "wsl", Severity: sev, Message: msg, Fix: fix})
	}
	if info.Version == 1 {
		add(doctorWarning, "WSL1 is not supported; use WSL2", fmt.Sprintf("from Windows: wsl --set-version %s 2", orDistro(info.Distro)))
	}
	if d := wslDataDirFn(); d != "" {
		if drv, err := wslDrvfsFn(d); err == nil && drv {
			out = append(out, doctorFinding{Check: "wsl", Severity: doctorError, Path: d,
				Message: fmt.Sprintf("data dir %s is on a Windows drive (/mnt/...): no POSIX modes, no reliable locks, slow git", d),
				Fix:     "move it under $HOME and edit data: in the server config, then `cartographer service restart`"})
		}
	}
	if info.Version != 2 {
		return out
	}
	if !info.Interop {
		add(doctorInfo, "cannot check the keep-alive task or read .wslconfig: Windows interop is disabled in this distro", "")
		return out
	}
	wslconfig, _, cfgErr := wslConfigFn(info)
	if cfgErr != nil {
		add(doctorInfo, fmt.Sprintf("cannot read .wslconfig: %v", cfgErr), "")
	}
	idleOff := iniValue(wslconfig, "general", "instanceIdleTimeout") == "-1"
	if st, err := statusServiceFn(); err == nil && st.Installed && !idleOff {
		if ok, err := wslKeepAliveFn(info); err == nil && !ok {
			fix := "cartographer service install"
			if cfgErr == nil {
				fix += `, or set instanceIdleTimeout=-1 under [general] in %USERPROFILE%\.wslconfig and run wsl --shutdown`
			}
			add(doctorWarning, "the WSL keep-alive task is missing: the distro stops ~15 s after the last terminal closes and takes the server with it", fix)
		}
	}
	if strings.EqualFold(iniValue(wslconfig, "wsl2", "networkingMode"), "mirrored") {
		add(doctorInfo, "mirrored networking was not verified by Cartographer; 127.0.0.1:39273 should still work, [::1] may differ", "")
	}
	return out
}

func orDistro(d string) string {
	if d == "" {
		return "<distro>"
	}
	return d
}

// wslServerDistro is the distro the Windows marker names, "" off Windows or
// without one.
func wslServerDistro() string {
	if serviceGOOS != "windows" {
		return ""
	}
	d, _ := readWslMarkerFn()
	return d
}

// iniValue is key's value in section of a .wslconfig, "" when absent. Section
// and key names compare case-insensitively; # and ; start a comment line.
func iniValue(content, section, key string) string {
	cur := ""
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		l := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		switch {
		case l == "" || l[0] == '#' || l[0] == ';':
		case l[0] == '[' && strings.HasSuffix(l, "]"):
			cur = strings.TrimSpace(l[1 : len(l)-1])
		default:
			k, v, ok := strings.Cut(l, "=")
			if ok && strings.EqualFold(cur, section) && strings.EqualFold(strings.TrimSpace(k), key) {
				v, _, _ = strings.Cut(v, "#")
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}
