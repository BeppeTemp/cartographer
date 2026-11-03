package lint

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// #1503 WP7: the write gate accepts what the contract produces.

func TestProseValueSkipsContractValues(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\nfield_values.region: [\"Rome, Italy\"]\nkind: journal\nopen_statuses: [\"waiting on: review\"]\n---\n")
	writeFile(t, k.DataRoot(), "m/ok.md", "---\ntype: Note\ntitle: A\nregion: \"Rome, Italy\"\nstatus: \"waiting on: review\"\n---\n# A\n")
	writeFile(t, k.DataRoot(), "m/bad.md", "---\ntype: Note\ntitle: B\nregion: \"Rome, the capital of Italy\"\n---\n# B\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"prose_value", "invalid_field_value"} {
		if hasCheck(findings, "m/ok.md", c) {
			t.Errorf("m/ok.md: unexpected %s: %v", c, findings)
		}
	}
	if !hasCheck(findings, "m/bad.md", "prose_value") {
		t.Errorf("a sentence the contract does not allow must still raise prose_value: %v", findings)
	}
	// The write path agrees.
	if got := CheckConcept(k, "m/ok", "---\ntype: Note\ntitle: A\nregion: \"Rome, Italy\"\n---\n# A\n"); hasCheck(got, "m/ok.md", "prose_value") {
		t.Errorf("write gate refuses the contract's own value: %v", got)
	}
}

func TestTemplateHeadingKeepsInlineCode(t *testing.T) {
	t.Parallel()
	const tpl = "---\ntype: Runbook\ntitle: \"{{title}}\"\n---\n# {{title}}\n\n## Run `make gate` first\n\n## Steps\n"
	k := tempKB(t)
	writeFile(t, k.Root, "templates/runbook.md", tpl)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\nkind: map\ntitle: Ops\ntemplates: [runbook]\n---\n# Ops\n")
	page := "---\ntype: Runbook\ntitle: R\nshape: runbook\n---\n# R\n\n## Run `make gate` first\n\nx\n\n## Steps\n\ny\n"
	writeFile(t, k.DataRoot(), "ops/r.md", page)
	for name, got := range map[string][]Finding{
		"Run":          mustRun(t, k),
		"CheckConcept": CheckConcept(k, "ops/r", page),
	} {
		for _, f := range got {
			if f.Path == "ops/r.md" && strings.HasPrefix(f.Check, "template_") {
				t.Errorf("%s: %s %s", name, f.Check, f.Message)
			}
		}
	}
	h := h2Lines("## Run `make gate` first\n\n```\n## fenced\n```\n## Steps ##\n")
	if len(h) != 2 || h[0].Text != "Run `make gate` first" || h[1].Text != "Steps" || h[1].Line != 5 {
		t.Errorf("h2Lines = %+v", h)
	}
	if got := kb.H2Headings("## A `b` c\n"); len(got) != 1 || got[0] != "A `b` c" {
		t.Errorf("H2Headings = %q", got)
	}
	if out, ok := RenameHeading("## Run `x` first\n", "Run `x` first", "Go"); !ok || out != "## Go\n" {
		t.Errorf("RenameHeading = %q %v", out, ok)
	}
}

// The write gate reports a satellite whose folder lacks index.md, as Run does.
func TestScopedCheckExpandedMissingIndex(t *testing.T) {
	t.Parallel()
	for _, idx := range []string{"curated", "generated"} {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\nindex: "+idx+"\n---\n")
		writeFile(t, k.DataRoot(), "m/c/s.md", noteHead+"---\nSatellite.\n")
		if run := mustRun(t, k); !hasCheck(run, "m/c/index.md", "expanded_missing_index") {
			t.Fatalf("%s: Run: %v", idx, run)
		}
		got := ScopedCheck(k, ids("m/c/s"))
		if !hasCheck(got, "m/c/index.md", "expanded_missing_index") {
			t.Errorf("%s: ScopedCheck: %v", idx, got)
		}
		if hasCheck(got, "m/c/index.md", "index_incomplete") {
			t.Errorf("%s: ScopedCheck reports index_incomplete for a missing index: %v", idx, got)
		}
	}
}
