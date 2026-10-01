package lint

import "strings"

import "testing"

func TestLinksSectionIssues(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		dups    int
		bare    bool
		heading string
		listed  int
	}{
		{"no section", "Text with [[a/b]].\n", 0, false, "", 0},
		{"bare list, links only there", "Intro.\n\n## Collegamenti\n\n- [[a/b]]\n- [B](../c/d.md)\n", 0, true, "Collegamenti", 2},
		{"list with reasons", "Intro.\n\n## Links\n\n- [[a/b]] — the service this page configures\n", 0, false, "Links", 1},
		{"duplicate of a text link", "Uses [[a/b]] daily.\n\n## See also\n\n- [[a/b]]\n- [[e/f]]\n", 1, true, "See also", 2},
		{"section ends at the next heading", "## Collegamenti\n\n- [[a/b]]\n\n## Note\n\nProse mentions [[a/b]].\n", 1, true, "Collegamenti", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			heading, dups, _, bare, n := linksSectionIssues(tc.body, "x/page.md", nil)
			if heading != tc.heading || len(dups) != tc.dups || bare != tc.bare || n != tc.listed {
				t.Fatalf("got heading=%q dups=%v bare=%v listed=%d; want heading=%q dups=%d bare=%v listed=%d",
					heading, dups, bare, n, tc.heading, tc.dups, tc.bare, tc.listed)
			}
		})
	}
}

func TestDropLinkItem(t *testing.T) {
	body := "# T\n\nText.\n\n## Links\n\n- [[a/b]]\n- [[c/d]] — why\n\n## After\n\nkept\n"
	got := DropLinkItem(body, "- [[a/b]]")
	want := "# T\n\nText.\n\n## Links\n\n- [[c/d]] — why\n\n## After\n\nkept\n"
	if got != want {
		t.Fatalf("got %q", got)
	}
	if DropLinkItem(body, "- [[zz/zz]]") != body {
		t.Fatal("absent line must be a no-op")
	}
	only := "# T\n\nText [[a/b]].\n\n## Links\n\n- [[a/b]]\n"
	if got := DropLinkItem(only, "- [[a/b]]"); strings.Contains(got, "## Links") || !strings.Contains(got, "Text [[a/b]].") {
		t.Fatalf("emptied section must lose its heading: %q", got)
	}
}

func TestLinkOnlyItemsNeedOneLinkAndNoWord(t *testing.T) {
	section := "\n- [[a/b]]\n- [[c/d]] [[e/f]]\n- [[g/h]] because\n"
	got := linkOnlyItems(section, "x/y.md", nil)
	if _, ok := got["a/b"]; !ok || len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}
