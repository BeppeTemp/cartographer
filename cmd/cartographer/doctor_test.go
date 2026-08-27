package main

// `cartographer doctor` (D143): every check, on a fabricated base dir.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/agents"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// doctorStubs describes the machine: which agents are installed and whether the
// scheduled trigger is there. Without it the checks would depend on whatever
// happens to be installed on the machine running the tests.
func doctorStubs(t *testing.T, installed []string, timerInstalled bool) {
	t.Helper()
	origDetect, origTimer := doctorDetectFn, syncTimerStatusFn
	set := map[string]bool{}
	for _, p := range installed {
		set[p] = true
	}
	doctorDetectFn = func() []agents.Agent {
		var out []agents.Agent
		for _, d := range configurator.DetectionOrder() {
			out = append(out, agents.Agent{Provider: d.Provider, Name: d.DisplayName, Installed: set[string(d.Provider)]})
		}
		return out
	}
	syncTimerStatusFn = func() (service.SyncTimerStatus, error) {
		return service.SyncTimerStatus{Installed: timerInstalled, Path: "/tmp/cartographer-sync.timer"}, nil
	}
	t.Cleanup(func() { doctorDetectFn, syncTimerStatusFn = origDetect, origTimer })
}

// doctorFixture connects one provider against a stub server and returns the
// base dir: the healthy starting point every case then breaks in one way.
func doctorFixture(t *testing.T, provider string) string {
	t.Helper()
	srv := multiKBServer(t, `{"status":"ok","version":"dev","kbs":[{"name":"alpha"}]}`)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	if _, err := doConnect(connectOptions{Providers: []string{provider}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", Trust: true}); err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	return dir
}

func findingsFor(r doctorReport, check string) []doctorFinding {
	var out []doctorFinding
	for _, f := range r.Findings {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

// A machine in order produces nothing, and exits 0.
func TestRunDoctor_HealthyTree(t *testing.T) {
	doctorStubs(t, []string{"claude"}, false)
	dir := doctorFixture(t, "claude")

	report := runDoctor(dir, "")
	if len(report.Findings) != 0 {
		t.Fatalf("healthy tree produced findings: %+v", report.Findings)
	}
	if code := doctorExitCode(report); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	// And it wrote nothing: the report is a read.
	if _, err := os.Stat(filepath.Join(dir, "skills")); !os.IsNotExist(err) {
		t.Errorf("doctor created something: %v", err)
	}
}

// One case per check, each breaking exactly one thing.
func TestRunDoctor_Checks(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
		// installed and timer describe the machine.
		installed []string
		timer     bool
		break_    func(t *testing.T, dir string)
		check     string
		severity  string
		wantFix   string
	}{
		{
			name: "provider no longer installed", provider: "claude", installed: nil,
			break_: func(*testing.T, string) {},
			check:  "client-config", severity: doctorWarning, wantFix: "cartographer disconnect claude",
		},
		{
			name: "managed file deleted", provider: "claude", installed: []string{"claude"},
			break_: func(t *testing.T, dir string) {
				if err := os.RemoveAll(filepath.Join(dir, ".claude", "hooks", "cartographer-bootstrap")); err != nil {
					t.Fatal(err)
				}
			},
			check: "managed-files", severity: doctorError, wantFix: "cartographer sync",
		},
		{
			name: "v1 lockfile", provider: "claude", installed: []string{"claude"},
			break_: func(t *testing.T, dir string) {
				raw := `{"applied_revision":"r1","provider":"claude","managed":[]}`
				if err := os.WriteFile(lockFilePath(dir), []byte(raw), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			check: "lockfile", severity: doctorWarning, wantFix: "cartographer sync",
		},
		{
			name: "unreadable lockfile", provider: "claude", installed: []string{"claude"},
			break_: func(t *testing.T, dir string) {
				if err := os.WriteFile(lockFilePath(dir), []byte("{not json"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			check: "lockfile", severity: doctorError, wantFix: "cartographer reconnect",
		},
		{
			name: "mcp entry for a KB that is gone", provider: "claude", installed: []string{"claude"},
			break_: func(t *testing.T, dir string) {
				path := filepath.Join(dir, ".claude.json")
				var root map[string]any
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, &root); err != nil {
					t.Fatal(err)
				}
				servers := root["mcpServers"].(map[string]any)
				// An entry Cartographer owns by name, for a KB the server
				// does not mount: exactly what an older multi-KB connection
				// leaves behind when a KB is unmounted.
				servers["cartographer-ghost"] = map[string]any{"type": "http", "url": "http://x/mcp?kb=ghost"}
				out, _ := json.Marshal(root)
				if err := os.WriteFile(path, out, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			check: "mcp-entries", severity: doctorError, wantFix: "cartographer reconnect",
		},
		{
			name: "instructions block duplicated", provider: "claude", installed: []string{"claude"},
			break_: func(t *testing.T, dir string) {
				path := filepath.Join(dir, ".claude", "CLAUDE.md")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				block := "<!-- cartographer:instructions:begin -->\nx\n<!-- cartographer:instructions:end -->\n"
				if err := os.WriteFile(path, []byte(block+block), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			check: "instructions", severity: doctorError, wantFix: "cartographer reconnect",
		},
		{
			name: "hook registered twice", provider: "claude", installed: []string{"claude"},
			break_: func(t *testing.T, dir string) {
				path := filepath.Join(dir, ".claude", "settings.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var root map[string]any
				if err := json.Unmarshal(data, &root); err != nil {
					t.Fatal(err)
				}
				hooks := root["hooks"].(map[string]any)
				groups := hooks["SessionStart"].([]any)
				hooks["SessionStart"] = append(groups, groups[0])
				out, _ := json.Marshal(root)
				if err := os.WriteFile(path, out, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			check: "hooks", severity: doctorError, wantFix: "cartographer sync",
		},
		{
			name: "hookless provider without a timer", provider: "kiro", installed: []string{"kiro"}, timer: false,
			break_: func(*testing.T, string) {},
			check:  "trigger", severity: doctorWarning, wantFix: "cartographer service sync-timer install",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doctorStubs(t, tc.installed, tc.timer)
			dir := doctorFixture(t, tc.provider)
			tc.break_(t, dir)

			report := runDoctor(dir, "")
			found := findingsFor(report, tc.check)
			if len(found) == 0 {
				t.Fatalf("check %q reported nothing; all findings: %+v", tc.check, report.Findings)
			}
			f := found[0]
			if f.Severity != tc.severity {
				t.Errorf("severity = %q, want %q (%+v)", f.Severity, tc.severity, f)
			}
			if f.Fix != tc.wantFix {
				t.Errorf("fix = %q, want %q", f.Fix, tc.wantFix)
			}
			// Every finding names an absolute location on this machine — the
			// path may legitimately be gone (that is what "missing" means),
			// but it must be somewhere the operator can go and look.
			if !filepath.IsAbs(f.Path) {
				t.Errorf("finding names no absolute path: %+v", f)
			}
			if code := doctorExitCode(report); code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
		})
	}
}

// A hookless provider with the timer installed has nothing to report.
func TestRunDoctor_TimerCoversHooklessProvider(t *testing.T) {
	doctorStubs(t, []string{"kiro"}, true)
	dir := doctorFixture(t, "kiro")
	if f := findingsFor(runDoctor(dir, ""), "trigger"); len(f) != 0 {
		t.Errorf("timer installed but still reported: %+v", f)
	}
}

// An unreachable server is one finding, not a failure: doctor must stay useful
// offline.
func TestRunDoctor_UnreachableServer(t *testing.T) {
	doctorStubs(t, []string{"claude"}, false)
	dir := doctorFixture(t, "claude")
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ServerURL = "http://127.0.0.1:1/mcp"
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}

	report := runDoctor(dir, "")
	found := findingsFor(report, "server")
	if len(found) != 1 || found[0].Severity != doctorWarning {
		t.Fatalf("unreachable server = %+v, want exactly one warning", found)
	}
	if !strings.Contains(found[0].Message, "unreachable") {
		t.Errorf("message does not say the server is unreachable: %q", found[0].Message)
	}
}

// --provider narrows the run to one provider.
func TestRunDoctor_ProviderNarrowsTheRun(t *testing.T) {
	doctorStubs(t, []string{"claude"}, false)
	dir := doctorFixture(t, "claude")

	// codex is neither connected nor installed: asking about it reports it,
	// asking about claude does not.
	if f := findingsFor(runDoctor(dir, "codex"), "client-config"); len(f) == 0 {
		t.Error("--provider codex reported nothing about codex")
	}
	if f := findingsFor(runDoctor(dir, "claude"), "client-config"); len(f) != 0 {
		t.Errorf("--provider claude reported %+v", f)
	}
}

// The JSON shape is a contract: counts plus a flat findings array.
func TestDoctorReport_JSONShape(t *testing.T) {
	doctorStubs(t, nil, false)
	dir := doctorFixture(t, "claude")

	data, err := json.Marshal(runDoctor(dir, ""))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("the output is not valid JSON: %v", err)
	}
	for _, key := range []string{"schema_version", "error_count", "warning_count", "info_count", "findings"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("missing key %q in %s", key, data)
		}
	}
	if decoded["schema_version"] != doctorSchema {
		t.Errorf("schema_version = %v, want %s", decoded["schema_version"], doctorSchema)
	}
	findings, ok := decoded["findings"].([]any)
	if !ok || len(findings) == 0 {
		t.Fatalf("findings is not a non-empty array: %v", decoded["findings"])
	}
	first := findings[0].(map[string]any)
	for _, key := range []string{"check", "severity", "message"} {
		if _, ok := first[key]; !ok {
			t.Errorf("finding missing key %q: %v", key, first)
		}
	}
}

// An unknown --provider is a usage error, not a finding.
func TestCmdDoctor_UnknownProvider(t *testing.T) {
	if code := cmdDoctor([]string{"--provider", "definitely-not-a-provider"}); code != 2 {
		t.Errorf("cmdDoctor(--provider bogus) = %d, want 2", code)
	}
}

// Errors sort before warnings, which sort before informational findings.
func TestSortDoctorFindings(t *testing.T) {
	findings := []doctorFinding{
		{Check: "a", Severity: doctorInfo},
		{Check: "b", Severity: doctorWarning},
		{Check: "c", Severity: doctorError},
		{Check: "d", Severity: doctorWarning},
	}
	sortDoctorFindings(findings)
	var order []string
	for _, f := range findings {
		order = append(order, f.Check)
	}
	if strings.Join(order, "") != "cbda" {
		t.Errorf("order = %v, want [c b d a]", order)
	}
}

// An entry with no materialized hash is DriftUnknown, which is deliberately
// not healable and which ComputeDiff does not see either — so `sync`
// re-materializes only what actually changed and leaves the finding standing.
// The suggested command must therefore be the rebuild, not the sync: a
// diagnosis that names a command which does not resolve it is exactly the
// noise D143 forbids.
func TestRunDoctor_UnknownHashSuggestsRebuild(t *testing.T) {
	doctorStubs(t, []string{"claude"}, false)
	dir := doctorFixture(t, "claude")

	// A lockfile as an older client wrote it: managed entries, no hashes.
	lockFile, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	lock := lockFile.ForProvider("claude")
	for i := range lock.Managed {
		lock.Managed[i].MaterializedHash = ""
	}
	lockFile.SetProvider("claude", lock)
	if err := provisioning.WriteLockFile(lockFilePath(dir), lockFile); err != nil {
		t.Fatal(err)
	}

	found := findingsFor(runDoctor(dir, ""), "managed-files")
	if len(found) != 1 {
		t.Fatalf("expected one aggregate finding, got %+v", found)
	}
	if found[0].Severity != doctorInfo {
		t.Errorf("severity = %q, want %q", found[0].Severity, doctorInfo)
	}
	if found[0].Fix != "cartographer reconnect" {
		t.Errorf("fix = %q, want the rebuild: a sync leaves these entries untouched", found[0].Fix)
	}
}
