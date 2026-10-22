package mcpserver

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Limits of the old_string_not_found hint (D342).
const (
	hintMaxLineRunes  = 500
	hintMaxTextBytes  = 200
	ambiguousMaxLines = 10
)

var quoteFolder = strings.NewReplacer("‘", "'", "’", "'", "“", `"`, "”", `"`)

func foldForHint(s string) []rune {
	return []rune(quoteFolder.Replace(strings.ToLower(s)))
}

// editDistance is the rune-level Levenshtein distance.
func editDistance(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// closestLineHint finds the body line nearest to the first non-empty line of
// oldString, and returns "; closest line N: \"...\"" (empty when no line is
// close enough). Both sides are lowercased with typographic quotes mapped to
// ASCII; a body line over hintMaxLineRunes is skipped; the distance must be
// at most max(3, 20% of the old line's length).
func closestLineHint(body, oldString string) string {
	var first string
	for _, l := range strings.Split(oldString, "\n") {
		if strings.TrimSpace(l) != "" {
			first = l
			break
		}
	}
	if first == "" {
		return ""
	}
	want := foldForHint(strings.TrimRight(first, "\r"))
	limit := max(3, len(want)/5)
	bestLine, bestDist := 0, limit+1
	var bestText string
	for i, line := range strings.Split(body, "\n") {
		if utf8.RuneCountInString(line) > hintMaxLineRunes {
			continue
		}
		if d := editDistance(want, foldForHint(line)); d < bestDist {
			bestLine, bestDist, bestText = i+1, d, line
		}
	}
	if bestLine == 0 {
		return ""
	}
	return fmt.Sprintf("; closest line %d: %q", bestLine, cutBytes(bestText, hintMaxTextBytes))
}

// matchLines lists the 1-based body lines where oldString matches, at most
// ambiguousMaxLines, as "a, b, c"; truncated reports that more lines match.
func matchLines(body, oldString string) (lines string, truncated bool) {
	var found []string
	last := 0
	for pos := 0; len(found) <= ambiguousMaxLines; {
		i := strings.Index(body[pos:], oldString)
		if i < 0 {
			break
		}
		line := 1 + strings.Count(body[:pos+i], "\n")
		if line != last {
			found = append(found, fmt.Sprint(line))
			last = line
		}
		pos += i + len(oldString)
	}
	if len(found) > ambiguousMaxLines {
		found, truncated = found[:ambiguousMaxLines], true
	}
	return strings.Join(found, ", "), truncated
}

// ambiguousHint is the old_string_ambiguous message: the true count, the
// lines (flagged when the list is cut), the usual cause when old_string
// starts or ends with a newline, and how to disambiguate.
func ambiguousHint(body, oldString string, count int) string {
	lines, truncated := matchLines(body, oldString)
	where := "lines " + lines
	if truncated {
		where = fmt.Sprintf("first %d lines: %s", ambiguousMaxLines, lines)
	}
	var cause string
	if edge := newlineEdge(oldString); edge != "" {
		cause = fmt.Sprintf("; a %s newline in old_string is the usual reason a snippet matches more places than intended (it also matches the line break of every neighbouring occurrence): drop it", edge)
	}
	return fmt.Sprintf("old_string_ambiguous: old_string matches %d times (%s)%s; extend old_string with the unique text on the line before or after, or pass replace_all=true if all %d occurrences are meant",
		count, where, cause, count)
}

// newlineEdge names the end(s) of s that are a newline: "leading", "trailing",
// "leading or trailing" when both, "" when neither.
func newlineEdge(s string) string {
	lead, trail := strings.HasPrefix(s, "\n"), strings.HasSuffix(s, "\n")
	switch {
	case lead && trail:
		return "leading or trailing"
	case lead:
		return "leading"
	case trail:
		return "trailing"
	}
	return ""
}

// editChainNote is the clause a failed edit i of an edits array carries when
// it was matched against the result of earlier edits rather than the stored
// body. It is appended by the callers, which own the index: applyPatchEdit
// stays index-agnostic. i is 1-based.
func editChainNote(i int) string {
	if i <= 1 {
		return ""
	}
	return fmt.Sprintf("; matched against the body after edits 1..%d, not the stored one; none of them was written", i-1)
}
