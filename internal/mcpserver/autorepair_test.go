package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
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

// autoRepairKB is repairKB with three pages the heartbeat has to repair.
func autoRepairKB(t *testing.T) (*kb.KB, *Server) {
	t.Helper()
	return repairKB(t, 3)
}

func TestAutoRepairQuotaAppliesEverySafeCheck(t *testing.T) {
	t.Parallel()
	k, s := autoRepairKB(t)
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
	if run := s.runAutoRepairQuota(context.Background()); len(run.Checks) != 0 || commitCount(t, k) != n {
		t.Fatalf("second run = %+v, commits +%d", run, commitCount(t, k)-n)
	}
}

func TestAutoRepairQuotaIsBoundedPerRun(t *testing.T) {
	t.Parallel()
	_, s := repairKB(t, autoRepairLimit+7)
	run := s.runAutoRepairQuota(context.Background())
	if run.Checks[0].Applied != autoRepairLimit {
		t.Fatalf("applied = %d, want the limit %d", run.Checks[0].Applied, autoRepairLimit)
	}
	if run := s.runAutoRepairQuota(context.Background()); run.Checks[0].Applied != 7 {
		t.Fatalf("the rest goes in the next run, got %+v", run.Checks[0])
	}
}

func TestAutoRepairQuotaSkipsADegradedKB(t *testing.T) {
	t.Parallel()
	k, s := autoRepairKB(t)
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
	t.Parallel()
	k, s := autoRepairKB(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.autoRepairLoop(ctx, 20*time.Millisecond, 5*time.Millisecond); close(done) }()
	deadline := time.Now().Add(gitxtest.Deadline(10 * time.Second))
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
	case <-time.After(gitxtest.Deadline(10 * time.Second)):
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

// D907: no setting turns the heartbeat off or moves it. The period is the
// constant, and starting it needs nothing from the KB's policy.
func TestStartAutoRepairNeedsNoSetting(t *testing.T) {
	t.Parallel()
	if autoRepairInterval != 24*time.Hour || kb.AutoRepairIntervalDays != 1 {
		t.Fatalf("the heartbeat period is %v (%d days), want the daily default of the removed doctor_auto_interval", autoRepairInterval, kb.AutoRepairIntervalDays)
	}
	_, s := autoRepairKB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartAutoRepair(ctx)
	if s.autoRepairTrigger == nil {
		t.Fatal("StartAutoRepair did not start the heartbeat on a KB with the zero policy")
	}
}

// D907: the removed doctor and repair keys are not capabilities any more; the
// gate and the safe repairs are not something a KB can report as off.
func TestCapabilitiesNoLongerListTheDoctorKeys(t *testing.T) {
	t.Parallel()
	k, _ := autoRepairKB(t)
	caps := kbCapabilities(k)
	for _, gone := range []string{"auto_repair", "repair_on_write", "write_gate", "doctor_interval", "doctor_mode", "doctor_budget", "doctor_auto_interval"} {
		if _, has := caps[gone]; has {
			t.Errorf("kb_status still reports the %s capability", gone)
		}
	}
	raw, _ := json.Marshal(caps)
	if strings.Contains(string(raw), "settings.yaml auto_repair") || strings.Contains(string(raw), "doctor_") {
		t.Errorf("capabilities still name a removed key: %s", raw)
	}
}
