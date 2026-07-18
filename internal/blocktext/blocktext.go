// Package blocktext implements the marker-delimited "managed block" primitive
// used by every Cartographer feature that must own a slice of a hand-curated
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
	"os"
	"path/filepath"
	"strings"
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
func Write(path, begin, end, body string) error {
	block := begin + "\n" + strings.TrimRight(body, "\n") + "\n" + end + "\n"

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(block), 0o644)
	}
	if err != nil {
		return err
	}

	content := string(data)
	if replaced, ok := ReplaceBetween(content, begin, end, block); ok {
		return os.WriteFile(path, []byte(replaced), 0o644)
	}
	return os.WriteFile(path, []byte(appendBlock(content, block)), 0o644)
}

// Remove strips the block delimited by begin/end (markers included) from the
// file at path, if present. If that leaves the file empty or whitespace-only,
// the file itself is removed (covers a file dedicated to a single block).
// Returns whether a block was found (and, unless dryRun, removed); (false,
// nil) if the file doesn't exist or doesn't contain the markers. If dryRun is
// true, computes and returns what would happen without writing anything.
func Remove(path, begin, end string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	content := string(data)
	stripped, ok := ReplaceBetween(content, begin, end, "")
	if !ok || dryRun {
		return ok, nil
	}
	if strings.TrimSpace(stripped) == "" {
		return true, os.Remove(path)
	}
	return true, os.WriteFile(path, []byte(stripped), 0o644)
}

// ReplaceBetween returns content with the text between begin and end (markers
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
