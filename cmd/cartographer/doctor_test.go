package main

// `cartographer doctor` (D143): every check, on a fabricated base dir.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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
// The remedy is now the narrow one: the content is on disk and the hash is
// computable without refetching anything, so rewriting every managed artifact on
// every client is three orders of magnitude more work than the problem (D157).
func TestRunDoctor_UnknownHashSuggestsRepairHashes(t *testing.T) {
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
	if !strings.Contains(found[0].Fix, "--repair-hashes") {
		t.Errorf("fix = %q, want the narrow remedy", found[0].Fix)
	}

	// The round trip: repairing clears the finding, and the entries are marked
	// adopted rather than silently passing as verified.
	out := withStdout(t, func() {
		if code := repairManagedHashes(dir, ""); code != 0 {
			t.Errorf("repairManagedHashes = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "adopted, not verified") {
		t.Errorf("output %q does not say the entries are adopted", out)
	}
	if again := findingsFor(runDoctor(dir, ""), "managed-files"); len(again) != 0 {
		t.Errorf("finding survived the repair: %+v", again)
	}
	repaired, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range repaired.ForProvider("claude").Managed {
		if mf.MaterializedHash == "" {
			t.Errorf("entry %s/%s still has no hash", mf.Kind, mf.Name)
		}
		if mf.AdoptedAt == "" {
			t.Errorf("entry %s/%s is not marked adopted", mf.Kind, mf.Name)
		}
	}
}

// Nothing to repair is a clean, explicit outcome rather than silence.
func TestRepairManagedHashes_NothingToDo(t *testing.T) {
	doctorStubs(t, []string{"claude"}, false)
	dir := doctorFixture(t, "claude")
	out := withStdout(t, func() {
		if code := repairManagedHashes(dir, ""); code != 0 {
			t.Errorf("repairManagedHashes = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "no unverifiable managed artifacts") {
		t.Errorf("output = %q", out)
	}
}

// A symlinked managed destination makes provisioning write outside the client,
// and the condition is invisible on any sync where the artifact is not in the
// diff — so doctor must be able to answer it on demand (D148).
func TestCheckSymlinkedDestinations(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}

	t.Run("reports a symlinked skills directory", func(t *testing.T) {
		dir := t.TempDir()
		foreign := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(foreign, filepath.Join(dir, ".claude", "skills")); err != nil {
			t.Fatal(err)
		}
		findings := checkSymlinkedDestinations(dir, []string{"claude"})
		if len(findings) != 1 {
			t.Fatalf("findings = %+v, want exactly one", findings)
		}
		if findings[0].Check != "symlink" || !strings.Contains(findings[0].Message, foreign) {
			t.Errorf("finding = %+v, want a symlink check naming %q", findings[0], foreign)
		}
	})

	t.Run("silent on real directories", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".claude", "skills"), 0o755); err != nil {
			t.Fatal(err)
		}
		if findings := checkSymlinkedDestinations(dir, []string{"claude"}); len(findings) != 0 {
			t.Errorf("findings = %+v, want none", findings)
		}
	})

	t.Run("silent when nothing exists yet", func(t *testing.T) {
		if findings := checkSymlinkedDestinations(t.TempDir(), []string{"claude", "kiro"}); len(findings) != 0 {
			t.Errorf("findings = %+v, want none", findings)
		}
	})
}

// A gate that is off changes what an agent can do, and nothing in the running
// system said so (D151). A missing signal must stay silent rather than be read
// as "everything is on".
func TestCheckCapabilities(t *testing.T) {
	newServer := func(body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, body)
		}))
	}

	t.Run("reports a disabled gate with its setting", func(t *testing.T) {
		srv := newServer(`{"status":"ok","version":"0.8.3","kbs":[{"name":"kb","capabilities":{"artifact_write":{"state":"disabled","setting":"kbs[].allow_artifact_write"},"git_sync":{"state":"enabled","setting":"git.sync"}}}]}`)
		defer srv.Close()
		findings := checkCapabilities(t.TempDir(), &clientconfig.Config{ServerURL: srv.URL})
		if len(findings) != 1 {
			t.Fatalf("findings = %+v, want exactly one", findings)
		}
		if !strings.Contains(findings[0].Message, "artifact_write") || !strings.Contains(findings[0].Fix, "allow_artifact_write") {
			t.Errorf("finding = %+v, want it to name the gate and its setting", findings[0])
		}
	})

	t.Run("reports a discovered mount", func(t *testing.T) {
		srv := newServer(`{"status":"ok","version":"0.8.3","kbs":[{"name":"kb","capabilities":{"mount":{"state":"discovered","setting":"kbs[]"}}}]}`)
		defer srv.Close()
		findings := checkCapabilities(t.TempDir(), &clientconfig.Config{ServerURL: srv.URL})
		if len(findings) != 1 || !strings.Contains(findings[0].Message, "discovered") {
			t.Fatalf("findings = %+v, want one naming the discovered mount", findings)
		}
	})

	t.Run("silent when everything is on", func(t *testing.T) {
		srv := newServer(`{"status":"ok","version":"0.8.3","kbs":[{"name":"kb","capabilities":{"artifact_write":{"state":"enabled","setting":"kbs[].allow_artifact_write"},"mount":{"state":"configured","setting":"kbs[]"}}}]}`)
		defer srv.Close()
		if findings := checkCapabilities(t.TempDir(), &clientconfig.Config{ServerURL: srv.URL}); len(findings) != 0 {
			t.Errorf("findings = %+v, want none", findings)
		}
	})

	t.Run("silent when the server is unreachable", func(t *testing.T) {
		if findings := checkCapabilities(t.TempDir(), &clientconfig.Config{ServerURL: "http://127.0.0.1:1"}); len(findings) != 0 {
			t.Errorf("findings = %+v, want none", findings)
		}
	})

	t.Run("silent when the server advertises no capabilities", func(t *testing.T) {
		srv := newServer(`{"status":"ok","version":"0.8.3","kbs":[{"name":"kb"}]}`)
		defer srv.Close()
		if findings := checkCapabilities(t.TempDir(), &clientconfig.Config{ServerURL: srv.URL}); len(findings) != 0 {
			t.Errorf("findings = %+v, want none", findings)
		}
	})
}

// --- D170: residues from an unbound KB ---

func TestCheckUnboundResidues(t *testing.T) {
	dir := t.TempDir()
	managed := []provisioning.ManagedFile{
		{Kind: "skill", Name: "kept", Path: "a", ContentHash: "h", Source: "kb:bound"},
		{Kind: "skill", Name: "stale", Path: "b", ContentHash: "h", Source: "kb:gone"},
		{Kind: "skill", Name: "bundled", Path: "c", ContentHash: "h", Source: "bundle"},
		{Kind: "skill", Name: "legacy", Path: "d", ContentHash: "h"}, // pre-D170 lockfile
	}
	lockFile := provisioning.LockFile{Providers: map[string]provisioning.Lock{
		"claude": {Provider: "claude", Managed: managed},
	}}

	t.Run("explicit binding reports only the unbound source", func(t *testing.T) {
		cfg := &clientconfig.Config{Agents: []string{"claude"}, KnownKBs: []string{"bound"},
			Clients: map[string]clientconfig.ClientBinding{"claude": {KBs: []string{"bound"}}}}
		findings := checkUnboundResidues(dir, cfg, []string{"claude"}, lockFile)
		if len(findings) != 1 {
			t.Fatalf("findings = %+v, want exactly the kb:gone one", findings)
		}
		if !strings.Contains(findings[0].Message, "kb:gone") {
			t.Errorf("finding = %q, want it to name kb:gone", findings[0].Message)
		}
		if !strings.Contains(findings[0].Fix, "--client claude") {
			t.Errorf("fix = %q, want the per-client sync", findings[0].Fix)
		}
	})

	t.Run("a provider on the default binding is never a residue", func(t *testing.T) {
		cfg := &clientconfig.Config{Agents: []string{"claude"}, KnownKBs: []string{"bound"}}
		if findings := checkUnboundResidues(dir, cfg, []string{"claude"}, lockFile); len(findings) != 0 {
			t.Errorf("findings = %+v, want none for a default binding", findings)
		}
	})
}

// TestNoLocalServiceFor pins the single combination that is actually broken
// (D174): the client points at this machine and no local service exists to
// answer. The three neighbouring combinations are legitimate.
func TestNoLocalServiceFor(t *testing.T) {
	cases := []struct {
		name      string
		url       string
		installed bool
		statusErr error
		want      bool
	}{
		{"loopback with no local service", "http://127.0.0.1:39273/mcp", false, nil, true},
		{"loopback with a local service", "http://127.0.0.1:39273/mcp", true, nil, false},
		{"remote server, no local service", "https://cartographer.example.com/mcp", false, nil, false},
		{"service state unknown", "http://localhost:39273/mcp", false, errors.New("unsupported platform"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := statusServiceFn
			statusServiceFn = func() (service.Status, error) {
				return service.Status{Installed: tc.installed}, tc.statusErr
			}
			t.Cleanup(func() { statusServiceFn = orig })
			if got := noLocalServiceFor(tc.url); got != tc.want {
				t.Errorf("noLocalServiceFor(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

// TestCheckOrphanedFiles: a file inside a managed directory that no lock entry
// accounts for is reported, never deleted — doctor cannot prove Cartographer
// wrote it, and a user may legitimately have added it (D178).
func TestCheckOrphanedFiles(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".claude", "skills", "alpha")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SKILL.md", "leftover.md"} {
		if err := os.WriteFile(filepath.Join(skillDir, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lockFile := provisioning.LockFile{Providers: map[string]provisioning.Lock{
		"claude": {Provider: "claude", Managed: []provisioning.ManagedFile{
			{Kind: "skill", Name: "alpha", Path: filepath.Join(".claude", "skills", "alpha", "SKILL.md")},
		}},
	}}

	findings := checkOrphanedFiles(dir, []string{"claude"}, lockFile)
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(findings), findings)
	}
	if !strings.Contains(findings[0].Message, "leftover.md") || !strings.Contains(findings[0].Message, "alpha") {
		t.Errorf("the finding must name the artifact and the path: %q", findings[0].Message)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "leftover.md")); err != nil {
		t.Errorf("doctor deleted the file it was only supposed to report: %v", err)
	}

	// Everything accounted for: no finding.
	lockFile.Providers["claude"] = provisioning.Lock{Provider: "claude", Managed: []provisioning.ManagedFile{
		{Kind: "skill", Name: "alpha", Path: filepath.Join(".claude", "skills", "alpha", "SKILL.md")},
		{Kind: "skill", Name: "alpha", Path: filepath.Join(".claude", "skills", "alpha", "leftover.md")},
	}}
	if got := checkOrphanedFiles(dir, []string{"claude"}, lockFile); len(got) != 0 {
		t.Errorf("a fully accounted directory produced findings: %+v", got)
	}
}

// --- shadowed instructions (D189) ---

// withManagedInstructions puts the fixture in the state the shadowing check
// cares about: a well-formed managed block in the provider's instructions file,
// recorded in the lockfile. doctorFixture's stub server serves an empty
// manifest, so connect materializes no instructions on its own.
func withManagedInstructions(t *testing.T, dir, provider string) string {
	t.Helper()
	rel := provisioning.InstructionsFile(configurator.Provider(provider))
	if rel == "" {
		t.Fatalf("%s has no instructions destination", provider)
	}
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	block := "<!-- cartographer:instructions:begin -->\nx\n<!-- cartographer:instructions:end -->\n"
	if err := os.WriteFile(path, []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}
	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	lock := lf.ForProvider(provider)
	lock.Managed = append(lock.Managed, provisioning.ManagedFile{
		Kind: "instructions", Name: "alpha", Path: rel, ContentHash: "h1",
	})
	if lf.Providers == nil {
		lf.Providers = map[string]provisioning.Lock{}
	}
	lf.Providers[provider] = lock
	if err := provisioning.WriteLockFile(lockFilePath(dir), lf); err != nil {
		t.Fatalf("WriteLockFile: %v", err)
	}
	return path
}

// An instructions block written correctly into a file the provider does not
// read is not installed. Codex resolves the global scope to AGENTS.override.md
// when it exists and does not merge it with AGENTS.md, so the managed block
// never reaches the model — while every other check reports a healthy tree.
func TestRunDoctor_ShadowedInstructions(t *testing.T) {
	doctorStubs(t, []string{"codex"}, false)
	dir := doctorFixture(t, "codex")
	withManagedInstructions(t, dir, "codex")

	if f := findingsFor(runDoctor(dir, ""), "instructions"); len(f) != 0 {
		t.Fatalf("fixture must start clean, got %d instructions finding(s): %+v", len(f), f)
	}

	override := filepath.Join(dir, ".codex", "AGENTS.override.md")
	if err := os.WriteFile(override, []byte("# My own rules\n"), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}

	report := runDoctor(dir, "")
	findings := findingsFor(report, "instructions")
	if len(findings) != 1 {
		t.Fatalf("expected exactly one instructions finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Severity != doctorError {
		t.Errorf("severity = %v; want error: the operator's stated intent is not met", f.Severity)
	}
	if !strings.Contains(f.Message, "AGENTS.override.md") || !strings.Contains(f.Message, "AGENTS.md") {
		t.Errorf("message must name both files, got: %s", f.Message)
	}
	if f.Fix == "" {
		t.Error("finding must name a way out")
	}
	if doctorExitCode(report) == 0 {
		t.Error("an error-severity finding must produce a non-zero exit code")
	}
}

// An empty override still shadows: Codex reads it and finds nothing.
func TestRunDoctor_ShadowedInstructions_EmptyOverride(t *testing.T) {
	doctorStubs(t, []string{"codex"}, false)
	dir := doctorFixture(t, "codex")
	withManagedInstructions(t, dir, "codex")

	if err := os.WriteFile(filepath.Join(dir, ".codex", "AGENTS.override.md"), nil, 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}
	if f := findingsFor(runDoctor(dir, ""), "instructions"); len(f) != 1 {
		t.Fatalf("an empty override still shadows, got %d finding(s): %+v", len(f), f)
	}
}

// The managed file absent *and* shadowed is one finding, not two: the absence
// wins, because that is the one the operator must fix first.
func TestRunDoctor_ShadowedInstructions_AbsentManagedFileWins(t *testing.T) {
	doctorStubs(t, []string{"codex"}, false)
	dir := doctorFixture(t, "codex")
	managed := withManagedInstructions(t, dir, "codex")

	if err := os.WriteFile(filepath.Join(dir, ".codex", "AGENTS.override.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}
	if err := os.Remove(managed); err != nil {
		t.Fatalf("remove managed file: %v", err)
	}
	findings := findingsFor(runDoctor(dir, ""), "instructions")
	if len(findings) != 1 {
		t.Fatalf("expected exactly one finding, got %d: %+v", len(findings), findings)
	}
	if strings.Contains(findings[0].Message, "takes precedence") {
		t.Errorf("the absence must win over the shadowing: %s", findings[0].Message)
	}
}

// A provider with no declared precedence chain is unaffected, even with a
// same-named file sitting next to its instructions.
func TestRunDoctor_NoPrecedenceChain_NoFinding(t *testing.T) {
	doctorStubs(t, []string{"claude"}, false)
	dir := doctorFixture(t, "claude")
	withManagedInstructions(t, dir, "claude")

	if err := os.WriteFile(filepath.Join(dir, ".claude", "CLAUDE.override.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if f := findingsFor(runDoctor(dir, ""), "instructions"); len(f) != 0 {
		t.Fatalf("claude declares no chain: expected no finding, got %+v", f)
	}
}
