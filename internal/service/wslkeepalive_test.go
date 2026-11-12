package service

import (
	"encoding/base64"
	"encoding/xml"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/wsl"
)

const testPS = "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"

func testKeepAliveSpec(logon string) WslKeepAliveSpec {
	return WslKeepAliveSpec{
		Distro: "Ubuntu-24.04", User: "user", Binary: "/home/user/My Apps/it's/cartographer",
		Account: `HOST\user`, SID: testSID, Logon: logon, Wslg: `C:\Program Files\WSL\wslg.exe`,
	}
}

type keepAliveDoc struct {
	URI       string `xml:"RegistrationInfo>URI"`
	SDDL      string `xml:"RegistrationInfo>SecurityDescriptor"`
	Trigger   string `xml:"Triggers>LogonTrigger>UserId"`
	Principal struct {
		UserID   string `xml:"UserId"`
		Logon    string `xml:"LogonType"`
		RunLevel string `xml:"RunLevel"`
	} `xml:"Principals>Principal"`
	Settings struct {
		Policy  string `xml:"MultipleInstancesPolicy"`
		Limit   string `xml:"ExecutionTimeLimit"`
		Hidden  string `xml:"Hidden"`
		Demand  string `xml:"AllowStartOnDemand"`
		Battery string `xml:"StopIfGoingOnBatteries"`
		Idle    string `xml:"IdleSettings>StopOnIdleEnd"`
		Restart struct {
			Interval string `xml:"Interval"`
			Count    string `xml:"Count"`
		} `xml:"RestartOnFailure"`
	} `xml:"Settings"`
	Command   string `xml:"Actions>Exec>Command"`
	Arguments string `xml:"Actions>Exec>Arguments"`
}

func TestRenderWslKeepAliveTaskXML(t *testing.T) {
	for _, c := range []struct{ logon, wantLogon, wantCmd string }{
		{WslLogonS4U, "S4U", "wsl.exe"},
		{WslLogonInteractive, "InteractiveToken", `C:\Program Files\WSL\wslg.exe`},
	} {
		out, err := RenderWslKeepAliveTaskXML(testKeepAliveSpec(c.logon))
		if err != nil {
			t.Fatalf("%s: %v", c.logon, err)
		}
		var doc keepAliveDoc
		if err := xml.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%s: not well-formed: %v\n%s", c.logon, err, out)
		}
		assertTaskXMLDeclaresNoEncoding(t, out)
		if doc.URI != `\Cartographer\WslKeepAlive` {
			t.Errorf("%s: URI = %q", c.logon, doc.URI)
		}
		if doc.Principal.Logon != c.wantLogon || doc.Principal.RunLevel != "LeastPrivilege" {
			t.Errorf("%s: principal = %+v", c.logon, doc.Principal)
		}
		if doc.Principal.UserID != `HOST\user` || doc.Trigger != `HOST\user` {
			t.Errorf("%s: user not named in principal and trigger: %+v / %q", c.logon, doc.Principal, doc.Trigger)
		}
		if !strings.Contains(doc.SDDL, "(A;;FA;;;"+testSID+")") {
			t.Errorf("%s: descriptor = %q", c.logon, doc.SDDL)
		}
		s := doc.Settings
		if s.Policy != "IgnoreNew" || s.Limit != "PT0S" || s.Hidden != "false" || s.Demand != "true" || s.Battery != "false" || s.Idle != "false" {
			t.Errorf("%s: settings = %+v", c.logon, s)
		}
		if s.Restart.Interval != "PT1M" || s.Restart.Count != "999" {
			t.Errorf("%s: RestartOnFailure = %+v", c.logon, s.Restart)
		}
		if doc.Command != c.wantCmd {
			t.Errorf("%s: Command = %q, want %q", c.logon, doc.Command, c.wantCmd)
		}
		// The binary is single-quoted for the shell, its own quote escaped.
		want := `-d Ubuntu-24.04 -u user -- '/home/user/My Apps/it'\''s/cartographer' wsl keepalive`
		if doc.Arguments != want {
			t.Errorf("%s: Arguments = %q, want %q", c.logon, doc.Arguments, want)
		}
		if strings.Contains(doc.Arguments, "serve") {
			t.Errorf("%s: the keep-alive must never run serve: %q", c.logon, doc.Arguments)
		}
	}
}

func TestRenderWslKeepAliveTaskXML_Refuses(t *testing.T) {
	for name, mut := range map[string]func(*WslKeepAliveSpec){
		"distro with a space":   func(s *WslKeepAliveSpec) { s.Distro = "Ubuntu 24.04" },
		"distro with ;":         func(s *WslKeepAliveSpec) { s.Distro = "a;calc" },
		"empty distro":          func(s *WslKeepAliveSpec) { s.Distro = "" },
		"user with a space":     func(s *WslKeepAliveSpec) { s.User = "a b" },
		"relative binary":       func(s *WslKeepAliveSpec) { s.Binary = "cartographer" },
		"binary with a newline": func(s *WslKeepAliveSpec) { s.Binary = "/bin/x\ny" },
		"binary with NUL":       func(s *WslKeepAliveSpec) { s.Binary = "/bin/x\x00" },
		"no account":            func(s *WslKeepAliveSpec) { s.Account = "" },
		"interactive no wslg":   func(s *WslKeepAliveSpec) { s.Logon, s.Wslg = WslLogonInteractive, "" },
		"unknown logon":         func(s *WslKeepAliveSpec) { s.Logon = "Password" },
	} {
		s := testKeepAliveSpec(WslLogonS4U)
		mut(&s)
		if out, err := RenderWslKeepAliveTaskXML(s); err == nil {
			t.Errorf("%s: rendered instead of refusing:\n%s", name, out)
		}
	}
	s := testKeepAliveSpec(WslLogonS4U)
	s.User = "svc$"
	if _, err := RenderWslKeepAliveTaskXML(s); err != nil {
		t.Errorf("a user name with $ is valid: %v", err)
	}
}

// fakeKeepAliveWindows models the scheduler behind interop: which logon types
// it accepts, whether wslg.exe exists, and whether the task is registered.
type fakeKeepAliveWindows struct {
	refuse     map[string]bool // logon type -> Register refused
	noWslg     bool
	registered bool
	refuseDrop bool // Unregister fails
	logons     []string
	calls      []string
}

var b64Re = regexp.MustCompile(`FromBase64String\('([^']*)'\)`)

func (f *fakeKeepAliveWindows) run(name string, args ...string) (string, error) {
	script := args[len(args)-1]
	f.calls = append(f.calls, name+" "+script)
	if name != testPS {
		return "", errors.New("unexpected command " + name)
	}
	switch {
	case strings.Contains(script, "WindowsIdentity"):
		return "HOST\\user name\r\n" + testSID + "\r\n", nil
	case strings.Contains(script, "Register-ScheduledTask"):
		raw, _ := base64.StdEncoding.DecodeString(b64Re.FindStringSubmatch(script)[1])
		var doc keepAliveDoc
		if err := xml.Unmarshal(raw, &doc); err != nil {
			return "", err
		}
		f.logons = append(f.logons, doc.Principal.Logon)
		if f.refuse[doc.Principal.Logon] {
			return "", exitCodeErr(1)
		}
		f.registered = true
		return "", nil
	case strings.Contains(script, "wslg.exe"):
		if f.noWslg {
			return "", exitCodeErr(taskNotFoundExit)
		}
		return `C:\Program Files\WSL\wslg.exe` + "\r\n", nil
	case strings.Contains(script, "Unregister-ScheduledTask"):
		if f.refuseDrop {
			return "", exitCodeErr(1)
		}
		f.registered = false
		return "", nil
	case strings.Contains(script, "Get-ScheduledTask"):
		if f.registered {
			return "", nil
		}
		return "", exitCodeErr(taskNotFoundExit)
	case strings.Contains(script, "Start-ScheduledTask"):
		return "", nil
	}
	return "", errors.New("unexpected script " + script)
}

func wslInfo() wsl.Info {
	return wsl.Info{Is: true, Distro: "Ubuntu-24.04", Version: 2, Interop: true, PowerShell: testPS}
}

func withKeepAliveHost(t *testing.T) {
	t.Helper()
	withTestHome(t, "linux")
	origExe, origUser := osExecutable, wslUser
	osExecutable = func() (string, error) { return "/home/user/.local/bin/cartographer", nil }
	wslUser = func() string { return "user" }
	t.Cleanup(func() { osExecutable, wslUser = origExe, origUser })
}

func TestInstallWslKeepAlive_S4U(t *testing.T) {
	withKeepAliveHost(t)
	f := &fakeKeepAliveWindows{}
	warn, err := (&Manager{run: f.run}).InstallWslKeepAlive(wslInfo())
	if err != nil || warn != "" {
		t.Fatalf("Install = %q, %v", warn, err)
	}
	if strings.Join(f.logons, ",") != "S4U" {
		t.Errorf("logons tried = %v, want S4U only", f.logons)
	}
	if last := f.calls[len(f.calls)-1]; !strings.Contains(last, "Start-ScheduledTask -TaskName 'WslKeepAlive'") {
		t.Errorf("the task was not started last: %s", last)
	}
}

func TestInstallWslKeepAlive_S4URefusedFallsBackToWslg(t *testing.T) {
	withKeepAliveHost(t)
	f := &fakeKeepAliveWindows{refuse: map[string]bool{"S4U": true}}
	warn, err := (&Manager{run: f.run}).InstallWslKeepAlive(wslInfo())
	if err != nil || warn != "" {
		t.Fatalf("Install = %q, %v", warn, err)
	}
	if strings.Join(f.logons, ",") != "S4U,InteractiveToken" {
		t.Errorf("logons tried = %v", f.logons)
	}
}

func TestInstallWslKeepAlive_NoTaskIsAWarning(t *testing.T) {
	for name, f := range map[string]*fakeKeepAliveWindows{
		"both refused": {refuse: map[string]bool{"S4U": true, "InteractiveToken": true}},
		"wslg missing": {refuse: map[string]bool{"S4U": true}, noWslg: true},
	} {
		withKeepAliveHost(t)
		warn, err := (&Manager{run: f.run}).InstallWslKeepAlive(wslInfo())
		if err != nil {
			t.Fatalf("%s: a missing keep-alive is a warning, got %v", name, err)
		}
		if !strings.Contains(warn, "instanceIdleTimeout=-1") || !strings.Contains(warn, "[general]") {
			t.Errorf("%s: warning lacks the manual step: %q", name, warn)
		}
		if f.registered || strings.Contains(joinedStrings(f.calls), "Start-ScheduledTask") {
			t.Errorf("%s: a task was registered or started: %v", name, f.calls)
		}
	}
}

func TestInstallWslKeepAlive_NoInteropTouchesNothing(t *testing.T) {
	withKeepAliveHost(t)
	f := &fakeKeepAliveWindows{}
	info := wslInfo()
	info.Interop, info.PowerShell = false, ""
	warn, err := (&Manager{run: f.run}).InstallWslKeepAlive(info)
	if err != nil || !strings.Contains(warn, "interop disabled") {
		t.Fatalf("Install = %q, %v", warn, err)
	}
	if len(f.calls) != 0 {
		t.Errorf("no interop, yet Windows was called: %v", f.calls)
	}
}

func TestInstallWslKeepAlive_InvalidDistroIsAnError(t *testing.T) {
	withKeepAliveHost(t)
	f := &fakeKeepAliveWindows{}
	info := wslInfo()
	info.Distro = "bad name"
	if _, err := (&Manager{run: f.run}).InstallWslKeepAlive(info); err == nil {
		t.Fatal("an invalid distro name must be an error")
	}
	if len(f.logons) != 0 {
		t.Errorf("an invalid spec reached the scheduler: %v", f.calls)
	}
}

func TestUninstallWslKeepAlive(t *testing.T) {
	withKeepAliveHost(t)
	f := &fakeKeepAliveWindows{registered: true}
	m := &Manager{run: f.run}
	if err := m.UninstallWslKeepAlive(wslInfo()); err != nil || f.registered {
		t.Fatalf("Uninstall = %v, registered = %v", err, f.registered)
	}
	// Idempotent: an unregister that fails on a task the scheduler does not
	// know is success.
	f.refuseDrop = true
	if err := m.UninstallWslKeepAlive(wslInfo()); err != nil {
		t.Errorf("absent task: %v", err)
	}
	// A task that survives is an error naming the manual command.
	f.registered = true
	if err := m.UninstallWslKeepAlive(wslInfo()); err == nil || !strings.Contains(err.Error(), "Unregister-ScheduledTask") {
		t.Errorf("surviving task: %v", err)
	}
	info := wslInfo()
	info.Interop = false
	n := len(f.calls)
	if err := m.UninstallWslKeepAlive(info); err != nil || len(f.calls) != n {
		t.Errorf("no interop: err %v, calls %v", err, f.calls[n:])
	}
}

func TestWslKeepAliveRegistered(t *testing.T) {
	withKeepAliveHost(t)
	f := &fakeKeepAliveWindows{}
	m := &Manager{run: f.run}
	if ok, err := m.WslKeepAliveRegistered(wslInfo()); ok || err != nil {
		t.Errorf("absent = %v, %v", ok, err)
	}
	f.registered = true
	if ok, err := m.WslKeepAliveRegistered(wslInfo()); !ok || err != nil {
		t.Errorf("present = %v, %v", ok, err)
	}
}

func joinedStrings(s []string) string { return strings.Join(s, "\n") }
