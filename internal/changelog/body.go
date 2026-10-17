package changelog

import (
	"regexp"
	"strings"
)

// WrapMessage is what the pr-title job says about a wrapped conventional line.
const WrapMessage = "a conventional line in the body must be one unwrapped line: release-please keeps only its first line"

var (
	conventionalLine = regexp.MustCompile(`^(feat|fix|perf|revert)(\([a-z_-]+\))?!?: `)
	listItem         = regexp.MustCompile(`^\s*([*+-]|\d+[.)])\s`)
	heading          = regexp.MustCompile(`^#{1,6}(\s|$)`) // not "#123": an issue reference is a continuation
)

// WrappedLines returns the 1-based numbers of the lines of a PR body that look
// like a conventional commit header and are continued by the line below.
//
// A squash merge puts the PR body in the commit message, and release-please
// splits that message at every line that parses as a conventional header and
// keeps only that first physical line as the changelog entry. A header hard
// wrapped at 72 columns therefore loses the tail of its sentence (the 0.24.0
// and 0.25.0 changelogs have a dozen entries cut mid-sentence), and nothing
// downstream can tell. Hence this check on the PR, before the squash.
//
// A header may be followed by a blank line, another header, a list item or a
// heading; anything else is a continuation.
func WrappedLines(body string) []int {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var out []int
	for i := 0; i+1 < len(lines); i++ {
		if !conventionalLine.MatchString(lines[i]) {
			continue
		}
		next := lines[i+1]
		if strings.TrimSpace(next) == "" ||
			conventionalLine.MatchString(next) ||
			heading.MatchString(next) ||
			listItem.MatchString(next) {
			continue
		}
		out = append(out, i+1)
	}
	return out
}
