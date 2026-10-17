package lint

import (
	"strings"
	"testing"
)

// D853: the types the server keys on are known to unknown_type without a
// template, outside strict maps.

func keyedPage(typ string) string { return "---\ntype: " + typ + "\ntitle: P\n---\n# P\n" }

// paletteKB is a KB whose only declared type is Runbook.
func paletteKB(t *testing.T, files map[string]string) []Finding {
	t.Helper()
	k := tempKB(t)
	writeFile(t, k.Root, "templates/runbook.md", "---\ntype: Runbook\ntitle: Runbook\n---\n# {{title}}\n")
	for rel, content := range files {
		writeFile(t, k.DataRoot(), rel, content)
	}
	f, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestKeyedTypesAreKnownWithAPalette(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"arch/s.md": keyedPage("Source"),
		"arch/c.md": keyedPage("Contradiction"),
		"arch/v.md": keyedPage("service"),
		"arch/d.md": keyedPage("Digest"),
		"arch/g.md": keyedPage("Gizmo"),
	}
	got := findingsOf(paletteKB(t, files), "unknown_type")
	if len(got) != 1 || got[0].Path != "arch/g.md" {
		t.Fatalf("only the undeclared Gizmo is reported: %+v", got)
	}
}

func TestKeyedTypeMisspellingStaysInfo(t *testing.T) {
	t.Parallel()
	got := findingsOf(paletteKB(t, map[string]string{
		"arch/s.md": keyedPage("source"),
		"arch/c.md": keyedPage("contradiction"),
	}), "unknown_type")
	if len(got) != 2 {
		t.Fatalf("findings: %+v", got)
	}
	for _, f := range got {
		if f.Severity != SevInfo || f.Fix != nil {
			t.Errorf("misspelling must stay info with no fix: %+v", f)
		}
	}
}

func TestKeyedTypeDeclaredSpellingWins(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.Root, "templates/service.md", "---\ntype: Service\ntitle: Service\n---\n# {{title}}\n")
	writeFile(t, k.DataRoot(), "arch/a.md", keyedPage("service"))
	f, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(f, "unknown_type")
	if len(got) != 1 || got[0].Fix == nil || got[0].Fix.To != "Service" {
		t.Fatalf("the case fix to the declared spelling stays: %+v", got)
	}
}

func TestKeyedTypeStillReportsTemplateMissing(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.Root, "templates/runbook.md", "---\ntype: Runbook\ntitle: Runbook\n---\n# {{title}}\n")
	writeFile(t, k.DataRoot(), "sources/_map.md", "---\ntype: Map\nkind: map\ntitle: S\nrequire_template: true\n---\n# S\n")
	writeFile(t, k.DataRoot(), "sources/s.md", keyedPage("Source"))
	f, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(f, "unknown_type"); len(got) != 0 {
		t.Errorf("unknown_type: %+v", got)
	}
	if got := findingsOf(f, "template_missing"); len(got) != 1 {
		t.Errorf("template_missing must still report: %+v", got)
	}
}

func TestKeyedTypeInAStrictMapKeepsTheWarning(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "strict/_map.md", "---\ntype: Map\nkind: map\ntitle: S\nontology_mode: strict\nconcept_types: [Alpha]\n---\n# S\n")
	writeFile(t, k.DataRoot(), "strict/s.md", keyedPage("Source"))
	f, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(f, "unknown_type")
	if len(got) != 1 || got[0].Severity != SevWarning || got[0].Fix != nil {
		t.Fatalf("findings: %+v", got)
	}
	if !strings.Contains(got[0].Message, "map_update") || strings.Contains(got[0].Message, "set type to a declared one") {
		t.Errorf("fix text: %q", got[0].Message)
	}
	if errs, err := k.Validate(""); err != nil || len(errs) == 0 {
		t.Errorf("validate must still refuse the page: %v %v", errs, err)
	}
}

func TestKeyedTypeWithoutAPaletteStaysSilent(t *testing.T) {
	t.Parallel()
	if got := findingsOf(driftRun(t, map[string]string{"arch/s.md": keyedPage("Source")}), "unknown_type"); len(got) != 0 {
		t.Fatalf("no palette: %+v", got)
	}
}
