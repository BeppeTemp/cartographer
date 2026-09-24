package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

func missFixture(t *testing.T) (*kb.KB, *Server, *searchMissLog) {
	t.Helper()
	k := setupTestKB(t)
	k.AuthName = "docs"
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return k, s, newSearchMissLog(k)
}

func missLines(t *testing.T, l *searchMissLog) []searchMiss {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	entries, err := l.readLocked()
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func fixClock(t *testing.T, at time.Time) {
	t.Helper()
	saved := searchMissNow
	searchMissNow = func() time.Time { return at }
	t.Cleanup(func() { searchMissNow = saved })
}

// D247: only a whole-KB miss is recorded — not a hit, not a narrowed miss.
func TestSearchMiss_RecordsOnlyWholeKBMisses(t *testing.T) {
	_, s, l := missFixture(t)
	searchText(t, s, "runbook")         // hit
	searchText(t, s, "  Attività   X ") // whole-KB miss
	narrowed := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"manutenzione"}}}})
	if tr := s.callTool(narrowed, "search", json.RawMessage(`{"query":"secretnarrowquery"}`)); tr.IsError {
		t.Fatalf("narrowed search: %+v", tr.Content)
	}
	s.callTool(authLocalContext(), "search", json.RawMessage(`{"query":""}`)) // rejected
	got := missLines(t, l)
	if len(got) != 1 || got[0].Query != "attivita x" {
		t.Fatalf("recorded = %+v, want only the normalised whole-KB miss", got)
	}
}

func TestSearchMiss_NormalisationMerges(t *testing.T) {
	for _, q := range []string{"Attività  X", "attivita x", "  ATTIVITÀ\tx "} {
		if got := normalizeMissQuery(q); got != "attivita x" {
			t.Errorf("normalizeMissQuery(%q) = %q", q, got)
		}
	}
}

func TestSearchMiss_TrimmedPastMax(t *testing.T) {
	k, _, l := missFixture(t)
	if err := os.MkdirAll(filepath.Join(k.Root, ".cartographer"), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < searchMissMax; i++ {
		fmt.Fprintf(&b, `{"at":"2026-01-01T00:00:00Z","query":"q%d"}`+"\n", i)
	}
	if err := os.WriteFile(l.path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	l.record("newest")
	got := missLines(t, l)
	if len(got) != searchMissKeep || got[len(got)-1].Query != "newest" {
		t.Fatalf("after trim: %d lines, last %+v", len(got), got[len(got)-1])
	}
}

// A log that cannot be written never fails the search.
func TestSearchMiss_UnwritableLogDoesNotFailSearch(t *testing.T) {
	k, s, _ := missFixture(t)
	if err := os.MkdirAll(filepath.Join(k.Root, ".cartographer", searchMissFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if out := searchText(t, s, "nothingmatches"); !strings.Contains(out, `"count": 0`) {
		t.Fatalf("search = %s", out)
	}
}

func TestKBStatus_SearchMisses(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	_, s, l := missFixture(t)
	status := func() map[string]json.RawMessage {
		t.Helper()
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(callOK(t, s, "kb_status", `{}`)), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if _, ok := status()["search_misses"]; ok {
		t.Fatal("search_misses present with an empty log")
	}

	record := func(at time.Time, q string) {
		fixClock(t, at)
		l.record(q)
	}
	record(now.Add(-40*24*time.Hour), "ancient") // outside the window
	record(now.Add(-40*24*time.Hour), "beta")    // outside: does not count
	for i := 0; i < 3; i++ {
		record(now.Add(-time.Duration(i)*time.Hour), "alpha")
	}
	record(now.Add(-2*time.Hour), "beta")
	record(now.Add(-1*time.Hour), "beta")
	record(now.Add(-5*time.Hour), "gamma")
	record(now.Add(-5*time.Hour), "delta")
	for i := 0; i < 12; i++ {
		record(now.Add(-10*time.Hour), fmt.Sprintf("filler%02d", i))
	}
	fixClock(t, now)

	var top []searchMissSummary
	if err := json.Unmarshal(status()["search_misses"], &top); err != nil {
		t.Fatal(err)
	}
	if len(top) != searchMissTop {
		t.Fatalf("top has %d entries, want %d: %+v", len(top), searchMissTop, top)
	}
	want := []string{"alpha", "beta", "delta", "gamma", "filler00"}
	for i, q := range want {
		if top[i].Query != q {
			t.Fatalf("top[%d] = %+v, want %q (full: %+v)", i, top[i], q, top)
		}
	}
	if top[0].Count != 3 || top[1].Count != 2 || top[0].LastSeen != now.Format(time.RFC3339) {
		t.Fatalf("counts/last_seen wrong: %+v", top[:2])
	}
	for _, e := range top {
		if e.Query == "ancient" {
			t.Fatal("a miss outside the 30-day window was reported")
		}
	}
}
