package service

// The task belongs to its user (#1961, amends D217): the rendered definition,
// the refusal of an elevated install, a restart that needs no write right on the
// task, and the message of a refused change.

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

const testSID = "S-1-5-21-1111111111-2222222222-3333333333-1001"

func TestRenderWindowsTaskXML_TaskBelongsToItsUser(t *testing.T) {
	out := RenderWindowsTaskXML(`C:\bin\cartographer.exe`, `C:\cfg\server.yaml`, `C:\logs\server.log`, `HOST\user`, testSID)
	var doc struct {
		Info struct {
			SDDL string `xml:"SecurityDescriptor"`
		} `xml:"RegistrationInfo"`
		Principal struct {
			UserID string `xml:"UserId"`
		} `xml:"Principals>Principal"`
	}
	if err := xml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not well-formed: %v\n%s", err, out)
	}
	if doc.Principal.UserID != `HOST\user` {
		t.Errorf("Principal UserId = %q", doc.Principal.UserID)
	}
	for _, want := range []string{"(A;;FA;;;" + testSID + ")", "(A;;FA;;;SY)", "(A;;FA;;;BA)"} {
		if !strings.Contains(doc.Info.SDDL, want) {
			t.Errorf("SDDL %q lacks %q", doc.Info.SDDL, want)
		}
	}
	assertTaskXMLDeclaresNoEncoding(t, out)
}

func TestRenderWindowsTaskXML_NoDescriptorWithoutAValidSID(t *testing.T) {
	for _, sid := range []string{"", "not-a-sid", testSID + "</SecurityDescriptor><x/>", "S-1-5"} {
		out := RenderWindowsTaskXML(`C:\bin\cartographer.exe`, `C:\cfg\server.yaml`, `C:\logs\server.log`, `HOST\user`, sid)
		if strings.Contains(out, "SecurityDescriptor") {
			t.Errorf("sid %q rendered a descriptor:\n%s", sid, out)
		}
	}
	out := RenderWindowsTaskXML(`C:\bin\cartographer.exe`, `C:\cfg\server.yaml`, `C:\logs\server.log`, "", testSID)
	if strings.Contains(out, "<UserId>") {
		t.Errorf("an unresolved user rendered a UserId:\n%s", out)
	}
}

// From an elevated shell the install goes on, says so in one line, and still
// registers the task for the logged-in user (UserId and descriptor): that is how
// a task an older install left owned by Administrators is handed back (#1961).
func TestInstall_Windows_ElevatedShellRegistersForTheUser(t *testing.T) {
	home := withTestHome(t, "windows")
	binPath := filepath.Join(t.TempDir(), "cartographer.exe")
	if err := os.WriteFile(binPath, []byte("bin"), 0o644); err != nil {
		t.Fatal(err)
	}
	origExe, origElev, origUser, origSID, origNotice := osExecutable, windowsElevated, windowsTaskUser, windowsTaskSID, noticef
	osExecutable = func() (string, error) { return binPath, nil }
	windowsElevated = func() bool { return true }
	windowsTaskUser = func() string { return `HOST\user` }
	windowsTaskSID = func() string { return testSID }
	var notices []string
	noticef = func(format string, args ...any) { notices = append(notices, fmt.Sprintf(format, args...)) }
	t.Cleanup(func() {
		osExecutable, windowsElevated, windowsTaskUser, windowsTaskSID, noticef = origExe, origElev, origUser, origSID, origNotice
	})

	m, _ := newTestManager()
	if _, err := m.Install(InstallOptions{DataDir: filepath.Join(home, "data"), HTTPAddr: "127.0.0.1:39273"}); err != nil {
		t.Fatalf("an elevated install must go on: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, "AppData", "Local", "cartographer", "tasks", "serve.xml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<UserId>HOST\user</UserId>`, "(A;;FA;;;" + testSID + ")"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("elevated install did not render %q:\n%s", want, data)
		}
	}
	elevatedNotes := 0
	for _, n := range notices {
		if strings.Contains(n, "elevated") && strings.Contains(n, `HOST\user`) {
			elevatedNotes++
		}
	}
	if elevatedNotes != 1 {
		t.Errorf("want one notice naming the elevation and the user, got %v", notices)
	}
}

// Register before stopping: a registration the scheduler refuses (an old
// Administrators-owned task) must leave the running server running (#1961).
func TestInstall_Windows_DeniedRegisterLeavesTheServerRunning(t *testing.T) {
	home := withTestHome(t, "windows")
	binPath := filepath.Join(t.TempDir(), "cartographer.exe")
	if err := os.WriteFile(binPath, []byte("bin"), 0o644); err != nil {
		t.Fatal(err)
	}
	origExe := osExecutable
	osExecutable = func() (string, error) { return binPath, nil }
	t.Cleanup(func() { osExecutable = origExe })

	f := &fakeServeTask{running: true, drains: true, enabled: true, denyRegister: true}
	eventFor(t, f, true)
	m := &Manager{run: f.run}
	_, err := m.Install(InstallOptions{DataDir: filepath.Join(home, "data"), HTTPAddr: "127.0.0.1:39273"})
	if !errors.Is(err, ErrTaskAccessDenied) {
		t.Fatalf("Install = %v, want ErrTaskAccessDenied", err)
	}
	if !f.running {
		t.Error("a refused registration stopped the running server")
	}
	all := joinedCalls(f.calls)
	if strings.Contains(all, "Stop-ScheduledTask") || strings.Contains(all, "Start-ScheduledTask") {
		t.Errorf("the server was touched before the registration was accepted:\n%s", all)
	}
	if !strings.Contains(err.Error(), "cartographer service install") {
		t.Errorf("message lacks the remedy: %v", err)
	}
}

func TestRenderWindowsSyncAndTrayTaskXML_BelongToTheirUser(t *testing.T) {
	for name, out := range map[string]string{
		"sync": RenderWindowsSyncTaskXML(`C:\bin\cartographer.exe`, `C:\logs\sync.log`, 0, `HOST\user`, testSID),
		"tray": RenderWindowsTrayTaskXML(`C:\bin\cartographer.exe`, `C:\logs\tray.log`, `HOST\user`, testSID),
	} {
		var doc struct {
			SDDL      string `xml:"RegistrationInfo>SecurityDescriptor"`
			Principal string `xml:"Principals>Principal>UserId"`
		}
		if err := xml.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%s: not well-formed: %v", name, err)
		}
		if doc.Principal != `HOST\user` {
			t.Errorf("%s: Principal UserId = %q", name, doc.Principal)
		}
		for _, want := range []string{"(A;;FA;;;" + testSID + ")", "(A;;FA;;;SY)", "(A;;FA;;;BA)"} {
			if !strings.Contains(doc.SDDL, want) {
				t.Errorf("%s: SDDL %q lacks %q", name, doc.SDDL, want)
			}
		}
	}
	if strings.Contains(RenderWindowsSyncTaskXML(`C:\b.exe`, `C:\s.log`, 0, "", ""), "SecurityDescriptor") {
		t.Error("an unresolved user rendered a descriptor")
	}
}

// The remedy names the command that registers the task that was refused.
func TestTaskDeniedError_NamesTheRemedyPerTask(t *testing.T) {
	for name, want := range map[string]string{
		windowsServeTaskName: "`cartographer service install`",
		windowsSyncTaskName:  "`cartographer service sync-timer install`",
		windowsTrayTaskName:  "`cartographer service tray install`",
	} {
		f := &fakeServeTask{owner: `BUILTIN\Administrators`}
		m := &Manager{run: f.run}
		err := m.taskDeniedError(name, exitCodeErr(taskDeniedExit))
		if !errors.Is(err, ErrTaskAccessDenied) || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "elevated") {
			t.Errorf("%s: %v, want remedy %s", name, err, want)
		}
	}
}

func installedServeTask(t *testing.T, home, sid string) string {
	t.Helper()
	taskPath := filepath.Join(home, "AppData", "Local", "cartographer", "tasks", "serve.xml")
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskPath, []byte(RenderWindowsTaskXML(`C:\bin\cartographer.exe`, `C:\cfg\server.yaml`, `C:\logs\server.log`, `HOST\user`, sid)), 0o644); err != nil {
		t.Fatal(err)
	}
	return taskPath
}

// upgrade-repair brings an old definition (no descriptor) up to date without
// touching the running server; a refusal keeps the old file and says what to do.
func TestRefreshTaskOwners(t *testing.T) {
	origUser, origSID := windowsTaskUser, windowsTaskSID
	windowsTaskUser = func() string { return `HOST\user` }
	windowsTaskSID = func() string { return testSID }
	t.Cleanup(func() { windowsTaskUser, windowsTaskSID = origUser, origSID })

	t.Run("old definition is re-registered, server untouched", func(t *testing.T) {
		home := withTestHome(t, "windows")
		taskPath := installedServeTask(t, home, "")
		f := &fakeServeTask{running: true, enabled: true}
		m := &Manager{run: f.run}
		refreshed, err := m.RefreshTaskOwners()
		if err != nil || len(refreshed) != 1 || refreshed[0] != windowsServeTaskName {
			t.Fatalf("refresh = %v, %v", refreshed, err)
		}
		data, _ := os.ReadFile(taskPath)
		if !strings.Contains(string(data), testSID) || !strings.Contains(string(data), `C:\cfg\server.yaml`) {
			t.Errorf("definition not replaced, or lost its config:\n%s", data)
		}
		all := joinedCalls(f.calls)
		if !strings.Contains(all, "Register-ScheduledTask") || strings.Contains(all, "Stop-ScheduledTask") || strings.Contains(all, "Start-ScheduledTask") {
			t.Errorf("want a registration and nothing else:\n%s", all)
		}
		if _, err := os.Stat(taskPath + ".new"); err == nil {
			t.Error("side file left behind")
		}
	})
	t.Run("current definition is left alone", func(t *testing.T) {
		home := withTestHome(t, "windows")
		installedServeTask(t, home, testSID)
		f := &fakeServeTask{}
		m := &Manager{run: f.run}
		if refreshed, err := m.RefreshTaskOwners(); len(refreshed) != 0 || err != nil || len(f.calls) != 0 {
			t.Errorf("refresh = %v, %v, calls %v", refreshed, err, f.calls)
		}
	})
	t.Run("a refusal keeps the old file", func(t *testing.T) {
		home := withTestHome(t, "windows")
		taskPath := installedServeTask(t, home, "")
		before, _ := os.ReadFile(taskPath)
		f := &fakeServeTask{denyRegister: true, owner: `BUILTIN\Administrators`}
		m := &Manager{run: f.run}
		refreshed, err := m.RefreshTaskOwners()
		if len(refreshed) != 0 || !errors.Is(err, ErrTaskAccessDenied) || !strings.Contains(err.Error(), "elevated") {
			t.Fatalf("refresh = %v, %v", refreshed, err)
		}
		if after, _ := os.ReadFile(taskPath); string(after) != string(before) {
			t.Error("a refused registration replaced the installed definition")
		}
	})
}

// A restart needs the right to start the task, not to change it: Enable runs
// only when Stop left the task disabled.
func TestRestart_Windows_EnablesOnlyADisabledTask(t *testing.T) {
	for _, tc := range []struct {
		name       string
		disabled   bool
		wantEnable bool
	}{{"enabled", false, false}, {"disabled", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			withTestHome(t, "windows")
			f := &fakeServeTask{running: true, drains: true, enabled: !tc.disabled}
			eventFor(t, f, true)
			m := &Manager{run: f.run}
			if err := m.Restart(); err != nil {
				t.Fatalf("Restart: %v", err)
			}
			all := joinedCalls(f.calls)
			if got := strings.Contains(all, "Enable-ScheduledTask"); got != tc.wantEnable {
				t.Errorf("Enable-ScheduledTask run = %v, want %v:\n%s", got, tc.wantEnable, all)
			}
			if !strings.Contains(all, "Start-ScheduledTask") {
				t.Errorf("no start:\n%s", all)
			}
			if strings.Contains(all, "Stop-ScheduledTask") || strings.Contains(all, "Disable-ScheduledTask") {
				t.Errorf("a graceful restart must not write to the task:\n%s", all)
			}
		})
	}
}

// A task this account cannot administer: the event drains the server, the start
// is all that is asked of the scheduler, and a refusal names the owner.
func TestRestart_Windows_AccessDeniedNamesTheOwner(t *testing.T) {
	withTestHome(t, "windows")
	f := &fakeServeTask{running: true, drains: true, enabled: true, denyEnable: true}
	eventFor(t, f, true)
	m := &Manager{run: f.run}
	if err := m.Restart(); err != nil {
		t.Fatalf("Restart on an enabled admin-owned task: %v", err)
	}

	f = &fakeServeTask{running: true, drains: true, denyEnable: true, owner: `BUILTIN\Administrators`}
	eventFor(t, f, true)
	m = &Manager{run: f.run}
	err := m.Restart()
	if err == nil {
		t.Fatal("Restart of a disabled admin-owned task succeeded")
	}
	for _, want := range []string{`BUILTIN\Administrators`, "cartographer service install", "elevated", `\Cartographer\Serve`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q: %v", want, err)
		}
	}
}

// Sync and Tray tasks from an older install get the same treatment, only when
// installed, keeping the interval and the binary they had (#1961).
func TestRefreshTaskOwners_SyncAndTray(t *testing.T) {
	origUser, origSID := windowsTaskUser, windowsTaskSID
	windowsTaskUser = func() string { return `HOST\user` }
	windowsTaskSID = func() string { return testSID }
	t.Cleanup(func() { windowsTaskUser, windowsTaskSID = origUser, origSID })

	home := withTestHome(t, "windows")
	syncPath, _ := SyncWindowsTaskPath()
	trayPath, _ := TrayWindowsTaskPath()
	if err := os.MkdirAll(filepath.Dir(syncPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(syncPath, []byte(RenderWindowsSyncTaskXML(`C:\old\cartographer.exe`, `C:\logs\sync.log`, 20*time.Minute, `HOST\user`, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trayPath, []byte(RenderWindowsTrayTaskXML(`C:\old\cartographer.exe`, `C:\logs\tray.log`, `HOST\user`, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = home
	f := &fakeServeTask{running: true, enabled: true}
	m := &Manager{run: f.run}
	refreshed, err := m.RefreshTaskOwners()
	if err != nil || strings.Join(refreshed, ",") != "Sync,Tray" {
		t.Fatalf("refresh = %v, %v (the absent serve task must be skipped)", refreshed, err)
	}
	data, _ := os.ReadFile(syncPath)
	if !strings.Contains(string(data), testSID) || !strings.Contains(string(data), `C:\old\cartographer.exe`) || !strings.Contains(string(data), "PT20M") {
		t.Errorf("sync definition lost its binary or interval, or has no descriptor:\n%s", data)
	}
	if data, _ := os.ReadFile(trayPath); !strings.Contains(string(data), testSID) {
		t.Errorf("tray definition has no descriptor:\n%s", data)
	}
	all := joinedCalls(f.calls)
	if strings.Contains(all, "Stop-ScheduledTask") || strings.Contains(all, "Start-ScheduledTask") {
		t.Errorf("a refresh must not stop or start anything:\n%s", all)
	}

	// A refusal keeps each file and is reported.
	before, _ := os.ReadFile(syncPath)
	os.WriteFile(syncPath, []byte(strings.Replace(string(before), "<SecurityDescriptor>", "<X>", 1)), 0o644)
	old, _ := os.ReadFile(syncPath)
	f = &fakeServeTask{denyRegister: true}
	m = &Manager{run: f.run}
	if refreshed, err := m.RefreshTaskOwners(); len(refreshed) != 0 || !errors.Is(err, ErrTaskAccessDenied) {
		t.Fatalf("refused refresh = %v, %v", refreshed, err)
	}
	if after, _ := os.ReadFile(syncPath); string(after) != string(old) {
		t.Error("a refused registration replaced the sync definition")
	}
}

// Register before stopping the tray: a refused registration leaves a running
// tray running (#1961).
func TestInstallTray_Windows_DeniedRegisterLeavesTheTrayRunning(t *testing.T) {
	withTestHome(t, "windows")
	f := &fakeServeTask{running: true, enabled: true, denyRegister: true}
	m := &Manager{run: f.run}
	err := m.InstallTray()
	if !errors.Is(err, ErrTaskAccessDenied) {
		t.Fatalf("InstallTray = %v, want ErrTaskAccessDenied", err)
	}
	all := joinedCalls(f.calls)
	if strings.Contains(all, "Stop-ScheduledTask") || strings.Contains(all, "Disable-ScheduledTask") || strings.Contains(all, "Start-ScheduledTask") {
		t.Errorf("the tray was touched before the registration was accepted:\n%s", all)
	}
	if !f.running {
		t.Error("the tray was stopped")
	}
}

// A refused registration keeps the installed definition, for the three installs:
// the refresh reads that file to know whether the task has its descriptor, and a
// file that already carried it would be skipped for good (#1961).
func TestInstalls_Windows_DeniedRegisterKeepTheOldDefinition(t *testing.T) {
	origUser, origSID, origExe := windowsTaskUser, windowsTaskSID, osExecutable
	windowsTaskUser = func() string { return `HOST\user` }
	windowsTaskSID = func() string { return testSID }
	t.Cleanup(func() { windowsTaskUser, windowsTaskSID, osExecutable = origUser, origSID, origExe })

	home := withTestHome(t, "windows")
	binPath := filepath.Join(t.TempDir(), "cartographer.exe")
	if err := os.WriteFile(binPath, []byte("bin"), 0o644); err != nil {
		t.Fatal(err)
	}
	osExecutable = func() (string, error) { return binPath, nil }

	servePath, _ := WindowsTaskPath()
	syncPath, _ := SyncWindowsTaskPath()
	trayPath, _ := TrayWindowsTaskPath()
	const old = "<old definition>"
	if err := os.MkdirAll(filepath.Dir(servePath), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{servePath, syncPath, trayPath} {
		if err := os.WriteFile(p, []byte(old), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, install := range map[string]func(m *Manager) error{
		"serve": func(m *Manager) error {
			_, err := m.Install(InstallOptions{DataDir: filepath.Join(home, "data"), HTTPAddr: "127.0.0.1:39273"})
			return err
		},
		"sync": func(m *Manager) error { return m.InstallSyncTimer(20 * time.Minute) },
		"tray": func(m *Manager) error { return m.InstallTray() },
	} {
		f := &fakeServeTask{denyRegister: true}
		err := install(&Manager{run: f.run})
		if !errors.Is(err, ErrTaskAccessDenied) {
			t.Fatalf("%s: install = %v, want ErrTaskAccessDenied", name, err)
		}
	}
	for name, p := range map[string]string{"serve": servePath, "sync": syncPath, "tray": trayPath} {
		if got, _ := os.ReadFile(p); string(got) != old {
			t.Errorf("%s: a refused registration replaced the installed definition:\n%s", name, got)
		}
		if _, err := os.Stat(p + ".new"); err == nil {
			t.Errorf("%s: side file left behind", name)
		}
	}
}
