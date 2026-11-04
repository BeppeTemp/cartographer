package mcpserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/search"
)

// The search-miss log (D247): a search that found nothing, for a principal
// who can see the whole KB, is the most direct evidence of a knowledge gap.
// Misses are appended to .cartographer/search-misses.jsonl — local state,
// never committed — and kb_status reports the most frequent recent ones.
const (
	searchMissFile = "search-misses.jsonl"
	// The log is rewritten to its last searchMissKeep lines whenever it grows
	// past searchMissMax: bounded, with no rotation machinery.
	searchMissMax  = 2500
	searchMissKeep = 2000
	// kb_status reports the top searchMissTop queries of the last
	// searchMissWindow.
	searchMissTop    = 10
	searchMissWindow = 30 * 24 * time.Hour
	// A recorded query is cut to searchMissQueryRunes runes: a line longer than
	// the reader's buffer could not be read back.
	searchMissQueryRunes = 512
	// top() re-checks at most searchMissMaxChecks queries, however many are
	// resolved: each check is a search.
	searchMissMaxChecks = 4 * searchMissTop
	// A line over searchMissMaxLine bytes is skipped by the reader, not fatal.
	searchMissMaxLine = 64 * 1024
)

// searchMissNow is the log's clock. Override in tests.
var searchMissNow = time.Now

type searchMiss struct {
	At    string `json:"at"`
	Query string `json:"query"`
}

// searchMissLog is one KB's miss log, owned by RegisterKBTools.
type searchMissLog struct {
	path string
	mu   sync.Mutex
	// lines is the file's line count, -1 until first counted.
	lines int
	// checker reports whether a missed query now has results (D319); nil
	// skips the re-check. Set once by SetChecker before any request.
	checker func(query string) bool
	// prepare runs once before a top() re-checks its queries: it brings the
	// index up to date, which the per-query checker must not redo — a whole-KB
	// stat walk per query made kb_status cost a walk per reported miss.
	prepare func()
}

// SetChecker installs the re-check top() runs on every reported query, so a
// gap that has since been filled is marked resolved instead of reported as
// open (D319).
func (l *searchMissLog) SetChecker(fn func(query string) bool) {
	if l != nil {
		l.checker = fn
	}
}

// SetCheckPrepare installs what top() runs once before its re-checks.
func (l *searchMissLog) SetCheckPrepare(fn func()) {
	if l != nil {
		l.prepare = fn
	}
}

func newSearchMissLog(k *kb.KB) *searchMissLog {
	return &searchMissLog{path: filepath.Join(k.Root, ".cartographer", searchMissFile), lines: -1}
}

// normalizeMissQuery merges spellings of the same question: trimmed,
// lowercased, diacritics folded as the indexes fold them (D246), inner
// whitespace collapsed.
func normalizeMissQuery(q string) string {
	return strings.Join(strings.Fields(search.Fold(q)), " ")
}

// record appends one miss. A failure is logged and never fails the search.
func (l *searchMissLog) record(query string) {
	if l == nil {
		return
	}
	q := normalizeMissQuery(query)
	if q == "" {
		return
	}
	if r := []rune(q); len(r) > searchMissQueryRunes {
		q = strings.TrimSpace(string(r[:searchMissQueryRunes]))
	}
	if err := l.append(searchMiss{At: searchMissNow().UTC().Format(time.RFC3339), Query: q}); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: search-miss log: %v\n", err)
	}
}

func (l *searchMissLog) append(m searchMiss) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Never append through a symlink, the file or its directory: it would
	// write into whatever it points at (D148).
	if err := ensureLocalStateDir(l.path); err != nil {
		return err
	}
	if l.lines < 0 {
		entries, err := l.readLocked()
		if err != nil {
			return err
		}
		l.lines = len(entries)
	}
	line, _ := json.Marshal(m)
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	l.lines++
	if l.lines > searchMissMax {
		return l.trimLocked()
	}
	return nil
}

// trimLocked rewrites the log to its last searchMissKeep entries, through a
// temporary file and a rename.
func (l *searchMissLog) trimLocked() error {
	entries, err := l.readLocked()
	if err != nil {
		return err
	}
	if len(entries) > searchMissKeep {
		entries = entries[len(entries)-searchMissKeep:]
	}
	var b strings.Builder
	for _, e := range entries {
		line, _ := json.Marshal(e)
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := writeLocalStateFile(l.path, []byte(b.String())); err != nil {
		return err
	}
	l.lines = len(entries)
	return nil
}

// readLocked returns the log's entries, skipping lines that do not parse. A
// missing file is an empty log.
func (l *searchMissLog) readLocked() ([]searchMiss, error) {
	if err := checkLocalStatePath(l.path); err != nil {
		return nil, err
	}
	f, err := os.Open(l.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []searchMiss
	// ReadLine-style reading with a bounded buffer: a line longer than the
	// buffer (a log poisoned by an old, untruncated query) is skipped, so the
	// file heals instead of failing every read.
	r := bufio.NewReaderSize(f, 4096)
	var line []byte
	tooLong := false
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			if err != io.EOF {
				return out, err
			}
			break
		}
		if !tooLong && len(line)+len(chunk) > searchMissMaxLine {
			tooLong = true
		}
		if !tooLong {
			line = append(line, chunk...)
		}
		if isPrefix {
			continue
		}
		if !tooLong {
			var m searchMiss
			if json.Unmarshal(line, &m) == nil && m.Query != "" {
				out = append(out, m)
			}
		}
		line, tooLong = line[:0], false
	}
	return out, nil
}

type searchMissSummary struct {
	Query    string `json:"query"`
	Count    int    `json:"count"`
	LastSeen string `json:"last_seen"`
	// Resolved: the same query now returns results (D319).
	Resolved bool `json:"resolved,omitempty"`
	// Unchecked: the re-check budget ran out before this query, so it is
	// neither known open nor resolved.
	Unchecked bool `json:"unchecked,omitempty"`
}

// top aggregates the misses of the last searchMissWindow: the searchMissTop
// most frequent queries, by count desc, then last seen desc, then query.
// With a checker, each query is re-run and one that now has results is
// marked Resolved and sorted after every open one (D319). Queries are checked
// in rank order until searchMissTop open ones are found or searchMissMaxChecks
// queries have been checked; the ones not reached are reported unchecked,
// after the open ones.
func (l *searchMissLog) top() []searchMissSummary {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	entries, err := l.readLocked()
	l.mu.Unlock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: search-miss log: %v\n", err)
		return nil
	}
	cutoff := searchMissNow().Add(-searchMissWindow)
	byQuery := map[string]*searchMissSummary{}
	for _, e := range entries {
		at, err := time.Parse(time.RFC3339, e.At)
		if err != nil || at.Before(cutoff) {
			continue
		}
		s := byQuery[e.Query]
		if s == nil {
			s = &searchMissSummary{Query: e.Query}
			byQuery[e.Query] = s
		}
		s.Count++
		if e.At > s.LastSeen {
			s.LastSeen = e.At
		}
	}
	out := make([]searchMissSummary, 0, len(byQuery))
	for _, s := range byQuery {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].LastSeen != out[j].LastSeen {
			return out[i].LastSeen > out[j].LastSeen
		}
		return out[i].Query < out[j].Query
	})
	if l.checker != nil {
		if l.prepare != nil && len(out) > 0 {
			l.prepare()
		}
		var open, resolved, unchecked []searchMissSummary
		checks := 0
		for _, s := range out {
			if len(open) == searchMissTop {
				break
			}
			if checks == searchMissMaxChecks {
				s.Unchecked = true
				unchecked = append(unchecked, s)
				continue
			}
			checks++
			if l.checker(s.Query) {
				s.Resolved = true
				resolved = append(resolved, s)
			} else {
				open = append(open, s)
			}
		}
		out = append(append(open, unchecked...), resolved...)
	}
	if len(out) > searchMissTop {
		out = out[:searchMissTop]
	}
	return out
}
