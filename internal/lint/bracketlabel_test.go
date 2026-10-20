package lint

import "testing"

func TestRun_IndexEntryWithBracketedLabel(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\n---\n")
	writeFile(t, k.DataRoot(), "ops/index.md", "---\ntype: Index\n---\n- [Issue #100 — [RFC] foo](alpha.md)\n")
	writeFile(t, k.DataRoot(), "ops/alpha.md", "---\ntype: Note\n---\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if hasCheck(findings, "ops/index.md", "index_incomplete") || hasCheck(findings, "ops/alpha.md", "index_incomplete") {
		t.Fatalf("bracketed label hid the index entry: %#v", findings)
	}
}
