package kb

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Retirement is one retirement the root log.md records (D820): where a
// concept went, when, with what status and why. The log is the only place the
// from-path, the date and the reason are kept — concept_archive writes the
// status into the page and the rest into the log entry — so reading it covers
// pages archived before anything else tracked them.
type Retirement struct {
	// From and To are the concept's id before and after. A concept_archive
	// moves it (To is its id now); a supersede leaves it in place (From is its
	// id, To the successor and Status "superseded").
	From, To string
	// Status is what the entry set: deprecated, archived, or superseded.
	Status string
	// Reason is the concept_archive reason, empty when none was given or for
	// a supersede (whose reason lives in the page's supersede_reason).
	Reason string
	// At is the entry's timestamp; zero when its heading did not parse.
	At time.Time
}

// Subject is the concept the entry is about, under the id it holds now: the
// target of an archive, the source of a supersede. A supersede's To is
// another concept, which a later archive may retire under its own entry, so
// matching a concept by To alone would read the wrong story.
func (r Retirement) Subject() string {
	if r.Status == "superseded" {
		return r.From
	}
	return r.To
}

// retirementCache is the parsed log, valid while log.md keeps its size and
// mtime.
type retirementCache struct {
	size    int64
	modTime int64 // nanoseconds
	list    []Retirement
}

// Retirements parses the root log.md into the retirements it records, newest
// first as the log is. An entry is one whose first body line is what
// concept_archive ("concept_archive: <src> -> <dst> (<status>[, <reason>])")
// or supersede ("supersede: <src> → <dst>") logs; anything else, a malformed
// line included, is skipped. The result is cached on the log's size and
// mtime and shared: callers must not modify it. A KB without a log has none.
func (kb *KB) Retirements() ([]Retirement, error) {
	info, err := os.Stat(filepath.Join(kb.DataRoot(), "log.md"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	kb.retireMu.Lock()
	defer kb.retireMu.Unlock()
	if c := kb.retireCache; c != nil && c.size == info.Size() && c.modTime == info.ModTime().UnixNano() {
		return c.list, nil
	}
	content, err := kb.ReadRaw("log.md")
	if err != nil {
		return nil, err
	}
	kb.retireCache = &retirementCache{size: info.Size(), modTime: info.ModTime().UnixNano(), list: parseRetirements(content)}
	return kb.retireCache.list, nil
}

func parseRetirements(content string) []Retirement {
	var out []Retirement
	for _, entry := range parseLogEntries(content) {
		r, ok := parseRetirementText(retirementHead(entry))
		if !ok {
			continue
		}
		if ts, err := time.Parse(time.RFC3339, strings.TrimSpace(strings.TrimPrefix(entry[0], "## "))); err == nil {
			r.At = ts
		}
		out = append(out, r)
	}
	return out
}

// retirementHead is the text of an entry's first line, or — for a
// concept_archive entry whose reason held line breaks, as older versions
// logged it unsanitised — the lines up to the one that closes the title with
// "):", joined with spaces. A title that never closes stays its first line,
// which then fails to parse and is skipped.
func retirementHead(entry []string) string {
	head := entryText(entry)
	if !strings.HasPrefix(head, "concept_archive: ") || strings.HasSuffix(head, "):") {
		return head
	}
	var lines []string
	for _, line := range entry[1:] {
		if t := strings.TrimSpace(line); t != "" {
			lines = append(lines, t)
		}
		if joined := strings.Join(lines, " "); strings.HasSuffix(joined, "):") {
			return joined
		}
	}
	return head
}

// parseRetirementText reads the first line of a log entry. concept_archive
// ends the line with ":" (the move details follow on the next lines) and the
// reason may hold commas and parentheses, so the status is the first word in
// the parentheses and the reason everything up to the last one.
func parseRetirementText(text string) (Retirement, bool) {
	if rest, ok := strings.CutPrefix(text, "concept_archive: "); ok {
		rest = strings.TrimSuffix(rest, ":")
		src, rest, ok := strings.Cut(rest, " -> ")
		if !ok {
			return Retirement{}, false
		}
		dst, detail, ok := strings.Cut(rest, " (")
		if !ok || !strings.HasSuffix(detail, ")") || src == "" || dst == "" || strings.ContainsAny(src+dst, " \t") {
			return Retirement{}, false
		}
		status, reason, _ := strings.Cut(strings.TrimSuffix(detail, ")"), ", ")
		if status == "" {
			return Retirement{}, false
		}
		return Retirement{From: src, To: dst, Status: status, Reason: reason}, true
	}
	if rest, ok := strings.CutPrefix(text, "supersede: "); ok {
		src, dst, ok := strings.Cut(rest, " → ")
		if !ok || src == "" || dst == "" || strings.ContainsAny(src+dst, " \t") {
			return Retirement{}, false
		}
		return Retirement{From: src, To: dst, Status: "superseded"}, true
	}
	return Retirement{}, false
}

// IsArchiveMap is the rule that marks a map as an archive (D820): a map that
// is not a journal and where at least half of its top-level concepts (top) are
// retired (retiredTop) lists retired pages by design. No contract field marks
// one, so the lint exemption of index_lists_retired and the overview's
// archive flag both ask here and cannot disagree.
func IsArchiveMap(contract MapContract, top, retiredTop int) bool {
	return contract.Kind != "journal" && top > 0 && retiredTop*2 >= top
}
