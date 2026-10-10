package mcpserver

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// The lint history (D370): is the KB getting better? The server keeps one
// sample of the whole-KB lint totals per day, so the Health panel and
// kb_status can show a trend. It is derived state, like the auto-repair log:
// a local file under .cartographer/ (excluded from git), never a concept and
// never in the KB's history.

const (
	lintHistoryName = "lint-history.jsonl"
	// lintHistoryRetention is how long a sample is kept; older ones are
	// dropped on the next write.
	lintHistoryRetention = 90 * 24 * time.Hour
	// lintTrendWindow is the span kb_status compares (first and last sample).
	lintTrendWindow = 7 * 24 * time.Hour
)

// lintSample is one line of the file: the unfiltered whole-KB totals.
type lintSample struct {
	At         string         `json:"at"`
	Total      int            `json:"total"`
	BySeverity map[string]int `json:"by_severity"`
	// ByHandler splits the total by who acts on a finding (D365).
	ByHandler map[string]int `json:"by_handler"`
}

func lintHistoryPath(k *kb.KB) string {
	return filepath.Join(k.Root, ".cartographer", lintHistoryName)
}

// newLintSample counts the findings by severity and by handler.
func newLintSample(k *kb.KB, findings []lint.Finding, now time.Time) lintSample {
	s := lintSample{
		At: now.UTC().Format(time.RFC3339), Total: len(findings),
		BySeverity: map[string]int{}, ByHandler: map[string]int{},
	}
	auto := uiAutoChecks(k)
	for _, f := range findings {
		s.BySeverity[f.Severity]++
		s.ByHandler[findingHandler(f, auto)]++
	}
	return s
}

// readLintHistory returns the samples in time order. A line that does not
// parse is skipped.
func readLintHistory(k *kb.KB) []lintSample {
	out := []lintSample{}
	f, err := os.Open(lintHistoryPath(k))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var s lintSample
		if json.Unmarshal(sc.Bytes(), &s) == nil && s.At != "" {
			out = append(out, s)
		}
	}
	return out
}

// recordLintSample keeps at most one sample per UTC day, the latest of the
// day, and drops what is older than the retention. The file is rewritten
// whole (it holds at most 90 short lines) through a temporary file, so a
// crash never leaves half a history. A failure is returned for the caller to
// ignore: the history is a convenience, never a reason to fail a lint.
func recordLintSample(k *kb.KB, s lintSample, now time.Time) error {
	at, err := time.Parse(time.RFC3339, s.At)
	if err != nil {
		return err
	}
	day := at.UTC().Format("2006-01-02")
	cutoff := now.Add(-lintHistoryRetention)
	kept := []lintSample{}
	for _, old := range readLintHistory(k) {
		t, err := time.Parse(time.RFC3339, old.At)
		if err != nil || t.Before(cutoff) || t.UTC().Format("2006-01-02") == day {
			continue
		}
		kept = append(kept, old)
	}
	kept = append(kept, s)
	p := lintHistoryPath(k)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if fi, err := os.Lstat(p); err == nil && !fi.Mode().IsRegular() {
		return os.ErrInvalid // never write through a symlink (D148)
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), lintHistoryName+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	w := bufio.NewWriter(tmp)
	for _, x := range kept {
		line, err := json.Marshal(x)
		if err != nil {
			tmp.Close()
			return err
		}
		w.Write(append(line, '\n'))
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// lintTrend is the compact kb_status view: the first and the last sample of
// the last 7 days and how the total moved. nil with fewer than two samples.
func lintTrend(samples []lintSample, now time.Time) map[string]interface{} {
	since := now.Add(-lintTrendWindow)
	var win []lintSample
	for _, s := range samples {
		if t, err := time.Parse(time.RFC3339, s.At); err == nil && !t.Before(since) {
			win = append(win, s)
		}
	}
	if len(win) < 2 {
		return nil
	}
	first, last := win[0], win[len(win)-1]
	return map[string]interface{}{
		"window_days": int(lintTrendWindow / (24 * time.Hour)),
		"samples":     len(win),
		"first":       first,
		"last":        last,
		"delta":       last.Total - first.Total,
	}
}
