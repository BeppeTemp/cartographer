// Package okf implements the Open Knowledge Format primitives for the Agentic Wiki.
// Handles concept IDs, raw frontmatter, section extraction, and normalized content-hash.
package okf

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path"
	"strings"
)

// Exported errors used by other packages.
var (
	ErrNotFound       = errors.New("concept not found")
	ErrStaleWrite     = errors.New("stale write: content-hash mismatch")
	ErrInvalidPath    = errors.New("invalid path")
	ErrInvalidConcept = errors.New("invalid concept")
)

// ConceptID identifies a concept as a path relative to the KB root without the .md extension.
type ConceptID string

// reserved names at every hierarchy level.
var reservedNames = map[string]bool{
	"index.md":    true,
	"log.md":      true,
	"_map.md":     true,
	"_archive.md": true, // legacy Map descriptor (D77 WP1), still read-compat
	"AGENTS.md":   true,
}

// IsReserved returns true if the file name is reserved (index.md, log.md, _map.md, _archive.md, AGENTS.md).
func IsReserved(name string) bool {
	return reservedNames[name]
}

// isKebabSegment checks that a single path segment is valid kebab-case.
// Allowed characters: lowercase letters, digits, hyphens, dots (for extensions),
// underscores (for _archive.md), and the "." prefix for hidden directories.
func isKebabSegment(seg string) bool {
	if seg == "" || seg == "." || seg == ".." {
		return false
	}
	for _, r := range seg {
		if (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '.' || r == '_' {
			continue
		}
		return false
	}
	return true
}

// PathToID converts a relative path (e.g. "maintenance/cert-rotation.md")
// to the corresponding ConceptID (e.g. "maintenance/cert-rotation").
// Returns ErrInvalidPath if the path contains non-kebab-case segments.
func PathToID(relPath string) (ConceptID, error) {
	relPath = path.Clean(relPath)
	if strings.HasPrefix(relPath, "/") || strings.HasPrefix(relPath, "..") {
		return "", ErrInvalidPath
	}

	id := strings.TrimSuffix(relPath, ".md")
	segments := strings.Split(id, "/")
	for _, seg := range segments {
		if !isKebabSegment(seg) {
			return "", fmt.Errorf("%w: non-kebab-case segment %q", ErrInvalidPath, seg)
		}
	}
	return ConceptID(id), nil
}

// IDToPath converts a ConceptID to the relative path of the corresponding .md file.
func IDToPath(id ConceptID) string {
	return string(id) + ".md"
}

// SplitFrontmatter separates the raw YAML frontmatter block from the markdown body.
// The frontmatter is delimited by "---" lines (first and second occurrence).
// The frontmatter is returned as RAW text without YAML parsing.
func SplitFrontmatter(content string) (frontmatterRaw string, body string, hasFrontmatter bool) {
	content = strings.ReplaceAll(content, "\r\n", "\n")

	if !strings.HasPrefix(content, "---\n") && content != "---" {
		return "", content, false
	}

	// Find the closing delimiter after the first "---" line.
	rest := content[4:] // skip "---\n"
	idx := strings.Index(rest, "\n---\n")
	if idx == -1 {
		// Check if it ends with "\n---" without a trailing newline.
		if strings.HasSuffix(rest, "\n---") {
			fm := rest[:len(rest)-4]
			return fm, "", true
		}
		return "", content, false
	}

	fm := rest[:idx]
	body = rest[idx+5:] // skip "\n---\n"
	return fm, body, true
}

// ExtractSection extracts the content under a markdown heading (matched by exact text)
// up to the next heading of equal or higher level.
// Returns the extracted content and true if found.
func ExtractSection(body string, heading string) (string, bool) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(body, "\n")

	// Determine the level of the target heading (number of leading '#').
	targetLevel := 0
	targetTitle := ""
	for _, r := range heading {
		if r == '#' {
			targetLevel++
		} else {
			break
		}
	}
	if targetLevel == 0 {
		// heading without '#': match at any level
		targetTitle = strings.TrimSpace(heading)
	} else {
		targetTitle = strings.TrimSpace(heading[targetLevel:])
	}

	headingEligible := headingEligibleLines(lines)

	startIdx := -1
	foundLevel := 0
	for i, line := range lines {
		if !headingEligible[i] {
			continue
		}
		level, title := parseHeading(line)
		if level == 0 {
			continue
		}
		if startIdx == -1 {
			match := false
			if targetLevel == 0 {
				match = title == targetTitle
			} else {
				match = level == targetLevel && title == targetTitle
			}
			if match {
				startIdx = i + 1
				foundLevel = level
			}
		} else {
			// Stop at the next heading of equal or higher level.
			if level <= foundLevel {
				section := strings.Join(lines[startIdx:i], "\n")
				return strings.TrimSpace(section), true
			}
		}
	}

	if startIdx == -1 {
		return "", false
	}
	section := strings.Join(lines[startIdx:], "\n")
	return strings.TrimSpace(section), true
}

// Heading describes one markdown heading found by ListHeadings, along with
// the byte size of the section it introduces.
type Heading struct {
	Level int
	Title string
	Bytes int
}

// ListHeadings returns every heading in body, in document order, with the
// byte size of each section — from the heading (excluded) up to the next
// heading of equal or higher level, or the end of the body. Same section
// boundary semantics as ExtractSection.
func ListHeadings(body string) []Heading {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(body, "\n")

	type raw struct {
		level    int
		title    string
		startIdx int // index into lines right after the heading line
	}
	headingEligible := headingEligibleLines(lines)

	var raws []raw
	for i, line := range lines {
		if !headingEligible[i] {
			continue
		}
		level, title := parseHeading(line)
		if level == 0 {
			continue
		}
		raws = append(raws, raw{level: level, title: title, startIdx: i + 1})
	}

	headings := make([]Heading, 0, len(raws))
	for i, r := range raws {
		endIdx := len(lines)
		for j := i + 1; j < len(raws); j++ {
			if raws[j].level <= r.level {
				endIdx = raws[j].startIdx - 1
				break
			}
		}
		section := strings.Join(lines[r.startIdx:endIdx], "\n")
		headings = append(headings, Heading{
			Level: r.level,
			Title: r.title,
			Bytes: len(section),
		})
	}
	return headings
}

// fenceMarkers are the recognized code-fence delimiters (pragmatic CommonMark subset).
var fenceMarkers = [...]string{"```", "~~~"}

// headingEligibleLines returns, for each line, whether it may be parsed as a heading.
// Shared by ListHeadings, ExtractSection and SectionHashes so the three consumers agree
// on section boundaries. A line whose trimmed content starts with one of fenceMarkers
// toggles fence state: the opening line (which may carry an info string) and every line
// up to and including the matching closing fence (same marker) are ineligible. An
// unclosed fence extends to the end of the document — the rest of the body is treated
// as fenced content rather than falling back to heading parsing.
func headingEligibleLines(lines []string) []bool {
	eligible := make([]bool, len(lines))
	inFence := false
	fenceMarker := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inFence {
			if strings.HasPrefix(trimmed, fenceMarker) {
				inFence = false
			}
			continue
		}
		if marker := fenceOpenMarker(trimmed); marker != "" {
			inFence = true
			fenceMarker = marker
			continue
		}
		eligible[i] = true
	}
	return eligible
}

// fenceOpenMarker returns the fence marker ("```" or "~~~") if the trimmed line opens
// a code fence, or "" otherwise. The remainder of the line (info string) is ignored.
func fenceOpenMarker(trimmed string) string {
	for _, m := range fenceMarkers {
		if strings.HasPrefix(trimmed, m) {
			return m
		}
	}
	return ""
}

// parseHeading parses a markdown line and returns the heading level (1-6) and title.
// Returns 0 if the line is not a heading.
func parseHeading(line string) (level int, title string) {
	if !strings.HasPrefix(line, "#") {
		return 0, ""
	}
	i := 0
	for i < len(line) && line[i] == '#' {
		i++
	}
	if i > 6 {
		return 0, ""
	}
	if i >= len(line) || line[i] != ' ' {
		return 0, ""
	}
	return i, strings.TrimSpace(line[i+1:])
}

// ContentHash computes the sha256 (hex) of the content normalized per OKF rules:
//   - CRLF -> LF
//   - trailing spaces/tabs stripped per line
//   - trailing blank lines removed
//   - frontmatter: canonical key ordering (via ParseFrontmatter/CanonicalString),
//     falling back to raw frontmatter text if parsing fails
//   - frontmatter: "timestamp:" line always removed to avoid spurious stale_write errors
func ContentHash(content string) string {
	normalized := normalizeContent(content)
	sum := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", sum)
}

// normalizeContent applies the normalizations described in ContentHash.
func normalizeContent(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")

	fm, body, hasFM := SplitFrontmatter(content)

	if hasFM {
		// Normalize frontmatter: use canonical ordering if parsing succeeds,
		// otherwise fall back to the raw text.
		var normalizedFM string
		if parsed, err := ParseFrontmatter(fm); err == nil {
			normalizedFM = parsed.CanonicalString()
		} else {
			normalizedFM = fm
		}
		normalizedFM = removeTimestampLine(normalizedFM)
		normalizedFM = trimTrailingWhitespaceLines(normalizedFM)
		body = trimTrailingWhitespaceLines(body)
		body = strings.TrimRight(body, "\n")
		normalizedFM = strings.TrimRight(normalizedFM, "\n")
		return "---\n" + normalizedFM + "\n---\n" + body
	}

	content = trimTrailingWhitespaceLines(content)
	content = strings.TrimRight(content, "\n")
	return content
}

// SectionHashes computes sha256 hashes (hex) for each heading section of the body.
// Returns a map of heading_text → hash. Also includes "_full" for the hash of the entire content.
// Only first- and second-level sections (# and ##) are considered.
func SectionHashes(content string) map[string]string {
	result := map[string]string{
		"_full": ContentHash(content),
	}

	_, body, _ := SplitFrontmatter(content)
	body = strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(body, "\n")

	type section struct {
		title   string
		level   int
		headIdx int // index of the heading line in lines
	}

	headingEligible := headingEligibleLines(lines)

	var sections []section
	for i, line := range lines {
		if !headingEligible[i] {
			continue
		}
		level, title := parseHeading(line)
		if level == 1 || level == 2 {
			sections = append(sections, section{title: title, level: level, headIdx: i})
		}
	}

	for i, s := range sections {
		// A section ends at the next heading of equal or higher level (numerically ≤).
		endIdx := len(lines)
		for j := i + 1; j < len(sections); j++ {
			if sections[j].level <= s.level {
				endIdx = sections[j].headIdx
				break
			}
		}
		contentLines := lines[s.headIdx+1 : endIdx]
		normalized := normalizeSectionContent(contentLines)
		sum := sha256.Sum256([]byte(normalized))
		result[s.title] = fmt.Sprintf("%x", sum)
	}

	return result
}

// normalizeSectionContent normalizes the lines of a section: strips trailing whitespace
// per line and removes trailing blank lines.
func normalizeSectionContent(lines []string) string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// removeTimestampLine removes all lines starting with "timestamp:" from the text.
func removeTimestampLine(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimLeft(line, " \t"), "timestamp:") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// trimTrailingWhitespaceLines strips spaces and tabs from the end of every line.
func trimTrailingWhitespaceLines(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}
