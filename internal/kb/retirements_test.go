package kb

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRetirements_ReadsArchivesAndSupersedes(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	at := func(day int) time.Time { return time.Date(2026, 3, day, 9, 0, 0, 0, time.UTC) }
	// Oldest first: AppendLog puts each entry on top, as concept_archive does.
	for _, e := range []struct {
		text string
		ts   time.Time
	}{
		{"concept_archive: notes/old-plan -> archive/old-plan (deprecated):\nmoved notes/old-plan -> archive/old-plan", at(1)},
		{"concept_archive: notes/rota -> archive/rota (archived, closed, (see D12), nothing left):\nmoved", at(2)},
		{"supersede: infra/api-v1 → infra/api-v2", at(3)},
		{"[infra] added a note about the cluster", at(4)},
		{"concept_archive: notes/broken -> (archived", at(5)}, // malformed: skipped
		{"supersede: lonely", at(6)},                          // malformed: skipped
	} {
		if err := k.AppendLog(e.text, e.ts); err != nil {
			t.Fatalf("AppendLog: %v", err)
		}
	}

	got, err := k.Retirements()
	if err != nil {
		t.Fatalf("Retirements: %v", err)
	}
	want := []Retirement{
		{From: "infra/api-v1", To: "infra/api-v2", Status: "superseded", At: at(3)},
		{From: "notes/rota", To: "archive/rota", Status: "archived", Reason: "closed, (see D12), nothing left", At: at(2)},
		{From: "notes/old-plan", To: "archive/old-plan", Status: "deprecated", At: at(1)},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d retirements, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("retirement %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	// A supersede is about its source, which stays; an archive about its target.
	if got[0].Subject() != "infra/api-v1" || got[1].Subject() != "archive/rota" {
		t.Errorf("Subject: supersede %q, archive %q", got[0].Subject(), got[1].Subject())
	}
}

func TestRetirements_NoLogAndCacheFollowsTheFile(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	logPath := filepath.Join(k.DataRoot(), "log.md")
	if err := os.Remove(logPath); err != nil {
		t.Fatal(err)
	}
	got, err := k.Retirements()
	if err != nil || len(got) != 0 {
		t.Fatalf("no log: got %v, %v; want none", got, err)
	}

	if err := k.AppendLog("supersede: a/x → a/y", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	got, _ = k.Retirements()
	if len(got) != 1 {
		t.Fatalf("after one entry: %+v", got)
	}
	if err := k.AppendLog("supersede: a/z → a/y", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	got, _ = k.Retirements()
	if len(got) != 2 || got[0].From != "a/z" {
		t.Fatalf("a new entry must invalidate the cache, got %+v", got)
	}
}

func TestIsArchiveMap(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		kind     string
		top, ret int
		want     bool
	}{
		{"empty map", "", 0, 0, false},
		{"none retired", "", 4, 0, false},
		{"under half", "", 5, 2, false},
		{"exactly half", "", 4, 2, true},
		{"all retired", "", 3, 3, true},
		{"journal at half", "journal", 4, 2, false},
		{"journal all retired", "journal", 3, 3, false},
	} {
		if got := IsArchiveMap(MapContract{Kind: tc.kind}, tc.top, tc.ret); got != tc.want {
			t.Errorf("%s: IsArchiveMap(kind %q, top %d, retired %d) = %v, want %v", tc.name, tc.kind, tc.top, tc.ret, got, tc.want)
		}
	}
}

// Older versions logged a concept_archive reason unsanitised, so a reason with
// line breaks spread the title over several lines of the entry. Those entries
// still read back, with the reason on one line.
func TestRetirements_MultiLineReasonFromAnOlderLog(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	at := func(day int) time.Time { return time.Date(2026, 4, day, 9, 0, 0, 0, time.UTC) }
	for _, e := range []struct {
		text string
		ts   time.Time
	}{
		{"concept_archive: notes/a -> archive/a (archived, first line\nsecond line):\n- notes/a → archive/a", at(1)},
		{"concept_archive: notes/b -> archive/b (deprecated, never closes\n- notes/b → archive/b", at(2)},
	} {
		if err := k.AppendLog(e.text, e.ts); err != nil {
			t.Fatalf("AppendLog: %v", err)
		}
	}
	got, err := k.Retirements()
	if err != nil {
		t.Fatalf("Retirements: %v", err)
	}
	want := Retirement{From: "notes/a", To: "archive/a", Status: "archived", Reason: "first line second line", At: at(1)}
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %+v, want just %+v (an entry whose title never closes is skipped)", got, want)
	}
}
