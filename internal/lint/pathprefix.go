package lint

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// PathPrefixMatch is one occurrence of a declared path prefix in a body.
type PathPrefixMatch struct {
	Start, End int // byte offsets in the original body
	Index      int // position of the prefix in the list given to the matcher
}

// FindPathPrefixes returns, left to right and never overlapping, the
// occurrences in body of the declared prefixes that really start a path (#1503
// WP2). It is the one matcher legacy_path detects and rewrites with, so the
// finding and its repair cannot disagree and the repair converges.
//
// Fenced blocks and code spans are skipped (the body is searched masked, the
// offsets are the original's), and so is a URL. An occurrence must start a
// path: at the start of a line, or after whitespace or one of ( [ " ' and
// backtick, never after a word character, "/" or ":". A prefix not ending in a
// separator must also end a path segment: followed by "/", the end of the
// text, whitespace or punctuation, so "/srv/app" is not "/srv/application" nor
// "/srv/app-old". At one position the longest prefix wins.
func FindPathPrefixes(body string, prefixes []string) []PathPrefixMatch {
	order := make([]int, 0, len(prefixes))
	for i, p := range prefixes {
		if p != "" {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return len(prefixes[order[a]]) > len(prefixes[order[b]]) })
	if len(order) == 0 {
		return nil
	}
	masked := kb.MaskCodeSpans(body)
	urls := urlRe.FindAllStringIndex(masked, -1)
	inURL := func(pos int) bool {
		for _, u := range urls {
			if pos >= u[0] && pos < u[1] {
				return true
			}
		}
		return false
	}
	var out []PathPrefixMatch
	for i := 0; i < len(masked); {
		matched := false
		if startsPath(masked, i) && !inURL(i) {
			for _, idx := range order {
				p := prefixes[idx]
				if !strings.HasPrefix(masked[i:], p) || !endsPathSegment(masked, i+len(p), p) {
					continue
				}
				out = append(out, PathPrefixMatch{Start: i, End: i + len(p), Index: idx})
				i += len(p)
				matched = true
				break
			}
		}
		if !matched {
			i++
		}
	}
	return out
}

// RewritePathPrefixes replaces the occurrences FindPathPrefixes finds, the
// i-th prefix by to[i], and returns the new body.
func RewritePathPrefixes(body string, prefixes, to []string) string {
	var b strings.Builder
	last := 0
	for _, m := range FindPathPrefixes(body, prefixes) {
		b.WriteString(body[last:m.Start])
		b.WriteString(to[m.Index])
		last = m.End
	}
	b.WriteString(body[last:])
	return b.String()
}

func startsPath(s string, i int) bool {
	if i == 0 {
		return true
	}
	switch s[i-1] {
	case '\n', ' ', '\t', '\r', '(', '[', '`', '"', '\'':
		return true
	}
	return false
}

func endsPathSegment(s string, end int, prefix string) bool {
	if last := prefix[len(prefix)-1]; last == '/' || last == '\\' {
		return true
	}
	if end >= len(s) {
		return true
	}
	switch s[end] {
	case '/', '\\', ' ', '\t', '\n', '\r', ',', ';', ':', '!', '?', ')', ']', '}', '"', '\'', '`':
		return true
	case '.':
		return end+1 >= len(s) || unicode.IsSpace(firstRune(s[end+1:]))
	}
	return false
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}
