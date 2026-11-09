// Package blocktext implements the marker-delimited "managed block" primitive.
// It is used by every Cartographer feature that must own a slice of a hand-curated
// user file (comments, ordering, everything else preserved) without parsing
// the file's own format: the Codex MCP server entry and per-hook
// registrations in config.toml (D58, TOML `#` comment markers). Callers pass
// their own marker strings — this package only ever does substring
// search/replace on raw text, never anything format-aware. (The similar,
// independently-implemented instructions block in internal/provisioning, D56,
// predates this package and uses HTML comment markers; it was left as-is —
// see D58 for why.)
package blocktext

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// Write creates or rewrites the block delimited by begin/end (each marker on
// its own line) in the file at path, with body as its content:
//   - file absent → created with just the block (parent dirs made as needed);
//   - file present with both markers → only the text between them (markers
//     included) is replaced; every other byte in the file is left untouched;
//   - file present without the markers (pre-existing, unrelated content) → the
//     block is appended at the end, separated from existing content by a
//     blank line.
//
// Idempotent: calling Write twice in a row with the same body produces the
// same file.
//
// baseDir is the client directory path lives under (home, or the project dir in
// workspace scope). Write refuses — with an error matching
// safewrite.ErrSymlinkDestination, before reading or writing anything — a path
// whose file, or any directory component below baseDir, is a symlink or
// otherwise not a plain file or directory. baseDir itself may be a link.
func Write(baseDir, path, begin, end, body string) error {
	block := begin + "\n" + strings.TrimRight(body, "\n") + "\n" + end + "\n"

	if err := safewrite.CheckPath(baseDir, path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := safewrite.MkdirAllNoFollow(baseDir, filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return safewrite.WriteFileAtomicNoFollow(baseDir, path, []byte(block), 0o644)
	}
	if err != nil {
		return err
	}

	content := string(data)
	replaced, outcome := ReplaceBlock(content, begin, end, block)
	switch outcome {
	case Corrupted:
		return corruptedError(path)
	case Found:
		return safewrite.WriteFileAtomicNoFollow(baseDir, path, []byte(replaced), 0o644)
	}
	return safewrite.WriteFileAtomicNoFollow(baseDir, path, []byte(appendBlock(content, block)), 0o644)
}

// Remove strips the block delimited by begin/end (markers included) from the
// file at path, if present. If that leaves the file empty or whitespace-only,
// the file itself is removed (covers a file dedicated to a single block).
// Returns whether a block was found (and, unless dryRun, removed); (false,
// nil) if the file doesn't exist or doesn't contain the markers. If dryRun is
// true, computes and returns what would happen without writing anything.
//
// baseDir and the refusal are those of Write, but the refusal applies only when
// a block was found: a file with nothing of ours in it is a no-op and never an
// error. A dry run refuses too when it would have changed something, so it
// reports what the real run would do.
func Remove(baseDir, path, begin, end string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	content := string(data)
	stripped, outcome := ReplaceBlock(content, begin, end, "")
	if outcome == Corrupted {
		return false, corruptedError(path)
	}
	if outcome != Found {
		return false, nil
	}
	if err := safewrite.CheckPath(baseDir, path); err != nil {
		return false, err
	}
	if dryRun {
		return true, nil
	}
	if strings.TrimSpace(stripped) == "" {
		// A listed leaf link is never removed: the file is rewritten through it.
		if err := safewrite.Remove(baseDir, path); !errors.Is(err, safewrite.ErrLinkedLeaf) {
			return true, err
		}
	}
	return true, safewrite.WriteFileAtomicNoFollow(baseDir, path, []byte(stripped), 0o644)
}

// ErrCorruptedBlock is returned (wrapped, naming the file) by Write and Remove
// when the file does not hold exactly one begin line followed by one end line:
// a begin whose end was lost, a second begin before the end, an end before the
// begin. The file is left untouched: cutting from the first begin to the next
// end would delete the user's text in between, and appending a second block
// would duplicate the entries. `cartographer doctor` reports the malformed block.
var ErrCorruptedBlock = errors.New("marker block is corrupted (not exactly one begin followed by one end)")

func corruptedError(path string) error {
	return fmt.Errorf("%s: %w; left untouched, fix the markers by hand (`cartographer doctor` lists the file)", path, ErrCorruptedBlock)
}

// Outcome says what ReplaceBlock found.
type Outcome int

const (
	// Absent: neither marker appears on a line of its own.
	Absent Outcome = iota
	// Found: exactly one begin line followed by exactly one end line.
	Found
	// Corrupted: any other combination of marker lines.
	Corrupted
)

// BeginStable is the part of a begin marker that identifies it: everything up
// to the first " — " (em dash). The tail is display text, older versions wrote
// it in a different language.
func BeginStable(begin string) string {
	if i := strings.Index(begin, " — "); i != -1 {
		return begin[:i]
	}
	return begin
}

// ReplaceBlock is the integrity-checked form of ReplaceBetween (#1502). Markers
// are whole lines: a begin line starts (at column 0) with the stable prefix of
// begin, an end line is end, both ignoring trailing blanks and "\r". A marker
// quoted in the middle of a line is text. Exactly one begin followed by exactly
// one end is Found and the text from the begin line through the end line (plus
// its terminator) is replaced; no marker line at all is Absent; every other
// shape is Corrupted and content comes back unchanged.
func ReplaceBlock(content, begin, end, replacement string) (string, Outcome) {
	stable := BeginStable(begin)
	var beginAt, endStop = -1, -1
	begins, ends := 0, 0
	endBeforeBegin := false
	for off := 0; off < len(content); {
		nl := strings.IndexByte(content[off:], '\n')
		lineEnd := len(content)
		next := len(content)
		if nl >= 0 {
			lineEnd = off + nl
			next = lineEnd + 1
		}
		line := strings.TrimRight(content[off:lineEnd], " \t\r")
		switch {
		case strings.HasPrefix(line, stable):
			begins++
			if begins == 1 {
				beginAt = off
			}
		case line == end:
			ends++
			if begins == 0 {
				endBeforeBegin = true
			}
			if ends == 1 {
				endStop = next
			}
		}
		off = next
	}
	switch {
	case begins == 0 && ends == 0:
		return content, Absent
	case begins != 1 || ends != 1 || endBeforeBegin:
		return content, Corrupted
	}
	return content[:beginAt] + replacement + content[endStop:], Found
}

// CheckFile reports ErrCorruptedBlock (wrapped) when the file at path holds a
// corrupted block, and nil when it is absent, unreadable, or sound. For a
// caller that must not edit the file before Write would refuse it anyway.
func CheckFile(path, begin, end string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if _, o := ReplaceBlock(string(data), begin, end, ""); o == Corrupted {
		return corruptedError(path)
	}
	return nil
}

// ReplaceBetween is the legacy locator with no integrity check (first begin,
// first end after it, anywhere in a line); new writers use ReplaceBlock. It
// returns content with the text between begin and end (markers
// included, plus one trailing newline if present right after end) replaced by
// replacement, and true — or content unchanged and false if begin/end don't
// both appear, in order.
//
// The begin marker is recognized by its stable prefix: everything up to the
// first " — " (em dash). The human-readable tail after the dash is display
// text, not marker identity — older versions wrote it in a different language,
// and matching the full current string would miss their blocks and duplicate
// the block on the next write.
func ReplaceBetween(content, begin, end, replacement string) (string, bool) {
	stable := begin
	if i := strings.Index(begin, " — "); i != -1 {
		stable = begin[:i]
	}
	beginIdx := strings.Index(content, stable)
	if beginIdx == -1 {
		return content, false
	}
	endMarkerIdx := strings.Index(content[beginIdx:], end)
	if endMarkerIdx == -1 {
		return content, false
	}
	endIdx := beginIdx + endMarkerIdx + len(end)
	// Consume a single trailing newline after the end marker so repeated
	// writes/removals don't accumulate blank lines.
	if endIdx < len(content) && content[endIdx] == '\n' {
		endIdx++
	}
	return content[:beginIdx] + replacement + content[endIdx:], true
}

// appendBlock appends block at the end of content, separated by a blank line.
// content's trailing newlines are normalized to none before the separator, so
// repeated appends stay idempotent regardless of the file's original trailing
// newline count.
func appendBlock(content, block string) string {
	trimmed := strings.TrimRight(content, "\n")
	if trimmed == "" {
		return block
	}
	return trimmed + "\n\n" + block
}

// Upsert puts block (the whole block, markers and a final newline included) into
// content: it replaces the one begin..end block when there is one, appends it
// when there is none, and returns ErrCorruptedBlock (content untouched) for any
// other shape. Unlike the file-based Write it adds no blank separator line: a
// missing trailing newline is added once and the block follows directly, so
// ReplaceBlock(..., "") restores the original text byte for byte when it ended
// with a newline (every crontab does). A text that did not gets that one
// newline: removal cannot know whether the newline was the user's or ours, and
// guessing would eat a user's own blank-line ending.
func Upsert(content, begin, end, block string) (string, error) {
	out, o := ReplaceBlock(content, begin, end, block)
	switch o {
	case Found:
		return out, nil
	case Corrupted:
		return content, ErrCorruptedBlock
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + block, nil
}
