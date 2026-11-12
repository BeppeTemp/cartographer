package service

// The WSL keep-alive: a Windows Scheduled Task, registered from inside the
// distro through interop, that holds the distro up so the server the Linux
// backend runs in it survives the last terminal closing. The task never owns
// the server: it runs `cartographer wsl keepalive`, nothing else.
//
// Unlike the native tasks there is no definition file: the XML goes to
// PowerShell inline as base64 (no file on \\wsl.localhost or /mnt/c), so
// "installed" is what the scheduler answers (WslKeepAliveRegistered).

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os/user"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/wsl"
)

// wslUser is the Linux account the keep-alive runs as (`wsl.exe -u`). A test
// seam.
var wslUser = func() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
}

// wslDetect is wsl.Detect behind a test seam: a test on a real WSL host must
// not reach Windows, and one elsewhere must be able to pretend.
var wslDetect = wsl.Detect

// installWslSide is the part of a Linux install that only WSL2 has: the
// keep-alive task and, for systemd, linger. Off WSL it does nothing at all.
func (m *Manager) installWslSide(backend Backend) ([]string, error) {
	info := wslDetect()
	if !info.Is || info.Version != 2 {
		return nil, nil
	}
	var warnings []string
	// A systemd user manager runs only while a logind session or linger
	// exists, and `wsl.exe -- cmd` (what the keep-alive task runs) need not
	// open a session: without linger the unit would not start after
	// `wsl --shutdown` or a reboot.
	if backend == BackendSystemd {
		if u := wslUser(); u != "" {
			if _, err := m.run("loginctl", "enable-linger", u); err != nil {
				warnings = append(warnings, fmt.Sprintf("`loginctl enable-linger %s` failed (%v): without linger the systemd user manager, and the server, may not start when the distro does; run it yourself", u, err))
			}
		}
	}
	w, err := m.InstallWslKeepAlive(info)
	if w != "" {
		warnings = append(warnings, w)
	}
	return warnings, err
}

// KeepAliveEnsure is the keep-alive's periodic step: on the cron backend it
// runs Ensure, because inside WSL nothing starts a cron daemon at distro boot,
// so after `wsl --shutdown` or a reboot no cron line would ever bring the
// server back. Ensure starts the server detached (its own session), so the
// keep-alive still does not own it. On systemd, or with nothing installed, it
// does nothing: the server is the user manager's.
func (m *Manager) KeepAliveEnsure() error {
	if goos != "linux" || linuxInstalledBackend() != BackendCron {
		return nil
	}
	cfg, err := m.EffectiveConfigPath("")
	if err != nil {
		return err
	}
	return m.Ensure(cfg)
}

// wslIdleTimeoutFix is the manual alternative to the task: WSL then never
// stops an idle distro. The key lives in [general], not [wsl2].
const wslIdleTimeoutFix = "add to %USERPROFILE%\\.wslconfig on Windows:\n  [general]\n  instanceIdleTimeout=-1\nthen run `wsl --shutdown` once"

// wslgDefault is where the WSL package installs wslg.exe when it is not on the
// Windows PATH.
const wslgDefault = `C:\Program Files\WSL\wslg.exe`

// InstallWslKeepAlive registers and starts `\Cartographer\WslKeepAlive` for
// the distro info names. S4U comes first (session 0, no window, no password);
// if the scheduler refuses it, the task is registered once more with an
// interactive token and wslg.exe (no window either). A visible terminal is
// never registered: when neither works, or Windows is unreachable, the result
// is a warning with the manual step, not an error, since the server itself is
// installed.
func (m *Manager) InstallWslKeepAlive(info wsl.Info) (warning string, err error) {
	if !info.Interop {
		return "cannot reach Windows (interop disabled), so the WSL keep-alive task was not created: the distro stops ~15 s after the last terminal closes. Create the task by hand, or " + wslIdleTimeoutFix, nil
	}
	ps := info.PowerShell
	binPath, err := osExecutable()
	if err != nil {
		return "", fmt.Errorf("service: resolve binary path: %w", err)
	}
	spec := WslKeepAliveSpec{Distro: info.Distro, User: wslUser(), Binary: resolveStableBinPath(binPath), Logon: WslLogonS4U}
	out, err := m.powershellAt(ps, "$i = [Security.Principal.WindowsIdentity]::GetCurrent(); $i.Name; $i.User.Value")
	if err != nil {
		return "the WSL keep-alive task was not created: the Windows account could not be resolved (" + err.Error() + "). " + wslIdleTimeoutFix, nil
	}
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l) // an account name may contain a space
		}
	}
	if len(lines) == 0 {
		return "the WSL keep-alive task was not created: Windows returned no account name. " + wslIdleTimeoutFix, nil
	}
	spec.Account = lines[0]
	if len(lines) > 1 {
		spec.SID = lines[1]
	}
	s4uErr := m.registerWslKeepAlive(ps, spec)
	if s4uErr != nil {
		var vErr *wslSpecError
		if errors.As(s4uErr, &vErr) {
			return "", vErr.err
		}
		wslg, lookErr := m.powershellAt(ps, fmt.Sprintf("$p = (Get-Command wslg.exe -ErrorAction SilentlyContinue).Source; if (-not $p) { $p = %s; if (-not (Test-Path -LiteralPath $p)) { exit %d } }; $p", psQuote(wslgDefault), taskNotFoundExit))
		if lookErr != nil || strings.TrimSpace(wslg) == "" {
			return fmt.Sprintf("the WSL keep-alive task was not created: the scheduler refused the S4U task (%v) and wslg.exe was not found. Create it by hand, or %s", s4uErr, wslIdleTimeoutFix), nil
		}
		spec.Logon, spec.Wslg = WslLogonInteractive, strings.TrimSpace(wslg)
		if err := m.registerWslKeepAlive(ps, spec); err != nil {
			return fmt.Sprintf("the WSL keep-alive task was not created: the scheduler refused it with S4U (%v) and with an interactive token (%v). Create it by hand, or %s", s4uErr, err, wslIdleTimeoutFix), nil
		}
	}
	// Stop first: IgnoreNew makes a start a no-op on a running keep-alive, which
	// would keep the previous binary path running. Stopping it ends wsl.exe and
	// its Linux child only; the server is the backend's and is not touched.
	sel := taskSelector(windowsWslKeepAliveTaskName)
	if _, err := m.powershellAt(ps, fmt.Sprintf("try { Stop-ScheduledTask %s } catch {}; Start-ScheduledTask %s", sel, sel)); err != nil {
		return "", fmt.Errorf("service: start scheduled task %s: %w", windowsWslKeepAliveTaskName, err)
	}
	return "", nil
}

// wslSpecError marks a spec RenderWslKeepAliveTaskXML refused: no logon type
// would fix it, so it is returned rather than retried.
type wslSpecError struct{ err error }

func (e *wslSpecError) Error() string { return e.err.Error() }

func (m *Manager) registerWslKeepAlive(ps string, spec WslKeepAliveSpec) error {
	def, err := RenderWslKeepAliveTaskXML(spec)
	if err != nil {
		return &wslSpecError{err}
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(def))
	script := fmt.Sprintf("Register-ScheduledTask %s -Xml ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'))) -Force | Out-Null", taskSelector(windowsWslKeepAliveTaskName), b64)
	if _, err := m.powershellAt(ps, script); err != nil {
		return fmt.Errorf("register scheduled task %s: %w", windowsWslKeepAliveTaskName, err)
	}
	return nil
}

// UninstallWslKeepAlive stops and unregisters the keep-alive task. A task the
// scheduler does not know is success; one that survives the unregister is an
// error, since it would hold the distro up again at the next logon. With no
// interop nothing can be reached, and that is said, not failed.
func (m *Manager) UninstallWslKeepAlive(info wsl.Info) error {
	if !info.Interop {
		noticef("note: cannot reach Windows (interop disabled): if a %s%s task exists, remove it from Windows with `Unregister-ScheduledTask -TaskName %s -TaskPath %s`", windowsTaskPathArg(), windowsWslKeepAliveTaskName, windowsWslKeepAliveTaskName, windowsTaskPathArg())
		return nil
	}
	sel := taskSelector(windowsWslKeepAliveTaskName)
	_, err := m.powershellAt(info.PowerShell, fmt.Sprintf("try { Stop-ScheduledTask %s } catch {}; Unregister-ScheduledTask %s -Confirm:$false", sel, sel))
	if err == nil {
		return nil
	}
	registered, probeErr := m.WslKeepAliveRegistered(info)
	if probeErr != nil {
		return fmt.Errorf("service: could not unregister scheduled task %s and could not tell whether it is still registered: %w", windowsWslKeepAliveTaskName, errors.Join(err, probeErr))
	}
	if registered {
		return fmt.Errorf("service: could not unregister scheduled task %s, it is still registered (run `Unregister-ScheduledTask -TaskName %s -TaskPath %s` on Windows): %w", windowsWslKeepAliveTaskName, windowsWslKeepAliveTaskName, windowsTaskPathArg(), err)
	}
	return nil
}

// WslKeepAliveRegistered says whether the scheduler knows the keep-alive task,
// telling "not registered" (the script's own exit code) from a probe that
// failed. Without interop it cannot tell, and says so.
func (m *Manager) WslKeepAliveRegistered(info wsl.Info) (bool, error) {
	if !info.Interop {
		return false, errors.New("cannot reach Windows (interop disabled)")
	}
	script := fmt.Sprintf("if (Get-ScheduledTask %s -ErrorAction SilentlyContinue) { exit 0 } else { exit %d }", taskSelector(windowsWslKeepAliveTaskName), taskNotFoundExit)
	_, err := m.powershellAt(info.PowerShell, script)
	if err == nil {
		return true, nil
	}
	var ex interface{ ExitCode() int }
	if errors.As(err, &ex) && ex.ExitCode() == taskNotFoundExit {
		return false, nil
	}
	return false, err
}
