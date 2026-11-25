package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/componentproto"
)

// The persistent history of the Surveyor's runs: daily counters per (KB,
// perimeter, task, outcome), kept in a file of the server's data directory
// (<data>/.cartographer/surveyor-history.json), not in a KB (a KB write is a
// commit and would feed the Surveyor's own activity signal). Counters, not a
// log: no run id, replica id or error text leaves the store through query.
//
// Traps, each held by a test (surveyorhistory_test.go):
//   - the day of a run is the UTC day of the server's receipt (the `now` add is
//     given), never the Surveyor's started_at: a skewed clock cannot move a bucket;
//   - a run is counted once: a seen-set per (principal, perimeter, replica) in the same
//     file survives a restart, so the re-post the Surveyor makes on runs_wanted
//     does not double it, and a crash cannot count a run and forget it was counted;
//   - the file is only replaced atomically and never written through a symlink;
//   - the history mutex and flushMu are never held while taking surveyorStore.mu
//     (the caller adds after putRuns released it).
const (
	runHistoryVersion  = 1
	runHistoryDays     = 90
	runHistorySeenIDs  = 64  // per replica: more than the 20 a Surveyor re-posts
	runHistoryReplicas = 512 // replica entries in the seen-set
	runHistoryMaxRows  = 20000
	// runHistoryMaxDayRows bounds the rows of one UTC day: task and perimeter
	// are free-form, so without it one principal could grow the file without
	// limit within a day, and a flood on many days would evict other KBs' old
	// days. Past it a new key is dropped (counted in droppedRows), existing
	// rows keep counting. 90 days of it fit the total cap.
	runHistoryMaxDayRows = 250
	runHistoryFile       = "surveyor-history.json"
	runHistoryDayFmt     = "2006-01-02"
	runHistoryMaxKB      = 128
	// seenSep joins the KB and the run id in the seen-set (the replica id is
	// client-chosen, so a run id counted for one KB must not suppress the same
	// id for another), and the principal and the replica in its key (two
	// principals declaring the same perimeter and replica id must not share a
	// ring: one could suppress or double the other's counts).
	seenSep = "\x1f"
)

type historyKey struct{ day, kb, perimeter, task, outcome string }

type historyCount struct{ runs, tokens, durationMS int64 }

// seenReplica is the ring of the last run ids counted for one replica.
type seenReplica struct {
	at  time.Time
	ids []string
}

// historyRow is one row of the file and of query.
type historyRow struct {
	Day        string `json:"day,omitempty"` // in the answer of query; the file nests rows by day
	KB         string `json:"kb,omitempty"`
	Perimeter  string `json:"perimeter"`
	Task       string `json:"task"`
	Outcome    string `json:"outcome"`
	Runs       int64  `json:"runs"`
	Tokens     int64  `json:"tokens"`
	DurationMS int64  `json:"duration_ms"`
}

type historyFile struct {
	Version int                         `json:"version"`
	Days    map[string][]historyRow     `json:"days"`
	Seen    map[string]historySeenEntry `json:"seen"`
}

type historySeenEntry struct {
	At  time.Time `json:"at"`
	IDs []string  `json:"ids"`
}

// historyView is what query answers: the rows of one KB, oldest day first.
type historyView struct {
	RetentionDays int
	Persisted     bool
	FirstDay      string // the oldest day the store holds for the queried KB only; "" when empty
	Days          []historyRow
}

type runHistory struct {
	mu      sync.Mutex
	path    string // "" = memory only
	rows    map[historyKey]*historyCount
	seen    map[string]*seenReplica
	gen     uint64 // bumped by every change
	flushed uint64 // gen of the last snapshot written
	maxRows int
	// maxDayRows is runHistoryMaxDayRows; droppedRows counts the keys refused
	// past it and dropLogged the day a drop was last logged (guarded by mu).
	maxDayRows  int
	droppedRows int64
	dropLogged  string
	logf        func(format string, args ...any)
	flushMu     sync.Mutex // serializes flushes; snapshots are taken inside it
	lastFail    string     // last flush error logged (guarded by flushMu)
}

func newRunHistory() *runHistory {
	return &runHistory{
		rows:    map[historyKey]*historyCount{},
		seen:    map[string]*seenReplica{},
		maxRows: runHistoryMaxRows, maxDayRows: runHistoryMaxDayRows,
		logf: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "cartographer: "+format+"\n", args...)
		},
	}
}

func (h *runHistory) persisted() bool { return h.path != "" }

// seenKey is the key of a replica's ring in the seen-set.
func seenKey(principal, perimeter, replica string) string {
	return principal + seenSep + perimeter + "/" + replica
}

func historyDay(t time.Time) string { return t.UTC().Format(runHistoryDayFmt) }

// cutoffDay is the oldest day kept at now: today minus the retention.
func historyCutoff(now time.Time) string {
	return historyDay(now.UTC().AddDate(0, 0, -runHistoryDays))
}

// load reads path (a missing file is empty) and keeps path for flush. A
// malformed or unknown-version file is renamed aside and the store starts
// empty: the server never guesses and never overwrites what it could not read.
func (h *runHistory) load(path string, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.path = path
	h.rows = map[historyKey]*historyCount{}
	h.seen = map[string]*seenReplica{}
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		// A read error (permissions, transient I/O) says nothing about the
		// content: keep the file and never write it in this process.
		h.logf("surveyor history %s not readable (%v): kept as it is, counting in memory only", path, err)
		h.path = ""
		return
	}
	var f historyFile
	err = json.Unmarshal(b, &f)
	if err == nil && f.Version != runHistoryVersion {
		err = fmt.Errorf("unsupported version %d", f.Version)
	}
	if err != nil {
		aside := path + ".bad-" + now.UTC().Format("20060102T150405Z")
		if rerr := os.Rename(path, aside); rerr != nil {
			h.logf("surveyor history %s unreadable (%v) and not moved aside (%v): starting empty, not writing it", path, err, rerr)
			h.path = "" // never overwrite what could be neither read nor moved aside
			return
		}
		h.logf("surveyor history %s unreadable (%v): moved to %s, starting empty", path, err, aside)
		return
	}
	cutoff := historyCutoff(now)
	for day, rows := range f.Days {
		if _, perr := time.Parse(runHistoryDayFmt, day); perr != nil || day < cutoff {
			continue
		}
		for _, r := range rows {
			// A hand-edited file cannot inject text the route echoes.
			if !validHistoryRow(r) {
				continue
			}
			k := historyKey{day, r.KB, r.Perimeter, r.Task, r.Outcome}
			c := h.rows[k]
			if c == nil {
				c = &historyCount{}
				h.rows[k] = c
			}
			c.runs += r.Runs
			c.tokens += r.Tokens
			c.durationMS += r.DurationMS
		}
	}
	for key, e := range f.Seen {
		pr, rest, ok := strings.Cut(key, seenSep)
		if !ok || pr == "" || len(pr) > runHistoryMaxKB || strings.ContainsFunc(pr, func(c rune) bool { return c < ' ' || c == 0x7f }) {
			continue
		}
		per, rep, ok := strings.Cut(rest, "/")
		if !ok || !perimeterRe.MatchString(per) || !runIDRe.MatchString(rep) || e.At.Before(now.AddDate(0, 0, -runHistoryDays)) {
			continue
		}
		var ids []string
		for _, id := range e.IDs {
			if _, rid, ok := strings.Cut(id, seenSep); ok && runIDRe.MatchString(rid) {
				ids = append(ids, id)
			}
		}
		if len(ids) > runHistorySeenIDs {
			ids = ids[len(ids)-runHistorySeenIDs:]
		}
		h.seen[key] = &seenReplica{at: e.At, ids: ids}
	}
	h.evictLocked(now)
}

func validHistoryRow(r historyRow) bool {
	if r.KB == "" || len(r.KB) > runHistoryMaxKB || strings.ContainsFunc(r.KB, func(c rune) bool { return c < ' ' || c == 0x7f }) {
		return false
	}
	if !perimeterRe.MatchString(r.Perimeter) || !taskKindRe.MatchString(r.Task) {
		return false
	}
	okOutcome := false
	for _, o := range componentproto.Outcomes() {
		if r.Outcome == o {
			okOutcome = true
		}
	}
	return okOutcome && r.Runs >= 0 && r.Tokens >= 0 && r.DurationMS >= 0
}

// evictLocked applies the retention, the row cap (the oldest day goes whole)
// and the replica bounds of the seen-set.
func (h *runHistory) evictLocked(now time.Time) {
	cutoff := historyCutoff(now)
	days := map[string]int{}
	for k := range h.rows {
		if k.day < cutoff {
			delete(h.rows, k)
			continue
		}
		days[k.day]++
	}
	for len(h.rows) > h.maxRows && len(days) > 1 {
		oldest := ""
		for d := range days {
			if oldest == "" || d < oldest {
				oldest = d
			}
		}
		for k := range h.rows {
			if k.day == oldest {
				delete(h.rows, k)
			}
		}
		delete(days, oldest)
	}
	limit := now.AddDate(0, 0, -runHistoryDays)
	for k, e := range h.seen {
		if e.at.Before(limit) {
			delete(h.seen, k)
		}
	}
	if len(h.seen) > runHistoryReplicas {
		keys := make([]string, 0, len(h.seen))
		for k := range h.seen {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			return h.seen[keys[i]].at.Before(h.seen[keys[j]].at)
		})
		for _, k := range keys[:len(keys)-runHistoryReplicas] {
			delete(h.seen, k)
		}
	}
}

// add counts the runs not yet counted for the principal's <perimeter>/<replica>, on the UTC day
// of now (the server's receipt clock), and flushes when anything changed or an
// earlier flush failed. It returns how many runs it counted.
func (h *runHistory) add(principal, perimeter, replica string, runs []surveyorRun, now time.Time) (counted int) {
	h.mu.Lock()
	key := seenKey(principal, perimeter, replica)
	e := h.seen[key]
	if e == nil {
		e = &seenReplica{}
	}
	have := make(map[string]bool, len(e.ids))
	for _, id := range e.ids {
		have[id] = true
	}
	day := historyDay(now)
	dayRows := 0
	for k := range h.rows {
		if k.day == day {
			dayRows++
		}
	}
	changed := false
	for _, r := range runs {
		sid := r.KB + seenSep + r.RunID
		if have[sid] {
			continue
		}
		have[sid] = true
		e.ids = append(e.ids, sid)
		changed = true
		k := historyKey{day, r.KB, perimeter, r.Task, r.Outcome}
		c := h.rows[k]
		if c == nil {
			if dayRows >= h.maxDayRows {
				h.droppedRows++
				if h.dropLogged != day {
					h.dropLogged = day
					h.logf("surveyor history: more than %d distinct rows on %s, new ones are not counted (existing rows keep counting)", h.maxDayRows, day)
				}
				continue
			}
			dayRows++
			c = &historyCount{}
			h.rows[k] = c
		}
		c.runs++
		c.tokens += r.TokensSpent
		c.durationMS += r.DurationMS
		counted++
	}
	if changed {
		if len(e.ids) > runHistorySeenIDs {
			e.ids = append([]string(nil), e.ids[len(e.ids)-runHistorySeenIDs:]...)
		}
		e.at = now
		h.seen[key] = e
		h.gen++
	}
	before := len(h.rows) + len(h.seen)
	h.evictLocked(now)
	if counted > 0 || len(h.rows)+len(h.seen) != before {
		h.gen++
	}
	dirty := h.gen != h.flushed
	h.mu.Unlock()
	if dirty {
		h.flush()
	}
	return counted
}

// query returns the rows of kb for the last days days (clamped to 1..90),
// oldest first, zero rows omitted.
func (h *runHistory) query(kb string, days int, now time.Time) historyView {
	days = min(max(days, 1), runHistoryDays)
	from := historyDay(now.UTC().AddDate(0, 0, -(days - 1)))
	h.mu.Lock()
	defer h.mu.Unlock()
	v := historyView{RetentionDays: runHistoryDays, Persisted: h.persisted()}
	for k, c := range h.rows {
		if k.kb != kb {
			continue
		}
		if v.FirstDay == "" || k.day < v.FirstDay {
			v.FirstDay = k.day
		}
		if k.day < from || c.runs == 0 {
			continue
		}
		v.Days = append(v.Days, historyRow{Day: k.day, Perimeter: k.perimeter, Task: k.task, Outcome: k.outcome,
			Runs: c.runs, Tokens: c.tokens, DurationMS: c.durationMS})
	}
	sort.Slice(v.Days, func(i, j int) bool {
		a, b := v.Days[i], v.Days[j]
		if a.Day != b.Day {
			return a.Day < b.Day
		}
		if a.Perimeter != b.Perimeter {
			return a.Perimeter < b.Perimeter
		}
		if a.Task != b.Task {
			return a.Task < b.Task
		}
		return a.Outcome < b.Outcome
	})
	return v
}

// snapshot is the file's content, taken under the history mutex.
func (h *runHistory) snapshot() (historyFile, uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f := historyFile{Version: runHistoryVersion, Days: map[string][]historyRow{}, Seen: map[string]historySeenEntry{}}
	for k, c := range h.rows {
		f.Days[k.day] = append(f.Days[k.day], historyRow{KB: k.kb, Perimeter: k.perimeter, Task: k.task,
			Outcome: k.outcome, Runs: c.runs, Tokens: c.tokens, DurationMS: c.durationMS})
	}
	for _, rows := range f.Days {
		sort.Slice(rows, func(i, j int) bool {
			a, b := rows[i], rows[j]
			return a.KB+"\x00"+a.Perimeter+"\x00"+a.Task+"\x00"+a.Outcome < b.KB+"\x00"+b.Perimeter+"\x00"+b.Task+"\x00"+b.Outcome
		})
	}
	for k, e := range h.seen {
		f.Seen[k] = historySeenEntry{At: e.at, IDs: append([]string(nil), e.ids...)}
	}
	return f, h.gen
}

// flush writes the file. flushMu is taken before the snapshot, so a snapshot
// older than the one on disk can never replace it. A failure is logged once per
// distinct error and retried at the next add; memory stays authoritative.
func (h *runHistory) flush() {
	if h.path == "" {
		return
	}
	h.flushMu.Lock()
	defer h.flushMu.Unlock()
	f, gen := h.snapshot()
	if err := writeHistoryFile(h.path, f); err != nil {
		if msg := err.Error(); msg != h.lastFail {
			h.lastFail = msg
			h.logf("surveyor history not written (kept in memory, retried at the next change): %v", err)
		}
		return
	}
	h.lastFail = ""
	h.mu.Lock()
	h.flushed = gen
	h.mu.Unlock()
}

// writeHistoryFile replaces path atomically: a temp file in the same
// directory, fsync, chmod, rename. A symlink at path is refused (writing
// through one would modify its target, D148).
func writeHistoryFile(path string, f historyFile) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink: refusing to write through it", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+runHistoryFile+".tmp-*")
	if err != nil {
		// The random temp name would make every failure a new message and
		// defeat the log-once of flush: name the directory and the cause.
		var pe *os.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return fmt.Errorf("create a temp file in %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil { // best effort: not every filesystem syncs a directory
		d.Sync()
		d.Close()
	}
	return nil
}
