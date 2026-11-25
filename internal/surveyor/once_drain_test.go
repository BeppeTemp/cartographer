package surveyor

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// floorKind mimics ingestInstance: Next returns the first claimable unit
// (a unit is claimable until Run consumes it); Run refuses, without claiming,
// when the grant is below the floor (ingest.go run, first lines).
type floorKind struct {
	mu           sync.Mutex
	claimable    []string // in listing order
	floor        int
	cost         int
	ran, refused []string
}

func (k *floorKind) Next(context.Context, string, ToolCaller) (*WorkItem, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.claimable) == 0 {
		return nil, nil
	}
	return &WorkItem{Key: k.claimable[0]}, nil
}

func (k *floorKind) Run(_ context.Context, it *WorkItem, rt Runtime) (Result, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if rt.Grant < k.floor {
		k.refused = append(k.refused, it.Key) // unit stays claimable
		return Result{Summary: "no unit claimed: the remaining budget is below the floor", Budget: true}, nil
	}
	k.ran = append(k.ran, it.Key)
	k.claimable = k.claimable[1:]
	return Result{Tokens: k.cost, Units: 1, Commits: 1, Summary: "did " + it.Key}, nil
}

func runFloor(t *testing.T, k *floorKind, daily, run int) (*runnerFixture, string) {
	t.Helper()
	nk := &noopKind{}
	f := newFixture(t, []string{"cloud"}, []*noopKind{nk}, func(c *Config, o *RunnerOptions) {
		c.Models = []Profile{{Name: "cloud", DailyTokens: daily, RunTokens: run}, testProfiles[1]}
	})
	f.r.instances[0] = k
	f.r.Run(context.Background(), true)
	return f, f.log.last()
}

// A grant above zero but below the floor: the unit stays claimable, so the run
// has not drained; it ends stopped=budget, never done, and says how many tasks ran.
func TestOnceFloorRefusalEndsAsBudget(t *testing.T) {
	k := &floorKind{claimable: []string{"u1", "u2", "u3"}, floor: 20000, cost: 15000}
	f, e := runFloor(t, k, 30000, 30000)
	if len(k.claimable) == 0 {
		t.Fatal("nothing left claimable: no repro")
	}
	if !strings.Contains(e, "stopped=budget") {
		t.Errorf("%d units still claimable, entry %q, want stopped=budget", len(k.claimable), e)
	}
	if strings.Contains(e, "stopped=done") {
		t.Errorf("entry %q says done with work left", e)
	}
	found := false
	for _, l := range f.logs {
		if strings.Contains(l, "run ended stopped=budget tasks=") {
			found = true
		}
	}
	if !found {
		t.Errorf("no exit line with the stop reason and the task count in %q", f.logs)
	}
}
