//go:build !windows

package service

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/blocktext"
)

// fakeCrontab is a runner that behaves like crontab(1) over an in-memory text,
// and records every other command.
type fakeCrontab struct {
	text      string
	has       bool
	failWrite bool
	failList  string // when set, `crontab -l` fails with this output
	calls     []string
	other     []string
}

func (f *fakeCrontab) run(name string, args ...string) (string, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, key)
	if name != "crontab" {
		f.other = append(f.other, key)
		return "", nil
	}
	switch {
	case len(args) == 1 && args[0] == "-l":
		if f.failList != "" {
			return f.failList, errors.New("exit status 1")
		}
		if !f.has {
			return "", errors.New("exit status 1 (output: no crontab for user)")
		}
		return f.text, nil
	case len(args) == 1 && args[0] == "-r":
		if f.failWrite {
			return "", errors.New("exit status 1")
		}
		f.text, f.has = "", false
		return "", nil
	case len(args) == 1:
		if f.failWrite {
			return "", errors.New("exit status 1")
		}
		data, err := os.ReadFile(args[0])
		if err != nil {
			return "", err
		}
		f.text, f.has = string(data), true
		return "", nil
	}
	return "", errors.New("bad crontab call")
}

// fakeProcs is a procOps with no real process.
type fakeProcs struct {
	mu       sync.Mutex
	next     int
	live     map[int]bool
	cmd      map[int]string
	stubborn bool // ignores SIGTERM
	starts   [][]string
	envs     [][]string
	signals  []string
	onSignal func()
}

func newFakeProcs() *fakeProcs {
	return &fakeProcs{next: 100, live: map[int]bool{}, cmd: map[int]string{}}
}

func (p *fakeProcs) ops() *procOps {
	return &procOps{
		start: func(bin string, args, env []string) (int, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.next++
			p.live[p.next] = true
			p.cmd[p.next] = bin + " " + strings.Join(args, " ")
			p.starts = append(p.starts, args)
			p.envs = append(p.envs, env)
			return p.next, nil
		},
		alive: func(pid int) bool { p.mu.Lock(); defer p.mu.Unlock(); return p.live[pid] },
		cmdline: func(pid int) (string, bool) {
			p.mu.Lock()
			defer p.mu.Unlock()
			c, ok := p.cmd[pid]
			return c, ok
		},
		signal: func(pid int, sig syscall.Signal) error {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.signals = append(p.signals, sig.String())
			if p.onSignal != nil {
				p.onSignal()
			}
			if sig == syscall.SIGKILL || !p.stubborn {
				p.live[pid] = false
			}
			return nil
		},
	}
}

type cronEnv struct {
	home, cfg string
	ct        *fakeCrontab
	procs     *fakeProcs
	m         *Manager
	answers   bool // what /health answers
}

// newCronEnv is a Linux host whose $HOME is the account's home, with a fake
// crontab and fake processes: the cron backend is the one chosen.
func newCronEnv(t *testing.T) *cronEnv {
	t.Helper()
	home := withTestHome(t, "linux")
	e := &cronEnv{home: home, ct: &fakeCrontab{}, procs: newFakeProcs()}
	lookPath = func(n string) (string, error) {
		if n == "systemctl" {
			return "", exec.ErrNotFound
		}
		return "/usr/bin/" + n, nil
	}
	accountHome = func() (string, error) { return home, nil }
	osExecutable = func() (string, error) { return "/opt/bin/cartographer", nil }
	healthAnswers = func(string) bool { return e.answers }
	serveAddrAnswers = func(string) bool { return e.answers }
	cronSleep = func(time.Duration) {}
	environ = func() []string { return nil }
	t.Cleanup(func() {
		osExecutable, healthAnswers, cronSleep, environ = os.Executable, checkHealth, time.Sleep, os.Environ
	})
	e.m = &Manager{run: e.ct.run, proc: e.procs.ops()}
	e.cfg = filepath.Join(home, ".config", "cartographer", "server.yaml")
	return e
}

func (e *cronEnv) install(t *testing.T) []string {
	t.Helper()
	w, err := e.m.Install(InstallOptions{DataDir: filepath.Join(e.home, "data"), HTTPAddr: "127.0.0.1:39999"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	return w
}

func (e *cronEnv) state(name string) string {
	return filepath.Join(e.home, ".local", "state", "cartographer", name)
}

func TestDetectBackend(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sandbox  bool
		systemd  bool // systemctl on PATH
		runDir   bool
		showEnv  bool
		crontab  bool
		listOut  string
		listFail bool
		want     Backend
	}{
		{"sandboxed home", true, true, true, true, true, "", false, BackendSystemd},
		{"systemd reachable", false, true, true, true, true, "", false, BackendSystemd},
		{"systemctl left on a non-systemd host", false, true, false, true, true, "", false, BackendCron},
		{"user manager unreachable", false, true, true, false, true, "", false, BackendCron},
		{"no systemctl, crontab ok", false, false, false, false, true, "", false, BackendCron},
		{"no crontab yet", false, false, false, false, true, "no crontab for user", true, BackendCron},
		{"crontab refused", false, false, false, false, true, "You (user) are not allowed to use this program", true, BackendNone},
		{"nothing", false, false, false, false, false, "", false, BackendNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := withTestHome(t, "linux")
			if !tc.sandbox {
				accountHome = func() (string, error) { return home, nil }
			}
			lookPath = func(n string) (string, error) {
				if (n == "systemctl" && tc.systemd) || (n == "crontab" && tc.crontab) {
					return "/usr/bin/" + n, nil
				}
				return "", exec.ErrNotFound
			}
			dir := t.TempDir()
			if !tc.runDir {
				dir = filepath.Join(dir, "absent")
			}
			orig := systemdRunDir
			systemdRunDir = dir
			t.Cleanup(func() { systemdRunDir = orig })
			m := &Manager{run: func(name string, args ...string) (string, error) {
				switch {
				case name == "systemctl" && !tc.showEnv:
					return "", errors.New("no bus")
				case name == "crontab" && tc.listFail:
					return tc.listOut, errors.New("exit status 1")
				}
				return "", nil
			}}
			if got := m.DetectBackend(); got != tc.want {
				t.Errorf("DetectBackend = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestBackend_InstalledDefinitionWins(t *testing.T) {
	e := newCronEnv(t)
	unit, _ := SystemdUnitPath()
	def, _ := CronServerDefinitionPath()
	write := func(p string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
	}
	if got := e.m.Backend(); got != BackendCron {
		t.Fatalf("probe: %s", got)
	}
	write(unit)
	if got := e.m.Backend(); got != BackendSystemd {
		t.Errorf("unit present: %s", got)
	}
	write(def)
	if got := e.m.Backend(); got != BackendSystemd {
		t.Errorf("both present: %s, want systemd", got)
	}
	os.Remove(unit)
	if got := e.m.Backend(); got != BackendCron {
		t.Errorf("cron definition: %s", got)
	}
}

const wantServerBlock = `# BEGIN cartographer server — managed by cartographer, do not edit
@reboot '/opt/bin/cartographer' service ensure --config '/h/c.yaml' --log-file '/h/s/supervisor.log'
*/5 * * * * '/opt/bin/cartographer' service ensure --config '/h/c.yaml' --log-file '/h/s/supervisor.log'
# END cartographer server
`

func TestRenderCronServerBlock(t *testing.T) {
	if got := renderCronServerBlock("/opt/bin/cartographer", "/h/c.yaml", "/h/s"); got != wantServerBlock {
		t.Errorf("block:\n%s", got)
	}
	if got := shQuote("it's"); got != `'it'\''s'` {
		t.Errorf("shQuote = %s", got)
	}
}

func TestCronInstallUninstall_PreservesForeignLines(t *testing.T) {
	e := newCronEnv(t)
	const foreign = "MAILTO=user\n0 3 * * * /usr/bin/backup\n"
	e.ct.text, e.ct.has = foreign, true
	e.install(t)
	if strings.Count(e.ct.text, "BEGIN cartographer server") != 1 || !strings.HasPrefix(e.ct.text, foreign) {
		t.Fatalf("crontab:\n%s", e.ct.text)
	}
	first := e.ct.text
	writes := 0
	for _, c := range e.ct.calls {
		if strings.HasPrefix(c, "crontab /") {
			writes++
		}
	}
	e.install(t) // idempotent: the same crontab, and no second write
	again := 0
	for _, c := range e.ct.calls {
		if strings.HasPrefix(c, "crontab /") {
			again++
		}
	}
	if e.ct.text != first || again != writes {
		t.Errorf("re-install changed the crontab or rewrote it (%d -> %d writes)", writes, again)
	}
	st, _ := e.m.Status(e.cfg)
	if !st.Installed || !st.Running || st.ServiceBinPath != "/opt/bin/cartographer" {
		t.Errorf("status = %+v", st)
	}
	if err := e.m.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if e.ct.text != foreign {
		t.Errorf("uninstall left:\n%q", e.ct.text)
	}
	def, _ := CronServerDefinitionPath()
	if fileExists(def) || fileExists(e.state("server.pid")) {
		t.Error("definition or pidfile left behind")
	}
	if st, _ := e.m.Status(e.cfg); st.Installed {
		t.Errorf("after uninstall: %+v", st)
	}
	if err := e.m.Uninstall(); err != nil {
		t.Errorf("second uninstall: %v", err)
	}
}

func TestCronUninstall_EmptyRemainderRemovesCrontab(t *testing.T) {
	e := newCronEnv(t)
	e.install(t)
	if err := e.m.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if e.ct.has {
		t.Errorf("crontab still there: %q", e.ct.text)
	}
	found := false
	for _, c := range e.ct.calls {
		found = found || c == "crontab -r"
	}
	if !found {
		t.Error("crontab -r was not used")
	}
}

func TestCronInstall_RefusesCorruptedBlockAndBadPaths(t *testing.T) {
	e := newCronEnv(t)
	bad := "a\n# BEGIN cartographer server — x\nno end\n"
	e.ct.text, e.ct.has = bad, true
	_, err := e.m.Install(InstallOptions{DataDir: filepath.Join(e.home, "d"), HTTPAddr: ":39999"})
	if !errors.Is(err, blocktext.ErrCorruptedBlock) || e.ct.text != bad {
		t.Errorf("err=%v text=%q", err, e.ct.text)
	}
	if def, _ := CronServerDefinitionPath(); fileExists(def) {
		t.Error("definition left after a failed install")
	}

	e = newCronEnv(t)
	_, err = e.m.Install(InstallOptions{ConfigPath: filepath.Join(e.home, "50%", "s.yaml"), DataDir: filepath.Join(e.home, "d"), HTTPAddr: ":39999"})
	if err == nil || !strings.Contains(err.Error(), "50%") {
		t.Errorf("a path with %% must be refused naming it: %v", err)
	}
}

// Uninstall after a failed crontab write re-reads the crontab (#1584): the
// definition goes only when the block is really gone.
func TestCronUninstall_VerifiesRemoval(t *testing.T) {
	e := newCronEnv(t)
	e.install(t)
	def, _ := CronServerDefinitionPath()

	e.ct.failWrite = true
	if err := e.m.Uninstall(); err == nil {
		t.Fatal("uninstall succeeded with the block still in the crontab")
	}
	if !fileExists(def) {
		t.Error("definition deleted although the block is still registered")
	}

	// The write "fails" but the block is gone (changed behind our back).
	e.ct.text, e.ct.has = "", false
	if err := e.m.Uninstall(); err != nil {
		t.Fatalf("block gone: %v", err)
	}
	if fileExists(def) {
		t.Error("definition kept although the block is gone")
	}
}

func TestEnsure(t *testing.T) {
	e := newCronEnv(t)
	e.install(t)
	if len(e.procs.starts) != 1 {
		t.Fatalf("install started %d servers", len(e.procs.starts))
	}
	args := strings.Join(e.procs.starts[0], " ")
	if !strings.Contains(args, "serve --config "+e.cfg) || !strings.Contains(args, "server.log") {
		t.Errorf("args %s", args)
	}
	ensure := func() {
		t.Helper()
		if err := e.m.Ensure(e.cfg); err != nil {
			t.Fatal(err)
		}
	}

	ensure() // live pid of ours
	if len(e.procs.starts) != 1 {
		t.Error("ensure started a second server next to a live pid")
	}

	// A stale pidfile (pid dead) is replaced.
	e.procs.live[101] = false
	ensure()
	if len(e.procs.starts) != 2 {
		t.Errorf("stale pid not replaced: %d starts", len(e.procs.starts))
	}

	// A pid reused by another program is stale as well.
	e.procs.cmd[102] = "/usr/bin/vim notes.txt"
	ensure()
	if len(e.procs.starts) != 3 {
		t.Errorf("foreign command line trusted: %d starts", len(e.procs.starts))
	}

	// A server answering with no pid of ours (started by hand): never a second.
	os.Remove(e.state("server.pid"))
	e.answers = true
	ensure()
	if len(e.procs.starts) != 3 {
		t.Error("ensure started a server next to one that answers")
	}

	// The stop marker is honoured.
	e.answers = false
	os.Remove(e.state("server.pid"))
	os.WriteFile(e.state("server.stopped"), nil, 0o644)
	ensure()
	if len(e.procs.starts) != 3 {
		t.Error("ensure ignored the stop marker")
	}
}

func TestEnsure_CleanEnvironment(t *testing.T) {
	e := newCronEnv(t)
	environ = func() []string {
		return []string{"HOME=/h", "CARTOGRAPHER_TOKENS=secret", "CARTOGRAPHER_AUTH=x", "LC_ALL=C", "XDG_STATE_HOME=/s", "AWS_SECRET=1", "PATH=/weird"}
	}
	e.install(t)
	env := strings.Join(e.procs.envs[0], "\n")
	if strings.Contains(env, "CARTOGRAPHER_") || strings.Contains(env, "AWS_") || strings.Contains(env, "/weird") {
		t.Errorf("environment leaks: %s", env)
	}
	for _, want := range []string{"HOME=/h", "LC_ALL=C", "XDG_STATE_HOME=/s", "PATH=" + servicePATH("/opt/bin/cartographer")} {
		if !strings.Contains(env, want) {
			t.Errorf("environment lacks %s: %s", want, env)
		}
	}
}

func TestCronStatusStartStopRestart(t *testing.T) {
	e := newCronEnv(t)
	e.install(t)
	running := func() bool { st, _ := e.m.Status(e.cfg); return st.Running }
	if !running() {
		t.Fatal("not running after install")
	}

	// Stop writes the marker before it signals.
	e.procs.onSignal = func() {
		if !fileExists(e.state("server.stopped")) {
			t.Error("signalled before the stop marker was written")
		}
	}
	if err := e.m.Stop(); err != nil {
		t.Fatal(err)
	}
	e.procs.onSignal = nil
	if running() {
		t.Error("running after stop")
	}
	if st, _ := e.m.Status(e.cfg); !st.Installed || st.Lifecycle != LifecycleNotLoaded {
		t.Errorf("stopped status %+v", st)
	}
	n := len(e.procs.starts)
	e.m.Ensure(e.cfg) // the watchdog does nothing
	if len(e.procs.starts) != n {
		t.Error("the watchdog restarted a stopped server")
	}

	if err := e.m.Start(); err != nil || !running() || len(e.procs.starts) != n+1 {
		t.Fatalf("start: %v running=%v starts=%d", err, running(), len(e.procs.starts))
	}
	if err := e.m.Restart(); err != nil || len(e.procs.starts) != n+2 || !running() {
		t.Fatalf("restart: %v starts=%d", err, len(e.procs.starts))
	}
	if err := e.m.signalGraceful(); err != nil || len(e.procs.starts) != n+3 {
		t.Fatalf("signalGraceful: %v", err)
	}

	// Something answers that is not ours: start refuses, stop says so.
	e.m.Stop()
	os.Remove(e.state("server.pid"))
	e.answers = true
	if err := e.m.Start(); err == nil || !strings.Contains(err.Error(), "not started by this service") {
		t.Errorf("start next to a foreign server: %v", err)
	}
	if err := e.m.Stop(); err == nil || !strings.Contains(err.Error(), "stop it by hand") {
		t.Errorf("stop of a foreign server: %v", err)
	}
}

func TestCronStop_EscalatesToSIGKILL(t *testing.T) {
	e := newCronEnv(t)
	e.install(t)
	e.procs.stubborn = true
	if err := e.m.Stop(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(e.procs.signals, ","); got != "terminated,killed" {
		t.Errorf("signals = %s", got)
	}
}

func TestTrayViewRunsNoCommandWithCronDefinition(t *testing.T) {
	e := newCronEnv(t)
	e.install(t)
	before := len(e.ct.calls)
	st, err := e.m.TrayView("")
	if err != nil || !st.Installed {
		t.Fatalf("%+v %v", st, err)
	}
	if len(e.ct.calls) != before {
		t.Errorf("TrayView ran %v", e.ct.calls[before:])
	}
}

func TestLockExclusiveSerialises(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l")
	unlock, err := lockExclusive(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockExclusive(path, 150*time.Millisecond); err == nil {
		t.Error("second lock succeeded while the first was held")
	}
	var wg sync.WaitGroup
	wg.Add(1)
	var got error
	go func() {
		defer wg.Done()
		u, e := lockExclusive(path, 2*time.Second)
		got = e
		if e == nil {
			u()
		}
	}()
	time.Sleep(200 * time.Millisecond)
	unlock()
	wg.Wait()
	if got != nil {
		t.Errorf("waiter did not get the lock after release: %v", got)
	}
}

// The real implementation: a detached start, and SIGTERM through the pid. The
// test binary re-executes itself as the "server".
func TestRealProcOps(t *testing.T) {
	if os.Getenv("CRON_TEST_HELPER") == "1" {
		time.Sleep(time.Minute)
		return
	}
	ops := realProcOps()
	exe, _ := os.Executable()
	pid, err := ops.start(exe, []string{"-test.run=TestRealProcOps"}, []string{"CRON_TEST_HELPER=1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
	if !ops.alive(pid) {
		t.Fatal("not alive after start")
	}
	if err := ops.signal(pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100 && ops.alive(pid); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if ops.alive(pid) {
		t.Error("still alive after SIGTERM")
	}
}

// Install resolves the backend with a fresh probe: a cron host that gained a
// reachable systemd migrates, and the cron leftovers go.
func TestInstall_SystemdWinsOverAnInstalledCronDefinition(t *testing.T) {
	e := newCronEnv(t)
	e.install(t)
	lookPath = func(n string) (string, error) { return "/usr/bin/" + n, nil }
	run := t.TempDir()
	orig := systemdRunDir
	systemdRunDir = run
	t.Cleanup(func() { systemdRunDir = orig })
	if _, err := e.m.Install(InstallOptions{DataDir: filepath.Join(e.home, "data"), HTTPAddr: "127.0.0.1:39999"}); err != nil {
		t.Fatal(err)
	}
	def, _ := CronServerDefinitionPath()
	unit, _ := SystemdUnitPath()
	if fileExists(def) || !fileExists(unit) {
		t.Errorf("cron definition %v, unit %v: want only the unit", fileExists(def), fileExists(unit))
	}
	if strings.Contains(e.ct.text, "cartographer") {
		t.Errorf("crontab still holds the block: %q", e.ct.text)
	}
	if fileExists(e.state("server.pid")) {
		t.Error("pidfile left behind")
	}
}

// A stale unit on a host with no usable systemd must not break a cron install,
// and the unit is removed.
func TestInstall_CronRemovesAStaleUnit(t *testing.T) {
	e := newCronEnv(t)
	unit, _ := SystemdUnitPath()
	os.MkdirAll(filepath.Dir(unit), 0o755)
	os.WriteFile(unit, []byte("x"), 0o644)
	e.install(t)
	if fileExists(unit) {
		t.Error("stale unit kept")
	}
	if def, _ := CronServerDefinitionPath(); !fileExists(def) {
		t.Error("cron definition not written")
	}
}

func TestUninstall_BothDefinitionsPresentRemovesBoth(t *testing.T) {
	e := newCronEnv(t)
	e.install(t)
	unit, _ := SystemdUnitPath()
	os.MkdirAll(filepath.Dir(unit), 0o755)
	os.WriteFile(unit, []byte("x"), 0o644)
	if err := e.m.Uninstall(); err != nil {
		t.Fatal(err)
	}
	def, _ := CronServerDefinitionPath()
	if fileExists(def) || fileExists(unit) || strings.Contains(e.ct.text, "cartographer") {
		t.Errorf("left: def=%v unit=%v crontab=%q", fileExists(def), fileExists(unit), e.ct.text)
	}
}

// Under a sandboxed HOME an inherited XDG_STATE_HOME must not point the cron
// backend at the real state directory, and nothing is touched.
func TestCron_SandboxedHomeIgnoresXDGStateAndTouchesNothing(t *testing.T) {
	e := newCronEnv(t)
	e.install(t)
	real := t.TempDir()
	getenv = func(k string) string {
		if k == "XDG_STATE_HOME" {
			return real
		}
		return ""
	}
	accountHome = func() (string, error) { return "/nonexistent-account-home", nil }
	if d, _ := StateDir(); strings.HasPrefix(d, real) {
		t.Errorf("StateDir = %s follows the inherited XDG_STATE_HOME", d)
	}
	before := len(e.procs.signals)
	for name, err := range map[string]error{"stop": e.m.Stop(), "start": e.m.Start(), "uninstall": e.m.Uninstall()} {
		if !errors.Is(err, errSandboxHome) {
			t.Errorf("%s: %v, want errSandboxHome", name, err)
		}
	}
	if ents, _ := os.ReadDir(real); len(ents) != 0 {
		t.Errorf("real state dir touched: %v", ents)
	}
	if len(e.procs.signals) != before || fileExists(e.state("server.stopped")) {
		t.Error("a process was signalled or the stop marker written")
	}
}

// ensure: only "held for the whole wait" is silent; a lock that cannot be
// opened is an error, and start fails when nothing ends up running.
func TestEnsure_LockErrorsAreNotSilent(t *testing.T) {
	e := newCronEnv(t)
	dir, _ := StateDir()
	os.MkdirAll(dir, 0o755)
	os.Mkdir(filepath.Join(dir, "server.lock"), 0o755) // a directory cannot be opened read-write
	if err := e.m.ensure("/opt/bin/cartographer", e.cfg); err == nil {
		t.Error("an unopenable lock returned nil")
	}
}

func TestStart_FailsWhenNothingRuns(t *testing.T) {
	e := newCronEnv(t)
	e.install(t)
	e.procs.live = map[int]bool{}
	e.m.proc = e.procs.ops()
	for pid := range e.procs.cmd {
		delete(e.procs.cmd, pid)
	}
	e.m.proc.start = func(string, []string, []string) (int, error) { return 999, nil } // never alive
	os.Remove(e.state("server.pid"))
	if err := e.m.Start(); err == nil {
		t.Error("Start succeeded with no server running")
	}
}

// crontab -l is read from stdout alone: the runner used for parsing is not the
// combined-output one.
func TestReadCrontab_UsesStdoutRunner(t *testing.T) {
	m := &Manager{
		run:    func(string, ...string) (string, error) { return "WARNING on stderr\n", nil },
		runOut: func(string, ...string) (string, error) { return "0 * * * * x\n", nil },
	}
	got, err := m.readCrontab()
	if err != nil || got != "0 * * * * x\n" {
		t.Errorf("readCrontab = %q, %v", got, err)
	}
}
