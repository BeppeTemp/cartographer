package lint

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// The repair asks required only when the heading would otherwise go, and a
// heading it keeps is followed by one blank line, whatever comes next.
func TestReplaceLinkItemKeepsAHeadingRequired(t *testing.T) {
	t.Parallel()
	always := func(level int, heading string) bool { return true }
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "the end of the body follows",
			body: "# T\n\nText [[a/b]].\n\n## Links\n\n- [[a/b]]\n",
			want: "# T\n\nText [[a/b]].\n\n## Links\n",
		},
		{
			name: "a sibling heading follows",
			body: "# T\n\nText [[a/b]].\n\n## Links\n\n- [[a/b]]\n\n## After\n\nkept\n",
			want: "# T\n\nText [[a/b]].\n\n## Links\n\n## After\n\nkept\n",
		},
		{
			name: "a deeper heading follows",
			body: "# T\n\nText [[a/b]].\n\n## Links\n\n- [[a/b]]\n\n### Sub\n\nprose\n",
			want: "# T\n\nText [[a/b]].\n\n## Links\n\n### Sub\n\nprose\n",
		},
	}
	for _, tc := range cases {
		if got := DropLinkItem(tc.body, "- [[a/b]]", always); got != tc.want {
			t.Errorf("%s:\ngot  %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

func TestReplaceLinkItemAsksRequiredOnlyForAnEmptiedSection(t *testing.T) {
	t.Parallel()
	var asked []string
	required := func(level int, heading string) bool {
		asked = append(asked, strings.Repeat("#", level)+" "+heading)
		return false
	}
	still := "Text [[a/b]].\n\n## Links\n\n- [[a/b]]\n- [[c/d]]\n"
	if got := DropLinkItem(still, "- [[a/b]]", required); len(asked) != 0 || !strings.Contains(got, "- [[c/d]]") {
		t.Fatalf("a section with an item left asked %v: %q", asked, got)
	}
	prose := "Text [[a/b]].\n\n## Links\n\nWhy they matter.\n- [[a/b]]\n"
	if got := DropLinkItem(prose, "- [[a/b]]", required); len(asked) != 0 || !strings.Contains(got, "## Links\n\nWhy they matter.") {
		t.Fatalf("a section with prose left asked %v: %q", asked, got)
	}
	only := "Text [[a/b]].\n\n## See also\n\n- [[a/b]]\n"
	if got := DropLinkItem(only, "- [[a/b]]", required); strings.Contains(got, "See also") || len(asked) != 1 || asked[0] != "## See also" {
		t.Fatalf("asked %v: %q", asked, got)
	}
}

// TemplateRequiresHeading is true for the H2 a page's template does not let it
// omit — by name or alias — and for nothing else: no map, no template, an
// optional section, a deeper heading, a map that declares no template.
func TestTemplateRequiresHeading(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	tpl := func(extra string) string {
		return "---\ntype: Topic\ntitle: \"{{title}}\"\n" + extra + "---\n# {{title}}\n\n## Summary\n\n## Links\n"
	}
	writeFile(t, k.Root, "templates/topic.md", tpl("x-template.section_aliases.Links: [Related]\n"))
	writeFile(t, k.Root, "templates/loose.md", tpl("x-template.optional_sections: [Links]\n"))
	writeFile(t, k.DataRoot(), "strict/_map.md", "---\ntype: Map\nkind: map\ntitle: S\ntemplates: [topic]\n---\n")
	writeFile(t, k.DataRoot(), "opt/_map.md", "---\ntype: Map\nkind: map\ntitle: O\ntemplates: [loose]\n---\n")
	writeFile(t, k.DataRoot(), "sections/_map.md", "---\ntype: Map\nkind: map\ntitle: T\ndefault_template: topic\n---\n")
	writeFile(t, k.DataRoot(), "free/_map.md", "---\ntype: Map\nkind: map\ntitle: F\n---\n")

	page := func(shape, typ string) *okf.Frontmatter {
		fm, err := okf.ParseFrontmatter("type: " + typ + "\ntitle: P\n" + shape)
		if err != nil {
			t.Fatal(err)
		}
		return fm
	}
	cases := []struct {
		name    string
		id      okf.ConceptID
		fm      *okf.Frontmatter
		level   int
		heading string
		want    bool
	}{
		{"required section", "strict/p", page("shape: topic\n", "Topic"), 2, "Links", true},
		{"folded name", "strict/p", page("shape: topic\n", "Topic"), 2, "  links ", true},
		{"alias of a required section", "strict/p", page("shape: topic\n", "Topic"), 2, "Related", true},
		{"template resolved by the type under a default template", "sections/p", page("", "Topic"), 2, "Links", true},
		{"a heading the template does not name", "strict/p", page("shape: topic\n", "Topic"), 2, "See also", false},
		{"deeper than the H2 a template names", "strict/p", page("shape: topic\n", "Topic"), 3, "Links", false},
		{"optional section", "opt/p", page("shape: loose\n", "Topic"), 2, "Links", false},
		{"map without a template key", "free/p", page("shape: topic\n", "Topic"), 2, "Links", false},
		{"page outside any map", "p", page("shape: topic\n", "Topic"), 2, "Links", false},
		{"unknown shape and no type default", "strict/p", page("shape: nope\n", "Other"), 2, "Links", false},
	}
	for _, tc := range cases {
		if got := TemplateRequiresHeading(k, tc.id, tc.fm)(tc.level, tc.heading); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The messages say that a links section needs no list: a line of prose under
// the heading is enough to keep it.
func TestLinksSectionMessagesSayProseIsEnough(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.Root, "templates/topic.md", "---\ntype: Topic\ntitle: \"{{title}}\"\n---\n# {{title}}\n\n## Summary\n\n## Links\n\n## Notes\n")
	writeFile(t, k.DataRoot(), "n/_map.md", "---\ntype: Map\nkind: map\ntitle: N\ntemplates: [topic]\n---\n")
	writeFile(t, k.DataRoot(), "n/y.md", "---\ntype: Topic\ntitle: Y\nshape: topic\n---\n# Y\n\n## Summary\n\nOther.\n\n## Links\n\nText.\n\n## Notes\n\nNone.\n")
	writeFile(t, k.DataRoot(), "n/x.md", "---\ntype: Topic\ntitle: X\nshape: topic\n---\n# X\n\n## Summary\n\nSee [[n/y]].\n\n## Links\n\n- [[n/y]]\n")
	writeFile(t, k.DataRoot(), "n/z.md", "---\ntype: Topic\ntitle: Z\nshape: topic\n---\n# Z\n\n## Summary\n\nText.\n\n## Links\n\nWhy.\n")
	writeFile(t, k.DataRoot(), "n/w.md", "---\ntype: Topic\ntitle: W\nshape: topic\n---\n# W\n\n## Summary\n\nText.\n\n## Notes\n\nNone.\n")
	f, err := Run(k, "n", false)
	if err != nil {
		t.Fatal(err)
	}
	dup := findingFor(f, "n/x.md", "duplicate_link")
	if dup == nil || !strings.Contains(dup.Message, "a line of prose keeps the section") {
		t.Fatalf("duplicate_link: %+v", dup)
	}
	// Links is present but Notes is missing: the message names Notes only, so it
	// stays free of the hint.
	if m := findingFor(f, "n/z.md", "template_section_missing"); m == nil || strings.Contains(m.Message, "prose") {
		t.Fatalf("a missing section that is no links section carries no hint: %+v", m)
	}
	if m := findingFor(f, "n/w.md", "template_section_missing"); m == nil || !strings.Contains(m.Message, "a line of prose under its heading is enough") {
		t.Fatalf("a missing links section says prose is enough: %+v", m)
	}
}
