package service

// The Windows half of the Manager (D217): a per-user Scheduled Task instead of
// an SCM service, driven by the PowerShell ScheduledTasks cmdlets.
//
// Two rules govern every command below, and both are inherited rather than new.
// Liveness is judged on **exit codes, never on printed text**, because the text
// is localised — `schtasks /Query` on an Italian Windows prints `In esecuzione`,
// which is why it is not used at all; `Get-ScheduledTask` returns a state whose
// string form (Ready, Running, Disabled) is locale-invariant, and even that is
// compared inside PowerShell so what crosses the process boundary is an exit
// code. And "installed" means a file on disk: the task XML is written where
// Status and EffectiveConfigPath can read it back, exactly as the plist and the
// unit are on the other two platforms.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
)

const (
	// windowsTaskFolder is the Task Scheduler folder both tasks live in, so
	// `Get-ScheduledTask -TaskPath \Cartographer\` shows exactly ours and an
	// uninstall cannot touch anything else.
	windowsTaskFolder = "Cartographer"
	// windowsServeTaskName / windowsSyncTaskName are the two task names inside
	// that folder. Distinct for the same reason the launchd labels are
	// (TestSyncTimerFilesAreDistinctFromServerService).
	windowsServeTaskName = "Serve"
	windowsSyncTaskName  = "Sync"
	// windowsTrayTaskName is the third task: the status icon (D1482).
	windowsTrayTaskName = "Tray"
	// windowsWslKeepAliveTaskName holds a WSL distro up for the server inside
	// it; registered from the distro through interop (wslkeepalive.go).
	windowsWslKeepAliveTaskName = "WslKeepAlive"
)

// The budgets of stopServeAndWait. Variables only so tests can shrink them.
var (
	// windowsDrainTimeout bounds how long the server gets to exit after the
	// shutdown event is set. It is the drain's budget, so it must exceed
	// serve's own shutdownHTTPTimeout (10s) plus its push flush. Replace's
	// poll deadline starts only after the stop returns, so the two do not
	// compete.
	windowsDrainTimeout = 20 * time.Second
	// windowsKillTimeout bounds the wait after Stop-ScheduledTask, which
	// returns before the process has exited (#411): it is a kill, so it needs
	// only the time Windows takes to tear the process down and free the port.
	windowsKillTimeout = 10 * time.Second
	// windowsDrainPoll paces both waits.
	windowsDrainPoll = 250 * time.Millisecond
	// windowsSleep paces the polls. The wait's budget is the sum of the
	// intervals slept, not the wall clock, so a test that replaces this with a
	// no-op gets an outcome that depends on the poll count and not on how
	// fast the runner is (#1339).
	windowsSleep = time.Sleep
)

// serveAddrAnswers reports whether something accepts TCP connections on the
// server's http address — a test seam over a real dial, which would otherwise
// reach whatever the test host has listening on the default port.
var serveAddrAnswers = func(addr string) bool {
	target := dialAddr(addr)
	if target == "" {
		return false
	}
	c, err := net.DialTimeout("tcp", target, 500*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// dialAddr turns a server http address into one a client can dial: a bare
// port or a wildcard bind (0.0.0.0, ::) becomes loopback, because dialing
// 0.0.0.0 fails on Windows and would read as "nothing listens".
func dialAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// noticef tells the operator something the Manager chose to do instead of
// what was asked, without failing: a test seam over stderr, where the
// package's other notices go (Install's "created <dir>").
var noticef = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// setShutdownEvent is signalShutdownEvent behind a test seam: the real one is
// build-tagged (shutdownevent_windows.go) and cannot run on a unix host, while
// the Windows branch of signalGraceful must still be testable there — goos is a
// package var, so every other test reaches these branches from macOS or Linux.
var setShutdownEvent = signalShutdownEvent

// windowsTaskPathArg is the -TaskPath value the cmdlets take: a leading and a
// trailing backslash around the folder name.
func windowsTaskPathArg() string { return `\` + windowsTaskFolder + `\` }

// psQuote renders s as a PowerShell single-quoted string. Single quotes mean
// "no expansion of any kind" there, so a path containing $, ` or a space needs
// no other escaping; a literal single quote is doubled.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// powershell runs one script through the interpreter that exists on every
// supported Windows. Never `pwsh`: PowerShell 7 is an optional install, and a
// service manager that only works where someone installed a newer shell is not
// a service manager.
//
// $ErrorActionPreference='Stop' is not decoration: without it a cmdlet's
// non-terminating error leaves powershell.exe exiting 0, and every check here
// reads the exit code.
func (m *Manager) powershell(script string) (string, error) {
	return m.powershellAt("powershell.exe", script)
}

// powershellAt is powershell with the interpreter's path: inside WSL it is the
// one interop resolved (wsl.Info.PowerShell), which need not be on PATH.
func (m *Manager) powershellAt(exe, script string) (string, error) {
	return m.run(exe, "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; "+script)
}

// taskDeniedExit is the exit code taskCmd's script uses for "the scheduler
// refused this account the right to change the task" (#1961), so the mapping
// reads an exit code and never the localised text of the error.
const taskDeniedExit = 5

// taskNotDisabledExit is the exit code windowsTaskDisabled's script uses for
// "the task is enabled", apart from 0 (disabled) and any failure of the probe.
const taskNotDisabledExit = 4

// taskCmd runs a script that changes or starts the serve task. A refusal for
// lack of rights on the task (an access-denied CIM error, which PowerShell
// reports as PermissionDenied or an UnauthorizedAccessException) exits
// taskDeniedExit, and the error then says whose task it is and what to do.
func (m *Manager) taskCmd(name, script string) (string, error) {
	wrapped := "try { " + script + " } catch { $e = $_.Exception; " +
		"if ($_.CategoryInfo.Category -eq 'PermissionDenied' -or $e -is [System.UnauthorizedAccessException] -or $e.HResult -eq -2147024891 -or $e.NativeErrorCode -eq 'AccessDenied') { exit " + fmt.Sprint(taskDeniedExit) + " }; throw }"
	out, err := m.powershell(wrapped)
	if err == nil {
		return out, nil
	}
	var ex interface{ ExitCode() int }
	if errors.As(err, &ex) && ex.ExitCode() == taskDeniedExit {
		return out, m.taskDeniedError(name, err)
	}
	return out, err
}

// ErrTaskAccessDenied marks a change to a scheduled task the scheduler refused
// this session for lack of rights on the task.
var ErrTaskAccessDenied = errors.New("access denied on a scheduled task")

// taskRemedy is the command that registers each task again, by task name.
func taskRemedy(name string) string {
	switch name {
	case windowsSyncTaskName:
		return "cartographer service sync-timer install"
	case windowsTrayTaskName:
		return "cartographer service tray install"
	}
	return "cartographer service install"
}

// taskDeniedError names the task's owner (best effort: reading it may be denied
// too) and the way out. A task an older install registered from an elevated
// shell belongs to Administrators: one install from an elevated shell hands it
// to the user (the definition names the user's SID), and after that a normal
// session administers it.
func (m *Manager) taskDeniedError(name string, cause error) error {
	owner := "an administrator"
	script := fmt.Sprintf("$sd = (Get-ScheduledTask %s).SecurityDescriptor; (New-Object System.Security.AccessControl.RawSecurityDescriptor $sd).Owner.Translate([System.Security.Principal.NTAccount]).Value", taskSelector(name))
	if out, err := m.powershell(script); err == nil && strings.TrimSpace(out) != "" {
		owner = strings.TrimSpace(out)
	}
	return fmt.Errorf("service: %w %s%s: it is owned by %s, so this session cannot change it. Run `%s` once from an elevated (administrator) terminal to hand the task to your user: %w", ErrTaskAccessDenied, windowsTaskPathArg(), name, owner, taskRemedy(name), cause)
}

// taskSelector is the `-TaskName x -TaskPath \Cartographer\` pair every cmdlet
// below needs.
func taskSelector(name string) string {
	return fmt.Sprintf("-TaskName %s -TaskPath %s", psQuote(name), psQuote(windowsTaskPathArg()))
}

// registerWindowsTask (re)registers a task from its XML on disk. -Force makes
// it an overwrite when the task already exists, which is what keeps Install
// idempotent.
//
// The definition is passed as a *string* read out of the file, not as a path:
// `Register-ScheduledTask -Xml` accepts the document itself, while
// `schtasks /Create /XML` requires the file to be UTF-16LE with a BOM — a
// constraint that would make the definition unreadable to an ordinary UTF-8
// read, and EffectiveConfigPath has to read it back.
func (m *Manager) registerWindowsTask(name, xmlPath string) error {
	script := fmt.Sprintf("Register-ScheduledTask %s -Xml (Get-Content -Raw -Encoding UTF8 -LiteralPath %s) -Force | Out-Null",
		taskSelector(name), psQuote(xmlPath))
	if _, err := m.taskCmd(name, script); err != nil {
		return fmt.Errorf("service: register scheduled task %s: %w", name, err)
	}
	return nil
}

// windowsTaskDisabled reports whether the task is Disabled, as Stop leaves it.
// Only an answer of "enabled" skips the Enable: a probe that failed reads as
// disabled, so the Enable runs as it always did.
func (m *Manager) windowsTaskDisabled(name string) bool {
	script := fmt.Sprintf("if ((Get-ScheduledTask %s).State -eq 'Disabled') { exit 0 } else { exit %d }", taskSelector(name), taskNotDisabledExit)
	_, err := m.powershell(script)
	if err == nil {
		return true
	}
	var ex interface{ ExitCode() int }
	return !(errors.As(err, &ex) && ex.ExitCode() == taskNotDisabledExit)
}

func (m *Manager) startWindowsTask(name string) error {
	// Enable first when Stop disabled the task (see Stop): starting a disabled
	// task succeeds silently without running anything — so a failed Enable is
	// an error, not something to start past. An enabled task is not touched:
	// Enable needs write rights on the task, and a session that only has the
	// right to start it (#1961) must still be able to restart it.
	if m.windowsTaskDisabled(name) {
		if _, err := m.taskCmd(name, fmt.Sprintf("Enable-ScheduledTask %s | Out-Null", taskSelector(name))); err != nil {
			return fmt.Errorf("service: enable scheduled task %s: %w", name, err)
		}
	}
	if _, err := m.taskCmd(name, fmt.Sprintf("Start-ScheduledTask %s", taskSelector(name))); err != nil {
		return fmt.Errorf("service: start scheduled task %s: %w", name, err)
	}
	return nil
}

func (m *Manager) stopWindowsTask(name string) error {
	if _, err := m.taskCmd(name, fmt.Sprintf("Stop-ScheduledTask %s", taskSelector(name))); err != nil {
		return fmt.Errorf("service: stop scheduled task %s: %w", name, err)
	}
	// A stopped service must stay stopped (D156): the logon trigger and
	// restart-on-failure would otherwise bring it straight back, which is the
	// same reason the launchd branch disables the job before killing it.
	if _, err := m.taskCmd(name, fmt.Sprintf("Disable-ScheduledTask %s | Out-Null", taskSelector(name))); err != nil {
		return fmt.Errorf("service: disable scheduled task %s: %w", name, err)
	}
	return nil
}

// unregisterWindowsTask removes the registration. A failure is an error only
// while the task is still registered: "not registered" is already the state
// the caller wants (the same shape as `launchctl bootout` on an unloaded job),
// but a task that survives keeps its logon trigger and would restart the
// program at the next login while the caller reports "uninstalled".
func (m *Manager) unregisterWindowsTask(name string) error {
	_, err := m.powershell(fmt.Sprintf("Unregister-ScheduledTask %s -Confirm:$false", taskSelector(name)))
	if err == nil {
		return nil
	}
	registered, probeErr := m.windowsTaskPresence(name)
	if probeErr != nil {
		return fmt.Errorf("service: could not unregister scheduled task %s and could not tell whether it is still registered: %w", name, errors.Join(err, probeErr))
	}
	if registered {
		return fmt.Errorf("service: could not unregister scheduled task %s, it is still registered and would start again at the next login (run `Unregister-ScheduledTask -TaskName %s -TaskPath %s`): %w", name, name, windowsTaskPathArg(), err)
	}
	return nil
}

// taskNotFoundExit is the exit code windowsTaskPresence's script uses for "the
// scheduler does not know this task", so that a PowerShell that failed for
// another reason (no ScheduledTasks module, no permission) is not read as it.
const taskNotFoundExit = 3

// windowsTaskPresence says whether the scheduler knows the task, telling "not
// registered" (the script's own exit code) from a probe that failed.
func (m *Manager) windowsTaskPresence(name string) (registered bool, err error) {
	script := fmt.Sprintf("if (Get-ScheduledTask %s -ErrorAction SilentlyContinue) { exit 0 } else { exit %d }", taskSelector(name), taskNotFoundExit)
	_, err = m.powershell(script)
	if err == nil {
		return true, nil
	}
	var ex interface{ ExitCode() int }
	if errors.As(err, &ex) && ex.ExitCode() == taskNotFoundExit {
		return false, nil
	}
	return false, err
}

// disableUnitNow runs `systemctl --user disable --now` and, when it fails,
// checks the unit is neither enabled nor active any more: a failure on a unit
// that is not installed is fine, one on a unit that stays enabled, or that was
// disabled but not stopped, is not, since the caller is about to delete its file.
func (m *Manager) disableUnitNow(unit string) error {
	_, err := m.run("systemctl", "--user", "disable", "--now", unit)
	if err == nil {
		return nil
	}
	if _, enabled := m.run("systemctl", "--user", "is-enabled", unit); enabled == nil {
		return fmt.Errorf("service: could not disable %s, it is still enabled: %w", unit, err)
	}
	if _, active := m.run("systemctl", "--user", "is-active", unit); active == nil {
		return fmt.Errorf("service: %s was disabled but could not be stopped, it is still running: %w", unit, err)
	}
	return nil
}

// windowsTaskRegistered reports whether the scheduler knows the task, on the
// cmdlet's exit status alone. This is the analogue of `launchctl print`
// succeeding, and like it, it says the job is known — not that a process is
// alive (see Status.Running).
func (m *Manager) windowsTaskRegistered(name string) bool {
	_, err := m.powershell(fmt.Sprintf("Get-ScheduledTask %s | Out-Null", taskSelector(name)))
	return err == nil
}

// windowsTaskRunning reports whether the task's action is currently executing.
// The State comparison happens inside PowerShell and only its exit code crosses
// back, so no localised string is ever parsed here. Used exclusively to know
// when a drain has finished.
func (m *Manager) windowsTaskRunning(name string) bool {
	script := fmt.Sprintf("if ((Get-ScheduledTask %s).State -eq 'Running') { exit 0 } else { exit 1 }", taskSelector(name))
	_, err := m.powershell(script)
	return err == nil
}

// windowsTaskStopped reports whether the registered task is one Stop left (or
// a crash did): Disabled, or Ready (enabled, no process). The State comparison
// happens inside PowerShell and only a token we define crosses back, so no
// localised string is parsed. It fails open: an error or any other output
// (Running, Queued, an unknown state, a format change) reads as live, so a live
// service is never hidden from upgrade-repair. The script deliberately does not
// contain the State -eq 'Running' text windowsTaskRunning uses.
func (m *Manager) windowsTaskStopped(name string) bool {
	script := fmt.Sprintf("$s = (Get-ScheduledTask %s).State; if ($s -eq 'Disabled' -or $s -eq 'Ready') { 'stopped' } else { 'live' }", taskSelector(name))
	out, err := m.powershell(script)
	return err == nil && strings.TrimSpace(out) == "stopped"
}

// windowsTaskUser is the account the serve task's logon trigger is scoped to:
// the user running install ("HOST\\name" or "DOMAIN\\name"). A test seam, and ""
// when the name cannot be resolved (see RenderWindowsTaskXML).
var windowsTaskUser = func() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
}

// windowsTaskSID is the SID of the installing user, for the task's descriptor
// (user.Current().Uid is the SID on Windows). A test seam; "" omits the
// descriptor (see RenderWindowsTaskXML).
var windowsTaskSID = func() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Uid
}

// windowsElevated reports whether this process runs with an elevated token
// (shutdownevent_windows.go's sibling, elevated_windows.go). A test seam.
var windowsElevated = processElevated

// windowsTaskUserLabel names the account the tasks are registered for, for the
// notice an elevated install prints.
func windowsTaskUserLabel() string {
	if u := windowsTaskUser(); u != "" {
		return u
	}
	return "the current user"
}

// installWindows writes the task definition and registers it, mirroring
// installDarwin/installLinux.
func (m *Manager) installWindows(binPath, configPath string, env ...EnvVar) error {
	logPath, err := WindowsLogPath()
	if err != nil {
		return fmt.Errorf("service: resolve log path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fmt.Errorf("service: create log dir: %w", err)
	}
	taskPath, err := WindowsTaskPath()
	if err != nil {
		return fmt.Errorf("service: resolve task path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		return fmt.Errorf("service: create tasks dir: %w", err)
	}
	// Register first: a refused registration (an old task owned by
	// Administrators) leaves the running server untouched, instead of stopped
	// with nothing to start it again, and the installed definition as it was
	// (#1961).
	if err := m.registerAndReplace(windowsServeTaskName, taskPath, []byte(RenderWindowsTaskXML(binPath, configPath, logPath, windowsTaskUser(), windowsTaskSID(), envFileArg(configPath, env)...)), writeDefinition); err != nil {
		return err
	}
	m.stopServeForReinstall(configHTTPAddr(configPath))
	return m.startWindowsTask(windowsServeTaskName)
}

// RefreshTaskOwners brings the installed Windows tasks (Serve, Sync, Tray) up
// to the definition that names their user (#1961): for each one whose
// definition file exists without a descriptor it re-renders the definition for
// the binary (and config, interval) the installed one names and registers it,
// without stopping or starting anything, so a running server or tray is
// untouched. It does nothing off Windows or when the user's SID cannot be
// resolved. It returns the names it re-registered; a task still owned by
// Administrators fails with ErrTaskAccessDenied, joined over the tasks, and the
// caller says what to do once.
func (m *Manager) RefreshTaskOwners() (refreshed []string, err error) {
	if goos != "windows" || windowsTaskSID() == "" {
		return nil, nil
	}
	var errs []error
	for _, t := range []struct {
		name   string
		path   func() (string, error)
		render func(data []byte, bin string) (string, error)
	}{
		{windowsServeTaskName, WindowsTaskPath, func(data []byte, bin string) (string, error) {
			configPath, err := extractTaskConfigPath(data)
			if err != nil {
				return "", err
			}
			logPath, err := WindowsLogPath()
			if err != nil {
				return "", err
			}
			return RenderWindowsTaskXML(bin, configPath, logPath, windowsTaskUser(), windowsTaskSID()), nil
		}},
		{windowsSyncTaskName, SyncWindowsTaskPath, func(data []byte, bin string) (string, error) {
			logPath, err := SyncWindowsLogPath()
			if err != nil {
				return "", err
			}
			return RenderWindowsSyncTaskXML(bin, logPath, intervalFromTaskXML(string(data)), windowsTaskUser(), windowsTaskSID()), nil
		}},
		{windowsTrayTaskName, TrayWindowsTaskPath, func(_ []byte, bin string) (string, error) {
			logPath, err := TrayWindowsLogPath()
			if err != nil {
				return "", err
			}
			return RenderWindowsTrayTaskXML(bin, logPath, windowsTaskUser(), windowsTaskSID()), nil
		}},
	} {
		done, err := m.refreshTaskOwner(t.name, t.path, t.render)
		if err != nil {
			errs = append(errs, err)
		} else if done {
			refreshed = append(refreshed, t.name)
		}
	}
	return refreshed, errors.Join(errs...)
}

// writeDefinition is registerAndReplace's plain writer.
func writeDefinition(path string, data []byte) error { return os.WriteFile(path, data, 0o644) }

// registerAndReplace registers a task from a side file and replaces the
// installed definition only once the scheduler accepted it. The file is the
// authority for "installed" and for whether the task already has its
// descriptor (refreshTaskOwner reads it): a refused registration must leave it
// as it was, or the refresh would skip a task the scheduler still holds in the
// old form, for good (#1961). write is the caller's writer (the tray's guards
// against symlinks).
func (m *Manager) registerAndReplace(name, path string, def []byte, write func(path string, data []byte) error) error {
	next := path + ".new"
	if err := write(next, def); err != nil {
		return fmt.Errorf("service: write %s task definition: %w", name, err)
	}
	defer os.Remove(next)
	if err := m.registerWindowsTask(name, next); err != nil {
		return err
	}
	if err := os.Rename(next, path); err != nil {
		return fmt.Errorf("service: replace %s task definition: %w", name, err)
	}
	return nil
}

// refreshTaskOwner is RefreshTaskOwners for one task. The new definition is
// registered from a side file and replaces the installed one only once the
// scheduler accepted it: the file is the authority for "installed", and a
// refused registration must leave it as it was, so the next run tries again.
func (m *Manager) refreshTaskOwner(name string, pathFn func() (string, error), render func([]byte, string) (string, error)) (bool, error) {
	taskPath, err := pathFn()
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(taskPath)
	if err != nil || strings.Contains(string(data), "<SecurityDescriptor>") {
		return false, nil
	}
	binPath, err := extractTaskBinary(data)
	if err != nil {
		return false, fmt.Errorf("service: read the installed %s task definition: %w", name, err)
	}
	def, err := render(data, binPath)
	if err != nil {
		return false, fmt.Errorf("service: read the installed %s task definition: %w", name, err)
	}
	if err := m.registerAndReplace(name, taskPath, []byte(def), writeDefinition); err != nil {
		return false, err
	}
	return true, nil
}

// stopServeForReinstall ends a serve task that is already running, so the start
// that follows runs the definition just written. MultipleInstancesPolicy
// IgnoreNew makes Start-ScheduledTask a silent no-op on a running task, so
// without this a re-install reported "installed and started" while the old
// process kept serving the old config — the counterpart of the launchctl bootout
// installDarwin does before bootstrap. Best-effort by contract, like bootout: a
// server that survives the stop is left for the start to report.
func (m *Manager) stopServeForReinstall(addr string) {
	_ = m.stopServeAndWait(addr)
}

// serveHTTPAddr is the http address of the installed serve task, read from the
// config its definition names, or "" when that cannot be resolved (no
// definition, unreadable config, stdio transport). Only used to know when the
// old process has let go of its port, so "" narrows the wait to the task state
// rather than failing anything.
func (m *Manager) serveHTTPAddr() string {
	configPath, err := m.EffectiveConfigPath("")
	if err != nil {
		return ""
	}
	return configHTTPAddr(configPath)
}

// configHTTPAddr is the http address a config file declares, "" when it
// cannot be read.
func configHTTPAddr(configPath string) string {
	cfg, err := config.Load(configPath)
	if err != nil {
		return ""
	}
	return cfg.HTTP
}

// serveExited reports that the old server is gone: the task is no longer
// Running **and** nothing answers on its port. The task state alone is not
// proof — Stop-ScheduledTask returns, and the state can leave Running, while
// the process is still tearing down with the socket bound, and a start in that
// window dies on "only one usage of each socket address" (#411, D266).
func (m *Manager) serveExited(addr string) bool {
	return !m.windowsTaskRunning(windowsServeTaskName) && (addr == "" || !serveAddrAnswers(addr))
}

// waitServeExit polls serveExited until it holds or budget elapses.
func (m *Manager) waitServeExit(addr string, budget time.Duration) bool {
	// Budget in slept intervals, not wall-clock time: a poll that takes long
	// on a loaded host does not eat into the number of polls the server gets.
	for waited := time.Duration(0); ; waited += windowsDrainPoll {
		if m.serveExited(addr) {
			return true
		}
		if waited >= budget {
			return false
		}
		windowsSleep(windowsDrainPoll)
	}
}

// stopServeAndWait ends the running server and returns only once it has
// exited (D266): the one stop that Restart, Uninstall, Replace and a
// re-install share, because each of them used to act on the task while the
// old process still held the port and the index files.
//
// The graceful path comes first: the shutdown event drains in-flight requests
// and flushes pending pushes, exactly as SIGTERM does elsewhere. When the
// event cannot be opened — the server runs in another logon session (the
// event lives in the Local\ namespace, which is per session, so an SSH session
// cannot see a desktop server's event), or predates the event — or the drain
// outlives its budget, the task is stopped outright, and the wait starts
// again. A server with nothing running is already stopped.
func (m *Manager) stopServeAndWait(addr string) error {
	if m.serveExited(addr) {
		return nil
	}
	if err := setShutdownEvent(); err != nil {
		noticef("note: cannot reach the server's shutdown event from this logon session — the server runs in another session (e.g. the desktop, while this runs over SSH) or predates the event (%v); stopping the scheduled task instead, so in-flight requests are cut and pending pushes are not flushed", err)
	} else if m.waitServeExit(addr, windowsDrainTimeout) {
		return nil
	}
	_, stopErr := m.taskCmd(windowsServeTaskName, fmt.Sprintf("Stop-ScheduledTask %s", taskSelector(windowsServeTaskName)))
	if m.waitServeExit(addr, windowsKillTimeout) {
		return nil
	}
	cause := "the task is still Running"
	if !m.windowsTaskRunning(windowsServeTaskName) {
		cause = fmt.Sprintf("something still answers on %s", addr)
	}
	err := fmt.Errorf("service: the server did not exit after the graceful drain and Stop-ScheduledTask: %s", cause)
	if stopErr != nil {
		err = fmt.Errorf("%w (Stop-ScheduledTask: %v)", err, stopErr)
	}
	return err
}

// restartWindowsServe stops the server, waits for it to exit, and starts the
// task again: Restart's Windows branch and signalGraceful's.
//
// The explicit relaunch is the part with no counterpart on the other two
// platforms, and the reason is that **a Scheduled Task is not a supervisor**:
// launchd's KeepAlive and systemd's Restart=on-failure both bring a process
// back after a clean exit, while Task Scheduler's RestartOnFailure only reacts
// to a failure to start and a logon trigger only fires at logon. Nothing would
// restart a server that drained successfully.
//
// The start is unconditional even when the stop reports a survivor. If the old
// process is genuinely still up, MultipleInstancesPolicy IgnoreNew makes the
// start request a no-op, and the stop's error is returned; skipping the start
// instead would leave a server that exited one tick after the deadline down for
// good.
func (m *Manager) restartWindowsServe() error {
	stopErr := m.stopServeAndWait(m.serveHTTPAddr())
	startErr := m.startWindowsTask(windowsServeTaskName)
	return errors.Join(stopErr, startErr)
}

var taskCommandRe = regexp.MustCompile(`(?s)<Command>(.*?)</Command>`)
var taskArgumentsRe = regexp.MustCompile(`(?s)<Arguments>(.*?)</Arguments>`)

// extractTaskConfigPath reads the argument following "serve --config" out of a
// generated task definition's <Arguments> element.
//
// The splitter is quote-aware, unlike extractUnitConfigPath's strings.Fields:
// the arguments are rendered with every path quoted (quotePath), because
// `C:\Program Files\cartographer\server.yaml` is an ordinary path here, and
// splitting that on whitespace yields `"C:\Program` — a config path that does
// not exist, reported as if the operator had configured it.
func extractTaskConfigPath(data []byte) (string, error) {
	match := taskArgumentsRe.FindSubmatch(data)
	if match == nil {
		return "", fmt.Errorf("no Arguments element found")
	}
	return configArgAfter(splitQuotedArgs(xmlUnescape(string(match[1]))))
}

// splitQuotedArgs splits a command line on whitespace, keeping a double-quoted
// run together and dropping the quotes. It knows nothing about backslash
// escaping, deliberately: a Windows path ends in `\` only for a drive or share
// root, and treating `\"` as an escape would break `"C:\"` — the one case where
// a trailing backslash is real.
func splitQuotedArgs(s string) []string {
	var args []string
	var cur strings.Builder
	inQuote, started := false, false
	flush := func() {
		if started {
			args = append(args, cur.String())
			cur.Reset()
			started = false
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			started = true
		case !inQuote && (r == ' ' || r == '\t' || r == '\n' || r == '\r'):
			flush()
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	flush()
	return args
}

// xmlUnescape reverses xmlEscape. The five entities are the only ones the
// renderer produces, and a definition this package did not write is not
// something to guess at: an unknown entity is left verbatim, which surfaces as a
// config path that does not exist rather than as a silently wrong one.
func xmlUnescape(s string) string {
	r := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'", "&amp;", "&")
	return r.Replace(s)
}

// intervalFromTaskXML recovers the repetition interval from a rendered sync task
// definition, so status reports the period with no second source of truth. It
// looks inside <Repetition> on purpose: the server task carries another
// <Interval> under RestartOnFailure, and a search for the bare element would
// report a restart backoff as if it were a sync period.
func intervalFromTaskXML(xml string) time.Duration {
	start := strings.Index(xml, "<Repetition>")
	if start == -1 {
		return 0
	}
	end := strings.Index(xml[start:], "</Repetition>")
	if end == -1 {
		return 0
	}
	block := xml[start : start+end]
	open := strings.Index(block, "<Interval>")
	if open == -1 {
		return 0
	}
	rest := block[open+len("<Interval>"):]
	close := strings.Index(rest, "</Interval>")
	if close == -1 {
		return 0
	}
	return parseISO8601Duration(rest[:close])
}
