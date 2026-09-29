package lint

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
			heading, dups, bare, n := linksSectionIssues(tc.body, "x/page.md", nil)
			if heading != tc.heading || len(dups) != tc.dups || bare != tc.bare || n != tc.listed {
				t.Fatalf("got heading=%q dups=%v bare=%v listed=%d; want heading=%q dups=%d bare=%v listed=%d",
					heading, dups, bare, n, tc.heading, tc.dups, tc.bare, tc.listed)
			}
		})
	}
}
