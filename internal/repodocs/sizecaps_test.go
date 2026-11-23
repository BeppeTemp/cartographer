package repodocs

import (
	"reflect"
	"strings"
	"testing"
)

func lines(n int) string { return strings.Repeat("x\n", n) }

// AC-P5-13: a page at its ceiling that grows fails with the split message.
func TestDocPageProblems(t *testing.T) {
	grand := map[string]int{"page-old": 1100}
	cases := []struct {
		name  string
		pages map[string]string
		grand map[string]int
		want  string
	}{
		{"within the cap", map[string]string{"page-a": lines(1000)}, nil, ""},
		{"new page over the cap", map[string]string{"page-a": lines(1001)}, nil, "split it"},
		{"listed, within ceiling", map[string]string{"page-old": lines(1100)}, grand, ""},
		{"listed, 200 lines added past the ceiling", map[string]string{"page-old": lines(1300)}, grand, "over its ceiling of 1100: split it"},
		{"listed, no longer over", map[string]string{"page-old": lines(900)}, grand, "remove it from GrandfatheredPages"},
		{"listed, page gone", map[string]string{}, grand, "which is not a docs page"},
		{"generated block not counted", map[string]string{"page-a": lines(990) + RunsWhereBegin + "\n" + lines(50) + RunsWhereEnd + "\n"}, nil, ""},
	}
	for _, c := range cases {
		got := strings.Join(DocPageProblems(c.pages, c.grand), "\n")
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// AC-P5-14: a list item of 1,600 characters fails; a fence, a table and nested items do not count.
func TestBlockProblems(t *testing.T) {
	long := strings.Repeat("a", 1600)
	grand := map[string]int{"Heading | This paragraph aaaaaaaaaaaaaaaaaaaaaaaaa": 1800}
	cases := []struct {
		name, text string
		grand      map[string]int
		want       string
	}{
		{"short items", "- a\n- b\n", nil, ""},
		{"long item", "- " + long + "\n", nil, "over the 1500-character cap"},
		{"fence and table skipped", "```\n" + long + "\n```\n\n| " + long + " |\n", nil, ""},
		{"parent without nested", "- parent\n  - " + strings.Repeat("b", 1000) + "\n  - " + strings.Repeat("c", 1000) + "\n", nil, ""},
		{"continuation counts", "- " + strings.Repeat("a", 800) + "\n  " + strings.Repeat("b", 800) + "\n", nil, "over the 1500"},
		{"listed within ceiling", "## Heading\n\nThis paragraph " + long + "\n", grand, ""},
		{"listed over ceiling", "## Heading\n\nThis paragraph " + strings.Repeat("a", 1900) + "\n", grand, "over its ceiling of 1800"},
		{"listed no longer over", "## Heading\n\nThis paragraph aaaaaaaaaaaaaaaaaaaaaaaaa\n", grand, "remove it from GrandfatheredBlocks"},
		{"listed, absent", "x\n", grand, "is no block"},
		{"wrapped line starting with # or | continues", "- " + strings.Repeat("a", 700) + "\n#tag " + strings.Repeat("b", 400) + "\n| " + strings.Repeat("c", 400) + "\n", nil, "over the 1500"},
		{"four-backtick fence holds a three-backtick line", "````\n```\n" + long + "\n```\n````\n", nil, ""},
		{"key is the heading and the text start", "## H\n\nshort\n\n" + long + "\n", nil, `key "H | aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`},
		{"a paragraph added above a listed block changes nothing", "## Heading\n\nA new short paragraph.\n\nThis paragraph " + long + "\n", grand, ""},
	}
	for _, c := range cases {
		got := strings.Join(BlockProblems(c.text, c.grand), "\n")
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// The real tree passes both caps.
func TestDocSizeCaps(t *testing.T) {
	root := repoRoot(t)
	pages, err := DocPages(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) == 0 {
		t.Fatal("no docs page read: the cap checks nothing")
	}
	for _, p := range DocPageProblems(pages, GrandfatheredPages) {
		t.Error(p)
	}
	for _, p := range BlockProblems(readFile(t, root, "CONTRIBUTING.md"), GrandfatheredBlocks) {
		t.Error(p)
	}
}

// The lists only shrink: the maps hold exactly these entries (adding or raising one is
// an edit of this test too, seen in review), and a ceiling more than one margin (10%
// rounded up to 50, plus 50) above its page or block is lowered.
func TestGrandfatherListsOnlyShrink(t *testing.T) {
	wantPages := map[string]int{
		"docs/configurator.md": 2250,
		"docs/deployment.md":   2450,
		"docs/sync.md":         1200,
		"docs/testing.md":      1600,
	}
	wantBlocks := map[string]int{
		"Roles: contributor and maintainer | The repository ships its subagents in `.": 2300,
		"Releases | - **The release candidate.** `action: rc":                          2200,
	}
	if !reflect.DeepEqual(GrandfatheredPages, wantPages) {
		t.Errorf("GrandfatheredPages is %v, this test holds %v: a list only shrinks", GrandfatheredPages, wantPages)
	}
	if !reflect.DeepEqual(GrandfatheredBlocks, wantBlocks) {
		t.Errorf("GrandfatheredBlocks is %v, this test holds %v: a list only shrinks", GrandfatheredBlocks, wantBlocks)
	}
	margin := func(n int) int { return (n*11+499)/500*50 + 50 }
	root := repoRoot(t)
	pages, err := DocPages(root)
	if err != nil {
		t.Fatal(err)
	}
	for name, ceiling := range GrandfatheredPages {
		if n := DocPageLines(pages[name]); ceiling > margin(n) {
			t.Errorf("%s is %d lines under a ceiling of %d: lower it to %d", name, n, ceiling, margin(n)-50)
		}
	}
	for _, b := range Blocks(stripAllGenerated(readFile(t, root, "CONTRIBUTING.md"))) {
		if ceiling, ok := GrandfatheredBlocks[b.Key]; ok && ceiling > margin(b.Chars) {
			t.Errorf("block %q is %d characters under a ceiling of %d: lower it to %d", b.Key, b.Chars, ceiling, margin(b.Chars)-50)
		}
	}
}
