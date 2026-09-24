package service

// The Windows half of the Manager (D217), tested from whatever host runs the
// suite: goos is a package var, so every branch here is reachable from macOS and
// Linux, and the `test-windows` CI leg runs the same assertions natively.
//
// Two conventions in this file are deliberate. Paths are built with
// filepath.Join, never with `/`-separated literals: the existing render fixtures
// can afford literals because a plist is a unix file, while these assertions
// compare against paths the host spells with its own separator. And the recorded
// commands are asserted, not merely the absence of an error — a service manager
// that runs the wrong command successfully is the failure this package is for.

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// noGUIDomain asserts no recorded call carries a launchd `gui/<uid>/…` target.
// os.Getuid returns -1 on Windows and has no analogue there, so a Windows branch
// that reached for it would build `gui/-1/com.cartographer.serve` — and pass
// every test in this file, because none of them stubs os.Getuid.
func noGUIDomain(t *testing.T, calls [][]string) {
	t.Helper()
	for _, c := range calls {
		if strings.Contains(strings.Join(c, " "), "gui/") {
			t.Errorf("a Windows branch built a launchd domain target: %v", c)
		}
	}
}

func TestConfigPath_Windows(t *testing.T) {
	t.Run("APPDATA unset falls back under the home directory", func(t *testing.T) {
		home := withTestHome(t, "windows")
		got, err := ConfigPath()
		if err != nil {
			t.Fatalf("ConfigPath: %v", err)
		}
		want := filepath.Join(home, "AppData", "Roaming", "cartographer", "server.yaml")
		if got != want {
			t.Errorf("ConfigPath() = %q, want %q", got, want)
		}
	})

	t.Run("APPDATA is honoured when set", func(t *testing.T) {
		withTestHome(t, "windows")
		roaming := t.TempDir()
		withTestEnv(t, map[string]string{"APPDATA": roaming})
		got, err := ConfigPath()
		if err != nil {
			t.Fatalf("ConfigPath: %v", err)
		}
		if want := filepath.Join(roaming, "cartographer", "server.yaml"); got != want {
			t.Errorf("ConfigPath() = %q, want %q", got, want)
		}
	})

	t.Run("an existing unix-location config is read instead", func(t *testing.T) {
		home := withTestHome(t, "windows")
		legacy := filepath.Join(home, ".config", "cartographer", "server.yaml")
		if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(legacy, []byte("http: \"127.0.0.1:39273\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := ConfigPath()
		if err != nil {
			t.Fatalf("ConfigPath: %v", err)
		}
		if got != legacy {
			t.Errorf("ConfigPath() = %q, want the pre-existing %q", got, legacy)
		}
	})

	t.Run("the roaming location wins when both exist", func(t *testing.T) {
		home := withTestHome(t, "windows")
		legacy := filepath.Join(home, ".config", "cartographer", "server.yaml")
		roaming := filepath.Join(home, "AppData", "Roaming", "cartographer", "server.yaml")
		for _, p := range []string{legacy, roaming} {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("http: \"\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := ConfigPath()
		if err != nil {
			t.Fatalf("ConfigPath: %v", err)
		}
		if got != roaming {
			t.Errorf("ConfigPath() = %q, want the roaming location %q", got, roaming)
		}
	})
}

func TestWindowsPaths(t *testing.T) {
	home := withTestHome(t, "windows")
	local := filepath.Join(home, "AppData", "Local", "cartographer")

	task, err := WindowsTaskPath()
	if err != nil {
		t.Fatalf("WindowsTaskPath: %v", err)
	}
	if want := filepath.Join(local, "tasks", "serve.xml"); task != want {
		t.Errorf("WindowsTaskPath() = %q, want %q", task, want)
	}
	logPath, err := WindowsLogPath()
	if err != nil {
		t.Fatalf("WindowsLogPath: %v", err)
	}
	if want := filepath.Join(local, "Logs", "server.log"); logPath != want {
		t.Errorf("WindowsLogPath() = %q, want %q", logPath, want)
	}
	syncTask, err := SyncWindowsTaskPath()
	if err != nil {
		t.Fatalf("SyncWindowsTaskPath: %v", err)
	}
	if want := filepath.Join(local, "tasks", "sync.xml"); syncTask != want {
		t.Errorf("SyncWindowsTaskPath() = %q, want %q", syncTask, want)
	}
	syncLog, err := SyncWindowsLogPath()
	if err != nil {
		t.Fatalf("SyncWindowsLogPath: %v", err)
	}
	if want := filepath.Join(local, "Logs", "sync.log"); syncLog != want {
		t.Errorf("SyncWindowsLogPath() = %q, want %q", syncLog, want)
	}
}

// On Windows the recorded binary path is the one the service was installed
// from: install.ps1 replaces that same file in place on every upgrade (D252),
// so unlike the darwin arm (D83) there is no versioned payload to step around.
func TestResolveStableBinPath_Windows(t *testing.T) {
	withTestHome(t, "windows")
	in := `C:\Users\u\AppData\Local\Cartographer\bin\cartographer.exe`
	if got := resolveStableBinPath(in); got != in {
		t.Errorf("resolveStableBinPath = %q, want %q unchanged", got, in)
	}
}

func TestRenderWindowsTaskXML(t *testing.T) {
	out := RenderWindowsTaskXML(`C:\bin\cartographer.exe`, `C:\Users\Nome Cognome\AppData\Roaming\cartographer\server.yaml`, `C:\logs\server.log`, `HOST\user`)

	for _, want := range []string{
		`<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">`,
		`<URI>\Cartographer\Serve</URI>`,
		"<LogonTrigger>",
		"<LogonType>InteractiveToken</LogonType>",
		"<RunLevel>LeastPrivilege</RunLevel>",
		`<Command>C:\bin\cartographer.exe</Command>`,
		// Every path quoted: the config path here contains a space, and an
		// unquoted one would reach the process as two arguments.
		`<Arguments>serve --config "C:\Users\Nome Cognome\AppData\Roaming\cartographer\server.yaml" --log-file "C:\logs\server.log"</Arguments>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("task XML missing %q\n---\n%s", want, out)
		}
	}

	// The four Task Scheduler defaults that would each kill or duplicate a
	// long-running server. Every one of them is a default, so forgetting one
	// produces a service that works until a laptop is unplugged.
	for _, want := range []string{
		"<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>",
		"<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>",
		"<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>",
		"<StopOnIdleEnd>false</StopOnIdleEnd>",
		"<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("task XML missing the override %q\n---\n%s", want, out)
		}
	}

	// The KeepAlive / Restart=on-failure analogue, bounded.
	for _, want := range []string{"<RestartOnFailure>", "<Interval>PT1M</Interval>", "<Count>3</Count>", "<StartWhenAvailable>true</StartWhenAvailable>"} {
		if !strings.Contains(out, want) {
			t.Errorf("task XML missing %q\n---\n%s", want, out)
		}
	}

	// No PATH, and no place to put one: the format has no environment block, and
	// a task inherits the user's own environment — see RenderWindowsTaskXML.
	if strings.Contains(out, "PATH") {
		t.Errorf("task XML declares a PATH, which the schema cannot express:\n%s", out)
	}
	assertTaskXMLDeclaresNoEncoding(t, out)
}

// assertTaskXMLDeclaresNoEncoding: the definition reaches Task Scheduler as a
// PowerShell string, UTF-16 in memory, so a declared encoding contradicts the
// buffer and the task is refused (#328). None may be named, and the document
// must still parse.
func assertTaskXMLDeclaresNoEncoding(t *testing.T, out string) {
	t.Helper()
	decl, _, _ := strings.Cut(out, "\n")
	if !strings.HasPrefix(decl, "<?xml") || strings.Contains(strings.ToLower(decl), "encoding") {
		t.Errorf("task XML declaration %q must name no encoding", decl)
	}
	if err := xml.Unmarshal([]byte(out), new(struct{ XMLName xml.Name })); err != nil {
		t.Errorf("task XML does not parse: %v", err)
	}
}

func TestRenderWindowsTaskXML_EscapesTheBinaryPath(t *testing.T) {
	out := RenderWindowsTaskXML(`C:\R&D\cartographer.exe`, `C:\cfg\server.yaml`, `C:\logs\server.log`, `HOST\user`)
	if !strings.Contains(out, `<Command>C:\R&amp;D\cartographer.exe</Command>`) {
		t.Errorf("an ampersand in the path was not escaped, which makes the whole definition unparseable:\n%s", out)
	}
	// And it round-trips: the reader has to give the path back as written.
	got, err := extractTaskConfigPath([]byte(RenderWindowsTaskXML(`C:\R&D\cartographer.exe`, `C:\R&D\server.yaml`, `C:\logs\server.log`, `HOST\user`)))
	if err != nil {
		t.Fatalf("extractTaskConfigPath: %v", err)
	}
	if got != `C:\R&D\server.yaml` {
		t.Errorf("extractTaskConfigPath = %q, want the unescaped path", got)
	}
}

func TestRenderWindowsSyncTaskXML(t *testing.T) {
	out := RenderWindowsSyncTaskXML(`C:\bin\cartographer.exe`, `C:\logs\sync.log`, 20*time.Minute)
	assertTaskXMLDeclaresNoEncoding(t, out)

	for _, want := range []string{
		`<URI>\Cartographer\Sync</URI>`,
		`<Arguments>sync --log-file "C:\logs\sync.log"</Arguments>`,
		"<Interval>PT20M</Interval>",
		"<StopAtDurationEnd>false</StopAtDurationEnd>",
		// systemd's Persistent=true analogue: a run missed while the machine was
		// off happens as soon as it is on.
		"<StartWhenAvailable>true</StartWhenAvailable>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sync task XML missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "auto-trust") {
		t.Errorf("the sync task must not pass --auto-trust (D54):\n%s", out)
	}
	if got := intervalFromTaskXML(out); got != 20*time.Minute {
		t.Errorf("intervalFromTaskXML = %v, want 20m — the interval written is not the one read back", got)
	}
}

// The server task also carries an <Interval> (the restart backoff), so a reader
// that searched for the bare element would report a 1-minute sync period.
func TestIntervalFromTaskXML_IgnoresTheRestartBackoff(t *testing.T) {
	out := RenderWindowsTaskXML(`C:\bin\cartographer.exe`, `C:\cfg\server.yaml`, `C:\logs\server.log`, `HOST\user`)
	if got := intervalFromTaskXML(out); got != 0 {
		t.Errorf("intervalFromTaskXML on the server task = %v, want 0 (it has no repetition)", got)
	}
}

func TestISO8601DurationRoundTrip(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Minute:                      "PT30M",
		time.Hour:                             "PT1H",
		90 * time.Second:                      "PT1M30S",
		15 * time.Second:                      "PT15S",
		2*time.Hour + 5*time.Minute:           "PT2H5M",
		time.Hour + time.Minute + time.Second: "PT1H1M1S",
	}
	for d, want := range cases {
		if got := formatISO8601Duration(d); got != want {
			t.Errorf("formatISO8601Duration(%v) = %q, want %q", d, got, want)
		}
		if got := parseISO8601Duration(want); got != d {
			t.Errorf("parseISO8601Duration(%q) = %v, want %v", want, got, d)
		}
	}
	if got := parseISO8601Duration("P1D"); got != 24*time.Hour {
		t.Errorf("parseISO8601Duration(P1D) = %v, want 24h", got)
	}
	// A month has no fixed length in seconds; reporting no interval is the only
	// honest answer, and a wrong one would be printed as fact by `status`.
	for _, bad := range []string{"P1M", "P1Y", "", "30M", "PTXM", "PT"} {
		if got := parseISO8601Duration(bad); got != 0 {
			t.Errorf("parseISO8601Duration(%q) = %v, want 0", bad, got)
		}
	}
}

func TestSplitQuotedArgs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`serve --config "C:\Program Files\cartographer\server.yaml"`, []string{"serve", "--config", `C:\Program Files\cartographer\server.yaml`}},
		{`serve --config C:\cfg\server.yaml`, []string{"serve", "--config", `C:\cfg\server.yaml`}},
		{`sync --log-file "C:\logs\sync.log"`, []string{"sync", "--log-file", `C:\logs\sync.log`}},
		// A drive root ends in a backslash: treating `\"` as an escape would
		// swallow the closing quote and merge this with whatever follows.
		{`serve --config "C:\" --log-file "D:\x.log"`, []string{"serve", "--config", `C:\`, "--log-file", `D:\x.log`}},
		{`  serve   --config   "a b"  `, []string{"serve", "--config", "a b"}},
		{`""`, []string{""}},
	}
	for _, c := range cases {
		got := splitQuotedArgs(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitQuotedArgs(%q) = %q, want %q", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitQuotedArgs(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestInstall_Windows_WritesTheTaskAndRegistersIt(t *testing.T) {
	home := withTestHome(t, "windows")
	binPath := filepath.Join(t.TempDir(), "cartographer.exe")
	if err := os.WriteFile(binPath, []byte("bin"), 0o644); err != nil {
		t.Fatal(err)
	}
	origExecutable := osExecutable
	osExecutable = func() (string, error) { return binPath, nil }
	t.Cleanup(func() { osExecutable = origExecutable })

	m, stub := newTestManager()
	if _, err := m.Install(InstallOptions{DataDir: filepath.Join(home, "data"), HTTPAddr: "127.0.0.1:39273"}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	configPath := filepath.Join(home, "AppData", "Roaming", "cartographer", "server.yaml")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config not written to the roaming location: %v", err)
	}
	taskPath := filepath.Join(home, "AppData", "Local", "cartographer", "tasks", "serve.xml")
	data, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatalf("task definition not written: %v", err)
	}
	if !strings.Contains(string(data), configPath) {
		t.Errorf("the task definition does not name the config it was installed with:\n%s", data)
	}
	// The log directory is created by Install, not by the first log line: an
	// unopenable --log-file is a startup failure.
	if _, err := os.Stat(filepath.Join(home, "AppData", "Local", "cartographer", "Logs")); err != nil {
		t.Errorf("log directory not created: %v", err)
	}

	joined := make([]string, 0, len(stub.calls))
	for _, c := range stub.calls {
		if c[0] != "powershell.exe" {
			t.Errorf("Windows install ran %q, want powershell.exe — schtasks prints localised state (D217)", c[0])
		}
		joined = append(joined, strings.Join(c, " "))
	}
	all := strings.Join(joined, "\n")
	for _, want := range []string{
		"Register-ScheduledTask",
		"-TaskName 'Serve'",
		`-TaskPath '\Cartographer\'`,
		"-Force",
		"Get-Content -Raw -Encoding UTF8",
		"Start-ScheduledTask",
		"$ErrorActionPreference='Stop'",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("install commands missing %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "schtasks") {
		t.Errorf("install used schtasks, whose printed state is localised:\n%s", all)
	}
	noGUIDomain(t, stub.calls)
}

// A stopped service must stay stopped: the logon trigger and RestartOnFailure
// would otherwise bring it straight back, which is this platform's shape of the
// D156 regression.
func TestStop_Windows_StopsThenDisables(t *testing.T) {
	withTestHome(t, "windows")
	m, stub := newTestManager()
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(stub.calls) != 2 {
		t.Fatalf("Stop calls = %v, want stop then disable", stub.calls)
	}
	if !strings.Contains(strings.Join(stub.calls[0], " "), "Stop-ScheduledTask") {
		t.Errorf("first call = %v, want Stop-ScheduledTask", stub.calls[0])
	}
	if !strings.Contains(strings.Join(stub.calls[1], " "), "Disable-ScheduledTask") {
		t.Errorf("second call = %v, want Disable-ScheduledTask", stub.calls[1])
	}
	noGUIDomain(t, stub.calls)
}

// fakeServeTask is a serve task with a process behind it, for the tests that
// care about *when* things happen rather than which commands ran: the State
// query answers from it, and a stop — the shutdown event or Stop-ScheduledTask
// — leaves the process alive for lingerPolls more State queries, which is how
// Stop-ScheduledTask behaves (#411): it returns before the process has exited.
type fakeServeTask struct {
	calls       [][]string
	running     bool
	stopping    bool
	lingerPolls int
	// drains says whether the process honours the shutdown event.
	drains bool
	// ignoresStop makes Stop-ScheduledTask a no-op: a process that survives.
	ignoresStop bool
	// startedWhileRunning counts Start-ScheduledTask calls that reached a
	// still-running process: the new instance that dies on bind.
	startedWhileRunning int
	fail                map[string]bool
}

func (f *fakeServeTask) stop() {
	if f.running {
		f.stopping = true
	}
}

func (f *fakeServeTask) run(name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	key := strings.Join(call, " ")
	for pat := range f.fail {
		if strings.Contains(key, pat) {
			return "", errNotLoaded
		}
	}
	switch {
	case strings.Contains(key, "State -eq 'Running'"):
		if f.stopping {
			if f.lingerPolls > 0 {
				f.lingerPolls--
			} else {
				f.running, f.stopping = false, false
			}
		}
		if !f.running {
			return "", errNotLoaded
		}
	case strings.Contains(key, "Stop-ScheduledTask"):
		if !f.ignoresStop {
			f.stop()
		}
	case strings.Contains(key, "Start-ScheduledTask"):
		if f.running {
			f.startedWhileRunning++
		}
		f.running = true
	}
	return "", nil
}

// eventFor installs a setShutdownEvent that drains f when it honours the event
// and fails as an unreachable event otherwise.
func eventFor(t *testing.T, f *fakeServeTask, reachable bool) *int {
	t.Helper()
	var set int
	orig := setShutdownEvent
	setShutdownEvent = func() error {
		if !reachable {
			return os.ErrNotExist
		}
		set++
		if f.drains {
			f.stop()
		}
		return nil
	}
	t.Cleanup(func() { setShutdownEvent = orig })
	return &set
}

func joinedCalls(calls [][]string) string {
	all := ""
	for _, c := range calls {
		all += strings.Join(c, " ") + "\n"
	}
	return all
}

// ...and a restart must undo that disable, or the server stays down.
func TestRestart_Windows_ReEnablesBeforeStarting(t *testing.T) {
	withTestHome(t, "windows")
	f := &fakeServeTask{running: true, drains: true}
	eventFor(t, f, true)
	m := &Manager{run: f.run}
	if err := m.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	all := joinedCalls(f.calls)
	for _, want := range []string{"Get-ScheduledTask", "Enable-ScheduledTask", "Start-ScheduledTask"} {
		if !strings.Contains(all, want) {
			t.Errorf("restart commands missing %q:\n%s", want, all)
		}
	}
	if strings.Index(all, "Enable-ScheduledTask") > strings.Index(all, "Start-ScheduledTask") {
		t.Errorf("Start-ScheduledTask ran before Enable-ScheduledTask, so a stopped service stays stopped:\n%s", all)
	}
	noGUIDomain(t, f.calls)
}

// #411: Stop-ScheduledTask returned before the old process exited, the start
// that followed at once reached a process still holding the port, and the new
// instance died on bind — every restart left the server stopped. The start
// must wait for the exit, whichever way the old process was ended.
func TestRestart_Windows_StartsOnlyAfterTheOldProcessExits(t *testing.T) {
	for _, tc := range []struct {
		name      string
		reachable bool // the shutdown event can be opened
		drains    bool // the process honours it within the budget
	}{
		{"graceful drain", true, true},
		{"drain times out, the task is stopped", true, false},
		{"event unreachable, the task is stopped", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withTestHome(t, "windows")
			f := &fakeServeTask{running: true, drains: tc.drains, lingerPolls: 5}
			set := eventFor(t, f, tc.reachable)
			m := &Manager{run: f.run}
			if err := m.Restart(); err != nil {
				t.Fatalf("Restart: %v", err)
			}
			if f.startedWhileRunning != 0 {
				t.Errorf("Start-ScheduledTask reached the old process %d time(s): the new instance dies on bind", f.startedWhileRunning)
			}
			if !f.running {
				t.Error("the task was not started again")
			}
			all := joinedCalls(f.calls)
			if tc.reachable && *set != 1 {
				t.Errorf("shutdown event set %d times, want 1 (the graceful path comes first)", *set)
			}
			if gotStop := strings.Contains(all, "Stop-ScheduledTask"); gotStop == tc.drains {
				t.Errorf("Stop-ScheduledTask ran = %v, want %v:\n%s", gotStop, !tc.drains, all)
			}
		})
	}
}

// The task state is not the whole proof: the port is what the next instance
// needs, and it stays bound while the process tears down.
func TestRestart_Windows_WaitsForThePortToBeReleased(t *testing.T) {
	home := withTestHome(t, "windows")
	taskPath := filepath.Join(home, "AppData", "Local", "cartographer", "tasks", "serve.xml")
	configPath := filepath.Join(home, "server.yaml")
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskPath, []byte(RenderWindowsTaskXML(`C:\bin\cartographer.exe`, configPath, `C:\logs\server.log`, `HOST\user`)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("http: \"127.0.0.1:39273\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeServeTask{running: true, drains: true}
	eventFor(t, f, true)
	portHeld := 5
	var probedAddr string
	serveAddrAnswers = func(addr string) bool {
		probedAddr = addr
		if f.running {
			return true
		}
		if portHeld > 0 {
			portHeld--
			return true
		}
		return false
	}
	m := &Manager{run: f.run}
	if err := m.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if probedAddr != "127.0.0.1:39273" {
		t.Errorf("port probed = %q, want the address of the config the task names", probedAddr)
	}
	if portHeld != 0 {
		t.Errorf("the task was started with the port still held (%d probes left)", portHeld)
	}
}

// A process that survives both the drain and Stop-ScheduledTask is reported,
// and the start is still attempted: skipping it would leave a server that
// exited one tick after the deadline down for good.
func TestRestart_Windows_ReportsASurvivor(t *testing.T) {
	withTestHome(t, "windows")
	f := &fakeServeTask{running: true, ignoresStop: true}
	eventFor(t, f, true)
	m := &Manager{run: f.run}
	err := m.Restart()
	if err == nil || !strings.Contains(err.Error(), "did not exit") {
		t.Fatalf("Restart = %v, want it to report the survivor", err)
	}
	if !strings.Contains(joinedCalls(f.calls), "Start-ScheduledTask") {
		t.Error("the start was skipped after a failed stop")
	}
}

// A task unregistered by hand (or by an older uninstall) must still be
// restartable from the definition on disk — the analogue of the darwin
// kickstart-cannot-revive fallback.
func TestRestart_Windows_FallsBackToRegisterWhenUnknown(t *testing.T) {
	home := withTestHome(t, "windows")
	taskPath := filepath.Join(home, "AppData", "Local", "cartographer", "tasks", "serve.xml")
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskPath, []byte(RenderWindowsTaskXML(`C:\bin\cartographer.exe`, `C:\cfg\server.yaml`, `C:\logs\server.log`, `HOST\user`)), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &stubRunner{fail: map[string]bool{"Get-ScheduledTask -TaskName 'Serve'": true}}
	m := &Manager{run: s.run}
	if err := m.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	all := ""
	for _, c := range s.calls {
		all += strings.Join(c, " ") + "\n"
	}
	if !strings.Contains(all, "Register-ScheduledTask") {
		t.Errorf("an unregistered task was not re-registered before starting:\n%s", all)
	}
}

func TestStart_Windows_WithoutADefinitionSaysSo(t *testing.T) {
	withTestHome(t, "windows")
	s := &stubRunner{fail: map[string]bool{"Get-ScheduledTask -TaskName 'Serve'": true}}
	m := &Manager{run: s.run}
	err := m.Start()
	if err == nil {
		t.Fatal("Start with no task definition should fail")
	}
	if !strings.Contains(err.Error(), "service install") {
		t.Errorf("error = %v, want it to name the command that fixes it", err)
	}
}

func TestUninstall_Windows_UnregistersAndRemovesTheDefinition(t *testing.T) {
	home := withTestHome(t, "windows")
	taskPath := filepath.Join(home, "AppData", "Local", "cartographer", "tasks", "serve.xml")
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskPath, []byte("<Task/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeServeTask{}
	m := &Manager{run: f.run}
	if err := m.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(taskPath); !os.IsNotExist(err) {
		t.Errorf("the task definition survived uninstall: %v", err)
	}
	var unregister []string
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c, " "), "Unregister-ScheduledTask") {
			unregister = c
		}
	}
	if unregister == nil {
		t.Fatalf("Uninstall never unregistered the task: %v", f.calls)
	}
	if !strings.Contains(strings.Join(unregister, " "), "-Confirm:$false") {
		t.Errorf("Unregister without -Confirm:$false blocks on a prompt no service manager can answer: %v", unregister)
	}
}

// #413: unregistering a task does not end its process, and the orphan kept the
// port and the data files locked. Uninstall stops the server — gracefully
// first — and unregisters only once it has exited.
func TestUninstall_Windows_StopsTheServerBeforeUnregistering(t *testing.T) {
	for _, tc := range []struct {
		name      string
		reachable bool
	}{
		{"graceful drain", true},
		{"event unreachable, the task is stopped", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withTestHome(t, "windows")
			f := &fakeServeTask{running: true, drains: true, lingerPolls: 5}
			set := eventFor(t, f, tc.reachable)
			var runningAtUnregister bool
			run := func(name string, args ...string) (string, error) {
				if strings.Contains(strings.Join(args, " "), "Unregister-ScheduledTask") {
					runningAtUnregister = f.running
				}
				return f.run(name, args...)
			}
			m := &Manager{run: run}
			if err := m.Uninstall(); err != nil {
				t.Fatalf("Uninstall: %v", err)
			}
			if runningAtUnregister {
				t.Errorf("the task was unregistered with its process still running:\n%s", joinedCalls(f.calls))
			}
			if tc.reachable && *set != 1 {
				t.Errorf("shutdown event set %d times, want 1 (graceful, as a restart)", *set)
			}
		})
	}
}

// A survivor does not keep the definition alive, but it is not a silent
// success either: it still holds the files the user is about to remove.
func TestUninstall_Windows_ReportsASurvivor(t *testing.T) {
	home := withTestHome(t, "windows")
	taskPath := filepath.Join(home, "AppData", "Local", "cartographer", "tasks", "serve.xml")
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskPath, []byte("<Task/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeServeTask{running: true, ignoresStop: true}
	eventFor(t, f, false)
	m := &Manager{run: f.run}
	err := m.Uninstall()
	if err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("Uninstall = %v, want it to report the surviving process", err)
	}
	if _, statErr := os.Stat(taskPath); !os.IsNotExist(statErr) {
		t.Errorf("the task definition survived uninstall: %v", statErr)
	}
}

// Uninstalling what the scheduler does not know is a success: the file on disk
// is what has to go.
func TestUninstall_Windows_UnknownTaskIsNotAFailure(t *testing.T) {
	withTestHome(t, "windows")
	s := &stubRunner{fail: map[string]bool{"Unregister-ScheduledTask": true, "Get-ScheduledTask": true}}
	m := &Manager{run: s.run}
	if err := m.Uninstall(); err != nil {
		t.Errorf("Uninstall with an unregistered task = %v, want success", err)
	}
}

func TestStatus_Windows_InstalledIsTheFileOnDisk(t *testing.T) {
	home := withTestHome(t, "windows")
	configPath := filepath.Join(home, "server.yaml")
	if err := os.WriteFile(configPath, []byte("http: \"127.0.0.1:1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := newTestManager()
	st, err := m.Status(configPath)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Installed {
		t.Error("Installed should be false with no task definition on disk, whatever the scheduler says")
	}
	if st.Lifecycle != LifecycleNotInstalled {
		t.Errorf("Lifecycle = %q, want %q", st.Lifecycle, LifecycleNotInstalled)
	}

	taskPath := filepath.Join(home, "AppData", "Local", "cartographer", "tasks", "serve.xml")
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskPath, []byte("<Task/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err = m.Status(configPath)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed {
		t.Error("Installed should be true once the definition is on disk")
	}
	if !st.Running {
		t.Error("Running should be true when Get-ScheduledTask succeeds")
	}
	if st.Lifecycle != LifecycleLoaded {
		t.Errorf("Lifecycle = %q, want %q", st.Lifecycle, LifecycleLoaded)
	}
}

func TestEffectiveConfigPath_Windows(t *testing.T) {
	t.Run("discovers the generated definition, spaces included", func(t *testing.T) {
		home := withTestHome(t, "windows")
		configPath := filepath.Join(home, "Program Files", "cartographer", "server.yaml")
		taskPath := filepath.Join(home, "AppData", "Local", "cartographer", "tasks", "serve.xml")
		if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
			t.Fatal(err)
		}
		xml := RenderWindowsTaskXML(`C:\bin\cartographer.exe`, configPath, `C:\logs\server.log`, `HOST\user`)
		if err := os.WriteFile(taskPath, []byte(xml), 0o644); err != nil {
			t.Fatal(err)
		}
		m, _ := newTestManager()
		got, err := m.EffectiveConfigPath("")
		if err != nil {
			t.Fatalf("EffectiveConfigPath: %v", err)
		}
		if got != configPath {
			t.Errorf("EffectiveConfigPath = %q, want %q", got, configPath)
		}
	})

	t.Run("no definition falls back to the standard path", func(t *testing.T) {
		home := withTestHome(t, "windows")
		m, _ := newTestManager()
		got, err := m.EffectiveConfigPath("")
		if err != nil {
			t.Fatalf("EffectiveConfigPath: %v", err)
		}
		if want := filepath.Join(home, "AppData", "Roaming", "cartographer", "server.yaml"); got != want {
			t.Errorf("EffectiveConfigPath = %q, want %q", got, want)
		}
	})

	// D121's contract: a definition that exists but says nothing usable is an
	// error, never a silent fall back to the default path — the caller is about
	// to signal a process on the strength of this answer.
	t.Run("a malformed definition is an error, not a fallback", func(t *testing.T) {
		home := withTestHome(t, "windows")
		taskPath := filepath.Join(home, "AppData", "Local", "cartographer", "tasks", "serve.xml")
		if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(taskPath, []byte("<Task><Actions><Exec><Command>x</Command></Exec></Actions></Task>"), 0o644); err != nil {
			t.Fatal(err)
		}
		m, _ := newTestManager()
		if _, err := m.EffectiveConfigPath(""); err == nil {
			t.Error("a definition with no Arguments should be an error")
		}
	})
}

// The drain is the whole reason the named event exists: without the explicit
// relaunch afterwards, nothing brings the server back, because a Scheduled Task
// is not a supervisor.
func TestSignalGraceful_Windows_SetsTheEventThenRestarts(t *testing.T) {
	withTestHome(t, "windows")
	f := &fakeServeTask{running: true, drains: true}
	set := eventFor(t, f, true)
	m := &Manager{run: f.run}
	if err := m.signalGraceful(); err != nil {
		t.Fatalf("signalGraceful: %v", err)
	}
	if *set != 1 {
		t.Errorf("shutdown event set %d times, want 1", *set)
	}
	all := joinedCalls(f.calls)
	if !strings.Contains(all, "Start-ScheduledTask") {
		t.Errorf("the task was not started again after the drain, so nothing would bring the server back:\n%s", all)
	}
	if strings.Contains(all, "Stop-ScheduledTask") {
		t.Errorf("a drained server was also killed:\n%s", all)
	}
	noGUIDomain(t, f.calls)
}

// #415 item 4: from another logon session (SSH, while the server runs on the
// desktop) Local\cartographer-serve-shutdown cannot be opened, and
// upgrade-repair — and the tail of an auto-patch apply — failed with a bare
// "cannot find the file specified". The replacement falls back to stopping the
// task, and says why in terms of the session.
func TestSignalGraceful_Windows_FallsBackToStoppingTheTask(t *testing.T) {
	withTestHome(t, "windows")
	f := &fakeServeTask{running: true, lingerPolls: 3}
	eventFor(t, f, false)
	var notice string
	noticef = func(format string, args ...any) { notice = fmt.Sprintf(format, args...) }

	m := &Manager{run: f.run}
	if err := m.signalGraceful(); err != nil {
		t.Fatalf("signalGraceful: %v", err)
	}
	all := joinedCalls(f.calls)
	stop, start := strings.Index(all, "Stop-ScheduledTask"), strings.Index(all, "Start-ScheduledTask")
	if stop < 0 || start < stop {
		t.Errorf("want Stop-ScheduledTask, then Start-ScheduledTask:\n%s", all)
	}
	if f.startedWhileRunning != 0 {
		t.Error("the task was started before the stopped process exited")
	}
	for _, want := range []string{"logon session", "SSH", "stopping the scheduled task", os.ErrNotExist.Error()} {
		if !strings.Contains(notice, want) {
			t.Errorf("fallback notice %q does not mention %q", notice, want)
		}
	}
}

func TestPSQuote(t *testing.T) {
	cases := map[string]string{
		`C:\bin\x.exe`:           `'C:\bin\x.exe'`,
		`C:\Program Files\x.exe`: `'C:\Program Files\x.exe'`,
		`C:\it's\x.exe`:          `'C:\it''s\x.exe'`,
		`C:\$env\x.exe`:          `'C:\$env\x.exe'`,
	}
	for in, want := range cases {
		if got := psQuote(in); got != want {
			t.Errorf("psQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// A re-install over a running serve task must end that process first: the
// start that follows is a no-op on a running task (IgnoreNew), so the old
// process would keep serving the old config while install reported success.
func TestInstall_Windows_StopsARunningTaskBeforeStarting(t *testing.T) {
	for _, tc := range []struct {
		name     string
		running  bool
		wantStop bool
	}{
		{"running", true, true},
		{"not running", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := withTestHome(t, "windows")
			binPath := filepath.Join(t.TempDir(), "cartographer.exe")
			if err := os.WriteFile(binPath, []byte("bin"), 0o644); err != nil {
				t.Fatal(err)
			}
			origExecutable := osExecutable
			osExecutable = func() (string, error) { return binPath, nil }
			t.Cleanup(func() { osExecutable = origExecutable })
			// The event cannot be set (no server listening for it): the task
			// is stopped outright rather than waited on.
			origEvent := setShutdownEvent
			setShutdownEvent = func() error { return os.ErrNotExist }
			t.Cleanup(func() { setShutdownEvent = origEvent })

			s := &stubRunner{fail: map[string]bool{}}
			if !tc.running {
				s.fail["State -eq 'Running'"] = true
			}
			m := &Manager{run: s.run}
			if _, err := m.Install(InstallOptions{DataDir: filepath.Join(home, "data"), HTTPAddr: "127.0.0.1:39273"}); err != nil {
				t.Fatalf("Install: %v", err)
			}
			stop, start := -1, -1
			for i, c := range s.calls {
				joined := strings.Join(c, " ")
				if strings.Contains(joined, "Stop-ScheduledTask") && stop < 0 {
					stop = i
				}
				if strings.Contains(joined, "Start-ScheduledTask") {
					start = i
				}
			}
			if start < 0 {
				t.Fatalf("the task was never started: %v", s.calls)
			}
			if tc.wantStop && (stop < 0 || stop > start) {
				t.Errorf("a running task must be stopped before the start: %v", s.calls)
			}
			if !tc.wantStop && stop >= 0 {
				t.Errorf("a task that is not running was stopped: %v", s.calls)
			}
		})
	}
}

// The logon trigger is scoped to the installing user: an unscoped one fires at
// any user's logon and needs administrator rights to register, which is how a
// standard user got "Access denied" and no service (verified on Windows 11).
func TestRenderWindowsTaskXML_ScopesTheLogonTriggerToItsUser(t *testing.T) {
	out := RenderWindowsTaskXML(`C:\bin\cartographer.exe`, `C:\cfg\server.yaml`, `C:\logs\server.log`, `CORP\R&D user`)
	want := "<LogonTrigger>\n      <Enabled>true</Enabled>\n      <UserId>CORP\\R&amp;D user</UserId>\n    </LogonTrigger>"
	if !strings.Contains(out, want) {
		t.Errorf("logon trigger not scoped to the user:\n%s", out)
	}
	assertTaskXMLDeclaresNoEncoding(t, out)

	if unscoped := RenderWindowsTaskXML(`C:\bin\cartographer.exe`, `C:\cfg\server.yaml`, `C:\logs\server.log`, ""); strings.Contains(unscoped, "<UserId>") {
		t.Errorf("an unresolved user must not render an empty UserId:\n%s", unscoped)
	}
}
