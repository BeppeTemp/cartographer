package lint

import "testing"

// D278: source_uncited.
func sourceKB(t *testing.T, status, citer string) []Finding {
	t.Helper()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "sources/2026-01-01-a.md", "---\ntype: Source\ntitle: A\nsource_kind: document\ningest_status: "+status+"\n---\n# A\n")
	writeFile(t, k.DataRoot(), "arch/n.md", "---\ntype: Note\ntitle: N\n"+citer+"---\nSee [[sources/2026-01-01-a]].\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	return findingsOf(findings, "source_uncited")
}

func TestRun_SourceUncited(t *testing.T) {
	t.Parallel()
	got := sourceKB(t, "ingested", "")
	if len(got) != 1 || got[0].Path != "sources/2026-01-01-a.md" || got[0].Severity != SevWarning {
		t.Fatalf("ingested and uncited: %+v", got)
	}
	if got := sourceKB(t, "ingested", "provenance: [https://example.com/x, sources/2026-01-01-a]\n"); len(got) != 0 {
		t.Fatalf("cited source flagged: %+v", got)
	}
	// A plain link is not a citation.
	for _, st := range []string{"pending", "skipped"} {
		if got := sourceKB(t, st, ""); len(got) != 0 {
			t.Fatalf("%s flagged: %+v", st, got)
		}
	}
}

func TestRun_SourceUncitedScoped(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "sources/a.md", "---\ntype: Source\ntitle: A\ningest_status: ingested\n---\n# A\n")
	writeFile(t, k.DataRoot(), "sources/b.md", "---\ntype: Source\ntitle: B\ningest_status: ingested\n---\n# B\n")
	writeFile(t, k.DataRoot(), "arch/n.md", "---\ntype: Note\ntitle: N\nprovenance: [sources/b]\n---\nx\n")
	findings, err := Run(k, "sources", false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(findings, "source_uncited")
	if len(got) != 1 || got[0].Path != "sources/a.md" {
		t.Fatalf("only the uncited source is flagged, one cited from outside the scope is not: %+v", got)
	}
}
