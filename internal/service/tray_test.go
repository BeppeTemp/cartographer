package service

import (
	"encoding/xml"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRenderWindowsTrayTaskXML(t *testing.T) {
	out := RenderWindowsTrayTaskXML(`C:\bin\cartographer.exe`, `C:\logs\tray.log`, `HOST\user`)
	assertTaskXMLDeclaresNoEncoding(t, out)
	for _, want := range []string{
		`<URI>\Cartographer\Tray</URI>`,
		"<UserId>HOST\\user</UserId>",
		"<LogonType>InteractiveToken</LogonType>",
		"<RunLevel>LeastPrivilege</RunLevel>",
		"<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>",
		"<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>",
		`<Command>C:\bin\cartographer.exe</Command>`,
		`<Arguments>tray --log-file "C:\logs\tray.log"</Arguments>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("tray task XML missing %q\n%s", want, out)
		}
	}
	// A tray that fails for lack of a shell must not loop.
	if strings.Contains(out, "RestartOnFailure") {
		t.Error("the tray task must not restart on failure")
	}
	if err := xml.Unmarshal([]byte(out), new(struct{})); err != nil {
		t.Errorf("not well-formed XML: %v", err)
	}
	noUser := RenderWindowsTrayTaskXML(`C:\bin\cartographer.exe`, `C:\logs\tray.log`, "")
	if strings.Contains(noUser, "<UserId>") || !strings.Contains(noUser, "<LogonTrigger>") {
		t.Error("an unresolved user falls back to the unscoped trigger")
	}
	amp := RenderWindowsTrayTaskXML(`C:\R&D\cartographer.exe`, `C:\logs\tray.log`, "")
	if !strings.Contains(amp, `C:\R&amp;D\cartographer.exe`) {
		t.Error("ampersand in the binary path was not escaped")
	}
}

func TestRenderTrayLaunchdPlist(t *testing.T) {
	out := RenderTrayLaunchdPlist("/opt/bin/cartographer", "/home/user/Library/Logs/cartographer/tray.log")
	for _, want := range []string{
		"<string>com.cartographer.tray</string>",
		"<string>tray</string>",
		"<key>SuccessfulExit</key>\n\t\t<false/>",
		"<string>Aqua</string>",
		"<string>Interactive</string>",
		"<key>RunAtLoad</key>\n\t<true/>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("tray plist missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "serve") || strings.Contains(out, "com.cartographer.sync") {
		t.Error("the tray plist must not borrow the server's or the sync timer's identity")
	}
}

func installTraySetup(t *testing.T, platform string) {
	t.Helper()
	withTestHome(t, platform)
	oldExec := osExecutable
	osExecutable = func() (string, error) { return "/bin/cartographer", nil }
	t.Cleanup(func() { osExecutable = oldExec })
	oldUser := windowsTaskUser
	windowsTaskUser = func() string { return "HOST\\user" }
	t.Cleanup(func() { windowsTaskUser = oldUser })
	// No tray test reaches the host's desktop: the real requestTrayClose posts
	// WM_CLOSE to any tray window there, a developer's own included (#1734).
	// A test that exercises the close overrides this stub.
	oldClose := closeTrayWindow
	closeTrayWindow = func() error { return errors.New("no tray window (test stub)") }
	t.Cleanup(func() { closeTrayWindow = oldClose })
}

// installTraySetup must leave closeTrayWindow failing, so a test that forgets
// to stub it falls back to Stop-ScheduledTask instead of the host's desktop.
func TestInstallTraySetup_StubsCloseTrayWindow(t *testing.T) {
	installTraySetup(t, "windows")
	if err := closeTrayWindow(); err == nil {
		t.Fatal("closeTrayWindow must fail by default under installTraySetup")
	}
}

func callNames(s *stubRunner) []string {
	var out []string
	for _, c := range s.calls {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

func indexOf(calls []string, sub string) int {
	for i, c := range calls {
		if strings.Contains(c, sub) {
			return i
		}
	}
	return -1
}

func TestInstallUninstallTray_Darwin(t *testing.T) {
	installTraySetup(t, "darwin")
	m, stub := newTestManager()
	if err := m.InstallTray(); err != nil {
		t.Fatalf("InstallTray: %v", err)
	}
	if !m.TrayInstalled() {
		t.Fatal("plist not written")
	}
	calls := callNames(stub)
	if b, s := indexOf(calls, "launchctl bootout"), indexOf(calls, "launchctl bootstrap"); b < 0 || s < b {
		t.Errorf("want bootout then bootstrap: %v", calls)
	}
	if err := m.UninstallTray(); err != nil {
		t.Fatal(err)
	}
	if m.TrayInstalled() {
		t.Error("plist not removed")
	}
	if err := m.UninstallTray(); err != nil {
		t.Errorf("uninstalling an absent tray must succeed: %v", err)
	}
}

func TestInstallTray_NoGUISessionIsAnErrorTheCallerWarnsAbout(t *testing.T) {
	installTraySetup(t, "darwin")
	m, stub := newTestManager()
	stub.fail = map[string]bool{"launchctl bootstrap": true}
	if err := m.InstallTray(); err == nil {
		t.Fatal("a failed bootstrap must be reported")
	}
}

func TestInstallUninstallTray_Windows(t *testing.T) {
	installTraySetup(t, "windows")
	m, stub := newTestManager()
	// The stub answers every cmdlet with success, which windowsTaskRunning
	// reads as Running; this test is about a tray that stops.
	stub.fail = map[string]bool{"State -eq 'Running'": true}
	if err := m.InstallTray(); err != nil {
		t.Fatalf("InstallTray: %v", err)
	}
	calls := callNames(stub)
	reg, start := indexOf(calls, "Register-ScheduledTask"), indexOf(calls, "Start-ScheduledTask")
	if reg < 0 || start < reg {
		t.Errorf("want register then start: %v", calls)
	}
	if !m.TrayInstalled() {
		t.Fatal("task definition not written")
	}
	// Nothing running: no stop is issued, the task is just unregistered.
	stub.calls = nil
	if err := m.UninstallTray(); err != nil {
		t.Fatal(err)
	}
	calls = callNames(stub)
	if indexOf(calls, "Unregister-ScheduledTask") < 0 {
		t.Errorf("want unregister: %v", calls)
	}
	if m.TrayInstalled() {
		t.Error("task definition not removed")
	}
}

// A tray that survives the stop keeps cartographer.exe locked: both calls
// report it, and uninstall keeps tray.xml so it can be retried.
func TestTraySurvivorIsReportedAndKeepsTheDefinition(t *testing.T) {
	installTraySetup(t, "windows")
	m, stub := newTestManager()
	stub.fail = map[string]bool{"State -eq 'Running'": true}
	if err := m.InstallTray(); err != nil {
		t.Fatal(err)
	}
	stub.fail = nil // the task now reads as Running whatever is stopped
	stub.calls = nil
	err := m.UninstallTray()
	if err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("UninstallTray with a survivor: err = %v", err)
	}
	if !m.TrayInstalled() {
		t.Error("tray.xml was removed although the tray survived; a retry would be a no-op")
	}
	calls := callNames(stub)
	if s := indexOf(calls, "Stop-ScheduledTask"); s < 0 || indexOf(calls, "Unregister-ScheduledTask") >= 0 {
		t.Errorf("want a stop and no unregister: %v", calls)
	}
	stub.calls = nil
	if err := m.InstallTray(); err == nil || indexOf(callNames(stub), "Start-ScheduledTask") >= 0 {
		t.Errorf("InstallTray must fail before starting when the old tray survives: %v", err)
	}
}

func TestInstallUninstallTray_WindowsStopsARunningTray(t *testing.T) {
	installTraySetup(t, "windows")
	m, stub := newTestManager()
	stub.fail = map[string]bool{"State -eq 'Running'": true}
	if err := m.InstallTray(); err != nil {
		t.Fatal(err)
	}
	stub.fail = nil
	runningPolls := 0
	m.run = func(name string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "State -eq 'Running'") {
			runningPolls++
			if runningPolls > 2 { // the stop takes effect after two polls
				return "", errNotLoaded
			}
		}
		return stub.run(name, args...)
	}
	stub.calls = nil
	if err := m.UninstallTray(); err != nil {
		t.Fatalf("UninstallTray: %v", err)
	}
	calls := callNames(stub)
	if s, u := indexOf(calls, "Stop-ScheduledTask"), indexOf(calls, "Unregister-ScheduledTask"); s < 0 || u < s {
		t.Errorf("want stop then unregister: %v", calls)
	}
	if m.TrayInstalled() {
		t.Error("task definition not removed")
	}
}

func TestInstallTray_LinuxDoesNothing(t *testing.T) {
	installTraySetup(t, "linux")
	m, stub := newTestManager()
	if err := m.InstallTray(); err != nil || len(stub.calls) != 0 {
		t.Fatalf("linux: err=%v calls=%v", err, stub.calls)
	}
	if err := m.UninstallTray(); err != nil || len(stub.calls) != 0 || m.TrayInstalled() {
		t.Fatalf("linux uninstall: err=%v calls=%v", err, stub.calls)
	}
}

func TestUninstallRemovesTheTrayFirst(t *testing.T) {
	installTraySetup(t, "darwin")
	m, stub := newTestManager()
	if err := m.InstallTray(); err != nil {
		t.Fatal(err)
	}
	stub.calls = nil
	if err := m.Uninstall(false); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	calls := callNames(stub)
	if tr, sv := indexOf(calls, "com.cartographer.tray"), indexOf(calls, "com.cartographer.serve"); tr < 0 || sv < tr {
		t.Errorf("tray teardown must precede the server's: %v", calls)
	}
	if m.TrayInstalled() {
		t.Error("Uninstall left the tray plist")
	}
}

// On a Mac the rendered plist is checked by the real linter, without loading
// it (loading would register a LaunchAgent in the developer's session).
func TestRenderTrayLaunchdPlist_PassesPlutil(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("plutil is macOS only")
	}
	plutil, err := exec.LookPath("plutil")
	if err != nil {
		t.Skip("plutil not found")
	}
	p := filepath.Join(t.TempDir(), "tray.plist")
	if err := os.WriteFile(p, []byte(RenderTrayLaunchdPlist("/opt/bin/cartographer", "/tmp/tray.log")), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(plutil, "-lint", p).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint: %v\n%s", err, out)
	}
}

// The tray is asked to close its window before the task is stopped, so the
// shell loses the icon; Stop-ScheduledTask stays the fallback for a tray with
// no window (another session) or one that does not leave.
func TestStopTray_ClosesTheWindowBeforeStoppingTheTask(t *testing.T) {
	for _, tc := range []struct {
		name     string
		closeErr error
		exits    bool // the process leaves after the close request
		wantStop bool
	}{
		{"window closes the tray", nil, true, false},
		{"no window: stop the task", errNotLoaded, false, true},
		{"window ignored: stop the task", nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installTraySetup(t, "windows")
			m, stub := newTestManager()
			stub.fail = map[string]bool{"State -eq 'Running'": true}
			if err := m.InstallTray(); err != nil {
				t.Fatal(err)
			}
			stub.fail = nil // the task reads as Running
			stub.calls = nil
			closed := false
			old := closeTrayWindow
			closeTrayWindow = func() error {
				if indexOf(callNames(stub), "Stop-ScheduledTask") >= 0 {
					t.Error("the close request came after Stop-ScheduledTask")
				}
				closed = true
				return tc.closeErr
			}
			t.Cleanup(func() { closeTrayWindow = old })
			base := m.run
			m.run = func(name string, args ...string) (string, error) {
				joined := strings.Join(args, " ")
				if strings.Contains(joined, "State -eq 'Running'") && closed && tc.exits {
					return "", errNotLoaded // the tray exited on WM_CLOSE
				}
				if strings.Contains(joined, "Stop-ScheduledTask") {
					closed = true
					tc.exits = true // the stop works
				}
				return base(name, args...)
			}
			if err := m.stopTrayAndWait(); err != nil {
				t.Fatalf("stopTrayAndWait: %v", err)
			}
			if !closed {
				t.Error("the close hook was not called")
			}
			if got := indexOf(callNames(stub), "Stop-ScheduledTask") >= 0; got != tc.wantStop {
				t.Errorf("Stop-ScheduledTask called = %v, want %v: %v", got, tc.wantStop, callNames(stub))
			}
		})
	}
}

// connect installs the tray on its own, so a symlink planted where the
// definition goes must be refused, not written through.
func TestInstallTray_RefusesASymlinkedDefinition(t *testing.T) {
	installTraySetup(t, "darwin")
	m, _ := newTestManager()
	p, err := TrayLaunchdPlistPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := m.InstallTray(); err == nil {
		t.Fatal("InstallTray wrote through a symlinked definition")
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Fatalf("the link target was overwritten: %q", b)
	}
}
