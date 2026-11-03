package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kbtest"
)

func fixReadAccessClock(t *testing.T, at time.Time) *time.Time {
	t.Helper()
	now := at
	prev := readAccessNow
	readAccessNow = func() time.Time { return now }
	t.Cleanup(func() { readAccessNow = prev })
	return &now
}

// A session shorter than the flush interval writes nothing by itself; the
// shutdown flush (Server and MultiKBServer) is what keeps it (D1529).
func TestReadAccessIsFlushedAtShutdown(t *testing.T) {
	k := kbtest.New(t)
	touchConcepts(t, k, "ops/a", "ops/b")
	now := fixReadAccessClock(t, time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))

	s := New("test")
	RegisterKBTools(s, k, Deps{})
	multi := NewMultiKBServer("test")
	multi.servers["kb-a"] = s

	s.reads.record("ops/a")
	*now = now.Add(4 * time.Minute)
	s.reads.recordSurfaced("ops/b")
	if r, _, _ := newReadAccessLog(k).summary(); r.TrackedConcepts != 0 {
		t.Fatal("nothing should be on disk before the flush interval")
	}
	multi.FlushReadAccess()

	r, sf, _ := newReadAccessLog(k).summary()
	if r.TrackedConcepts != 1 || sf.TrackedConcepts != 1 {
		t.Fatalf("after shutdown: read %+v surfaced %+v", r, sf)
	}

	// A flush with nothing new is a no-op; the Server path works alone too.
	s.reads.record("ops/b")
	s.FlushReadAccess()
	if r, _, _ := newReadAccessLog(k).summary(); r.TrackedConcepts != 2 {
		t.Fatalf("Server.FlushReadAccess lost a read: %+v", r)
	}
	(&Server{}).FlushReadAccess() // a server without KB tools
}

// A migrated legacy file is dirty from the start and reaches the disk at
// shutdown even if no record ever arrives.
func TestReadAccessMigratedFileIsWrittenAtShutdown(t *testing.T) {
	k := kbtest.New(t)
	touchConcepts(t, k, "ops/a")
	fixReadAccessClock(t, time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
	path := filepath.Join(k.Root, ".cartographer", readAccessFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"ops/a":"2026-10-06T09:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	s.FlushReadAccess()
	data, _ := os.ReadFile(path)
	if string(data) == `{"ops/a":"2026-10-06T09:00:00Z"}` {
		t.Fatalf("the legacy file was not rewritten: %s", data)
	}
	if _, ok := newReadAccessLog(k).lastSurfacedTime("ops/a"); !ok {
		t.Fatal("migrated entry lost")
	}
}

// Ids of concepts that no longer exist are gone after the restart.
func TestReadAccessPrunesIdsOfDeletedConcepts(t *testing.T) {
	k := kbtest.New(t)
	touchConcepts(t, k, "ops/a", "ops/gone")
	fixReadAccessClock(t, time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
	l := newReadAccessLog(k)
	l.record("ops/a", "ops/gone")
	l.recordSurfaced("ops/gone")
	l.save()
	if err := os.Remove(filepath.Join(k.DataRoot(), "ops", "gone.md")); err != nil {
		t.Fatal(err)
	}
	again := newReadAccessLog(k)
	if _, ok := again.lastReadTime("ops/gone"); ok {
		t.Fatal("read of a deleted concept survived the restart")
	}
	if _, ok := again.lastSurfacedTime("ops/gone"); ok {
		t.Fatal("surfaced entry of a deleted concept survived the restart")
	}
	if _, ok := again.lastReadTime("ops/a"); !ok {
		t.Fatal("a live concept was pruned")
	}
	again.save() // pruning marks the log dirty: the shrunken file is written
	if r, _, _ := newReadAccessLog(k).summary(); r.TrackedConcepts != 1 {
		t.Fatalf("pruned file = %+v", r)
	}
}

// A data root that is empty or cannot be listed at boot (a volume not mounted
// yet) must not wipe the log: the prune is skipped (D1529).
func TestReadAccessPruneSkippedWhenNoConceptExists(t *testing.T) {
	for _, name := range []string{"empty", "missing"} {
		t.Run(name, func(t *testing.T) {
			k := kbtest.New(t)
			touchConcepts(t, k, "ops/a")
			fixReadAccessClock(t, time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
			l := newReadAccessLog(k)
			l.record("ops/a")
			l.recordSurfaced("ops/a")
			l.save()

			root := k.DataRoot()
			if name == "empty" {
				if err := os.Remove(filepath.Join(root, "ops", "a.md")); err != nil {
					t.Fatal(err)
				}
				entries, _ := os.ReadDir(root)
				for _, e := range entries {
					if !strings.HasPrefix(e.Name(), ".") {
						if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
							t.Fatal(err)
						}
					}
				}
			} else if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			again := newReadAccessLog(k)
			if _, ok := again.lastReadTime("ops/a"); !ok {
				t.Fatal("read entry wiped by a boot with no concept on disk")
			}
			if _, ok := again.lastSurfacedTime("ops/a"); !ok {
				t.Fatal("surfaced entry wiped by a boot with no concept on disk")
			}
		})
	}
}

// A write failure at shutdown is returned, not lost.
func TestReadAccessSaveReportsAWriteFailure(t *testing.T) {
	k := kbtest.New(t)
	touchConcepts(t, k, "ops/a")
	fixReadAccessClock(t, time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
	l := newReadAccessLog(k)
	l.record("ops/a")
	// A directory where the temp file goes makes the write fail.
	if err := os.MkdirAll(l.path+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := l.save(); err == nil {
		t.Fatal("save swallowed the write failure")
	}
	if err := os.Remove(l.path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := l.save(); err != nil {
		t.Fatalf("the retry must succeed: %v", err)
	}
}
