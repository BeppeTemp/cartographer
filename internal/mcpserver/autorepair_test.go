package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

func gitOut(t *testing.T, k *kb.KB, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", k.Root}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// autoRepairKB is repairKB with the heartbeat's settings on.
func autoRepairKB(t *testing.T, checks ...string) (*kb.KB, *Server) {
	t.Helper()
	k, s := repairKB(t, 3)
	k.AutoRepair = checks
	k.DoctorAutoIntervalDays = 1
	return k, s
}

func TestAutoRepairQuotaAppliesOnlyTheListedChecks(t *testing.T) {
	k, s := autoRepairKB(t, "nonstandard_field")
	before := commitCount(t, k)
	run := s.runAutoRepairQuota(context.Background())
	if len(run.Checks) != 1 || run.Checks[0].Applied != 3 || run.Checks[0].Commit == "" {
		t.Fatalf("run = %+v", run)
	}
	if commitCount(t, k) != before+1 {
		t.Fatalf("want one commit per check that changed something, got %d", commitCount(t, k)-before)
	}
	// One commit per run (D355): the subject names the run, the body one line per check.
	if got := gitOut(t, k, "log", "-1", "--format=%s"); got != autoRepairReason {
		t.Fatalf("subject = %q", got)
	}
	if got := gitOut(t, k, "log", "-1", "--format=%b"); !strings.Contains(got, "nonstandard_field: 3") {
		t.Fatalf("body = %q", got)
	}
	if got := gitOut(t, k, "log", "-1", "--format=%(trailers:key=Reason,valueonly)"); got != autoRepairReason {
		t.Fatalf("reason trailer = %q", got)
	}
	// It is not a doctor session (D290).
	if got := lastDoctorDate(k); got != "" {
		t.Fatalf("the heartbeat stamped the doctor marker: %q", got)
	}
	// The JSONL log records the run, and lives where git does not see it.
	last, ok := lastAutoRepairRun(k)
	if !ok || last.At == "" || len(last.Checks) != 1 || last.Checks[0].Check != "nonstandard_field" {
		t.Fatalf("log = %+v, %v", last, ok)
	}
	if st := gitOut(t, k, "status", "--short"); st != "" {
		t.Fatalf("the log leaked into git: %q", st)
	}
	// A second run finds nothing and commits nothing.
	n := commitCount(t, k)
	if run := s.runAutoRepairQuota(context.Background()); run.Checks[0].Applied != 0 || commitCount(t, k) != n {
		t.Fatalf("second run = %+v, commits +%d", run, commitCount(t, k)-n)
	}
}

func TestAutoRepairQuotaIsBoundedPerRun(t *testing.T) {
	k, s := repairKB(t, autoRepairLimit+7)
	k.AutoRepair = []string{"nonstandard_field"}
	run := s.runAutoRepairQuota(context.Background())
	if run.Checks[0].Applied != autoRepairLimit {
		t.Fatalf("applied = %d, want the limit %d", run.Checks[0].Applied, autoRepairLimit)
	}
	if run := s.runAutoRepairQuota(context.Background()); run.Checks[0].Applied != 7 {
		t.Fatalf("the rest goes in the next run, got %+v", run.Checks[0])
	}
}

func TestAutoRepairQuotaSkipsADegradedKB(t *testing.T) {
	k, s := autoRepairKB(t, "nonstandard_field")
	if err := os.MkdirAll(k.Root+"/.cartographer", 0o755); err != nil {
		t.Fatal(err)
	}
	conflicts := `[{"concept_id":"ops/n0","path":"ops/n0.md","detected_at":"2026-01-01T00:00:00Z"}]`
	if err := os.WriteFile(k.Root+"/.cartographer/conflicts.json", []byte(conflicts), 0o644); err != nil {
		t.Fatal(err)
	}
	if cs, _ := k.ListConflicts(); len(cs) != 1 {
		t.Fatalf("fixture: %v", cs)
	}
	before := commitCount(t, k)
	run := s.runAutoRepairQuota(context.Background())
	if run.Skipped == "" || len(run.Checks) != 0 || commitCount(t, k) != before {
		t.Fatalf("a KB with an open conflict was written: %+v", run)
	}
	if last, _ := lastAutoRepairRun(k); last.Skipped == "" {
		t.Fatalf("the skip is not in the log: %+v", last)
	}
}

func TestAutoRepairHeartbeatFiresAndStops(t *testing.T) {
	k, s := autoRepairKB(t, "nonstandard_field")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.autoRepairLoop(ctx, 20*time.Millisecond, 5*time.Millisecond); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if run, ok := lastAutoRepairRun(k); ok && len(run.Checks) == 1 && run.Checks[0].Applied == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the heartbeat never repaired the KB")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the heartbeat outlived its context")
	}
	// Nothing runs after the stop.
	n := commitCount(t, k)
	b, _ := os.ReadFile(autoRepairLogPath(k))
	time.Sleep(100 * time.Millisecond)
	if after, _ := os.ReadFile(autoRepairLogPath(k)); len(after) != len(b) || commitCount(t, k) != n {
		t.Fatal("a run started after cancellation")
	}
}

func TestStartAutoRepairIsOffUnlessConfigured(t *testing.T) {
	for name, set := range map[string]func(k *kb.KB){
		"interval zero": func(k *kb.KB) { k.DoctorAutoIntervalDays = 0 },
		"no checks":     func(k *kb.KB) { k.AutoRepair = nil },
	} {
		k, s := autoRepairKB(t, "nonstandard_field")
		set(k)
		ctx, cancel := context.WithCancel(context.Background())
		s.StartAutoRepair(ctx)
		cancel()
		time.Sleep(30 * time.Millisecond)
		if _, ok := lastAutoRepairRun(k); ok {
			t.Errorf("%s: the heartbeat ran", name)
		}
	}
}

func TestCapabilitiesReportAutoRepairDefaultAndInterval(t *testing.T) {
	k, _ := autoRepairKB(t, "nonstandard_field", "duplicate_link")
	k.AutoRepairDefault = true
	caps := kbCapabilities(k)
	if c := caps["auto_repair"]; c.State != "enabled" || !c.Default || len(c.Checks) != 2 {
		t.Fatalf("auto_repair = %+v", c)
	}
	if c := caps["doctor_auto_interval"]; c.State != "1 days" {
		t.Fatalf("doctor_auto_interval = %+v", c)
	}
	k.DoctorAutoIntervalDays = 0
	if c := kbCapabilities(k)["doctor_auto_interval"]; c.State != "disabled" {
		t.Fatalf("doctor_auto_interval = %+v", c)
	}
	raw, _ := json.Marshal(caps["auto_repair"])
	if !strings.Contains(string(raw), `"default":true`) {
		t.Fatalf("default is not on the wire: %s", raw)
	}
}
