package service

// The cron + supervisor backend (D1682): on a Linux host
// with no reachable systemd user manager the server is kept up by the user's
// crontab. A managed block holds an @reboot line and a watchdog every five
// minutes, both running `service ensure`, which starts the server detached
// unless it already runs or was stopped on purpose. The pidfile, the lock, the
// stop marker and the logs live in StateDir.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BeppeTemp/cartographer/internal/blocktext"
)

const (
	cronServerBegin = "# BEGIN cartographer server — managed by cartographer, do not edit"
	cronServerEnd   = "# END cartographer server"
)

// procOps are the process operations of the cron backend, a struct so tests
// replace them (Manager.proc; nil means the real ones).
type procOps struct {
	// start launches bin detached (new session, no stdio) with exactly env and
	// returns the child's pid.
	start   func(bin string, args, env []string) (int, error)
	alive   func(pid int) bool
	cmdline func(pid int) (line string, readable bool)
	signal  func(pid int, sig syscall.Signal) error
}

func (m *Manager) procs() *procOps {
	if m.proc != nil {
		return m.proc
	}
	return realProcOps()
}

// Budgets of the cron stop, variables so tests shrink them. They count polls
// (cronSleep), not wall-clock time, as the Windows stop does (#1339).
var (
	cronDrainTimeout = 20 * time.Second // the server drains for up to 10 s (serve.go)
	cronKillTimeout  = 5 * time.Second
	cronPoll         = 200 * time.Millisecond
	cronSleep        = time.Sleep
	ensureLockWait   = 10 * time.Second
	// listenerAt is ProbeListener behind a seam: a real dial reaches whatever
	// the test host has on the port. Cartographer and a foreign program (#1998)
	// are told apart: neither is fixed by spawning a server.
	listenerAt = func(addr string) ListenerState { return ProbeListener(addr, healthTimeout) }
	// environ is the environment the clean one is filtered from.
	environ = os.Environ
)

func statePath(name string) (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", fmt.Errorf("service: resolve state dir: %w", err)
	}
	return filepath.Join(dir, name), nil
}

// shQuote single-quotes s for a POSIX shell.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// checkCronPath refuses what a crontab line cannot carry: `%` becomes a newline
// in cron (and its escaping is dialect-dependent), a newline or NUL ends the line.
func checkCronPath(what, p string) error {
	if strings.ContainsAny(p, "%\n\r\x00") {
		return fmt.Errorf("service: the %s path %q contains %%, a newline or NUL, which a crontab line cannot carry; use another path", what, p)
	}
	return nil
}

func renderCronServerBlock(bin, cfg, stateDir string) string {
	cmd := fmt.Sprintf("%s service ensure --config %s --log-file %s", shQuote(bin), shQuote(cfg), shQuote(filepath.Join(stateDir, "supervisor.log")))
	// @reboot is not honoured by every cron (busybox crond ignores it); the
	// five-minute watchdog is the mechanism that works on all of them. Only the
	// five-field syntax and */n are used: the common subset of cronie, dcron and
	// busybox.
	return cronServerBegin + "\n@reboot " + cmd + "\n*/5 * * * * " + cmd + "\n" + cronServerEnd + "\n"
}

// cronDefinition is the file whose presence means "installed": the block as
// put in the crontab, behind bin=/config= comments the readers use (they must
// not parse shell quoting).
func cronDefinition(bin, cfg, block string) string {
	return "# bin=" + bin + "\n# config=" + cfg + "\n" + block
}

func cronDefinitionField(data []byte, key string) string {
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "# "+key+"="); ok {
			return v
		}
	}
	return ""
}

func extractCronBinary(data []byte) (string, error) {
	if v := cronDefinitionField(data, "bin"); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no bin= line found")
}

func cronDefinitionConfig() (string, error) {
	p, err := CronServerDefinitionPath()
	if err != nil {
		return "", fmt.Errorf("service: resolve cron definition path: %w", err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("service: read installed cron definition %s: %w", p, err)
	}
	cfg := cronDefinitionField(data, "config")
	if cfg == "" {
		return "", fmt.Errorf("service: installed cron definition %s does not declare a usable config", p)
	}
	return cfg, nil
}

// --- crontab I/O -----------------------------------------------------------

// readCrontab returns the user's crontab; "no crontab" is an empty one, any
// other failure is an error.
func (m *Manager) readCrontab() (string, error) {
	out, err := m.stdout("crontab", "-l")
	if err != nil {
		if isNoCrontab(out, err) {
			return "", nil
		}
		return "", fmt.Errorf("service: crontab -l: %w (cron may be refused for this account: cron.allow/cron.deny)", err)
	}
	return out, nil
}

// writeCrontab installs content as the user's crontab through a temp file and
// `crontab <file>`: `crontab -` is not supported by every implementation. An
// empty content removes the crontab.
func (m *Manager) writeCrontab(content string) error {
	if strings.TrimSpace(content) == "" {
		if _, err := m.run("crontab", "-r"); err != nil {
			return fmt.Errorf("service: crontab -r: %w", err)
		}
		return nil
	}
	dir, err := StateDir()
	if err != nil {
		return fmt.Errorf("service: resolve state dir: %w", err)
	}
	if err := mkdirSafe(dir); err != nil {
		return fmt.Errorf("service: create state dir: %w", err)
	}
	f, err := os.CreateTemp(dir, "crontab-*")
	if err != nil {
		return fmt.Errorf("service: write crontab file: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return fmt.Errorf("service: write crontab file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("service: write crontab file: %w", err)
	}
	if _, err := m.run("crontab", f.Name()); err != nil {
		return fmt.Errorf("service: crontab %s: %w", f.Name(), err)
	}
	return nil
}

// editCrontab upserts block between begin and end ("" strips the block). The
// user's other lines are never touched; an unchanged crontab is not rewritten;
// a corrupted block is refused.
func (m *Manager) editCrontab(begin, end, block string) error {
	cur, err := m.readCrontab()
	if err != nil {
		return err
	}
	var next string
	if block == "" {
		var o blocktext.Outcome
		next, o = blocktext.ReplaceBlock(cur, begin, end, "")
		switch o {
		case blocktext.Absent:
			return nil
		case blocktext.Corrupted:
			return corruptedCrontab()
		}
	} else if next, err = blocktext.Upsert(cur, begin, end, block); err != nil {
		return corruptedCrontab()
	}
	if next == cur {
		return nil
	}
	return m.writeCrontab(next)
}

func corruptedCrontab() error {
	return fmt.Errorf("service: your crontab holds a damaged cartographer block: %w; fix it by hand with `crontab -e`", blocktext.ErrCorruptedBlock)
}

// removeCronBlock strips the block and verifies it: when the write fails the
// crontab is read again, and only a block that is really gone counts as
// removed, so the caller deletes the definition file only then (#1584).
func (m *Manager) removeCronBlock(begin, end string) error {
	err := m.editCrontab(begin, end, "")
	if err == nil || errors.Is(err, blocktext.ErrCorruptedBlock) {
		return err
	}
	cur, readErr := m.readCrontab()
	if readErr == nil {
		if _, o := blocktext.ReplaceBlock(cur, begin, end, ""); o == blocktext.Absent {
			return nil
		}
	}
	return fmt.Errorf("service: the cartographer block is still in your crontab, definition kept so the command can be rerun: %w", err)
}

// --- install / uninstall ---------------------------------------------------

func (m *Manager) installCron(binPath, configPath string) ([]string, error) {
	if err := cronGuard(); err != nil {
		return nil, err
	}
	stateDir, err := StateDir()
	if err != nil {
		return nil, fmt.Errorf("service: resolve state dir: %w", err)
	}
	for what, p := range map[string]string{"binary": binPath, "config": configPath, "state": stateDir} {
		if err := checkCronPath(what, p); err != nil {
			return nil, err
		}
	}
	defPath, err := CronServerDefinitionPath()
	if err != nil {
		return nil, fmt.Errorf("service: resolve cron definition path: %w", err)
	}
	for _, d := range []string{stateDir, filepath.Dir(defPath)} {
		if err := mkdirSafe(d); err != nil {
			return nil, fmt.Errorf("service: create %s: %w", d, err)
		}
	}
	block := renderCronServerBlock(binPath, configPath, stateDir)
	if err := writeSafe(defPath, []byte(cronDefinition(binPath, configPath, block))); err != nil {
		return nil, fmt.Errorf("service: write cron definition: %w", err)
	}
	if err := m.editCrontab(cronServerBegin, cronServerEnd, block); err != nil {
		removeSafe(defPath)
		return nil, err
	}
	m.removeSystemdLeftover()

	// A re-install must run the new binary and config: stop the old process
	// first, as the systemd arm restarts an active unit.
	wasRunning := m.ownPID(configPath) != 0
	if err := m.clearStopMarker(); err != nil {
		return nil, err
	}
	if wasRunning {
		if err := m.cronStopAndWait(configPath); err != nil {
			return nil, err
		}
	}
	if err := m.ensure(binPath, configPath); err != nil {
		return nil, fmt.Errorf("service: the crontab entry is installed (the watchdog retries every 5 minutes) but the server did not start: %w", err)
	}
	var warnings []string
	if found, known := cronDaemonRunning(); known && !found {
		warnings = append(warnings, "no cron daemon is running: the server runs now but will not survive a reboot or a crash until you start it (rc.crond, rc-service crond start, ...)")
	}
	return warnings, nil
}

// removeSystemdLeftover removes a unit file of ours so the two backends never
// both exist; best effort, a missing systemctl is fine.
func (m *Manager) removeSystemdLeftover() {
	unit, err := SystemdUnitPath()
	if err != nil || !fileExists(unit) {
		return
	}
	if _, err := lookPath("systemctl"); err == nil {
		m.run("systemctl", "--user", "disable", "--now", systemdUnit)
	}
	removeSafe(unit)
}

// removeCronLeftover is the cron backend's uninstall run before a systemd
// install: the watchdog must not start a second server next to the unit.
func (m *Manager) removeCronLeftover() error {
	if p, err := CronServerDefinitionPath(); err != nil || !fileExists(p) || homeSandboxed() {
		return nil // nothing there, or a files-only install that touches no scheduler
	}
	return m.uninstallCron()
}

func (m *Manager) uninstallCron() error {
	if err := cronGuard(); err != nil {
		return err
	}
	defPath, err := CronServerDefinitionPath()
	if err != nil {
		return fmt.Errorf("service: resolve cron definition path: %w", err)
	}
	if !fileExists(defPath) {
		return nil
	}
	cfg, _ := cronDefinitionConfig() // not EffectiveConfigPath: a unit may coexist
	// The marker first, so the watchdog cannot restart what is being removed.
	stopErr := errors.Join(m.setStopMarker(), m.cronStopAndWait(cfg))
	if err := m.removeCronBlock(cronServerBegin, cronServerEnd); err != nil {
		return errors.Join(err, stopErr)
	}
	if err := removeSafe(defPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("service: remove cron definition: %w", err)
	}
	for _, n := range []string{"server.pid", "server.lock", "server.stopped"} {
		if p, err := statePath(n); err == nil {
			removeSafe(p)
		}
	}
	if stopErr != nil {
		return fmt.Errorf("service: cron entry removed, but its server is still running and holds its port and data files: %w", stopErr)
	}
	return nil
}

// --- start / stop / restart / status ---------------------------------------

func (m *Manager) cronDefinitionPresent() error {
	if p, err := CronServerDefinitionPath(); err == nil && !fileExists(p) {
		return fmt.Errorf("service: no cron definition at %s (run `cartographer service install`)", p)
	}
	return nil
}

func (m *Manager) startCron(b Backend) error {
	if b == BackendNone {
		return ErrNoServiceManager
	}
	if err := cronGuard(); err != nil {
		return err
	}
	if err := m.cronDefinitionPresent(); err != nil {
		return err
	}
	cfg, err := m.EffectiveConfigPath("")
	if err != nil {
		return err
	}
	if err := m.clearStopMarker(); err != nil {
		return err
	}
	bin, err := m.cronBin()
	if err != nil {
		return err
	}
	if err := m.ensure(bin, cfg); err != nil {
		return err
	}
	if m.ownPID(cfg) == 0 {
		if addr := configHTTPAddr(cfg); addr != "" {
			if err := busyAddressError(addr, listenerAt(addr)); err != nil {
				return err
			}
		}
		return fmt.Errorf("service: the server is not running after start (see server.log in the state directory)")
	}
	return nil
}

func (m *Manager) stopCron(b Backend) error {
	if b == BackendNone {
		return ErrNoServiceManager
	}
	if err := cronGuard(); err != nil {
		return err
	}
	if err := m.cronDefinitionPresent(); err != nil {
		return err
	}
	cfg, err := m.EffectiveConfigPath("")
	if err != nil {
		return err
	}
	// The marker first: a watchdog tick between the signal and the exit must
	// not start a second server.
	if err := m.setStopMarker(); err != nil {
		return err
	}
	return m.cronStopAndWait(cfg)
}

func (m *Manager) restartCron(b Backend) error {
	if err := m.stopCron(b); err != nil {
		return err
	}
	return m.startCron(b)
}

// cronBin is the program the definition runs, the one to start.
func (m *Manager) cronBin() (string, error) {
	bin, err := m.DefinitionBinary()
	if err != nil {
		return "", err
	}
	return bin, nil
}

// cronRunning is Status.Running for the cron backend: registered and not
// stopped on purpose, with a live process of ours or a /health answer.
func (m *Manager) cronRunning(configPath string) bool {
	if p, err := statePath("server.stopped"); err == nil && fileExists(p) {
		return false
	}
	if m.ownPID(configPath) != 0 {
		return true
	}
	addr := configHTTPAddr(configPath)
	return addr != "" && listenerAt(addr) == ListenerCartographer
}

func (m *Manager) setStopMarker() error {
	p, err := statePath("server.stopped")
	if err != nil {
		return err
	}
	if err := mkdirSafe(filepath.Dir(p)); err != nil {
		return fmt.Errorf("service: create state dir: %w", err)
	}
	if err := writeSafe(p, nil); err != nil {
		return fmt.Errorf("service: write stop marker: %w", err)
	}
	return nil
}

func (m *Manager) clearStopMarker() error {
	p, err := statePath("server.stopped")
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("service: clear stop marker: %w", err)
	}
	return nil
}

// ownPID returns the pid of the server this backend started, 0 when there is
// none. A pid is trusted only with a matching command line (it must name
// `serve` and the config), so a pid the kernel reused for another program is a
// stale pidfile, removed here.
func (m *Manager) ownPID(configPath string) int {
	p, err := statePath("server.pid")
	if err != nil {
		return 0
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	ops := m.procs()
	if err == nil && pid > 0 && ops.alive(pid) {
		line, readable := ops.cmdline(pid)
		if !readable || (strings.Contains(line, "serve") && strings.Contains(line, configPath)) {
			return pid
		}
	}
	os.Remove(p)
	return 0
}

// cronStopAndWait stops the server of this backend: SIGTERM, wait for the pid
// to go and the port to be free (the server drains for up to 10 s), then
// SIGKILL. Nothing of ours running is a success unless something else answers
// on the address.
func (m *Manager) cronStopAndWait(configPath string) error {
	addr := configHTTPAddr(configPath)
	pid := m.ownPID(configPath)
	if pid == 0 {
		if addr != "" && serveAddrAnswers(addr) {
			return fmt.Errorf("service: a server answers on %s but was not started by this service: stop it by hand", addr)
		}
		return nil
	}
	ops := m.procs()
	gone := func() bool { return !ops.alive(pid) && (addr == "" || !serveAddrAnswers(addr)) }
	wait := func(budget time.Duration) bool {
		for waited := time.Duration(0); ; waited += cronPoll {
			if gone() {
				return true
			}
			if waited >= budget {
				return false
			}
			cronSleep(cronPoll)
		}
	}
	_ = ops.signal(pid, syscall.SIGTERM)
	if !wait(cronDrainTimeout) {
		_ = ops.signal(pid, syscall.SIGKILL)
		if !wait(cronKillTimeout) {
			return fmt.Errorf("service: the server (pid %d) did not exit after SIGTERM and SIGKILL", pid)
		}
	}
	if p, err := statePath("server.pid"); err == nil {
		os.Remove(p)
	}
	return nil
}

// errLockHeld is lockExclusive's answer when another holder kept the lock for
// the whole wait, the only lock failure that is not an error.
var errLockHeld = errors.New("lock held by another process")

// --- ensure ----------------------------------------------------------------

// Ensure is what the cron lines run: it starts the server unless it is
// already up or was stopped on purpose.
func (m *Manager) Ensure(configPath string) error {
	exe, err := osExecutable()
	if err != nil {
		return fmt.Errorf("service: resolve binary path: %w", err)
	}
	return m.ensure(resolveStableBinPath(exe), configPath)
}

func (m *Manager) ensure(bin, configPath string) error {
	dir, err := StateDir()
	if err != nil {
		return fmt.Errorf("service: resolve state dir: %w", err)
	}
	if err := mkdirSafe(dir); err != nil {
		return fmt.Errorf("service: create state dir: %w", err)
	}
	unlock, err := lockExclusive(filepath.Join(dir, "server.lock"), ensureLockWait)
	if err != nil {
		if errors.Is(err, errLockHeld) {
			return nil // another ensure has been working for the whole wait
		}
		return fmt.Errorf("service: lock %s: %w", filepath.Join(dir, "server.lock"), err)
	}
	defer unlock()

	if fileExists(filepath.Join(dir, "server.stopped")) || m.ownPID(configPath) != 0 {
		return nil
	}
	// A server started by hand, or a lost pidfile: never a second instance.
	// Nor next to a foreign program on the address (#1998): `serve` could not
	// bind, and the watchdog would retry it every tick; `start` and `status`
	// name the cause.
	if addr := configHTTPAddr(configPath); addr != "" && listenerAt(addr) != ListenerNone {
		return nil
	}
	args := []string{"serve", "--config", configPath, "--log-file", filepath.Join(dir, "server.log")}
	// The shell's proxy never reaches cron's environment: `serve` reads the
	// variables `service install` recorded from the env file beside the config
	// (EnvFilePath), so the command line carries no flag a previous stable
	// does not know (#2094).
	pid, err := m.procs().start(bin, args, cleanServeEnv(environ(), bin))
	if err != nil {
		return fmt.Errorf("service: start server: %w", err)
	}
	tmp := filepath.Join(dir, "server.pid.tmp")
	if err := writeSafe(tmp, []byte(strconv.Itoa(pid)+"\n")); err != nil {
		return fmt.Errorf("service: write pidfile: %w", err)
	}
	return os.Rename(tmp, filepath.Join(dir, "server.pid"))
}

// cleanServeEnv is the whole environment of the server this backend starts:
// a fixed set of variables and the service PATH. Anything else, notably the
// CARTOGRAPHER_TOKENS/AUTH of the operator's shell, would make a server started
// by `setup` differ from the one cron starts (the 401 of D268 that a launchd or
// systemd service never sees).
func cleanServeEnv(env []string, bin string) []string {
	var out []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "HOME", "USER", "LOGNAME", "SHELL", "LANG", "TZ", "TMPDIR":
		default:
			if !strings.HasPrefix(k, "LC_") && !strings.HasPrefix(k, "XDG_") {
				continue
			}
		}
		out = append(out, kv)
	}
	return append(out, "PATH="+servicePATH(bin))
}

// cronDaemonRunning scans /proc for a cron daemon; known is false when /proc
// cannot be read, so the caller stays silent.
func cronDaemonRunning() (found, known bool) {
	comms, _ := filepath.Glob("/proc/[0-9]*/comm")
	if len(comms) == 0 {
		return false, false
	}
	for _, c := range comms {
		data, err := os.ReadFile(c)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(string(data)) {
		case "crond", "cron", "cronie", "dcron":
			return true, true
		case "busybox":
			if cl, err := os.ReadFile(filepath.Join(filepath.Dir(c), "cmdline")); err == nil && strings.Contains(string(cl), "crond") {
				return true, true
			}
		}
	}
	return false, true
}
