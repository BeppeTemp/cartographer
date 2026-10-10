package main

// `doctor schedule|unschedule|status|run` (D369) against stubbed schedulers,
// clients and server: nothing here touches a real launchd, systemd or model.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/service"
)

type doctorSched struct {
	installed   []string // args of the last install
	hour, min   int
	binDir      string
	removed     bool
	status      service.DoctorTimerStatus
	declared    []string // "kb client RFC3339"
	withdrawn   []string
	declareErr  error
	clientRuns  [][]string // bin + argv
	clientErr   error
	now         time.Time
	lookupCalls []string
}

func stubDoctorSchedule(t *testing.T, agents []string, kbs []string) *doctorSched {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfg := clientconfig.Default()
	cfg.ServerURL, cfg.Agents, cfg.KnownKBs = "http://localhost:1/mcp", agents, kbs
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	s := &doctorSched{now: time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)}
	o := [9]any{doctorLookPathFn, doctorNowFn, doctorTimerInstallFn, doctorTimerRemoveFn, doctorTimerStatusFn, doctorDeclareFn, doctorWithdrawFn, doctorRunClientFn, doctorLogPathFn}
	t.Cleanup(func() {
		doctorLookPathFn, doctorNowFn = o[0].(func(string) (string, error)), o[1].(func() time.Time)
		doctorTimerInstallFn = o[2].(func([]string, int, int, string) error)
		doctorTimerRemoveFn = o[3].(func() error)
		doctorTimerStatusFn = o[4].(func() (service.DoctorTimerStatus, error))
		doctorDeclareFn = o[5].(func(*clientconfig.Config, string, string, time.Time) error)
		doctorWithdrawFn = o[6].(func(*clientconfig.Config, string) error)
		doctorRunClientFn = o[7].(func(string, []string) error)
		doctorLogPathFn = o[8].(func() string)
	})
	doctorLookPathFn = func(b string) (string, error) {
		s.lookupCalls = append(s.lookupCalls, b)
		return "/opt/agents/" + b, nil
	}
	doctorNowFn = func() time.Time { return s.now }
	doctorLogPathFn = func() string { return "" }
	doctorTimerInstallFn = func(args []string, h, m int, dir string) error {
		s.installed, s.hour, s.min, s.binDir = args, h, m, dir
		return nil
	}
	doctorTimerRemoveFn = func() error { s.removed = true; return nil }
	doctorTimerStatusFn = func() (service.DoctorTimerStatus, error) { return s.status, nil }
	doctorDeclareFn = func(_ *clientconfig.Config, kb, client string, next time.Time) error {
		s.declared = append(s.declared, kb+" "+client+" "+next.Format(time.RFC3339))
		return s.declareErr
	}
	doctorWithdrawFn = func(_ *clientconfig.Config, kb string) error { s.withdrawn = append(s.withdrawn, kb); return nil }
	doctorRunClientFn = func(bin string, argv []string) error {
		s.clientRuns = append(s.clientRuns, append([]string{bin}, argv...))
		return s.clientErr
	}
	return s
}

func TestDoctorSchedule_Install(t *testing.T) {
	s := stubDoctorSchedule(t, []string{"claude", "hermes"}, []string{"kb-a"})
	var code int
	out := withStdout(t, func() {
		code = cmdDoctor([]string{"schedule", "--at", "06:30", "--client-flag", "--allowedTools", "--client-flag", "mcp"})
	})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	// The client path is made absolute: on Windows that adds the drive and
	// turns the separators, so the expectation goes through the same call.
	bin, _ := filepath.Abs("/opt/agents/claude")
	want := "doctor run --kb kb-a --client claude --client-bin " + bin + " --at 06:30 --client-flag --allowedTools --client-flag mcp"
	if got := strings.Join(s.installed, " "); got != want {
		t.Errorf("job args = %q, want %q", got, want)
	}
	if s.hour != 6 || s.min != 30 || s.binDir != filepath.Dir(bin) {
		t.Errorf("hour/min/dir = %d %d %q", s.hour, s.min, s.binDir)
	}
	// Now is 08:00 UTC, so 06:30 is tomorrow.
	if len(s.declared) != 1 || s.declared[0] != "kb-a claude 2026-10-11T06:30:00Z" {
		t.Errorf("declared = %v", s.declared)
	}
	if !strings.Contains(out, "unattended") {
		t.Errorf("output must say it spends quota unattended: %q", out)
	}
}

func TestDoctorSchedule_InstallRefusals(t *testing.T) {
	cases := map[string]struct {
		agents, kbs []string
		args        []string
	}{
		"no client chosen, two capable": {[]string{"claude", "codex"}, []string{"kb-a"}, []string{"schedule"}},
		"no headless mode (hermes)":     {[]string{"hermes"}, []string{"kb-a"}, []string{"schedule", "--client", "hermes"}},
		"no kb chosen, two known":       {[]string{"claude"}, []string{"kb-a", "kb-b"}, []string{"schedule"}},
		"bad kb name":                   {[]string{"claude"}, []string{"kb-a"}, []string{"schedule", "--kb", `a" --x`}},
		"bad time":                      {[]string{"claude"}, []string{"kb-a"}, []string{"schedule", "--at", "25:00"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := stubDoctorSchedule(t, c.agents, c.kbs)
			var code int
			withStdout(t, func() { code = cmdDoctor(c.args) })
			if code == 0 || s.installed != nil {
				t.Errorf("exit %d, installed %v: want a refusal that writes nothing", code, s.installed)
			}
		})
	}
}

func TestDoctorSchedule_ClientMissingOnPath(t *testing.T) {
	s := stubDoctorSchedule(t, []string{"claude"}, []string{"kb-a"})
	doctorLookPathFn = func(string) (string, error) { return "", errors.New("not found") }
	var code int
	withStdout(t, func() { code = cmdDoctor([]string{"schedule"}) })
	if code == 0 || s.installed != nil {
		t.Errorf("exit %d, installed %v", code, s.installed)
	}
}

// A declaration the server refuses (older server, unreachable) never undoes the
// schedule: it is installed and the first run declares.
func TestDoctorSchedule_DeclarationFailureIsAWarning(t *testing.T) {
	s := stubDoctorSchedule(t, []string{"claude"}, []string{"kb-a"})
	s.declareErr = errors.New("HTTP 404")
	var code int
	out := withStdout(t, func() { code = cmdDoctor([]string{"schedule"}) })
	if code != 0 || s.installed == nil || !strings.Contains(out, "warning") {
		t.Errorf("exit %d, installed %v, out %q", code, s.installed, out)
	}
}

func TestDoctorSchedule_Reschedule_ReplacesNotAccumulates(t *testing.T) {
	s := stubDoctorSchedule(t, []string{"claude", "codex"}, []string{"kb-a", "kb-b"})
	withStdout(t, func() { cmdDoctor([]string{"schedule", "--client", "claude", "--kb", "kb-a"}) })
	withStdout(t, func() { cmdDoctor([]string{"schedule", "--client", "codex", "--kb", "kb-b", "--at", "7:15"}) })
	if got := strings.Join(s.installed, " "); !strings.Contains(got, "--kb kb-b --client codex") || strings.Contains(got, "kb-a") {
		t.Errorf("second schedule = %q", got)
	}
}

func TestDoctorSchedule_UnscheduleAndStatus(t *testing.T) {
	s := stubDoctorSchedule(t, []string{"claude"}, []string{"kb-a"})
	var code int
	out := withStdout(t, func() { code = cmdDoctor([]string{"status"}) })
	if code != exitStatusNotInstalled || !strings.Contains(out, "no scheduled doctor") {
		t.Errorf("not installed: exit %d, %q", code, out)
	}
	s.status = service.DoctorTimerStatus{Installed: true, Active: true, Path: "/x/doctor.plist",
		Definition: "<string>--kb</string>\n<string>kb-a</string>\n<string>--client</string>\n<string>claude</string>\n<string>--at</string>\n<string>06:00</string>"}
	out = withStdout(t, func() { code = cmdDoctor([]string{"status"}) })
	if code != exitStatusRunning || !strings.Contains(out, `client claude, kb "kb-a", daily at 06:00`) || !strings.Contains(out, "next run 2026-10-11 06:00") {
		t.Errorf("installed: exit %d, %q", code, out)
	}
	withStdout(t, func() { code = cmdDoctor([]string{"unschedule"}) })
	if code != 0 || !s.removed || len(s.withdrawn) != 1 || s.withdrawn[0] != "kb-a" {
		t.Errorf("unschedule: exit %d removed %v withdrawn %v", code, s.removed, s.withdrawn)
	}
}

func TestDoctorJobFlags_AllThreeDefinitionFormats(t *testing.T) {
	for name, def := range map[string]string{
		"systemd": `ExecStart="/bin/cartographer" "doctor" "run" "--kb" "kb-a" "--client" "codex" "--client-bin" "/a b/codex" "--at" "21:05"`,
		"windows": `<Arguments>"doctor" "run" "--kb" "kb-a" "--client" "codex" "--at" "21:05"</Arguments>`,
	} {
		kb, client, at := doctorJobFlags(def)
		if kb != "kb-a" || client != "codex" || at != "21:05" {
			t.Errorf("%s: got %q %q %q", name, kb, client, at)
		}
	}
}

func TestDoctorRun_DeclaresNextRunOnlyOnSuccess(t *testing.T) {
	s := stubDoctorSchedule(t, []string{"claude"}, []string{"kb-a"})
	args := []string{"run", "--kb", "kb-a", "--client", "claude", "--client-bin", "/opt/agents/claude", "--at", "06:00", "--client-flag", "--flag"}
	var code int
	withStdout(t, func() { code = cmdDoctor(args) })
	if code != 0 || len(s.clientRuns) != 1 {
		t.Fatalf("exit %d runs %v", code, s.clientRuns)
	}
	if got := strings.Join(s.clientRuns[0], "|"); got != `/opt/agents/claude|--flag|-p|Run the kb-doctor skill on kb "kb-a" unattended.` {
		t.Errorf("client invocation = %q", got)
	}
	if len(s.declared) != 1 || s.declared[0] != "kb-a claude 2026-10-11T06:00:00Z" {
		t.Errorf("declared = %v", s.declared)
	}

	s.declared, s.clientErr = nil, errors.New("exit status 1")
	withStdout(t, func() { code = cmdDoctor(args) })
	if code == 0 || len(s.declared) != 0 {
		t.Errorf("failed run: exit %d declared %v; a failing client must let the declaration go stale", code, s.declared)
	}
}

func TestDoctorRun_RejectsAnUnknownClient(t *testing.T) {
	s := stubDoctorSchedule(t, nil, nil)
	var code int
	withStdout(t, func() {
		code = cmdDoctor([]string{"run", "--kb", "kb-a", "--client", "hermes", "--client-bin", "/x"})
	})
	if code == 0 || len(s.clientRuns) != 0 {
		t.Errorf("exit %d runs %v", code, s.clientRuns)
	}
}

func TestNextOccurrence(t *testing.T) {
	now := time.Date(2026, 3, 28, 6, 0, 0, 0, time.UTC)
	if got := nextOccurrence(now, 6, 0); !got.Equal(time.Date(2026, 3, 29, 6, 0, 0, 0, time.UTC)) {
		t.Errorf("at the exact minute the next run is tomorrow, got %v", got)
	}
	if got := nextOccurrence(now, 6, 1); !got.Equal(time.Date(2026, 3, 28, 6, 1, 0, 0, time.UTC)) {
		t.Errorf("later today, got %v", got)
	}
}

// sync, upgrade and repair never install the scheduled doctor: only connect
// (and setup, which runs it) does, through ensureDoctorTimer (D690).
func TestDoctorSchedule_OnlyConnectInstalls(t *testing.T) {
	for _, f := range []string{"sync.go", "reconnect.go", "upgraderepair.go", "update.go"} {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "doctorTimerInstallFn") || strings.Contains(string(data), "InstallDoctorTimer") || strings.Contains(string(data), "ensureDoctorTimer") {
			t.Errorf("%s installs the scheduled doctor; only connect does (D690)", f)
		}
	}
}

func TestEnsureDoctorTimer_InstallsWithDefaultGrant(t *testing.T) {
	s := stubDoctorSchedule(t, []string{"claude"}, []string{"kb-a"})
	dir, _ := clientconfig.TargetDir()
	out := withStdout(t, func() { ensureDoctorTimer(dir, []string{"codex", "copilot", "claude"}, false) })
	bin, _ := filepath.Abs("/opt/agents/claude")
	want := "doctor run --kb kb-a --client claude --client-bin " + bin + " --at 06:00 --client-flag --allowedTools --client-flag mcp__cartographer__*"
	if got := strings.Join(s.installed, " "); got != want {
		t.Errorf("job args = %q, want %q", got, want)
	}
	if !strings.Contains(out, "doctor unschedule") || !strings.Contains(out, "unattended") {
		t.Errorf("output must say how to undo and the quota: %q", out)
	}
}

func TestEnsureDoctorTimer_CopilotGrantUsesServerName(t *testing.T) {
	s := stubDoctorSchedule(t, nil, []string{"kb-a"})
	dir, _ := clientconfig.TargetDir()
	cfg, _ := clientconfig.Load(dir)
	cfg.ServerName = "kb-server"
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	withStdout(t, func() { ensureDoctorTimer(dir, []string{"copilot"}, false) })
	if got := strings.Join(s.installed, " "); !strings.HasSuffix(got, "--client-flag --allow-tool --client-flag kb-server") {
		t.Errorf("job args = %q", got)
	}
}

func TestEnsureDoctorTimer_Skips(t *testing.T) {
	cases := []struct {
		name      string
		kbs       []string
		providers []string
		optOut    bool
		installed bool
		dryRun    bool
		corrupt   bool
		wantHint  bool
		wantNote  string
	}{
		{name: "opt-out", kbs: []string{"kb-a"}, providers: []string{"claude"}, optOut: true},
		{name: "two KBs", kbs: []string{"kb-a", "kb-b"}, providers: []string{"claude"}, wantHint: true},
		{name: "only codex", kbs: []string{"kb-a"}, providers: []string{"codex"}, wantHint: true},
		{name: "already installed", kbs: []string{"kb-a"}, providers: []string{"claude"}, installed: true},
		{name: "dry run", kbs: []string{"kb-a"}, providers: []string{"claude"}, dryRun: true, wantNote: "[dry-run] would schedule"},
		{name: "unreadable config", kbs: []string{"kb-a"}, providers: []string{"claude"}, corrupt: true, wantNote: "cannot be read"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := stubDoctorSchedule(t, c.providers, c.kbs)
			dir, _ := clientconfig.TargetDir()
			if c.optOut {
				cfg, _ := clientconfig.Load(dir)
				cfg.DoctorTimerOptOut = true
				if err := clientconfig.Save(dir, cfg); err != nil {
					t.Fatal(err)
				}
			}
			if c.corrupt {
				if err := os.WriteFile(clientconfig.Path(dir), []byte("agents: [unclosed"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			s.status.Installed = c.installed
			out := withStdout(t, func() { ensureDoctorTimer(dir, c.providers, c.dryRun) })
			if s.installed != nil {
				t.Errorf("installed %v, want nothing", s.installed)
			}
			if got := strings.Contains(out, "cartographer doctor schedule"); got != c.wantHint {
				t.Errorf("hint = %v, want %v: %q", got, c.wantHint, out)
			}
			if c.wantNote != "" && !strings.Contains(out, c.wantNote) {
				t.Errorf("output %q lacks %q", out, c.wantNote)
			}
		})
	}
}

func TestDoctorUnscheduleSetsOptOutAndScheduleClearsIt(t *testing.T) {
	stubDoctorSchedule(t, []string{"claude"}, []string{"kb-a"})
	dir, _ := clientconfig.TargetDir()
	withStdout(t, func() { cmdDoctor([]string{"unschedule"}) })
	cfg, err := clientconfig.Load(dir)
	if err != nil || !cfg.DoctorTimerOptOut {
		t.Fatalf("unschedule must remember the opt-out: %+v %v", cfg, err)
	}
	data, _ := os.ReadFile(clientconfig.Path(dir))
	if !strings.Contains(string(data), "doctor_timer_opt_out: true") {
		t.Errorf("opt-out not persisted: %s", data)
	}
	withStdout(t, func() { cmdDoctor([]string{"schedule"}) })
	cfg, err = clientconfig.Load(dir)
	if err != nil || cfg.DoctorTimerOptOut {
		t.Fatalf("schedule must clear the opt-out: %+v %v", cfg, err)
	}
}
