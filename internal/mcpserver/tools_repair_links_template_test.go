package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// seedLinksTemplateKB writes a template of the given sections and a map that
// declares it, with the page notes/x repeating its one links item in the text.
func seedLinksTemplateKB(t *testing.T, k *kb.KB, template, linksHeading string) {
	t.Helper()
	write := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(k.Root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("templates/topic.md", template)
	write("data/notes/_map.md", "---\ntype: Map\nkind: map\ntitle: Notes\ntemplates: [topic]\n---\n# Notes\n")
	write("data/notes/y.md", "---\ntype: Topic\ntitle: Y\nshape: topic\n---\n# Y\n\n## Summary\n\nOther.\n\n"+linksHeading+"\n\nSee Z.\n")
	write("data/notes/x.md", "---\ntype: Topic\ntitle: X\nshape: topic\n---\n# X\n\n## Summary\n\nSee [[notes/y]].\n\n"+linksHeading+"\n\n- [[notes/y]]\n")
}

const topicTemplate = "---\ntype: Topic\ntitle: \"{{title}}\"\n%s---\n# {{title}}\n\n## Summary\n\n## Links\n"

func linksTemplate(extra string) string {
	return strings.Replace(topicTemplate, "%s", extra, 1)
}

func hasFindingFor(findings []lint.Finding, path, check string) bool {
	for _, f := range findings {
		if f.Check == check && f.Path == path {
			return true
		}
	}
	return false
}

// A duplicate_link repair that empties a links section keeps the heading when
// the page's template requires it (an alias counts); otherwise the heading goes.
// Repairing one check must not report another: no template_section_missing on
// the next lint.
func TestKBRepairDuplicateLinkKeepsHeadingTheTemplateRequires(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		template string
		heading  string
		wantKept bool
	}{
		{"a required section is kept", linksTemplate(""), "## Links", true},
		{"an alias of a required section is kept", linksTemplate("x-template.section_aliases.Links: [Related]\n"), "## Related", true},
		{"an optional section goes", linksTemplate("x-template.optional_sections: [Links]\n"), "## Links", false},
		{"a section the template does not name goes", strings.Replace(linksTemplate(""), "\n## Links\n", "\n", 1), "## Links", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			k, s := repairKB(t, 0)
			seedLinksTemplateKB(t, k, tc.template, tc.heading)
			findings, err := lint.Run(k, "notes", false)
			if err != nil {
				t.Fatal(err)
			}
			if !hasFindingFor(findings, "notes/x.md", "duplicate_link") {
				t.Fatalf("no duplicate_link before the repair: %+v", findings)
			}
			if hasFindingFor(findings, "notes/x.md", "template_section_missing") {
				t.Fatalf("template_section_missing before the repair: %+v", findings)
			}
			repairCall(t, s, `{"check":"duplicate_link","dry_run":false}`)
			cd, err := k.ReadConcept("notes/x")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(cd.Body, "- [[notes/y]]") {
				t.Fatalf("the item stays:\n%s", cd.Body)
			}
			if kept := strings.Contains(cd.Body, tc.heading+"\n"); kept != tc.wantKept {
				t.Fatalf("heading %q kept = %v, want %v:\n%s", tc.heading, kept, tc.wantKept, cd.Body)
			}
			after, err := lint.Run(k, "notes", false)
			if err != nil {
				t.Fatal(err)
			}
			if hasFindingFor(after, "notes/x.md", "template_section_missing") {
				t.Errorf("the repair left template_section_missing:\n%s", cd.Body)
			}
			if hasFindingFor(after, "notes/x.md", "duplicate_link") {
				t.Errorf("duplicate_link remains:\n%s", cd.Body)
			}
		})
	}
}

// A page outside any map, or in a map with no template, has no section to
// keep: the repair drops the emptied heading as before.
func TestKBRepairDuplicateLinkDropsHeadingWithoutTemplate(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	body := "---\ntype: Note\ntitle: P\nupdated: 2026-01-02\n---\n# P\n\nSee [[ops/q]].\n\n## Links\n\n- [[ops/q]]\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "ops", "p.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	repairCall(t, s, `{"check":"duplicate_link","dry_run":false}`)
	cd, err := k.ReadConcept("ops/p")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cd.Body, "## Links") || strings.Contains(cd.Body, "- [[ops/q]]") {
		t.Fatalf("heading or item left:\n%s", cd.Body)
	}
}

// applyFixes is the one place both kb_repair and the repair on write reach
// DropLinkItem: the template's section is kept there, whichever pass calls it.
func TestApplyFixesKeepsRequiredLinksHeading(t *testing.T) {
	t.Parallel()
	k, _ := repairKB(t, 0)
	seedLinksTemplateKB(t, k, linksTemplate(""), "## Links")
	cd, err := k.ReadConcept("notes/x")
	if err != nil {
		t.Fatal(err)
	}
	fm, _, err := parseForRepair(cd.FrontmatterRaw, false)
	if err != nil {
		t.Fatal(err)
	}
	body := cd.Body
	fix := &lint.Fix{Kind: lint.FixDropLinkItem, Field: "- [[notes/y]]"}
	if changed, _, fatal := applyFixes(k, "notes/x", fm, &body, []*lint.Fix{fix}); fatal != "" || changed != 1 {
		t.Fatalf("changed=%d fatal=%q", changed, fatal)
	}
	if !strings.Contains(body, "## Links\n") || strings.Contains(body, "- [[notes/y]]") {
		t.Errorf("heading lost or item kept:\n%s", body)
	}
}
