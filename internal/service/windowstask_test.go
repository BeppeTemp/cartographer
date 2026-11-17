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
	"errors"
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
	out := RenderWindowsTaskXML(`C:\bin\cartographer.exe`, `C:\Users\Nome Cognome\AppData\Roaming\cartographer\server.yaml`, `C:\logs\server.log`, `HOST\user`, "")

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
	out := RenderWindowsTaskXML(`C:\R&D\cartographer.exe`, `C:\cfg\server.yaml`, `C:\logs\server.log`, `HOST\user`, "")
	if !strings.Contains(out, `<Command>C:\R&amp;D\cartographer.exe</Command>`) {
		t.Errorf("an ampersand in the path was not escaped, which makes the whole definition unparseable:\n%s", out)
	}
	// And it round-trips: the reader has to give the path back as written.
	got, err := extractTaskConfigPath([]byte(RenderWindowsTaskXML(`C:\R&D\cartographer.exe`, `C:\R&D\server.yaml`, `C:\logs\server.log`, `HOST\user`, "")))
	if err != nil {
		t.Fatalf("extractTaskConfigPath: %v", err)
	}
	if got != `C:\R&D\server.yaml` {
		t.Errorf("extractTaskConfigPath = %q, want the unescaped path", got)
	}
}

func TestRenderWindowsSyncTaskXML(t *testing.T) {
	out := RenderWindowsSyncTaskXML(`C:\bin\cartographer.exe`, `C:\logs\sync.log`, 20*time.Minute, `HOST\user`, "")
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
	out := RenderWindowsTaskXML(`C:\bin\cartographer.exe`, `C:\cfg\server.yaml`, `C:\logs\server.log`, `HOST\user`, "")
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
	stub.fail = map[string]bool{"State -eq 'Running'": true} // a first install: nothing registered, nothing running
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
	// enabled makes the task answer "enabled" to the Disabled probe (the zero
	// value is a task Stop left disabled, as before).
	enabled bool
	// denyEnable refuses Enable-ScheduledTask the way an admin-owned task does
	// (exit taskDeniedExit); owner is what the owner query then answers.
	denyEnable bool
	// denyRegister refuses Register-ScheduledTask the same way.
	denyRegister bool
	// denyRegisterN refuses the next N registrations, then lets them through.
	denyRegisterN int
	owner         string
	// registered holds the definition each Register-ScheduledTask was given,
	// read from the side file its script names.
	registered []string
	// ownerDiffers makes the descriptor probe answer "the user has no full
	// control" (taskOwnerDiffersExit) until the task is unregistered.
	ownerDiffers bool
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
	if strings.Contains(key, "Register-ScheduledTask") && !strings.Contains(key, "Unregister") {
		if i := strings.Index(key, "-LiteralPath '"); i >= 0 {
			rest := key[i+len("-LiteralPath '"):]
			if j := strings.Index(rest, "'"); j >= 0 {
				if data, err := os.ReadFile(rest[:j]); err == nil {
					f.registered = append(f.registered, string(data))
				}
			}
		}
	}
	switch {
	case strings.Contains(key, "State -eq 'Disabled'"):
		if f.enabled {
			return "", exitCodeErr(taskNotDisabledExit)
		}
	case strings.Contains(key, "Enable-ScheduledTask") && f.denyEnable:
		return "", exitCodeErr(taskDeniedExit)
	case strings.Contains(key, "Register-ScheduledTask") && f.denyRegister:
		return "", exitCodeErr(taskDeniedExit)
	case strings.Contains(key, "Register-ScheduledTask") && !strings.Contains(key, "Unregister") && f.denyRegisterN > 0:
		f.denyRegisterN--
		return "", exitCodeErr(taskDeniedExit)
	case strings.Contains(key, "AccessMask"):
		if f.ownerDiffers {
			return "", exitCodeErr(taskOwnerDiffersExit)
		}
	case strings.Contains(key, "Unregister-ScheduledTask"):
		f.ownerDiffers = false
	case strings.Contains(key, "RawSecurityDescriptor"):
		return f.owner + "\r\n", nil
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
	if err := os.WriteFile(taskPath, []byte(RenderWindowsTaskXML(`C:\bin\cartographer.exe`, configPath, `C:\logs\server.log`, `HOST\user`, "")), 0o644); err != nil {
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
	if err := os.WriteFile(taskPath, []byte(RenderWindowsTaskXML(`C:\bin\cartographer.exe`, `C:\cfg\server.yaml`, `C:\logs\server.log`, `HOST\user`, "")), 0o644); err != nil {
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
	if err := m.Uninstall(false); err != nil {
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
			if err := m.Uninstall(false); err != nil {
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
	err := m.Uninstall(false)
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
	s := &stubRunner{fail: map[string]bool{"Unregister-ScheduledTask": true}}
	run := func(name string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "Get-ScheduledTask") {
			s.calls = append(s.calls, append([]string{name}, args...))
			return "", exitCodeErr(taskNotFoundExit) // the scheduler does not know the task
		}
		return s.run(name, args...)
	}
	m := &Manager{run: run}
	if err := m.Uninstall(false); err != nil {
		t.Errorf("Uninstall with an unregistered task = %v, want success", err)
	}
}

// exitCodeErr models the exit status of a PowerShell run.
type exitCodeErr int

func (e exitCodeErr) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e exitCodeErr) ExitCode() int { return int(e) }

// A probe that itself fails (no module, no permission) is not "not registered":
// the uninstall cannot tell, and says so instead of reporting success.
func TestUninstall_Windows_FailedProbeIsAnError(t *testing.T) {
	withTestHome(t, "windows")
	s := &stubRunner{fail: map[string]bool{"Unregister-ScheduledTask": true}}
	run := func(name string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "-ErrorAction SilentlyContinue") {
			return "", exitCodeErr(1)
		}
		return s.run(name, args...)
	}
	err := (&Manager{run: run}).Uninstall(false)
	if err == nil || !strings.Contains(err.Error(), "could not tell") {
		t.Fatalf("Uninstall = %v, want an error saying the registration could not be checked", err)
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
		t.Error("Running should be true when the task is registered and the state query gives no stopped verdict")
	}
	if st.Lifecycle != LifecycleLoaded {
		t.Errorf("Lifecycle = %q, want %q", st.Lifecycle, LifecycleLoaded)
	}
}

// TestStatus_Windows_ReadsTheTaskState pins D1014: Running is "registered and
// not stopped", decided inside PowerShell, and fails open.
func TestStatus_Windows_ReadsTheTaskState(t *testing.T) {
	cases := []struct {
		name        string
		state       string // the task State the scenario simulates; "" = not registered
		stateOut    string // the token the state script prints, when stateOutSet
		stateOutSet bool
		stateFails  bool
		wantRunning bool
		wantLife    Lifecycle
	}{
		{name: "disabled (what Stop leaves)", state: "Disabled", wantRunning: false, wantLife: LifecycleNotLoaded},
		{name: "ready (no process)", state: "Ready", wantRunning: false, wantLife: LifecycleNotLoaded},
		{name: "running", state: "Running", wantRunning: true, wantLife: LifecycleLoaded},
		{name: "queued", state: "Queued", wantRunning: true, wantLife: LifecycleLoaded},
		{name: "state query fails: fail open", state: "Disabled", stateFails: true, wantRunning: true, wantLife: LifecycleLoaded},
		{name: "empty output: fail open", state: "Disabled", stateOutSet: true, wantRunning: true, wantLife: LifecycleLoaded},
		{name: "localised word: fail open", state: "Disabled", stateOut: "Disattivato", stateOutSet: true, wantRunning: true, wantLife: LifecycleLoaded},
		{name: "not registered", state: "", wantRunning: false, wantLife: LifecycleNotLoaded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := withTestHome(t, "windows")
			configPath := filepath.Join(home, "server.yaml")
			if err := os.WriteFile(configPath, []byte("http: \"127.0.0.1:1\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			taskPath := filepath.Join(home, "AppData", "Local", "cartographer", "tasks", "serve.xml")
			if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(taskPath, []byte("<Task/>"), 0o644); err != nil {
				t.Fatal(err)
			}
			run := func(name string, args ...string) (string, error) {
				key := strings.Join(append([]string{name}, args...), " ")
				switch {
				case strings.Contains(key, "$s = (Get-ScheduledTask"):
					if tc.stateFails {
						return "", errNotLoaded
					}
					if tc.stateOutSet {
						return tc.stateOut, nil
					}
					if tc.state == "Disabled" || tc.state == "Ready" {
						return "stopped\r\n", nil
					}
					return "live\r\n", nil
				case strings.Contains(key, "Get-ScheduledTask"):
					if tc.state == "" {
						return "", errNotLoaded
					}
				}
				return "", nil
			}
			st, err := (&Manager{run: run}).Status(configPath)
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if st.Running != tc.wantRunning {
				t.Errorf("Running = %v, want %v", st.Running, tc.wantRunning)
			}
			if st.Lifecycle != tc.wantLife {
				t.Errorf("Lifecycle = %q, want %q", st.Lifecycle, tc.wantLife)
			}
		})
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
		xml := RenderWindowsTaskXML(`C:\bin\cartographer.exe`, configPath, `C:\logs\server.log`, `HOST\user`, "")
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

			// A task that exits once stopped, with no definition on disk (it is
			// stopped before its registration is replaced, #2144).
			s := &fakeServeTask{running: tc.running, enabled: true}
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
	out := RenderWindowsTaskXML(`C:\bin\cartographer.exe`, `C:\cfg\server.yaml`, `C:\logs\server.log`, `CORP\R&D user`, "")
	want := "<LogonTrigger>\n      <Enabled>true</Enabled>\n      <UserId>CORP\\R&amp;D user</UserId>\n    </LogonTrigger>"
	if !strings.Contains(out, want) {
		t.Errorf("logon trigger not scoped to the user:\n%s", out)
	}
	assertTaskXMLDeclaresNoEncoding(t, out)

	if unscoped := RenderWindowsTaskXML(`C:\bin\cartographer.exe`, `C:\cfg\server.yaml`, `C:\logs\server.log`, "", ""); strings.Contains(unscoped, "<UserId>") {
		t.Errorf("an unresolved user must not render an empty UserId:\n%s", unscoped)
	}
}

// #1339: the wait's budget counts polls, not wall-clock time. A sleep that
// takes far longer than the poll interval (a loaded runner) must not shorten
// the number of polls a lingering process gets.
func TestWaitServeExit_Windows_BudgetCountsPollsNotTime(t *testing.T) {
	withTestHome(t, "windows")
	windowsSleep = func(time.Duration) { time.Sleep(5 * time.Millisecond) }
	f := &fakeServeTask{running: true, stopping: true, lingerPolls: 30}
	m := &Manager{run: f.run}
	if !m.waitServeExit("", windowsDrainTimeout) {
		t.Fatalf("a process that exits within the poll budget was reported as a survivor (%d polls left)", f.lingerPolls)
	}
}

// An unregister that fails while the task is still registered must not be
// reported as an uninstall: the task keeps its logon trigger and would restart
// the program at the next login. The definition stays, so a retry is possible.
func TestUninstall_Windows_StillRegisteredIsAnError(t *testing.T) {
	home := withTestHome(t, "windows")
	taskPath := filepath.Join(home, "AppData", "Local", "cartographer", "tasks", "serve.xml")
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskPath, []byte("<Task/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Get-ScheduledTask keeps succeeding: the scheduler still knows the task.
	s := &stubRunner{fail: map[string]bool{"Unregister-ScheduledTask": true, "State -eq 'Running'": true}}
	m := &Manager{run: s.run}
	err := m.Uninstall(false)
	if err == nil || !strings.Contains(err.Error(), "still registered") {
		t.Fatalf("Uninstall = %v, want an error saying the task is still registered", err)
	}
	if _, statErr := os.Stat(taskPath); statErr != nil {
		t.Errorf("the definition was removed although the task is still registered: %v", statErr)
	}
}

// Starting a disabled task succeeds without running anything, so a failed
// Enable-ScheduledTask must fail the start instead of printing "started".
func TestStart_Windows_FailedEnableFailsTheStart(t *testing.T) {
	withTestHome(t, "windows")
	s := &stubRunner{fail: map[string]bool{"Enable-ScheduledTask": true}}
	m := &Manager{run: s.run}
	err := m.Start()
	if err == nil || !strings.Contains(err.Error(), "enable scheduled task") {
		t.Fatalf("Start = %v, want the Enable failure", err)
	}
	if strings.Contains(joinedCalls(s.calls), "Start-ScheduledTask") {
		t.Errorf("Start-ScheduledTask ran after a failed Enable:\n%s", joinedCalls(s.calls))
	}
}

// The task's command line is the previous stable's (#2094): a flag it does not
// know makes a downgraded task exit 2. The config path stays readable from it.
func TestWindowsTaskArgumentsAreThePreviousStables(t *testing.T) {
	out := RenderWindowsTaskXML(`C:\bin\cartographer.exe`, `C:\Users\Nome Cognome\server.yaml`, `C:\logs\server.log`, `HOST\user`, "")
	want := `<Arguments>serve --config "C:\Users\Nome Cognome\server.yaml" --log-file "C:\logs\server.log"</Arguments>`
	if !strings.Contains(out, want) {
		t.Fatalf("task XML lacks %q:\n%s", want, out)
	}
	got, err := extractTaskConfigPath([]byte(out))
	if err != nil || got != `C:\Users\Nome Cognome\server.yaml` {
		t.Errorf("extractTaskConfigPath = %q, %v", got, err)
	}
}

// A task written by a release candidate passes `--supervised --env-file X`, which
// the previous stable does not know (#2094): Status reports it stale so that
// upgrade-repair re-renders it; the current render is not stale.
func TestServiceArgsStale_WindowsTask(t *testing.T) {
	home := withTestHome(t, "windows")
	taskPath, err := WindowsTaskPath()
	if err != nil {
		t.Fatal(err)
	}
	logPath, err := WindowsLogPath()
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "server.yaml")
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(xml string) {
		t.Helper()
		if err := os.WriteFile(taskPath, []byte(xml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := &Manager{}
	current := RenderWindowsTaskXML(`C:\bin\cartographer.exe`, configPath, logPath, `HOST\user`, "")
	write(current)
	if m.ServiceArgsStale(configPath) {
		t.Error("the current render must not be stale")
	}
	candidate := strings.Replace(current, `</Arguments>`, ` --supervised --env-file "`+EnvFilePath(configPath)+`"</Arguments>`, 1)
	write(candidate)
	if !m.ServiceArgsStale(configPath) {
		t.Error("a release candidate's arguments must be stale")
	}
	goos = "linux"
	if m.ServiceArgsStale(configPath) {
		t.Error("only the Windows task is compared")
	}
}

// installOnAddress runs a Windows Install over a fake serve task whose address
// answers as state says, and returns the error and the sleeps taken.
func installOnAddress(t *testing.T, f *fakeServeTask, state ListenerState) (error, int) {
	t.Helper()
	home := withTestHome(t, "windows")
	binPath := filepath.Join(t.TempDir(), "cartographer.exe")
	if err := os.WriteFile(binPath, []byte("bin"), 0o644); err != nil {
		t.Fatal(err)
	}
	origExecutable := osExecutable
	osExecutable = func() (string, error) { return binPath, nil }
	t.Cleanup(func() { osExecutable = origExecutable })
	sleeps := 0
	windowsSleep = func(time.Duration) { sleeps++ }
	listenerAt = func(string) ListenerState { return state }
	serveAddrAnswers = func(string) bool { return state != ListenerNone }
	m := &Manager{run: f.run}
	_, err := m.Install(InstallOptions{DataDir: filepath.Join(home, "data"), HTTPAddr: "127.0.0.1:39273"})
	return err, sleeps
}

// #2092: an install next to a program that holds the address printed "installed
// and started" while the server died on bind. Nothing of ours is Running, so
// no stop wait either.
func TestInstall_Windows_RefusesAnAddressAnotherProgramHolds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state ListenerState
		want  string
	}{
		{"foreign program", ListenerForeign, "another program answers on 127.0.0.1:39273"},
		{"cartographer not started by the task", ListenerCartographer, "already answers on 127.0.0.1:39273"},
		{"a program that accepts and never answers", ListenerUnresponsive, "accepted the connection on 127.0.0.1:39273 but did not answer /health"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeServeTask{}
			err, sleeps := installOnAddress(t, f, tc.state)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "nothing was changed") {
				t.Fatalf("Install error = %v, want %q and the nothing-changed note", err, tc.want)
			}
			if all := joinedCalls(f.calls); strings.Contains(all, "Start-ScheduledTask") {
				t.Errorf("the task was started next to a busy address:\n%s", all)
			}
			if strings.Contains(joinedCalls(f.calls), "Register-ScheduledTask") {
				t.Error("a refused install must register nothing (#2179)")
			}
			if sleeps != 0 {
				t.Errorf("slept %d times: a task that is not Running has nothing to wait for", sleeps)
			}
		})
	}
}

func TestInstall_Windows_StartsWhenTheAddressIsFree(t *testing.T) {
	f := &fakeServeTask{}
	if err, _ := installOnAddress(t, f, ListenerNone); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !strings.Contains(joinedCalls(f.calls), "Start-ScheduledTask") || !f.running {
		t.Errorf("the task was not started:\n%s", joinedCalls(f.calls))
	}
}

// replaceOver runs a Windows Install whose default config differs from the one
// the installed definition names, over a running fake serve task. The shutdown
// event is recorded in the call log, so the order against the registration is
// what the tests read.
func replaceOver(t *testing.T, f *fakeServeTask, withDefinition ...bool) (error, string) {
	t.Helper()
	return replaceOverListener(t, f, func() ListenerState { return ListenerNone }, withDefinition...)
}

// replaceOverListener is replaceOver with the state listenerAt answers.
func replaceOverListener(t *testing.T, f *fakeServeTask, listener func() ListenerState, withDefinition ...bool) (error, string) {
	t.Helper()
	home := withTestHome(t, "windows")
	binPath := filepath.Join(t.TempDir(), "cartographer.exe")
	if err := os.WriteFile(binPath, []byte("bin"), 0o644); err != nil {
		t.Fatal(err)
	}
	origExecutable, origEvent, origAnswers, origSleep := osExecutable, setShutdownEvent, serveAddrAnswers, windowsSleep
	osExecutable = func() (string, error) { return binPath, nil }
	setShutdownEvent = func() error {
		f.calls = append(f.calls, []string{"SHUTDOWN-EVENT"})
		if f.drains {
			f.stop()
		}
		return nil
	}
	serveAddrAnswers = func(string) bool { return f.running }
	windowsSleep = func(time.Duration) {}
	listenerAt = func(string) ListenerState { return listener() }
	t.Cleanup(func() {
		osExecutable, setShutdownEvent, serveAddrAnswers, windowsSleep = origExecutable, origEvent, origAnswers, origSleep
	})

	oldConfig := filepath.Join(home, "old", "server.yaml")
	if err := os.MkdirAll(filepath.Dir(oldConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldConfig, []byte("http: \"127.0.0.1:39274\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	taskPath, err := WindowsTaskPath()
	if err != nil {
		t.Fatal(err)
	}
	logPath, _ := WindowsLogPath()
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		t.Fatal(err)
	}
	old := RenderWindowsTaskXML(`C:\old\cartographer.exe`, oldConfig, logPath, "", "")
	if len(withDefinition) == 0 || withDefinition[0] {
		if err := os.WriteFile(taskPath, []byte(old), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := &Manager{run: f.run}
	_, err = m.Install(InstallOptions{DataDir: filepath.Join(home, "data"), HTTPAddr: "127.0.0.1:39273"})
	return err, taskPath
}

func callIndex(calls [][]string, sub string) int {
	for i, c := range calls {
		if strings.Contains(strings.Join(c, " "), sub) {
			return i
		}
	}
	return -1
}

// #2144: an install that replaces the task with another config stops the old
// instance (the graceful stop) before registering: once the new registration is
// in, the old process belongs to no task and would hold its port for good.
func TestInstall_Windows_ReplacingTheDefinitionStopsTheOldServerFirst(t *testing.T) {
	f := &fakeServeTask{running: true, drains: true, enabled: true}
	err, _ := replaceOver(t, f)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	event, register := callIndex(f.calls, "SHUTDOWN-EVENT"), callIndex(f.calls, "Register-ScheduledTask")
	if event < 0 || register < 0 || event > register {
		t.Errorf("the old server must be stopped before the registration (event at %d, register at %d):\n%s", event, register, joinedCalls(f.calls))
	}
	if callIndex(f.calls, "Start-ScheduledTask") < register || !f.running {
		t.Errorf("the new task must be started after the registration:\n%s", joinedCalls(f.calls))
	}
	if f.startedWhileRunning != 0 {
		t.Errorf("the new instance was started while the old one ran: %d", f.startedWhileRunning)
	}
}

// A server that survives the stop leaves the old task registered, and the
// install says why instead of orphaning it.
func TestInstall_Windows_ReplacingTheDefinitionFailsWhenTheOldServerSurvives(t *testing.T) {
	f := &fakeServeTask{running: true, enabled: true, ignoresStop: true}
	err, taskPath := replaceOver(t, f)
	if err == nil || !strings.Contains(err.Error(), "could not be stopped before its task is replaced") {
		t.Fatalf("Install = %v, want the stop refusal", err)
	}
	if callIndex(f.calls, "Register-ScheduledTask") >= 0 {
		t.Errorf("the task was registered over a server that is still running:\n%s", joinedCalls(f.calls))
	}
	if data, _ := os.ReadFile(taskPath); !strings.Contains(string(data), `C:\old\cartographer.exe`) {
		t.Errorf("the installed definition was replaced:\n%s", data)
	}
}

// A registration the scheduler refuses after the stop starts the old task
// again, from the definition still registered.
func TestInstall_Windows_RefusedReplacementRestartsTheOldServer(t *testing.T) {
	f := &fakeServeTask{running: true, drains: true, enabled: true, denyRegister: true}
	err, _ := replaceOver(t, f)
	if !errors.Is(err, ErrTaskAccessDenied) {
		t.Fatalf("Install = %v, want ErrTaskAccessDenied", err)
	}
	if !f.running {
		t.Errorf("the old server was left stopped:\n%s", joinedCalls(f.calls))
	}
}

// repairBeforeSecond, when set, runs between the two installs of repairInstall.
var repairBeforeSecond func()

// repairNotices, when set, receives the notices of repairInstall's installs.
var repairNotices func(string, ...any)

// repairInstall installs twice over f from a shell that is, or is not, elevated:
// the second one finds the same definition (nothing replaced) and a running
// server, the descriptor probe answers differs and the scheduler refuses the
// registration when deny is set. It returns the calls of
// the second install.
func repairInstall(t *testing.T, f *fakeServeTask, elevated, differs bool, deny int) (error, [][]string) {
	t.Helper()
	home := withTestHome(t, "windows")
	if repairNotices != nil {
		noticef = repairNotices // withTestHome's cleanup restores it
	}
	binPath := filepath.Join(t.TempDir(), "cartographer.exe")
	if err := os.WriteFile(binPath, []byte("bin"), 0o644); err != nil {
		t.Fatal(err)
	}
	origExe, origElev, origUser, origSID, origEvent, origAnswers, origSleep := osExecutable, windowsElevated, windowsTaskUser, windowsTaskSID, setShutdownEvent, serveAddrAnswers, windowsSleep
	osExecutable = func() (string, error) { return binPath, nil }
	windowsElevated = func() bool { return elevated }
	windowsTaskUser = func() string { return `HOST\user` }
	windowsTaskSID = func() string { return testSID }
	setShutdownEvent = func() error {
		f.calls = append(f.calls, []string{"SHUTDOWN-EVENT"})
		if f.drains {
			f.stop()
		}
		return nil
	}
	serveAddrAnswers = func(string) bool { return f.running }
	windowsSleep = func(time.Duration) {}
	listenerAt = func(string) ListenerState { return ListenerNone }
	t.Cleanup(func() {
		osExecutable, windowsElevated, windowsTaskUser, windowsTaskSID, setShutdownEvent, serveAddrAnswers, windowsSleep = origExe, origElev, origUser, origSID, origEvent, origAnswers, origSleep
	})
	m := &Manager{run: f.run}
	opts := InstallOptions{DataDir: filepath.Join(home, "data"), HTTPAddr: "127.0.0.1:39273"}
	if _, err := m.Install(opts); err != nil {
		t.Fatalf("first install: %v", err)
	}
	f.calls, f.running, f.ownerDiffers, f.denyRegisterN = nil, true, differs, deny
	f.registered = nil
	if repairBeforeSecond != nil {
		repairBeforeSecond()
	}
	_, err := m.Install(opts)
	return err, f.calls
}

// #2142: Register -Force keeps the descriptor of the task it overwrites, so an
// elevated install over a task whose descriptor does not grant the user deletes
// it and registers it again, after the running server has been stopped.
func TestInstall_Windows_ElevatedInstallRepairsAnAdminOwnedTask(t *testing.T) {
	f := &fakeServeTask{drains: true, enabled: true}
	err, calls := repairInstall(t, f, true, true, 0)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	event, unregister, register := callIndex(calls, "SHUTDOWN-EVENT"), callIndex(calls, "Unregister-ScheduledTask"), callIndex(calls, "Register-ScheduledTask")
	if event < 0 || unregister < event || register < unregister {
		t.Errorf("want stop, then unregister, then register (event %d, unregister %d, register %d):\n%s", event, unregister, register, joinedCalls(calls))
	}
	if !f.running || f.ownerDiffers {
		t.Errorf("running=%v ownerDiffers=%v after the repair:\n%s", f.running, f.ownerDiffers, joinedCalls(calls))
	}
}

// The decision: a descriptor that already grants the user, or a shell that is
// not elevated, re-registers nothing; the non-elevated one keeps today's refusal.
func TestInstall_Windows_OwnerRepairDecision(t *testing.T) {
	for _, tc := range []struct {
		name              string
		elevated, differs bool
		deny              int
		wantUnregister    bool
		wantProbe         bool
		wantDenied        bool
	}{
		{"elevated, descriptor grants the user", true, false, 0, false, true, false},
		{"not elevated, descriptor differs: the refusal stays", false, true, 99, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeServeTask{drains: true, enabled: true}
			err, calls := repairInstall(t, f, tc.elevated, tc.differs, tc.deny)
			if tc.wantDenied != errors.Is(err, ErrTaskAccessDenied) {
				t.Fatalf("Install = %v, denied want %v", err, tc.wantDenied)
			}
			if got := callIndex(calls, "Unregister-ScheduledTask") >= 0; got != tc.wantUnregister {
				t.Errorf("unregister = %v, want %v:\n%s", got, tc.wantUnregister, joinedCalls(calls))
			}
			if got := callIndex(calls, "AccessMask") >= 0; got != tc.wantProbe {
				t.Errorf("descriptor probe = %v, want %v", got, tc.wantProbe)
			}
		})
	}
}

// #2144, definition file gone: the task is registered and running but there is
// nothing to compare, so it is stopped as for a changed definition.
func TestInstall_Windows_ReplacingWithoutAnInstalledDefinitionStopsTheServer(t *testing.T) {
	f := &fakeServeTask{running: true, drains: true, enabled: true}
	err, _ := replaceOver(t, f, false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	event, register := callIndex(f.calls, "SHUTDOWN-EVENT"), callIndex(f.calls, "Register-ScheduledTask")
	if event < 0 || register < 0 || event > register {
		t.Errorf("the running server must be stopped before the registration (event %d, register %d):\n%s", event, register, joinedCalls(f.calls))
	}
}

// A repair whose registration is refused puts the old task back from the
// definition still on disk and starts it; when that fails too, the error says
// the task is gone and what restores it.
func TestInstall_Windows_RefusedRepairRestoresTheOldTask(t *testing.T) {
	f := &fakeServeTask{drains: true, enabled: true}
	err, calls := repairInstall(t, f, true, true, 1)
	if !errors.Is(err, ErrTaskAccessDenied) {
		t.Fatalf("Install = %v, want the refusal", err)
	}
	registers := 0
	for _, c := range calls {
		if strings.Contains(strings.Join(c, " "), "Register-ScheduledTask") && !strings.Contains(strings.Join(c, " "), "Unregister") {
			registers++
		}
	}
	if registers != 2 || !f.running {
		t.Errorf("registers=%d running=%v, want the old task registered again and started:\n%s", registers, f.running, joinedCalls(calls))
	}
}

func TestInstall_Windows_RefusedRepairThatCannotRestoreSaysTheTaskIsGone(t *testing.T) {
	f := &fakeServeTask{drains: true, enabled: true}
	err, _ := repairInstall(t, f, true, true, 2)
	if err == nil || !strings.Contains(err.Error(), "is no longer registered") || !strings.Contains(err.Error(), "cartographer service install") {
		t.Fatalf("Install = %v, want the task-is-gone message with the remedy", err)
	}
}

// A probe that fails for another reason than "differs" changes nothing and is
// visible: one line on stderr.
func TestInstall_Windows_FailedDescriptorProbeWarns(t *testing.T) {
	var notices []string
	repairNotices = func(format string, args ...any) { notices = append(notices, fmt.Sprintf(format, args...)) }
	t.Cleanup(func() { repairNotices = nil })
	f := &fakeServeTask{drains: true, enabled: true, fail: map[string]bool{"AccessMask": true}}
	err, calls := repairInstall(t, f, true, true, 0)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if callIndex(calls, "Unregister-ScheduledTask") >= 0 {
		t.Error("a failed probe must not re-register the task")
	}
	found := false
	for _, n := range notices {
		found = found || strings.Contains(n, "could not read the descriptor")
	}
	if !found {
		t.Errorf("no warning in %v", notices)
	}
}

// The restore of a refused repair runs under the elevated token: it registers a
// definition rendered from the binary and the config the file names, never the
// bytes of a file a process of the same user could have rewritten.
func TestInstall_Windows_RefusedRepairRestoresARenderedDefinitionNotTheFile(t *testing.T) {
	repairBeforeSecond = func() {
		taskPath, err := WindowsTaskPath()
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(taskPath)
		if err != nil {
			t.Fatal(err)
		}
		planted := strings.Replace(string(data), "</Actions>", "  <Exec><Command>C:\\evil.exe</Command></Exec>\n  </Actions>", 1)
		planted = strings.Replace(planted, "LeastPrivilege", "HighestAvailable", 1)
		if planted == string(data) {
			t.Fatal("the plant changed nothing")
		}
		if err := os.WriteFile(taskPath, []byte(planted), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { repairBeforeSecond = nil })
	f := &fakeServeTask{drains: true, enabled: true}
	err, _ := repairInstall(t, f, true, true, 1)
	if !errors.Is(err, ErrTaskAccessDenied) {
		t.Fatalf("Install = %v, want the refusal", err)
	}
	if len(f.registered) != 1 {
		// the refused registration is captured too: two Register calls, the second is the restore
		if len(f.registered) != 2 {
			t.Fatalf("registered %d definitions, want the refused one and the restore", len(f.registered))
		}
	}
	restored := f.registered[len(f.registered)-1]
	if strings.Contains(restored, "evil") || strings.Contains(restored, "HighestAvailable") {
		t.Errorf("the restored task carries what the file was rewritten with:\n%s", restored)
	}
	if !strings.Contains(restored, "cartographer.exe") {
		t.Errorf("the restored task lost its binary:\n%s", restored)
	}
}

// A definition file that names no binary cannot be restored: nothing is
// registered and the error says the task is gone.
func TestInstall_Windows_RefusedRepairWithAnUnreadableFileSaysTheTaskIsGone(t *testing.T) {
	repairBeforeSecond = func() {
		taskPath, _ := WindowsTaskPath()
		if err := os.WriteFile(taskPath, []byte("<Task/>"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { repairBeforeSecond = nil })
	f := &fakeServeTask{drains: true, enabled: true}
	err, _ := repairInstall(t, f, true, true, 1)
	if err == nil || !strings.Contains(err.Error(), "is no longer registered") || !strings.Contains(err.Error(), "cartographer service install") {
		t.Fatalf("Install = %v, want the task-is-gone message", err)
	}
	if len(f.registered) != 1 {
		t.Errorf("registered %d definitions, want only the refused one", len(f.registered))
	}
}

// An unchanged definition over a running task keeps the order of #1961: the
// registration first, then the stop and the start.
func TestInstall_Windows_UnchangedDefinitionStopsAfterTheRegistration(t *testing.T) {
	f := &fakeServeTask{drains: true, enabled: true}
	err, calls := repairInstall(t, f, false, false, 0)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	register, event, start := callIndex(calls, "Register-ScheduledTask"), callIndex(calls, "SHUTDOWN-EVENT"), callIndex(calls, "Start-ScheduledTask")
	if register < 0 || event < register || start < event || !f.running {
		t.Errorf("want register (%d), stop (%d), start (%d), running=%v:\n%s", register, event, start, f.running, joinedCalls(calls))
	}
}

// #2179: a refused install leaves the previous task and process as they were.
// The address the new config names is held by another program while the old
// server runs on its own: nothing is stopped, registered or rewritten.
func TestInstall_Windows_RefusalBeforeAnythingIsTouched(t *testing.T) {
	f := &fakeServeTask{running: true, drains: true, enabled: true}
	err, taskPath := replaceOverListener(t, f, func() ListenerState { return ListenerForeign })
	if err == nil || !strings.Contains(err.Error(), "another program answers") {
		t.Fatalf("Install = %v, want the listener refusal", err)
	}
	all := joinedCalls(f.calls)
	for _, bad := range []string{"SHUTDOWN-EVENT", "Stop-ScheduledTask", "Register-ScheduledTask", "Unregister-ScheduledTask"} {
		if strings.Contains(all, bad) {
			t.Errorf("%s ran before the refusal:\n%s", bad, all)
		}
	}
	if !f.running {
		t.Error("the old server was stopped")
	}
	if data, _ := os.ReadFile(taskPath); !strings.Contains(string(data), `C:\old\cartographer.exe`) {
		t.Errorf("the installed definition was replaced:\n%s", data)
	}
}

// A listener that appears after the old server was stopped (the pre-check saw
// the address free) puts the previous definition back and starts it again.
func TestInstall_Windows_FailureAfterRegistrationRestoresThePreviousTask(t *testing.T) {
	f := &fakeServeTask{running: true, drains: true, enabled: true}
	err, taskPath := replaceOverListener(t, f, func() ListenerState {
		if f.running {
			return ListenerNone
		}
		return ListenerForeign
	})
	if err == nil || !strings.Contains(err.Error(), "registered again") {
		t.Fatalf("Install = %v, want the refusal naming the restored task", err)
	}
	data, _ := os.ReadFile(taskPath)
	if !strings.Contains(string(data), `C:\old\cartographer.exe`) || strings.Contains(string(data), "39273") {
		t.Errorf("the previous definition was not put back:\n%s", data)
	}
	if n := len(f.registered); n != 2 || !strings.Contains(f.registered[1], `C:\old\cartographer.exe`) {
		t.Errorf("registrations = %d, want the new one then the old one again", n)
	}
	if !f.running {
		t.Error("the previous server was not started again")
	}
}

// #2179: the live DACL comes from the Task Scheduler service, not from the
// cmdlet, which returns the descriptor of the definition XML.
func TestServeTaskOwnerDiffers_ReadsTheLiveDescriptor(t *testing.T) {
	withTestHome(t, "windows")
	var scripts []string
	m := &Manager{run: func(name string, args ...string) (string, error) {
		scripts = append(scripts, strings.Join(args, " "))
		return "", nil
	}}
	if m.serveTaskOwnerDiffers(testSID) {
		t.Error("an exit 0 probe is not differs")
	}
	var probe string
	for _, sc := range scripts {
		if strings.Contains(sc, "AccessMask") {
			probe = sc
		}
	}
	if !strings.Contains(probe, "Schedule.Service") || !strings.Contains(probe, "GetSecurityDescriptor(4)") || !strings.Contains(probe, `GetFolder('\Cartographer')`) {
		t.Errorf("the probe does not read the live descriptor through COM:\n%s", probe)
	}
	if strings.Contains(probe, "(Get-ScheduledTask") {
		t.Errorf("the probe still reads the cmdlet's descriptor:\n%s", probe)
	}
}

// #2179: status reads the config the installed definition passes, not the
// default server.yaml.
func TestStatus_Windows_ReadsTheConfigOfTheInstalledTask(t *testing.T) {
	home := withTestHome(t, "windows")
	taskPath, _ := WindowsTaskPath()
	logPath, _ := WindowsLogPath()
	custom := filepath.Join(home, "other", "server.yaml")
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskPath, []byte(RenderWindowsTaskXML(`C:\bin\cartographer.exe`, custom, logPath, "", "")), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manager{run: (&fakeServeTask{running: true, enabled: true}).run}
	st, err := m.Status("")
	if err != nil {
		t.Fatal(err)
	}
	if st.ConfigPath != custom {
		t.Errorf("ConfigPath = %q, want the installed task's %q", st.ConfigPath, custom)
	}
	if st, _ := m.Status("explicit.yaml"); st.ConfigPath != "explicit.yaml" {
		t.Errorf("an explicit config must win, got %q", st.ConfigPath)
	}
}
