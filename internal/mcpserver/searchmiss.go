package mcpserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
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
	if err := l.append(searchMiss{At: searchMissNow().UTC().Format(time.RFC3339), Query: q}); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: search-miss log: %v\n", err)
	}
}

func (l *searchMissLog) append(m searchMiss) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	// Never append through a symlink: it would write into whatever it points
	// at (D148).
	if info, err := os.Lstat(l.path); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink, refusing to write through it", l.path)
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
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, l.path); err != nil {
		os.Remove(tmp)
		return err
	}
	l.lines = len(entries)
	return nil
}

// readLocked returns the log's entries, skipping lines that do not parse. A
// missing file is an empty log.
func (l *searchMissLog) readLocked() ([]searchMiss, error) {
	f, err := os.Open(l.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []searchMiss
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m searchMiss
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.Query != "" {
			out = append(out, m)
		}
	}
	return out, sc.Err()
}

type searchMissSummary struct {
	Query    string `json:"query"`
	Count    int    `json:"count"`
	LastSeen string `json:"last_seen"`
}

// top aggregates the misses of the last searchMissWindow: the searchMissTop
// most frequent queries, by count desc, then last seen desc, then query.
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
	if len(out) > searchMissTop {
		out = out[:searchMissTop]
	}
	return out
}
