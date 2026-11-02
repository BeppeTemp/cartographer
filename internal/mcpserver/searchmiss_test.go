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
	t.Parallel()
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
	t.Parallel()
	for _, q := range []string{"Attività  X", "attivita x", "  ATTIVITÀ\tx "} {
		if got := normalizeMissQuery(q); got != "attivita x" {
			t.Errorf("normalizeMissQuery(%q) = %q", q, got)
		}
	}
}

func TestSearchMiss_TrimmedPastMax(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	k, s, _ := missFixture(t)
	if err := os.MkdirAll(filepath.Join(k.Root, ".cartographer", searchMissFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if out := searchText(t, s, "nothingmatches"); !strings.Contains(out, `"count":0`) {
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

// D319: a miss whose query now has results is marked resolved and sorted
// after every open one; with no checker, top() behaves as before.
func checkedTop(t *testing.T, checker func(string) bool, queries ...string) []searchMissSummary {
	t.Helper()
	_, _, l := missFixture(t)
	for _, q := range queries {
		l.record(q)
	}
	l.SetChecker(checker)
	return l.top()
}

func findMiss(top []searchMissSummary, q string) (searchMissSummary, bool) {
	for _, e := range top {
		if e.Query == q {
			return e, true
		}
	}
	return searchMissSummary{}, false
}

func TestSearchMiss_AutoEvictsResolvedMisses(t *testing.T) {
	t.Parallel()
	top := checkedTop(t, func(q string) bool { return q == "kafka" }, "kafka", "kafka", "zephyr")
	e, ok := findMiss(top, "kafka")
	if !ok || !e.Resolved {
		t.Fatalf("kafka = %+v (present %v), want resolved", e, ok)
	}
	// Resolved sorts last even with the higher count.
	if top[len(top)-1].Query != "kafka" || top[0].Query != "zephyr" {
		t.Fatalf("order = %+v, want open before resolved", top)
	}
}

func TestSearchMiss_KeepsUnresolvedMisses(t *testing.T) {
	t.Parallel()
	top := checkedTop(t, func(string) bool { return false }, "zephyr")
	if e, ok := findMiss(top, "zephyr"); !ok || e.Resolved {
		t.Fatalf("zephyr = %+v (present %v), want open", e, ok)
	}
}

func TestSearchMiss_NilCheckerSkipsEviction(t *testing.T) {
	t.Parallel()
	top := checkedTop(t, nil, "zephyr")
	if e, ok := findMiss(top, "zephyr"); !ok || e.Resolved {
		t.Fatalf("zephyr = %+v (present %v), want open", e, ok)
	}
}

// The re-check stops once searchMissTop open queries are found, but resolved
// ones never crowd an open one out of the report.
func TestSearchMiss_ResolvedNeverDisplaceOpen(t *testing.T) {
	t.Parallel()
	_, _, l := missFixture(t)
	for i := 0; i < searchMissTop+3; i++ {
		q := fmt.Sprintf("q%02d", i)
		for j := 0; j <= searchMissTop+3-i; j++ { // higher index, lower count
			l.record(q)
		}
	}
	checked := 0
	l.SetChecker(func(q string) bool { checked++; return q < "q03" })
	top := l.top()
	if len(top) != searchMissTop {
		t.Fatalf("top has %d entries", len(top))
	}
	for _, e := range top {
		if e.Resolved {
			t.Fatalf("a resolved entry displaced an open one: %+v", top)
		}
	}
	if checked != searchMissTop+3 {
		t.Fatalf("checked %d queries, want %d", checked, searchMissTop+3)
	}
}

func TestKBStatus_SearchMissesResolvedAreGone(t *testing.T) {
	t.Parallel()
	k, s, _ := missFixture(t)
	searchText(t, s, "reconcileunique") // miss: the concept does not exist yet
	searchText(t, s, "zephyrnothing")   // miss that stays open
	writeOutOfBand(t, k)                // fills the gap, outside any tool
	var top []searchMissSummary
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(callOK(t, s, "kb_status", `{}`)), &m); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(m["search_misses"], &top); err != nil {
		t.Fatal(err)
	}
	if e, ok := findMiss(top, "reconcileunique"); !ok || !e.Resolved {
		t.Fatalf("filled gap = %+v (present %v), want resolved: %+v", e, ok, top)
	}
	if e, ok := findMiss(top, "zephyrnothing"); !ok || e.Resolved {
		t.Fatalf("open gap = %+v (present %v): %+v", e, ok, top)
	}
	if !strings.Contains(string(m["search_misses"]), `"resolved":true`) || strings.Count(string(m["search_misses"]), `"resolved"`) != 1 {
		t.Fatalf("resolved must be emitted only when true: %s", m["search_misses"])
	}
}

// D319: record_miss: false keeps a verification probe out of the log.
func assertRecorded(t *testing.T, args string, want int) {
	t.Helper()
	_, s, l := missFixture(t)
	callOK(t, s, "search", args)
	if got := missLines(t, l); len(got) != want {
		t.Fatalf("recorded %d entries, want %d: %+v", len(got), want, got)
	}
}

func TestSearchMiss_RecordMissFalseSuppresses(t *testing.T) {
	t.Parallel()
	assertRecorded(t, `{"query":"nothingmatches","record_miss":false}`, 0)
}

func TestSearchMiss_RecordMissDefaultRecords(t *testing.T) {
	t.Parallel()
	assertRecorded(t, `{"query":"nothingmatches"}`, 1)
}

func TestSearchMiss_RecordMissTrueRecords(t *testing.T) {
	t.Parallel()
	assertRecorded(t, `{"query":"nothingmatches","record_miss":true}`, 1)
}

// A query far over the reader's line limit is cut when recorded: the log stays
// readable and the earlier misses are still reported.
func TestSearchMiss_LongQueryIsTruncated(t *testing.T) {
	t.Parallel()
	_, _, l := missFixture(t)
	l.record("earlier miss")
	l.record(strings.Repeat("è", 100*1024))
	got := missLines(t, l)
	if len(got) != 2 || got[0].Query != "earlier miss" {
		t.Fatalf("entries = %d, want 2 with the earlier one first", len(got))
	}
	if n := len([]rune(got[1].Query)); n != searchMissQueryRunes {
		t.Fatalf("stored query has %d runes, want %d", n, searchMissQueryRunes)
	}
	if top := l.top(); len(top) != 2 {
		t.Fatalf("top = %+v", top)
	}
}

// A file already poisoned by an over-long line is read past it, and accepts an
// append after a restart.
func TestSearchMiss_PoisonedLogHeals(t *testing.T) {
	t.Parallel()
	k, _, l := missFixture(t)
	l.record("before")
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	huge, _ := json.Marshal(searchMiss{At: searchMissNow().UTC().Format(time.RFC3339), Query: strings.Repeat("x", 200*1024)})
	f.Write(append(huge, '\n'))
	f.Close()

	restarted := newSearchMissLog(k)
	restarted.record("after")
	got := missLines(t, restarted)
	if len(got) != 2 || got[0].Query != "before" || got[1].Query != "after" {
		t.Fatalf("entries = %+v", got)
	}
	if top := restarted.top(); len(top) != 2 {
		t.Fatalf("top = %+v", top)
	}
}

// A miss of a search that did not look at the whole KB is not a gap of the KB.
func TestSearchMiss_ScopedOrArchiveExcludedMissIsNotRecorded(t *testing.T) {
	t.Parallel()
	_, s, l := missFixture(t)
	for _, a := range []string{
		`{"query":"zzscopedmiss","scope":"manutenzione"}`,
		`{"query":"zzarchivemiss","include_archived":false}`,
	} {
		if tr := s.callTool(authLocalContext(), "search", json.RawMessage(a)); tr.IsError {
			t.Fatalf("%s: %+v", a, tr.Content)
		}
	}
	if got := missLines(t, l); len(got) != 0 {
		t.Fatalf("recorded = %+v, want none", got)
	}
	searchText(t, s, "zzwholemiss")
	if got := missLines(t, l); len(got) != 1 {
		t.Fatalf("a whole-KB miss must still be recorded, got %+v", got)
	}
}

// top() re-checks a bounded number of queries even when all are resolved.
func TestSearchMiss_TopBoundsItsRechecks(t *testing.T) {
	t.Parallel()
	_, _, l := missFixture(t)
	for i := 0; i < 200; i++ {
		l.record(fmt.Sprintf("query %d", i))
	}
	calls := 0
	l.SetChecker(func(string) bool { calls++; return true })
	l.top()
	if calls != searchMissMaxChecks {
		t.Fatalf("checker ran %d times, want %d", calls, searchMissMaxChecks)
	}
}
