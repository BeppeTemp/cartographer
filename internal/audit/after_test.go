package audit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// allHashes reads every retained entry of the chain in order, through EventsAfter.
func allHashes(t *testing.T, l *Log, cursor string, limit int) ([]string, bool) {
	t.Helper()
	var out []string
	gapSeen := false
	for {
		es, gap, err := l.EventsAfter(cursor, limit)
		if err != nil {
			t.Fatal(err)
		}
		gapSeen = gapSeen || gap
		if len(es) == 0 {
			return out, gapSeen
		}
		for _, e := range es {
			out = append(out, e.Hash)
		}
		cursor = es[len(es)-1].Hash
	}
}

func TestEventsAfter_ActiveFile(t *testing.T) {
	l, _ := tempLog(t)
	for i := 0; i < 3; i++ {
		if err := appendOne(l, "tool"); err != nil {
			t.Fatal(err)
		}
	}
	es, gap, err := l.EventsAfter("", 100)
	if err != nil || gap || len(es) != 6 || es[0].PrevHash != "genesis" {
		t.Fatalf("from the start: %d events, gap %v, %v", len(es), gap, err)
	}
	rest, gap, err := l.EventsAfter(es[1].Hash, 2)
	if err != nil || gap || len(rest) != 2 || rest[0].Hash != es[2].Hash {
		t.Fatalf("after the second: %+v gap %v %v", rest, gap, err)
	}
	if tail, _, _ := l.EventsAfter(es[5].Hash, 10); len(tail) != 0 {
		t.Fatalf("after the last: %d events", len(tail))
	}
	if none, _, _ := l.EventsAfter("", 0); none != nil {
		t.Fatal("limit 0 returned events")
	}
	// A hash this log never wrote is a gap: replay from the oldest entry.
	es2, gap, err := l.EventsAfter("not-a-hash", 100)
	if err != nil || !gap || len(es2) != 6 {
		t.Fatalf("unknown cursor: %d events, gap %v, %v", len(es2), gap, err)
	}
}

// Across rotated segments the walk is the chain, with no duplicate and no loss.
func TestEventsAfter_AcrossSegments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	l, err := OpenWithOptions(path, Options{MaxBytes: 600, ArchiveDir: filepath.Join(dir, "archive")})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for i := 0; i < 10; i++ {
		if err := appendOne(l, "tool"); err != nil {
			t.Fatal(err)
		}
	}
	if len(l.index.Segments) < 2 {
		t.Fatalf("want rotations, got %d segments", len(l.index.Segments))
	}
	for _, limit := range []int{1, 3, 100} {
		got, gap := allHashes(t, l, "", limit)
		if gap || len(got) != 20 {
			t.Fatalf("limit %d: %d entries, gap %v", limit, len(got), gap)
		}
		seen := map[string]bool{}
		for _, h := range got {
			if seen[h] {
				t.Fatalf("limit %d: duplicate %s", limit, h)
			}
			seen[h] = true
		}
	}
	// The cursor at a segment's boundary continues in the next one.
	first := l.index.Segments[0]
	es, gap, err := l.EventsAfter(first.LastHash, 1)
	if err != nil || gap || len(es) != 1 || es[0].PrevHash != first.LastHash {
		t.Fatalf("after a boundary: %+v gap %v %v", es, gap, err)
	}
}

// A segment deleted by retention is a gap; a cursor inside it replays from the
// oldest retained entry.
func TestEventsAfter_GapAfterRetention(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	archive := filepath.Join(dir, "archive")
	l, err := OpenWithOptions(path, Options{MaxBytes: 600, ArchiveDir: archive})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for i := 0; i < 10; i++ {
		if err := appendOne(l, "tool"); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := allHashes(t, l, "", 100)
	gone := l.index.Segments[0]
	if err := os.Remove(filepath.Join(archive, gone.Name)); err != nil {
		t.Fatal(err)
	}
	// A cursor inside the deleted segment.
	es, gap, err := l.EventsAfter(gone.FirstHash, 1000)
	if err != nil || !gap || len(es) != len(all)-gone.Count || es[0].PrevHash != gone.LastHash {
		t.Fatalf("cursor in a deleted segment: %d events (want %d), gap %v, %v", len(es), len(all)-gone.Count, gap, err)
	}
	// A cursor before a deleted segment: what follows it is gone.
	l2Dir := t.TempDir()
	l2, err := OpenWithOptions(filepath.Join(l2Dir, "audit.jsonl"), Options{MaxBytes: 600, ArchiveDir: filepath.Join(l2Dir, "archive")})
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	for i := 0; i < 10; i++ {
		if err := appendOne(l2, "tool"); err != nil {
			t.Fatal(err)
		}
	}
	firstEntries, _, _ := l2.EventsAfter("", 1)
	if err := os.Remove(filepath.Join(l2Dir, "archive", l2.index.Segments[1].Name)); err != nil {
		t.Fatal(err)
	}
	if _, gap, err := l2.EventsAfter(firstEntries[0].Hash, 1000); err != nil || !gap {
		t.Fatalf("cursor before a deleted segment: gap %v, %v", gap, err)
	}
}

// EventsAfter reads while appends run, and the chain still verifies.
func TestEventsAfter_ConcurrentWithAppendsAndWake(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	l, err := OpenWithKeyAndOptions(path, kp, Options{MaxBytes: 2000, ArchiveDir: filepath.Join(dir, "archive")})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	wake := l.Wake()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if err := appendOne(l, "tool"); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	seen := map[string]bool{}
	cursor := ""
	for done := false; !done; {
		es, _, err := l.EventsAfter(cursor, 7)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range es {
			if seen[e.Hash] {
				t.Fatalf("duplicate %s", e.Hash)
			}
			if cursor != "" && e.PrevHash != cursor {
				t.Fatalf("out of order: %s after %s", e.PrevHash, cursor)
			}
			seen[e.Hash], cursor = true, e.Hash
		}
		if len(seen) == 100 {
			done = true
		} else if len(es) == 0 {
			<-wake
		}
	}
	wg.Wait()
	if _, err := VerifyAudit(path, filepath.Join(dir, "archive"), kp.Public, VerifyOptions{}); err != nil && !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("verify: %v", err)
	}
}

// A snapshot size that no longer ends a line (a rotation replaced the active
// file between the snapshot and the read) is a changed segment, read again,
// never a parse error.
func TestReadPrefix_ACutLineIsAChangedSegment(t *testing.T) {
	l, path := tempLog(t)
	appendN := func(n int) {
		for i := 0; i < n; i++ {
			if err := appendOne(l, "tool"); err != nil {
				t.Fatal(err)
			}
		}
	}
	appendN(2)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readPrefix(path, fi.Size()); err != nil {
		t.Fatalf("whole lines: %v", err)
	}
	if _, err := readPrefix(path, fi.Size()-5); !errors.Is(err, errSegmentChanged) {
		t.Fatalf("a cut line: %v", err)
	}
	if _, err := readPrefix(path, fi.Size()+100); !errors.Is(err, errSegmentChanged) {
		t.Fatalf("a shorter file: %v", err)
	}
}

// The first hash of a deleted segment places the cursor at that segment's end.
func TestEventsAfter_FirstHashOfADeletedSegment(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "archive")
	l, err := OpenWithOptions(filepath.Join(dir, "audit.jsonl"), Options{MaxBytes: 600, ArchiveDir: archive})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for i := 0; i < 10; i++ {
		if err := appendOne(l, "tool"); err != nil {
			t.Fatal(err)
		}
	}
	gone := l.index.Segments[1]
	if err := os.Remove(filepath.Join(archive, gone.Name)); err != nil {
		t.Fatal(err)
	}
	es, gap, err := l.EventsAfter(gone.FirstHash, 1)
	if err != nil || !gap || len(es) != 1 || es[0].PrevHash != gone.LastHash {
		t.Fatalf("got %+v, gap %v, %v", es, gap, err)
	}
}

// A broken active file (no rotation explains it) fails with an error naming
// the file once the retries run out.
func TestEventsAfter_BrokenActiveFileIsNamed(t *testing.T) {
	l, path := tempLog(t)
	if err := appendOne(l, "tool"); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{not json}\n")
	f.Close()
	if _, _, err := l.EventsAfter("", 10); err == nil || !strings.Contains(err.Error(), "parse "+path) {
		t.Fatalf("got %v", err)
	}
}
