package docskb

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// The scanner below knows just enough markdown to cut a page at its headings:
// ATX headings outside fenced blocks. It is not a markdown parser and must not
// grow into one (no dependency is added for it, docs/conventions.md).

var (
	headingRe = regexp.MustCompile(`^(#{1,6})[ \t]+(\S.*?)[ \t]*$`)
	linkRe    = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	nonSlugRe = regexp.MustCompile(`[^a-z0-9]+`)
	tagRe     = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
)

// fence tracks whether a line sits inside a fenced code block.
type fence struct {
	open byte // '`' or '~' while inside a fence
	n    int  // length of the opening run
}

// inside reports whether line belongs to a fenced block, opening and closing
// fence lines included, and advances the state.
func (f *fence) inside(line string) bool {
	t := strings.TrimLeft(line, " \t")
	if len(t) < 3 || (t[0] != '`' && t[0] != '~') {
		return f.open != 0
	}
	run := 0
	for run < len(t) && t[run] == t[0] {
		run++
	}
	if run < 3 {
		return f.open != 0
	}
	switch {
	case f.open == 0:
		f.open, f.n = t[0], run
		return true
	case t[0] == f.open && run >= f.n && strings.TrimSpace(t[run:]) == "":
		f.open, f.n = 0, 0
		return true
	}
	return true
}

// node is a heading and what sits under it: its own lines up to the first
// sub-heading, then its sub-headings.
type node struct {
	level  int
	text   string // the heading as written, without the #s
	plain  string // text with inline markup removed
	anchor string // GitHub anchor, unique in the page
	ord    int    // 1-based position among the page's headings
	own    []string
	kids   []*node
}

// parsed is a page cut at its headings.
type parsed struct {
	title string   // the first H1, "" when there is none
	lead  []string // everything before the first H2, the H1 line excluded
	h2    []*node
	nodes []*node // every heading below the H1 in document order
}

// scan cuts lines at their headings. A heading below the H1 attaches to the
// nearest shallower one, so a skipped level does not lose its parent.
func scan(lines []string) parsed {
	var p parsed
	var stack []*node
	seen := map[string]int{}
	var f fence
	for _, line := range lines {
		if !f.inside(line) {
			if m := headingRe.FindStringSubmatch(line); m != nil {
				level, text := len(m[1]), m[2]
				if level == 1 && p.title == "" && len(p.nodes) == 0 {
					p.title = plainText(text)
					continue
				}
				if level >= 2 {
					n := &node{level: level, text: text, plain: plainText(text), ord: len(p.nodes) + 1}
					n.anchor = githubAnchor(n.plain)
					if k := seen[n.anchor]; k > 0 {
						seen[n.anchor] = k + 1
						n.anchor += "-" + strconv.Itoa(k)
					} else {
						seen[n.anchor] = 1
					}
					for len(stack) > 0 && stack[len(stack)-1].level >= level {
						stack = stack[:len(stack)-1]
					}
					if len(stack) > 0 {
						stack[len(stack)-1].kids = append(stack[len(stack)-1].kids, n)
					} else {
						p.h2 = append(p.h2, n)
					}
					stack = append(stack, n)
					p.nodes = append(p.nodes, n)
					continue
				}
			}
		}
		switch {
		case len(stack) > 0:
			stack[len(stack)-1].own = append(stack[len(stack)-1].own, line)
		default:
			p.lead = append(p.lead, line)
		}
	}
	return p
}

// plainText drops the inline markup of a heading or a sentence: links keep
// their label, code spans, emphasis and HTML tags go.
func plainText(s string) string {
	s = linkRe.ReplaceAllString(s, "$1")
	// HTML tags go, but a code span is literal: `<subcommand>` stays, as it does
	// in the anchor GitHub gives the heading.
	parts := strings.Split(s, "`")
	for i := 0; i < len(parts); i += 2 {
		parts[i] = tagRe.ReplaceAllString(parts[i], "")
	}
	s = strings.Join(parts, "`")
	s = strings.NewReplacer("`", "", "**", "", "__", "", "*", "").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// githubAnchor is the id GitHub gives a heading (before its -1, -2 suffix):
// lowercase, punctuation dropped, spaces to hyphens. Relative `#anchor` links in
// the docs are written against it.
func githubAnchor(plain string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(plain) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// slug is the concept-id spelling: ASCII, lowercase, runs of anything else
// collapsed into one hyphen. "Workspace scope (D193)" is workspace-scope-d193.
func slug(s string) string {
	s = strings.Trim(nonSlugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 100 {
		s = strings.Trim(s[:100], "-")
	}
	return s
}

// size is the number of bytes of the section n, sub-sections included.
func size(n *node) int {
	t := len(n.text) + 4
	for _, l := range n.own {
		t += len(l) + 1
	}
	for _, k := range n.kids {
		t += size(k)
	}
	return t
}

// render writes n's body with every sub-heading demoted so that n's own
// heading, replaced by the concept's H1, is the only top level: an H3 inside an
// H2 unit becomes an H2.
func render(n *node) []string {
	out := append([]string(nil), n.own...)
	var walk func(k *node)
	walk = func(k *node) {
		out = append(out, strings.Repeat("#", k.level-n.level+1)+" "+k.text)
		out = append(out, k.own...)
		for _, c := range k.kids {
			walk(c)
		}
	}
	for _, k := range n.kids {
		walk(k)
	}
	return out
}

// firstSentence is the description of a unit: its first paragraph of prose up
// to the first full stop, without markup, at most max characters.
func firstSentence(lines []string, max int) string {
	var para []string
	var f fence
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if f.inside(l) {
			continue
		}
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "|") || strings.HasPrefix(t, "<") || strings.HasPrefix(t, "![") || strings.HasPrefix(t, "---") {
			if len(para) > 0 {
				break
			}
			continue
		}
		para = append(para, strings.TrimLeft(t, "-*+> "))
	}
	s := plainText(strings.Join(para, " "))
	s = strings.TrimSpace(strings.TrimPrefix(s, "Decision."))
	if i := strings.Index(s, ". "); i > 0 {
		s = s[:i+1]
	}
	if r := []rune(s); len(r) > max {
		s = strings.TrimSpace(string(r[:max-1])) + "…"
	}
	return s
}

// blocks cuts body at the blank lines a reader would accept as a break: outside
// fences, never inside a table and never before an indented continuation.
func blocks(lines []string) [][]string {
	var out [][]string
	var cur []string
	var f fence
	for i, l := range lines {
		cur = append(cur, l)
		if f.inside(l) || strings.TrimSpace(l) != "" || i+1 >= len(lines) {
			continue
		}
		next := lines[i+1]
		prev := ""
		if len(cur) > 1 {
			prev = cur[len(cur)-2]
		}
		if strings.HasPrefix(next, " ") || strings.HasPrefix(next, "\t") ||
			(strings.HasPrefix(strings.TrimSpace(prev), "|") && strings.HasPrefix(strings.TrimSpace(next), "|")) {
			continue
		}
		out = append(out, cur)
		cur = nil
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// parts groups blocks greedily into pieces of at most limit bytes. A block
// above the limit stays whole: it is the one thing the split cannot cut.
func parts(lines []string, limit int) [][]string {
	var out [][]string
	var cur []string
	n := 0
	for _, b := range blocks(lines) {
		bn := 0
		for _, l := range b {
			bn += len(l) + 1
		}
		if n > 0 && n+bn > limit {
			out, cur, n = append(out, cur), nil, 0
		}
		cur, n = append(cur, b...), n+bn
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func trimBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
