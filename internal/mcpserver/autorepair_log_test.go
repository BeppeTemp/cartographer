package mcpserver

import (
	"os"
	"strings"
	"sync"
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

func TestAutoRepairLog_OversizeLineIsPrunedAndNoRunIsGlued(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	for name, tail := range map[string]string{"terminated": "\n", "last line unterminated": ""} {
		k, _ := autoRepairKB(t)
		if err := appendAutoRepairRun(k, autoRepairRun{At: now.Add(-2 * time.Hour).Format(time.RFC3339), Skipped: "first"}); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(autoRepairLogPath(k), os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(strings.Repeat("x", 2<<20) + tail)
		f.Close()
		if err := appendAutoRepairRun(k, autoRepairRun{At: now.Format(time.RFC3339), Skipped: "next"}); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(autoRepairLogPath(k))
		if len(raw) > 4096 {
			t.Errorf("%s: the oversize line was not pruned (%d bytes)", name, len(raw))
		}
		if last, _ := lastAutoRepairRun(k); last.Skipped != "next" {
			t.Errorf("%s: the next run was lost: %+v", name, last)
		}
		if runs := autoRepairRunsSince(k, now.Add(-24*time.Hour), 10); len(runs) != 2 {
			t.Errorf("%s: runs = %d, want 2", name, len(runs))
		}
		if fi, _ := os.Stat(autoRepairLogPath(k)); fi.Mode().Perm() != 0o644 {
			t.Errorf("%s: mode %v, want 0644", name, fi.Mode().Perm())
		}
	}
}

func TestAutoRepairLog_StaleTempIsCleaned(t *testing.T) {
	t.Parallel()
	k, _ := autoRepairKB(t)
	now := time.Now().UTC()
	appendAutoRepairRun(k, autoRepairRun{At: now.Add(-100 * 24 * time.Hour).Format(time.RFC3339)})
	stale := autoRepairLogPath(k) + ".123456"
	os.WriteFile(stale, []byte("junk"), 0o600)
	if err := appendAutoRepairRun(k, autoRepairRun{At: now.Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("a crash's temporary file survived the rewrite")
	}
}

// Concurrent writers of one log lose no run.
func TestAutoRepairLog_ConcurrentAppendsLoseNothing(t *testing.T) {
	t.Parallel()
	k, _ := autoRepairKB(t)
	now := time.Now().UTC()
	appendAutoRepairRun(k, autoRepairRun{At: now.Add(-100 * 24 * time.Hour).Format(time.RFC3339)})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			appendAutoRepairRun(k, autoRepairRun{At: now.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339)})
		}(i)
	}
	wg.Wait()
	if runs := autoRepairRunsSince(k, now.Add(-time.Hour), 50); len(runs) != 20 {
		t.Fatalf("runs = %d, want 20", len(runs))
	}
}
