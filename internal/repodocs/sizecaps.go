package repodocs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// GrandfatheredPages maps a docs/*.md page over MaxDocPageLines to the ceiling it
// may not pass: its size when the cap came in plus 10%, rounded up to 50. A
// ceiling is only ever lowered; raising one is a visible edit of this map, in
// review, the same stance as MaxRootChars. Each page has a `process` issue that
// asks for its split.
var GrandfatheredPages = map[string]int{
	"docs/configurator.md": 2250,
	"docs/deployment.md":   2450,
	"docs/sync.md":         1200,
	"docs/testing.md":      1600,
}

// GrandfatheredBlocks maps the key of a CONTRIBUTING.md block over
// MaxContributingBlockChars (its nearest heading and its first 40 characters) to its
// ceiling, by the same rule as GrandfatheredPages.
//
// Not capped: the length of one line of a docs page. A page keeps a paragraph on a
// line and many do pass 2.000 characters; the gap is stated in the decision.
var GrandfatheredBlocks = map[string]int{
	"Roles: contributor and maintainer | The repository ships its subagents in `.": 2300,
	"Releases | - **The release candidate.** `action: rc":                          2200,
}

// generatedPairs is every generated block a docs page, CONTRIBUTING.md or AGENTS.md
// carries: begin marker prefix and end marker. The size caps measure the text
// without them. StripGeneratedBlocks is narrower on purpose (the docs KB reads it).
var generatedPairs = [][2]string{
	{CodeMapBegin, CodeMapEnd},
	{IndexBegin, IndexEnd},
	{RunsWhereBegin, RunsWhereEnd},
	{"<!-- lint:catalogue:begin", "<!-- lint:catalogue:end -->"},
	{"<!-- lint:observations:begin", "<!-- lint:observations:end -->"},
}

// stripAllGenerated removes every block of generatedPairs, markers included.
func stripAllGenerated(content string) string {
	for _, pair := range generatedPairs {
		for {
			i := strings.Index(content, pair[0])
			j := strings.Index(content, pair[1])
			if i < 0 || j < i {
				break
			}
			content = content[:i] + content[j+len(pair[1]):]
		}
	}
	return content
}

// DocPageLines counts the lines of a docs page, generated blocks removed.
func DocPageLines(content string) int {
	return strings.Count(stripAllGenerated(content), "\n")
}

// DocPageProblems judges pages (path to content) against the cap and the
// grandfather map: one message per page over its cap or ceiling and per listed
// page that is back under the cap.
func DocPageProblems(pages map[string]string, grand map[string]int) []string {
	var out []string
	names := make([]string, 0, len(pages))
	for n := range pages {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		lines := DocPageLines(pages[name])
		ceiling, listed := grand[name]
		switch {
		case !listed && lines > MaxDocPageLines:
			out = append(out, fmt.Sprintf("%s is %d lines, over the %d-line cap: split it: move the reference or the how-to half to a page of its own and leave a pointer", name, lines, MaxDocPageLines))
		case listed && lines > ceiling:
			out = append(out, fmt.Sprintf("%s is %d lines, over its ceiling of %d: split it: move the reference or the how-to half to a page of its own and leave a pointer", name, lines, ceiling))
		case listed && lines <= MaxDocPageLines:
			out = append(out, fmt.Sprintf("%s is %d lines, at or under the %d-line cap: remove it from GrandfatheredPages", name, lines, MaxDocPageLines))
		}
	}
	for name := range grand {
		if _, ok := pages[name]; !ok {
			out = append(out, fmt.Sprintf("GrandfatheredPages lists %s, which is not a docs page: remove it", name))
		}
	}
	sort.Strings(out)
	return out
}

// DocPages reads every docs/*.md page but the generated decision index.
func DocPages(root string) (map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		return nil, err
	}
	pages := map[string]string{}
	for _, f := range files {
		rel := "docs/" + filepath.Base(f)
		if rel == "docs/decisions.md" {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		pages[rel] = string(data)
	}
	return pages, nil
}

// TextBlock is one paragraph or list item of a Markdown file.
type TextBlock struct {
	Line  int    // first line, 1-based
	Chars int    // characters of its text
	Key   string // "<nearest heading> | <first 40 characters>", the key of GrandfatheredBlocks
	Head  string // its first 40 characters, for messages
}

var (
	listItem     = regexp.MustCompile(`^\s*([-*+]|[0-9]+\.)\s`)
	headingRow   = regexp.MustCompile(`^#{1,6}\s`)
	capFenceOpen = regexp.MustCompile("^(`{3,}|~{3,})")
)

// Blocks splits Markdown into paragraphs and list items. A nested item is a block
// of its own, so a parent counts without its children. A fence (three or more
// backticks or tildes, closed by at least as many of the same character) is
// skipped; a real heading (`# ` to `###### `) or a table row (starts and ends with
// `|`) and a blank line end a block and are not counted. A wrapped line that only
// starts with `#` or `|` continues its paragraph. The key of a block is the text of
// the nearest heading above it and the first 40 characters of its text, so a
// paragraph added above it does not change it.
func Blocks(content string) []TextBlock {
	var out []TextBlock
	var cur []string
	start := 0
	heading := ""
	flush := func() {
		if len(cur) == 0 {
			return
		}
		text := strings.TrimSpace(strings.Join(cur, "\n"))
		head := []rune(text)
		if len(head) > 40 {
			head = head[:40]
		}
		out = append(out, TextBlock{Line: start, Chars: len([]rune(text)),
			Key: heading + " | " + string(head), Head: string(head)})
		cur = nil
	}
	fence := ""
	for i, l := range strings.Split(content, "\n") {
		t := strings.TrimSpace(l)
		if fence != "" {
			if strings.HasPrefix(t, fence[:1]) && len(strings.TrimLeft(t, fence[:1])) == 0 && len(t) >= len(fence) {
				fence = ""
			}
			continue
		}
		if m := capFenceOpen.FindString(t); m != "" {
			flush()
			fence = m
			continue
		}
		switch {
		case t == "":
			flush()
		case headingRow.MatchString(t):
			flush()
			heading = strings.TrimSpace(strings.TrimLeft(t, "# "))
		case strings.HasPrefix(t, "|") && strings.HasSuffix(t, "|"):
			flush()
		case listItem.MatchString(l):
			flush()
			start, cur = i+1, []string{l}
		case len(cur) > 0:
			cur = append(cur, l)
		default:
			start, cur = i+1, []string{l}
		}
	}
	flush()
	return out
}

// BlockProblems judges the blocks of CONTRIBUTING.md like DocPageProblems judges pages.
func BlockProblems(content string, grand map[string]int) []string {
	var out []string
	seen := map[string]bool{}
	for _, b := range Blocks(stripAllGenerated(content)) {
		ceiling, listed := grand[b.Key]
		if listed {
			seen[b.Key] = true
		}
		switch {
		case !listed && b.Chars > MaxContributingBlockChars:
			out = append(out, fmt.Sprintf("the block at line %d (%q, key %q) is %d characters, over the %d-character cap: split it: one clause per paragraph or item", b.Line, b.Head, b.Key, b.Chars, MaxContributingBlockChars))
		case listed && b.Chars > ceiling:
			out = append(out, fmt.Sprintf("the block at line %d (%q, key %q) is %d characters, over its ceiling of %d: split it: one clause per paragraph or item", b.Line, b.Head, b.Key, b.Chars, ceiling))
		case listed && b.Chars <= MaxContributingBlockChars:
			out = append(out, fmt.Sprintf("the block at line %d (%q, key %q) is %d characters, at or under the %d cap: remove it from GrandfatheredBlocks", b.Line, b.Head, b.Key, b.Chars, MaxContributingBlockChars))
		}
	}
	for k := range grand {
		if !seen[k] {
			out = append(out, fmt.Sprintf("GrandfatheredBlocks lists %q, which is no block: remove it", k))
		}
	}
	sort.Strings(out)
	return out
}
