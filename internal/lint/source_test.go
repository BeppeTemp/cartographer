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

func TestRun_SourceUncitedSuppressibleAndScoped(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "sources/a.md", "---\ntype: Source\ntitle: A\ningest_status: ingested\nlint_ignore: [source_uncited]\n---\n# A\n")
	writeFile(t, k.DataRoot(), "sources/b.md", "---\ntype: Source\ntitle: B\ningest_status: ingested\n---\n# B\n")
	writeFile(t, k.DataRoot(), "arch/n.md", "---\ntype: Note\ntitle: N\nprovenance: [sources/b]\n---\nx\n")
	findings, err := Run(k, "sources", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(findings, "source_uncited"); len(got) != 0 {
		t.Fatalf("suppressed or cited from outside the scope: %+v", got)
	}
	if got := findingsOf(findings, "lint_ignore_invalid"); len(got) != 0 {
		t.Fatalf("source_uncited must be a valid lint_ignore name: %+v", got)
	}
}
