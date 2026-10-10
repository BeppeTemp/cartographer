package service

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testDoctorSpec() DoctorTimerSpec {
	return DoctorTimerSpec{
		BinPath: "/opt/bin/cartographer",
		Args:    []string{"doctor", "run", "--kb", "kb-a", "--client", "claude", "--client-bin", "/opt/agent tools/claude", "--at", "06:30"},
		Hour:    6, Minute: 30,
		PathEnv: "/opt/agent tools:/usr/bin",
		LogPath: "/logs/doctor.log",
	}
}

func TestRenderDoctorSystemdUnits(t *testing.T) {
	s := testDoctorSpec()
	wantTimer := `[Unit]
Description=Cartographer scheduled kb-doctor session timer

[Timer]
OnCalendar=*-*-* 06:30:00
Persistent=true

[Install]
WantedBy=timers.target
`
	if got := RenderDoctorSystemdTimer(s); got != wantTimer {
		t.Errorf("timer:\n%s\nwant:\n%s", got, wantTimer)
	}
	wantService := `[Unit]
Description=Cartographer scheduled kb-doctor session

[Service]
Type=oneshot
Environment="PATH=/opt/agent tools:/usr/bin"
ExecStart="/opt/bin/cartographer" "doctor" "run" "--kb" "kb-a" "--client" "claude" "--client-bin" "/opt/agent tools/claude" "--at" "06:30"
`
	if got := RenderDoctorSystemdService(s); got != wantService {
		t.Errorf("service:\n%s\nwant:\n%s", got, wantService)
	}
	// systemd expands % and $ inside quotes.
	s.Args = []string{`a%b$c`}
	if got := RenderDoctorSystemdService(s); !strings.Contains(got, `"a%%b$$c"`) {
		t.Errorf("specifiers not escaped:\n%s", got)
	}
}

func TestRenderDoctorLaunchdPlist(t *testing.T) {
	got := RenderDoctorLaunchdPlist(testDoctorSpec())
	for _, frag := range []string{
		"<string>com.cartographer.doctor</string>",
		"<string>/opt/bin/cartographer</string>\n\t\t<string>doctor</string>",
		"<string>/opt/agent tools/claude</string>",
		"<key>StartCalendarInterval</key>\n\t<dict>\n\t\t<key>Hour</key>\n\t\t<integer>6</integer>\n\t\t<key>Minute</key>\n\t\t<integer>30</integer>",
		"<key>RunAtLoad</key>\n\t<false/>",
		"<key>PATH</key>\n\t\t<string>/opt/agent tools:/usr/bin</string>",
		"<string>/logs/doctor.log</string>",
	} {
		if !strings.Contains(got, frag) {
			t.Errorf("plist missing %q\n%s", frag, got)
		}
	}
	// Not the sync timer's label: the two jobs must never replace each other.
	if strings.Contains(got, syncLaunchdLabel) {
		t.Error("doctor plist carries the sync label")
	}
}

func TestRenderWindowsDoctorTaskXML(t *testing.T) {
	s := testDoctorSpec()
	s.BinPath = `C:\Program Files\cartographer\cartographer.exe`
	got := RenderWindowsDoctorTaskXML(s)
	var doc struct{ XMLName xml.Name }
	if err := xml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("task XML does not parse: %v\n%s", err, got)
	}
	for _, frag := range []string{
		`<URI>\Cartographer\Doctor</URI>`,
		"<StartBoundary>2000-01-01T06:30:00</StartBoundary>",
		"<DaysInterval>1</DaysInterval>",
		"<StartWhenAvailable>true</StartWhenAvailable>",
		`<Command>C:\Program Files\cartographer\cartographer.exe</Command>`,
		`<Arguments>"doctor" "run" "--kb" "kb-a"`,
	} {
		if !strings.Contains(got, frag) {
			t.Errorf("task XML missing %q\n%s", frag, got)
		}
	}
}

func TestDoctorTimerSpecValidate(t *testing.T) {
	s := testDoctorSpec()
	if err := s.Validate(); err != nil {
		t.Fatalf("valid spec: %v", err)
	}
	s.Hour = 24
	if s.Validate() == nil {
		t.Error("hour 24 accepted")
	}
	s = testDoctorSpec()
	s.Args = append(s.Args, `--client-flag`, `x" --evil`)
	if s.Validate() == nil {
		t.Error("an argument with a double quote was accepted")
	}
}

func TestDoctorTimerInstallIsIdempotentAndUninstallable(t *testing.T) {
	for _, platform := range []string{"darwin", "linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			home := withTestHome(t, platform)
			origExec := osExecutable
			osExecutable = func() (string, error) { return "/usr/local/bin/cartographer", nil }
			t.Cleanup(func() { osExecutable = origExec })
			m, stub := newTestManager()

			var paths []string
			switch platform {
			case "darwin":
				p, _ := DoctorLaunchdPlistPath()
				paths = []string{p}
			case "windows":
				p, _ := DoctorWindowsTaskPath()
				paths = []string{p}
			default:
				svc, _ := DoctorSystemdServicePath()
				timer, _ := DoctorSystemdTimerPath()
				paths = []string{svc, timer}
			}
			args := []string{"doctor", "run", "--kb", "kb-a", "--client", "claude", "--at", "06:00"}

			st, err := m.DoctorTimerStatus()
			if err != nil || st.Installed {
				t.Fatalf("fresh status = %+v, %v", st, err)
			}
			if err := m.InstallDoctorTimer(args, 6, 0, "/opt/agent"); err != nil {
				t.Fatalf("install: %v", err)
			}
			first := map[string]string{}
			for _, p := range paths {
				data, err := os.ReadFile(p)
				if err != nil {
					t.Fatalf("expected %s: %v", p, err)
				}
				first[p] = string(data)
			}
			st, _ = m.DoctorTimerStatus()
			if !st.Installed || !strings.Contains(st.Definition, "--kb") {
				t.Errorf("status = %+v", st)
			}
			// Again: same bytes, still registered.
			if err := m.InstallDoctorTimer(args, 6, 0, "/opt/agent"); err != nil {
				t.Fatalf("second install: %v", err)
			}
			for _, p := range paths {
				data, _ := os.ReadFile(p)
				if string(data) != first[p] {
					t.Errorf("%s changed on re-install", p)
				}
			}
			if err := m.UninstallDoctorTimer(); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			for _, p := range paths {
				if _, err := os.Stat(p); !os.IsNotExist(err) {
					t.Errorf("%s survived uninstall", p)
				}
			}
			if err := m.UninstallDoctorTimer(); err != nil {
				t.Errorf("second uninstall: %v", err)
			}
			if len(stub.calls) == 0 {
				t.Error("no platform command was run")
			}
			// Nothing outside the doctor's own files was created under home.
			var extra []string
			filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
				if err == nil && !fi.IsDir() {
					extra = append(extra, p)
				}
				return nil
			})
			for _, p := range extra {
				if !strings.Contains(filepath.Base(p), "doctor") {
					t.Errorf("unexpected file left behind: %s", p)
				}
			}
		})
	}
}

// A symlink at the definition's path is someone else's file (D148).
func TestInstallDoctorTimerRefusesASymlink(t *testing.T) {
	withTestHome(t, "linux")
	origExec := osExecutable
	osExecutable = func() (string, error) { return "/usr/local/bin/cartographer", nil }
	t.Cleanup(func() { osExecutable = origExec })
	timer, _ := DoctorSystemdTimerPath()
	svc, _ := DoctorSystemdServicePath()
	victim := filepath.Join(t.TempDir(), "precious")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(svc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, svc); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	m, _ := newTestManager()
	if err := m.InstallDoctorTimer([]string{"doctor", "run"}, 6, 0, ""); err == nil {
		t.Fatal("install through a symlink succeeded")
	}
	if data, _ := os.ReadFile(victim); string(data) != "keep" {
		t.Errorf("symlink target rewritten: %q", data)
	}
	if _, err := os.Stat(timer); err == nil {
		t.Error("timer written despite the refusal")
	}
}

func TestDoctorTimerFilesAreDistinctFromOtherUnits(t *testing.T) {
	withTestHome(t, "linux")
	d1, _ := DoctorSystemdServicePath()
	d2, _ := DoctorSystemdTimerPath()
	s1, _ := SyncSystemdServicePath()
	s2, _ := SyncSystemdTimerPath()
	srv, _ := SystemdUnitPath()
	seen := map[string]bool{}
	for _, p := range []string{d1, d2, s1, s2, srv} {
		if seen[p] {
			t.Errorf("path shared between units: %s", p)
		}
		seen[p] = true
	}
	if windowsDoctorTask == windowsSyncTaskName || windowsDoctorTask == windowsServeTaskName || doctorLaunchdLabel == syncLaunchdLabel {
		t.Error("doctor timer reuses a sync/serve name")
	}
}
