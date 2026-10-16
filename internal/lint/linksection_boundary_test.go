package lint

import (
	"strings"
	"testing"
)

// A links section ends at the first heading of any level after it. A `###`
// under `## Links` is, in practice, a heading demoted from `##` to get out of
// template_extra_section: prose, not part of the links.
func TestLinksSectionEndsAtAnyHeading(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		body        string
		dups        int
		bare        bool
		listed      int
		sectionHas  string
		sectionMiss string
	}{
		{
			name:        "an H3 after the section is not part of it",
			body:        "Text [[a/b]].\n\n## Links\n\n- [[c/d]] — why\n\n### Extra\n\n- [[a/b]]\n",
			dups:        0,
			listed:      1,
			sectionHas:  "[[c/d]]",
			sectionMiss: "[[a/b]]",
		},
		{
			name:        "words in a demoted H3 do not make the list non-bare",
			body:        "## Links\n\n- [[c/d]]\n\n### Notes\n\nSome words about it.\n",
			bare:        true,
			listed:      1,
			sectionHas:  "[[c/d]]",
			sectionMiss: "Some words",
		},
		{
			name:        "a link in the H3 is text: it duplicates an item of the section",
			body:        "## Links\n\n- [[c/d]]\n\n### Notes\n\nSee [[c/d]].\n",
			dups:        1,
			bare:        true,
			listed:      1,
			sectionHas:  "[[c/d]]",
			sectionMiss: "See",
		},
		{
			name:        "an H4 directly under the heading ends it too",
			body:        "## Links\n- [[c/d]]\n#### Deep\n- [[e/f]]\n",
			bare:        true,
			listed:      1,
			sectionHas:  "[[c/d]]",
			sectionMiss: "[[e/f]]",
		},
		{
			name:        "a level-3 links heading ends at the next H4",
			body:        "## Topic\n\n### Links\n\n- [[c/d]]\n\n#### Deep\n\n- [[e/f]]\n",
			bare:        true,
			listed:      1,
			sectionHas:  "[[c/d]]",
			sectionMiss: "[[e/f]]",
		},
		{
			name:        "same-level and higher headings still end it",
			body:        "## Links\n\n- [[c/d]]\n\n# Next\n\n- [[e/f]]\n",
			bare:        true,
			listed:      1,
			sectionHas:  "[[c/d]]",
			sectionMiss: "[[e/f]]",
		},
		{
			name:       "the last section runs to the end of the body",
			body:       "Intro.\n\n## Links\n\n- [[c/d]]\n- [[e/f]]\n",
			bare:       true,
			listed:     2,
			sectionHas: "[[e/f]]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, dups, _, bare, n := linksSectionIssues(tc.body, "x/page.md", nil)
			if len(dups) != tc.dups || bare != tc.bare || n != tc.listed {
				t.Fatalf("got dups=%v bare=%v listed=%d; want dups=%d bare=%v listed=%d", dups, bare, n, tc.dups, tc.bare, tc.listed)
			}
			_, section, _, ok := linksSection(tc.body)
			if !ok {
				t.Fatal("no links section found")
			}
			if tc.sectionHas != "" && !strings.Contains(section, tc.sectionHas) {
				t.Errorf("section %q lacks %q", section, tc.sectionHas)
			}
			if tc.sectionMiss != "" && strings.Contains(section, tc.sectionMiss) {
				t.Errorf("section %q must not hold %q", section, tc.sectionMiss)
			}
		})
	}
}

// The repair used to drop a list line inside a demoted subsection as a
// "duplicate": the line is not in the links section, so it is left alone.
func TestDropLinkItemLeavesADemotedSubsectionAlone(t *testing.T) {
	t.Parallel()
	body := "# T\n\nText [[a/b]].\n\n## Links\n\n- [[c/d]] — why\n\n### Extra\n\n- [[a/b]]\n"
	if got := DropLinkItem(body, "- [[a/b]]", nil); got != body {
		t.Fatalf("a line under a ### after the links heading is not the section's: %q", got)
	}
	if got := DropLinkItem(body, "- [[c/d]] — why", nil); !strings.Contains(got, "### Extra\n\n- [[a/b]]\n") || strings.Contains(got, "[[c/d]]") {
		t.Fatalf("the section's own line goes, the subsection stays: %q", got)
	}
}

// Emptying the section removes its heading only when nothing but blank space
// is left up to the next heading of the same or a higher level. A `###` that
// follows is a child of the heading: dropping the parent would re-parent it
// under the previous H2.
func TestEmptiedLinksSectionKeepsItsHeadingWhenDeeperHeadingsFollow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "an H3 follows: the heading stays",
			body: "# T\n\nText [[a/b]].\n\n## Links\n\n- [[a/b]]\n\n### Sub\n\nprose\n",
			want: "# T\n\nText [[a/b]].\n\n## Links\n\n### Sub\n\nprose\n",
		},
		{
			name: "an H4 follows: the heading stays",
			body: "# T\n\nText [[a/b]].\n\n## Links\n\n- [[a/b]]\n#### Deep\n\nprose\n",
			want: "# T\n\nText [[a/b]].\n\n## Links\n\n#### Deep\n\nprose\n",
		},
		{
			name: "a sibling H2 follows: the heading goes",
			body: "# T\n\nText [[a/b]].\n\n## Links\n\n- [[a/b]]\n\n## After\n\nkept\n",
			want: "# T\n\nText [[a/b]].\n## After\n\nkept\n",
		},
		{
			name: "an H1 follows: the heading goes",
			body: "Text [[a/b]].\n\n## Links\n\n- [[a/b]]\n\n# Next\n\nkept\n",
			want: "Text [[a/b]].\n# Next\n\nkept\n",
		},
		{
			name: "the end of the body follows: the heading goes",
			body: "# T\n\nText [[a/b]].\n\n## Links\n\n- [[a/b]]\n",
			want: "# T\n\nText [[a/b]].\n",
		},
		{
			name: "a level-3 links heading with an H4 child keeps its heading",
			body: "## Topic\n\nText [[a/b]].\n\n### Links\n\n- [[a/b]]\n\n#### Sub\n\nprose\n",
			want: "## Topic\n\nText [[a/b]].\n\n### Links\n\n#### Sub\n\nprose\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DropLinkItem(tc.body, "- [[a/b]]", nil)
			if got != tc.want {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

// linksSection (what the checks read) and ReplaceLinkItem (what the repair
// edits) share one boundary: a list line of the section is removable, a line
// anywhere else is not, whatever headings surround it.
func TestLinksSectionAndRepairShareOneBoundary(t *testing.T) {
	t.Parallel()
	bodies := []string{
		"Intro.\n\n## Links\n\n- [[a/b]]\n\n### Sub\n\n- [[c/d]]\n\n## After\n\n- [[e/f]]\n",
		"## Related\n- [[a/b]]\n#### Deep\n- [[c/d]]\n# Top\n- [[e/f]]\n",
		"## Topic\n\n### See also\n\n- [[a/b]]\n\n#### Deep\n\n- [[c/d]]\n\n### Next\n\n- [[e/f]]\n",
		"## Links\n\n- [[a/b]]\n\n###### Six\n\n- [[c/d]]\n",
	}
	for _, body := range bodies {
		_, section, rest, ok := linksSection(body)
		if !ok {
			t.Fatalf("no section in %q", body)
		}
		for _, line := range strings.Split(body, "\n") {
			if !listMarkRe.MatchString(line) {
				continue
			}
			inSection := strings.Contains(section, line+"\n") || strings.HasSuffix(section, line)
			inRest := strings.Contains(rest, line)
			if inSection == inRest {
				t.Fatalf("line %q must be in exactly one of section %q and rest %q", line, section, rest)
			}
			changed := ReplaceLinkItem(body, line, "- [[x/y]]", nil) != body
			if changed != inSection {
				t.Errorf("body %q: line %q in section = %v but the repair changed it = %v", body, line, inSection, changed)
			}
		}
	}
}

// The drop path of a duplicate that sits in a demoted H3 no longer produces a
// finding at all, so no fix exists to misapply.
func TestDemotedSubsectionYieldsNoDuplicateFix(t *testing.T) {
	t.Parallel()
	body := "Text [[a/b]].\n\n## Links\n\n- [[c/d]] — why\n\n### Extra\n\n- [[a/b]]\n"
	_, dups, fixable, _, _ := linksSectionIssues(body, "x/page.md", nil)
	if len(dups) != 0 || len(fixable) != 0 {
		t.Fatalf("dups=%v fixable=%v", dups, fixable)
	}
}
