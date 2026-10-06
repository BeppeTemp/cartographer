package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// The read-access log (D322): when each concept was last handed to an agent
// by concept_read or by a search hit. It is the foundation of an abandonment
// signal (a future plan composes it with in-degree and age); it is telemetry,
// not a trust event, so it lives in its own file beside the search-miss log
// and never in the audit trail or git. Navigation tools (graph_*, atlas_
// overview, concept_list) are not content reads and are not recorded; there is
// no per-client breakdown because the protocol carries no session id.
//
// Not retroactive: a concept read before this log existed has no entry, and a
// consumer must read that as "unknown", never "never read".
const (
	readAccessFile = "read-access.json"
	// readAccessFlushEvery bounds how stale the file may be: a record flushes
	// when the last write is older, so no goroutine has to outlive the server
	// and a read costs one file write at most once per interval.
	readAccessFlushEvery = 5 * time.Minute
)

// readAccessNow is the log's clock. Override in tests.
var readAccessNow = time.Now

type readAccessLog struct {
	path string
	mu   sync.Mutex
	// lastRead maps a concept id to the instant it was last read.
	lastRead  map[string]time.Time
	dirty     bool
	lastFlush time.Time
}

// newReadAccessLog opens the KB's log, loading the file when there is one.
func newReadAccessLog(k *kb.KB) *readAccessLog {
	l := &readAccessLog{
		path:      filepath.Join(k.Root, ".cartographer", readAccessFile),
		lastRead:  map[string]time.Time{},
		lastFlush: readAccessNow(),
	}
	l.load()
	return l
}

// record notes a read of id. A nil log (the UI path) records nothing.
func (l *readAccessLog) record(ids ...string) {
	if l == nil || len(ids) == 0 {
		return
	}
	now := readAccessNow()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, id := range ids {
		l.lastRead[id] = now
	}
	l.dirty = true
	if now.Sub(l.lastFlush) >= readAccessFlushEvery {
		l.saveLocked()
	}
}

// lastReadTime returns when id was last read.
func (l *readAccessLog) lastReadTime(id string) (time.Time, bool) {
	if l == nil {
		return time.Time{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.lastRead[id]
	return t, ok
}

// summary reports how many concepts are tracked and the oldest and newest
// last-read instants (RFC3339, empty when nothing is tracked).
func (l *readAccessLog) summary() (n int, oldest, newest string) {
	if l == nil {
		return 0, "", ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var lo, hi time.Time
	for _, t := range l.lastRead {
		if lo.IsZero() || t.Before(lo) {
			lo = t
		}
		if t.After(hi) {
			hi = t
		}
	}
	if len(l.lastRead) > 0 {
		oldest, newest = lo.UTC().Format(time.RFC3339), hi.UTC().Format(time.RFC3339)
	}
	return len(l.lastRead), oldest, newest
}

// save writes the file now.
func (l *readAccessLog) save() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.saveLocked()
}

// saveLocked writes {"id": "RFC3339"} atomically; a failure is dropped (the
// log is telemetry) and retried at the next interval.
func (l *readAccessLog) saveLocked() {
	l.lastFlush = readAccessNow()
	if !l.dirty {
		return
	}
	m := make(map[string]string, len(l.lastRead))
	for id, t := range l.lastRead {
		m[id] = t.UTC().Format(time.RFC3339)
	}
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, l.path); err != nil {
		_ = os.Remove(tmp)
		return
	}
	l.dirty = false
}

// load reads the file; a missing or malformed one starts empty.
func (l *readAccessLog) load() {
	data, err := os.ReadFile(l.path)
	if err != nil {
		return
	}
	var m map[string]string
	if json.Unmarshal(data, &m) != nil {
		return
	}
	for id, s := range m {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			l.lastRead[id] = t
		}
	}
}
