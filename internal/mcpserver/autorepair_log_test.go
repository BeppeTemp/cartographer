package mcpserver

import (
	"os"
	"strings"
	"testing"
	"time"
)

// #1503 WP13: the run log survives an oversize line, is pruned at append, and
// a failure text is bounded.
func TestAutoRepairLog_OversizeLineDoesNotHideLaterRuns(t *testing.T) {
	t.Parallel()
	k, _ := autoRepairKB(t)
	now := time.Now().UTC()
	first := autoRepairRun{At: now.Add(-3 * time.Hour).Format(time.RFC3339)}
	if err := appendAutoRepairRun(k, first); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(autoRepairLogPath(k), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(strings.Repeat("x", 2<<20) + "\n")
	f.Close()
	later := autoRepairRun{At: now.Add(-time.Hour).Format(time.RFC3339), Skipped: "later"}
	if err := appendAutoRepairRun(k, later); err != nil {
		t.Fatal(err)
	}
	if last, ok := lastAutoRepairRun(k); !ok || last.Skipped != "later" {
		t.Fatalf("last run = %+v, %v: the oversize line froze it", last, ok)
	}
	if runs := autoRepairRunsSince(k, now.Add(-24*time.Hour), 10); len(runs) != 2 || runs[0].Skipped != "later" {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestAutoRepairLog_PrunesOldLinesAtAppend(t *testing.T) {
	t.Parallel()
	k, _ := autoRepairKB(t)
	now := time.Now().UTC()
	for _, age := range []time.Duration{120 * 24 * time.Hour, 91 * 24 * time.Hour, 89 * 24 * time.Hour, 24 * time.Hour} {
		if err := appendAutoRepairRun(k, autoRepairRun{At: now.Add(-age).Format(time.RFC3339), Skipped: age.String()}); err != nil {
			t.Fatal(err)
		}
	}
	// Each append prunes against its own timestamp: the oldest runs were
	// logged "in the past", so build the final state with one run from now.
	if err := appendAutoRepairRun(k, autoRepairRun{At: now.Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	runs := autoRepairRunsSince(k, now.Add(-200*24*time.Hour), 50)
	for _, r := range runs {
		at, _ := time.Parse(time.RFC3339, r.At)
		if now.Sub(at) > autoRepairLogRetention {
			t.Errorf("a run older than the retention survived: %+v", r)
		}
	}
	if len(runs) != 3 {
		t.Fatalf("runs = %d, want the 89d, 1d and current ones", len(runs))
	}
	// An empty run is still written.
	if last, ok := lastAutoRepairRun(k); !ok || last.At != now.Format(time.RFC3339) || len(last.Checks) != 0 {
		t.Fatalf("last = %+v", last)
	}
}

func TestTruncateFailure(t *testing.T) {
	t.Parallel()
	if got := truncateFailure("short"); got != "short" {
		t.Fatal(got)
	}
	long := strings.Repeat("é", autoRepairFailureMax)
	got := truncateFailure(long)
	if len(got) > autoRepairFailureMax+len("... (truncated)") || !strings.HasSuffix(got, "(truncated)") {
		t.Fatalf("len %d", len(got))
	}
	if strings.ContainsRune(strings.TrimSuffix(got, "... (truncated)"), '�') {
		t.Fatal("cut in the middle of a rune")
	}
}
