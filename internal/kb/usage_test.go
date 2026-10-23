package kb

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func usageTestKB(t *testing.T) *KB {
	t.Helper()
	k := newTestKB(t)
	return k
}

func TestMergeUsage_NewerWinsPerProvider(t *testing.T) {
	t.Parallel()
	k := usageTestKB(t)
	now := time.Now().UTC().Truncate(time.Second)
	old := UsageEntry{Name: "ops-tool", Kind: "skill", Provider: "claude", LastUsed: now.Add(-48 * time.Hour), Count: 5}
	newer := UsageEntry{Name: "ops-tool", Kind: "skill", Provider: "claude", LastUsed: now.Add(-time.Hour), Count: 2}
	other := UsageEntry{Name: "ops-tool", Kind: "skill", Provider: "kiro", LastUsed: now.Add(-72 * time.Hour), Count: 1}
	if n, err := k.MergeUsage([]UsageEntry{old, other}); err != nil || n != 2 {
		t.Fatalf("first merge: %d, %v", n, err)
	}
	if n, _ := k.MergeUsage([]UsageEntry{newer}); n != 1 {
		t.Fatalf("a newer sighting must change the store, changed %d", n)
	}
	if n, _ := k.MergeUsage([]UsageEntry{old}); n != 0 {
		t.Fatalf("an older sighting must not win, changed %d", n)
	}
	got, err := k.LoadUsage()
	if err != nil || len(got) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	// Count is replaced, not summed.
	sum := SummarizeUsage(got)[UsageKey("skill", "ops-tool")]
	if sum.Provider != "claude" || !sum.LastUsed.Equal(newer.LastUsed) || sum.Count != 3 {
		t.Fatalf("summary = %+v, want claude, the newer time, 2+1 activations", sum)
	}
}

func TestMergeUsage_DropsNothingWorthKeepingOnly(t *testing.T) {
	t.Parallel()
	k := usageTestKB(t)
	n, err := k.MergeUsage([]UsageEntry{
		{Name: "bundled", Kind: "skill", Provider: "claude", Source: "bundle", LastUsed: time.Now(), Count: 1},
		{Name: "never", Kind: "skill", Provider: "claude"},
		{Kind: "skill", Provider: "claude", LastUsed: time.Now()},
	})
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v, want nothing stored", n, err)
	}
	if _, statErr := os.Stat(k.usageFilePath()); statErr == nil {
		t.Error("an empty merge must not create the store")
	}
}

func TestSummarizeUsage_ActivationBeatsCatalogue(t *testing.T) {
	t.Parallel()
	now := time.Now()
	sum := SummarizeUsage([]UsageEntry{
		{Name: "a", Kind: "skill", Provider: "claude", LastUsed: now.Add(-30 * 24 * time.Hour), Count: 3},
		{Name: "a", Kind: "skill", Provider: "codex", LastUsed: now.Add(-time.Hour), Count: 0},
		{Name: "b", Kind: "skill", Provider: "codex", LastUsed: now.Add(-time.Hour), Count: 0},
	})
	if s := sum[UsageKey("skill", "a")]; s.Provider != "claude" || s.Count != 3 {
		t.Errorf("a = %+v: a recent catalogue load must not hide an older real activation", s)
	}
	if s := sum[UsageKey("skill", "b")]; s.Count != 0 || s.Provider != "codex" {
		t.Errorf("b = %+v", s)
	}
}

func TestMergeUsage_RefusesPastTheFileCap(t *testing.T) {
	t.Parallel()
	k := usageTestKB(t)
	big := make([]UsageEntry, 0, 40000)
	for i := 0; i < 40000; i++ {
		big = append(big, UsageEntry{Name: fmt.Sprintf("skill-%06d-%s", i, strings.Repeat("x", 40)), Kind: "skill", Provider: "p", LastUsed: time.Now()})
	}
	if _, err := k.MergeUsage(big); !errors.Is(err, ErrUsageTooLarge) {
		t.Fatalf("err = %v, want ErrUsageTooLarge", err)
	}
	if got, _ := k.LoadUsage(); len(got) != 0 {
		t.Fatalf("a refused merge wrote %d entries", len(got))
	}
}
