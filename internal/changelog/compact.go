package changelog

import (
	"regexp"
	"strings"
)

// Compact rewrites one version section of CHANGELOG.md (already grouped by
// area, as Section returns it) as the notes the release PR and the release page
// show: one bullet per commit, references as plain text.
//
//   - A commit release-please lists several times (its header and the
//     conventional lines of its body) yields one bullet. After the area grouping
//     the entries of a commit are sorted by text, so the header cannot be told
//     from a body line: the entry that carries a PR reference is the header,
//     else the first one stays.
//   - A line with no commit (a breaking-change line) is kept once however many
//     times it is repeated verbatim.
//   - `[abcdef0](…/commit/…)` and `[#N](…/issues/N)` become `abcdef0` and `#N`,
//     which GitHub turns into links in a PR body and in release notes, and the
//     references of an entry are one parenthesis: `(#N, abcdef0, closes #M)`.
//   - A heading left with nothing under it is dropped, and blank lines are
//     single.
//
// The order is the section's own: breaking changes, then per area Features and
// Bug Fixes. CHANGELOG.md keeps the full links, and the compact text is
// derived from it each time, never stored.
func Compact(section string) string {
	lines := strings.Split(strings.TrimRight(section, "\n"), "\n")
	keep := keepers(lines)
	var out []string
	seenText := map[string]bool{}
	for i, l := range lines {
		l = strings.TrimRight(l, "\r")
		if !strings.HasPrefix(l, "* ") {
			out = append(out, l)
			continue
		}
		if sha := entrySHA(l); sha != "" {
			if keep[sha] != i {
				continue
			}
		} else if seenText[l] {
			continue
		} else {
			seenText[l] = true
		}
		out = append(out, compactEntry(l))
	}
	return strings.Join(squeeze(dropEmptyHeadings(out)), "\n") + "\n"
}

var (
	entryLink   = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	entryTail   = regexp.MustCompile(`^(.*?)((?: \(\[[^\]]*\]\([^)]*\)\))*)(?:, closes (.*))?$`)
	entrySHARef = regexp.MustCompile(`\(\[[0-9a-f]{7,40}\]\([^)]*/commit/([0-9a-f]{7,40})\)\)`)
	prRef       = regexp.MustCompile(`\(\[#\d+\]\(`)
)

// entrySHA is the full hash an entry's commit link points at, "" when none.
func entrySHA(l string) string {
	m := entrySHARef.FindStringSubmatch(l)
	if m == nil {
		return ""
	}
	return m[1]
}

// keepers returns, for each commit hash, the index of the line that stays: the
// first with a PR reference, else the first.
func keepers(lines []string) map[string]int {
	keep := map[string]int{}
	withPR := map[string]bool{}
	for i, l := range lines {
		if !strings.HasPrefix(l, "* ") {
			continue
		}
		sha := entrySHA(l)
		if sha == "" {
			continue
		}
		pr := prRef.MatchString(l)
		if _, seen := keep[sha]; !seen || pr && !withPR[sha] {
			keep[sha] = i
			withPR[sha] = pr
		}
	}
	return keep
}

// compactEntry turns the links of an entry's first line into plain references.
func compactEntry(l string) string {
	m := entryTail.FindStringSubmatch(l)
	if m == nil {
		return entryLink.ReplaceAllString(l, "$1")
	}
	var refs []string
	for _, r := range entryLink.FindAllStringSubmatch(m[2], -1) {
		refs = append(refs, r[1])
	}
	if m[3] != "" {
		refs = append(refs, "closes "+entryLink.ReplaceAllString(m[3], "$1"))
	}
	if len(refs) == 0 {
		return l
	}
	return m[1] + " (" + strings.Join(refs, ", ") + ")"
}

func headingLevel(l string) int {
	n := 0
	for n < len(l) && l[n] == '#' {
		n++
	}
	if n == 0 || n >= len(l) || l[n] != ' ' {
		return 0
	}
	return n
}

// dropEmptyHeadings removes the `###` and `####` headings with no text under
// them, up to the next heading of the same or a higher level.
func dropEmptyHeadings(lines []string) []string {
	for changed := true; changed; {
		changed = false
		for i, l := range lines {
			lv := headingLevel(l)
			if lv < 3 {
				continue
			}
			empty := true
			for _, n := range lines[i+1:] {
				if nl := headingLevel(n); nl > 0 && nl <= lv {
					break
				}
				if !isBlank(n) {
					empty = false
					break
				}
			}
			if empty {
				lines = append(lines[:i:i], lines[i+1:]...)
				changed = true
				break
			}
		}
	}
	return lines
}

// squeeze makes every run of blank lines one.
func squeeze(lines []string) []string {
	var out []string
	for _, l := range lines {
		if isBlank(l) && len(out) > 0 && isBlank(out[len(out)-1]) {
			continue
		}
		out = append(out, l)
	}
	for len(out) > 0 && isBlank(out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	return out
}
