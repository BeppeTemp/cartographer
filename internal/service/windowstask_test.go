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
// from: install.ps1 replaces that same file in place on every upgrade (D238),
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

// ...and a restart must undo that disable, or the server stays down.
func TestRestart_Windows_ReEnablesBeforeStarting(t *testing.T) {
	withTestHome(t, "windows")
	m, stub := newTestManager()
	if err := m.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	all := ""
	for _, c := range stub.calls {
		all += strings.Join(c, " ") + "\n"
	}
	for _, want := range []string{"Get-ScheduledTask", "Stop-ScheduledTask", "Enable-ScheduledTask", "Start-ScheduledTask"} {
		if !strings.Contains(all, want) {
			t.Errorf("restart commands missing %q:\n%s", want, all)
		}
	}
	if strings.Index(all, "Enable-ScheduledTask") > strings.Index(all, "Start-ScheduledTask") {
		t.Errorf("Start-ScheduledTask ran before Enable-ScheduledTask, so a stopped service stays stopped:\n%s", all)
	}
	noGUIDomain(t, stub.calls)
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
	m, stub := newTestManager()
	if err := m.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(taskPath); !os.IsNotExist(err) {
		t.Errorf("the task definition survived uninstall: %v", err)
	}
	if !strings.Contains(strings.Join(stub.calls[0], " "), "Unregister-ScheduledTask") {
		t.Errorf("first call = %v, want Unregister-ScheduledTask", stub.calls[0])
	}
	if !strings.Contains(strings.Join(stub.calls[0], " "), "-Confirm:$false") {
		t.Errorf("Unregister without -Confirm:$false blocks on a prompt no service manager can answer: %v", stub.calls[0])
	}
}

// Uninstalling what the scheduler does not know is a success: the file on disk
// is what has to go.
func TestUninstall_Windows_UnknownTaskIsNotAFailure(t *testing.T) {
	withTestHome(t, "windows")
	s := &stubRunner{fail: map[string]bool{"Unregister-ScheduledTask": true}}
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
	var signalled int
	orig := setShutdownEvent
	setShutdownEvent = func() error { signalled++; return nil }
	t.Cleanup(func() { setShutdownEvent = orig })

	// The task reports itself as not running, so the drain wait returns at once.
	s := &stubRunner{fail: map[string]bool{"State -eq 'Running'": true}}
	m := &Manager{run: s.run}
	if err := m.signalGraceful(); err != nil {
		t.Fatalf("signalGraceful: %v", err)
	}
	if signalled != 1 {
		t.Errorf("shutdown event set %d times, want 1", signalled)
	}
	all := ""
	for _, c := range s.calls {
		all += strings.Join(c, " ") + "\n"
	}
	if !strings.Contains(all, "Start-ScheduledTask") {
		t.Errorf("the task was not started again after the drain, so nothing would bring the server back:\n%s", all)
	}
	noGUIDomain(t, s.calls)
}

func TestSignalGraceful_Windows_FailsWhenTheEventCannotBeSet(t *testing.T) {
	withTestHome(t, "windows")
	orig := setShutdownEvent
	setShutdownEvent = func() error { return os.ErrNotExist }
	t.Cleanup(func() { setShutdownEvent = orig })

	m, stub := newTestManager()
	if err := m.signalGraceful(); err == nil {
		t.Fatal("signalGraceful should fail when the shutdown event cannot be set")
	}
	for _, c := range stub.calls {
		if strings.Contains(strings.Join(c, " "), "Start-ScheduledTask") {
			t.Errorf("the task was started after a failed drain: %v", stub.calls)
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
