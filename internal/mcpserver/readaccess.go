package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// The read-access log (D322, D778): when each concept was last read, and when
// it was last surfaced. A read is concept_read returning the concept; a search
// hit is only surfaced, a pointer the agent may never follow, so it never
// counts as a read. Most hits are never opened, and counting them would make a
// page search keeps offering look alive however rarely anyone reads it. It is
// the foundation of an abandonment signal (a future plan composes it with
// in-degree and age); it is telemetry, not a trust event, so it lives in its
// own file beside the search-miss log and never in the audit trail or git.
// Navigation tools (graph_*, atlas_overview, concept_list) stamp neither;
// there is no per-client breakdown because the protocol carries no session id.
//
// Not retroactive: a concept with no read entry was not read since `since`,
// and a consumer must read that as "unknown" before it, never as "never read".
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
	// lastRead maps a concept id to the instant concept_read last returned it.
	lastRead map[string]time.Time
	// lastSurfaced maps a concept id to the instant a search last listed it
	// among its hits.
	lastSurfaced map[string]time.Time
	// since is when lastRead began counting reads only.
	since     time.Time
	dirty     bool
	lastFlush time.Time
}

// readAccessFileV2 is the file's shape. The first one, a flat {"id": "RFC3339"}
// map that mixed reads and search hits, is migrated by load.
type readAccessFileV2 struct {
	Since    string            `json:"since"`
	Read     map[string]string `json:"read"`
	Surfaced map[string]string `json:"surfaced"`
}

// accessSummary is one map of the log as kb_status reports it.
type accessSummary struct {
	TrackedConcepts int    `json:"tracked_concepts"`
	Oldest          string `json:"oldest,omitempty"`
	Newest          string `json:"newest,omitempty"`
}

// newReadAccessLog opens the KB's log, loading the file when there is one.
func newReadAccessLog(k *kb.KB) *readAccessLog {
	now := readAccessNow()
	l := &readAccessLog{
		path:         filepath.Join(k.Root, ".cartographer", readAccessFile),
		lastRead:     map[string]time.Time{},
		lastSurfaced: map[string]time.Time{},
		since:        now,
		lastFlush:    now,
	}
	l.load()
	return l
}

// record notes a read of ids by concept_read. A nil log (the UI path) records
// nothing.
func (l *readAccessLog) record(ids ...string) {
	if l != nil {
		l.stamp(l.lastRead, ids)
	}
}

// recordSurfaced notes that a search listed ids among its hits. It never
// stamps a read: the agent was shown a pointer, not the content.
func (l *readAccessLog) recordSurfaced(ids ...string) {
	if l != nil {
		l.stamp(l.lastSurfaced, ids)
	}
}

// stamp sets ids to now in m, one of the log's two maps (never reassigned
// after construction, so reading the field outside the lock is safe).
func (l *readAccessLog) stamp(m map[string]time.Time, ids []string) {
	if len(ids) == 0 {
		return
	}
	now := readAccessNow()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, id := range ids {
		m[id] = now
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
	return l.lookup(l.lastRead, id)
}

// lastSurfacedTime returns when id was last listed as a search hit.
func (l *readAccessLog) lastSurfacedTime(id string) (time.Time, bool) {
	if l == nil {
		return time.Time{}, false
	}
	return l.lookup(l.lastSurfaced, id)
}

func (l *readAccessLog) lookup(m map[string]time.Time, id string) (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := m[id]
	return t, ok
}

// summary reports both maps (how many concepts each tracks and its oldest and
// newest instants, RFC3339) and since, the instant reads began to be counted.
func (l *readAccessLog) summary() (read, surfaced accessSummary, since string) {
	if l == nil {
		return accessSummary{}, accessSummary{}, ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return summarize(l.lastRead), summarize(l.lastSurfaced), l.since.UTC().Format(time.RFC3339)
}

func summarize(m map[string]time.Time) accessSummary {
	var lo, hi time.Time
	for _, t := range m {
		if lo.IsZero() || t.Before(lo) {
			lo = t
		}
		if t.After(hi) {
			hi = t
		}
	}
	s := accessSummary{TrackedConcepts: len(m)}
	if len(m) > 0 {
		s.Oldest, s.Newest = lo.UTC().Format(time.RFC3339), hi.UTC().Format(time.RFC3339)
	}
	return s
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

// saveLocked writes the file atomically; a failure is dropped (the log is
// telemetry) and retried at the next interval.
func (l *readAccessLog) saveLocked() {
	l.lastFlush = readAccessNow()
	if !l.dirty {
		return
	}
	data, err := json.Marshal(readAccessFileV2{
		Since:    l.since.UTC().Format(time.RFC3339),
		Read:     formatTimes(l.lastRead),
		Surfaced: formatTimes(l.lastSurfaced),
	})
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

func formatTimes(m map[string]time.Time) map[string]string {
	out := make(map[string]string, len(m))
	for id, t := range m {
		out[id] = t.UTC().Format(time.RFC3339)
	}
	return out
}

// load reads the file; a missing or malformed one starts empty.
//
// Trap: a file from before reads and hits were told apart is one flat map,
// and an entry in it may be a hit nobody opened. Its entries become surfaced
// and reads start over from now, so none of them is ever taken for a read.
func (l *readAccessLog) load() {
	data, err := os.ReadFile(l.path)
	if err != nil {
		return
	}
	var v2 readAccessFileV2
	if json.Unmarshal(data, &v2) == nil && (v2.Since != "" || v2.Read != nil || v2.Surfaced != nil) {
		if t, err := time.Parse(time.RFC3339, v2.Since); err == nil {
			l.since = t
		}
		parseTimes(v2.Read, l.lastRead)
		parseTimes(v2.Surfaced, l.lastSurfaced)
		return
	}
	var legacy map[string]string
	if json.Unmarshal(data, &legacy) != nil {
		return
	}
	parseTimes(legacy, l.lastSurfaced)
	l.dirty = len(l.lastSurfaced) > 0
}

func parseTimes(in map[string]string, out map[string]time.Time) {
	for id, s := range in {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			out[id] = t
		}
	}
}
