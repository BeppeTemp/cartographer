package service

import (
	"encoding/xml"
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
	if err := m.InstallTray(); err != nil {
		t.Fatalf("InstallTray: %v", err)
	}
	calls := callNames(stub)
	stop, reg, start := indexOf(calls, "Stop-ScheduledTask"), indexOf(calls, "Register-ScheduledTask"), indexOf(calls, "Start-ScheduledTask")
	if stop < 0 || reg < stop || start < reg {
		t.Errorf("want stop (IgnoreNew), register, start: %v", calls)
	}
	if !m.TrayInstalled() {
		t.Fatal("task definition not written")
	}
	stub.calls = nil
	if err := m.UninstallTray(); err != nil {
		t.Fatal(err)
	}
	calls = callNames(stub)
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
	if err := m.Uninstall(); err != nil {
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
