package changelog

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// NotesLimit is the part size `gen -split` is called with: GitHub refuses a PR
// body or a comment over 65536 characters, and counts them as runes. The margin
// absorbs a different count of the same text.
const NotesLimit = 60000

// ContinuedNote is the last line of the first part when there are more.
const ContinuedNote = "The release notes continue in the comments below."

// The frame release-please gives a release PR body (src/util/pull-request-body.ts,
// PullRequestBody.toString) and needs back to parse it on merge: its
// splitBody takes the notes between the first and the last line that is exactly
// `---`, and without one it finds no release to create.
const (
	prBodyHeader = ":robot: I have created a release *beep* *boop*"
	prBodyFooter = "This PR was generated with [Release Please](https://github.com/googleapis/release-please). See [documentation](https://github.com/googleapis/release-please#release-please)."
	prDelimiter  = "---"
)

// ReleasePRBody frames part 1 as release-please writes a release PR body.
func ReleasePRBody(part1 string) string {
	return prBodyHeader + "\n" + prDelimiter + "\n\n\n" + strings.TrimRight(part1, "\n") + "\n\n" + prDelimiter + "\n" + prBodyFooter
}

// PartMarker is the hidden first line of part n (n >= 2): the release PR job
// finds its own comment of that part by it.
func PartMarker(n int) string {
	return "<!-- release-notes-part:" + strconv.Itoa(n) + " -->"
}

// Split cuts release notes (one `## [x.y.z]` section, already grouped by area)
// into parts of at most limit runes, markers and the continued note included.
// It cuts only before a `### ` heading; a category over the limit is cut before
// a `* ` entry and its heading is repeated, "(continued)", at the top of the
// next part. The notes must not hold a line that is exactly `---`: it would
// end the notes for release-please. Part 1 (framed by ReleasePRBody, which the
// limit counts) keeps the version heading, ends with ContinuedNote when
// there are more parts, and each later part starts with PartMarker(n).
//
// It is an error when one entry, or the text before the first category, does not
// fit a part: no cut can help.
func Split(notes string, limit int) ([]string, error) {
	lines := strings.Split(strings.TrimRight(notes, "\n"), "\n")
	for i, l := range lines {
		if strings.TrimRight(l, "\r") == prDelimiter {
			return nil, fmt.Errorf("line %d of the notes is a bare %s: release-please would end the notes there", i+1, prDelimiter)
		}
	}
	var units [][]string
	for _, l := range lines {
		if strings.HasPrefix(l, "### ") || len(units) == 0 {
			units = append(units, nil)
		}
		units[len(units)-1] = append(units[len(units)-1], l)
	}
	// Room for the longest overhead: the continued note on part 1, a marker
	// (three digits) and the blank line after it on the others.
	first := limit - runeLen(ContinuedNote) - 3 - runeLen(ReleasePRBody(""))
	later := limit - runeLen(PartMarker(999)) - 3
	room := min(first, later)
	var cut [][]string
	for i, u := range units {
		pieces, err := cutUnit(u, room, i == 0)
		if err != nil {
			return nil, err
		}
		cut = append(cut, pieces...)
	}
	var parts [][]string
	var cur []string
	size := 0
	for _, u := range cut {
		text := unitText(u)
		budget := later
		if len(parts) == 0 {
			budget = first
		}
		if len(cur) > 0 && size+2+runeLen(text) > budget {
			parts = append(parts, cur)
			cur, size = nil, 0
		}
		if len(cur) > 0 {
			size += 2
		}
		cur = append(cur, text)
		size += runeLen(text)
	}
	parts = append(parts, cur)
	out := make([]string, len(parts))
	for i, p := range parts {
		s := strings.Join(p, "\n\n")
		switch {
		case i == 0 && len(parts) > 1:
			s += "\n\n" + ContinuedNote
		case i > 0:
			s = PartMarker(i+1) + "\n\n" + s
		}
		out[i] = s + "\n"
	}
	return out, nil
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// unitText is a unit's lines without the blank lines that end it.
func unitText(lines []string) string {
	end := len(lines)
	for end > 0 && isBlank(lines[end-1]) {
		end--
	}
	return strings.Join(lines[:end], "\n")
}

// cutUnit returns u whole when it fits room, else cut before `* ` entries, each
// piece after the first under the repeated heading. The head unit (the version
// heading) is never cut.
func cutUnit(u []string, room int, head bool) ([][]string, error) {
	if runeLen(unitText(u)) <= room {
		return [][]string{u}, nil
	}
	if head || !strings.HasPrefix(u[0], "### ") {
		return nil, fmt.Errorf("the text before the first category is over %d characters", room)
	}
	heading := u[0] + " (continued)"
	if strings.HasSuffix(u[0], " (continued)") {
		heading = u[0]
	}
	var entries [][]string // the heading line alone comes first
	for _, l := range u[1:] {
		if strings.HasPrefix(l, "* ") || len(entries) == 0 {
			entries = append(entries, nil)
		}
		entries[len(entries)-1] = append(entries[len(entries)-1], l)
	}
	var pieces [][]string
	cur, n := []string{u[0]}, 0 // n entries in cur
	for _, e := range entries {
		next := append(append([]string(nil), cur...), e...)
		if n > 0 && runeLen(unitText(next)) > room {
			pieces = append(pieces, cur)
			cur, n = []string{heading, ""}, 0
			next = append(append([]string(nil), cur...), e...)
		}
		if runeLen(unitText(next)) > room {
			return nil, fmt.Errorf("one entry of %q is over %d characters", strings.TrimPrefix(u[0], "### "), room)
		}
		cur = next
		n++
	}
	return append(pieces, cur), nil
}
