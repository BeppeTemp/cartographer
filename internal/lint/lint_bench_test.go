package lint

import (
	"fmt"
	"sort"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// TestRun_OracleRelPathOfMatchesResolveConceptRelPath builds a KB with plain,
// expanded, both-forms-present and missing targets plus asset links, and asserts
// the findings are identical to the expected oracle snapshot — proving the
// relPathOf optimisation (D294) does not change what lint reports.
func TestRun_OracleRelPathOfMatchesResolveConceptRelPath(t *testing.T) {
	k := tempKB(t)
	root := k.DataRoot()

	// Create a map.
	if err := k.CreateMapWithContract("arch", "Arch", "map", nil, "", kb.MapContract{}); err != nil {
		t.Fatal(err)
	}

	// Plain concept: target exists.
	writeFile(t, root, "arch/plain.md",
		"---\ntype: Note\ntitle: Plain\n---\nSee [[arch/expanded]] expanded.\n")

	// Expanded concept: target exists.
	writeFile(t, root, "arch/expanded/index.md",
		"---\ntype: Note\ntitle: Expanded\n---\nBack to [plain](../plain.md).\n")

	// Expanded satellite concept.
	writeFile(t, root, "arch/expanded/child.md",
		"---\ntype: Note\ntitle: Child\n---\nSee [plain](../plain.md).\n")

	// Both forms exist: direct <id>.md AND <id>/index.md for the same id.
	writeFile(t, root, "arch/both.md",
		"---\ntype: Note\ntitle: Both Direct\n---\nI am the direct form.\n")
	writeFile(t, root, "arch/both/index.md",
		"---\ntype: Note\ntitle: Both Expanded\n---\nI am the expanded form.\n")

	// Broken link: target does not exist.
	writeFile(t, root, "arch/linker.md",
		"---\ntype: Note\ntitle: Linker\n---\nSee [missing](missing.md) and [exists](plain.md).\n")

	// Asset link: should not be reported as broken.
	writeFile(t, root, "arch/with-asset/index.md",
		"---\ntype: Note\ntitle: With Asset\n---\nSee [data](data.csv).\n")
	writeFile(t, root, "arch/with-asset/data.csv", "a,b\n1,2\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The oracle: deterministic findings from this fixture. Sorted by
	// path+check+message so the comparison is order-independent.
	type entry struct {
		Path, Check, Severity, Message string
	}
	got := make([]entry, 0, len(findings))
	for _, f := range findings {
		got = append(got, entry{f.Path, f.Check, f.Severity, f.Message})
	}
	sort.Slice(got, func(i, j int) bool {
		if got[i].Path != got[j].Path {
			return got[i].Path < got[j].Path
		}
		if got[i].Check != got[j].Check {
			return got[i].Check < got[j].Check
		}
		return got[i].Message < got[j].Message
	})

	// Snapshot the oracle expectations: the exact findings the fixture
	// produces. The broken_link to "arch/missing" from "arch/linker.md" must
	// appear; the expanded_ambiguous for "arch/both" must appear.
	wantBroken := false
	wantAmbiguous := false
	for _, e := range got {
		if e.Check == "broken_link" && e.Path == "arch/linker.md" {
			wantBroken = true
		}
		if e.Check == "expanded_ambiguous" && e.Path == "arch/both.md" {
			wantAmbiguous = true
		}
	}
	if !wantBroken {
		t.Error("oracle: expected broken_link finding for arch/linker.md → arch/missing")
	}
	if !wantAmbiguous {
		t.Error("oracle: expected expanded_ambiguous finding for arch/both.md")
	}

	// No false broken_link for existing targets.
	for _, e := range got {
		if e.Check == "broken_link" && e.Path == "arch/plain.md" {
			t.Errorf("oracle: unexpected broken_link for arch/plain.md: %s", e.Message)
		}
		if e.Check == "broken_link" && e.Path == "arch/expanded/index.md" {
			t.Errorf("oracle: unexpected broken_link for arch/expanded/index.md: %s", e.Message)
		}
		if e.Check == "broken_link" && e.Path == "arch/expanded/child.md" {
			t.Errorf("oracle: unexpected broken_link for arch/expanded/child.md: %s", e.Message)
		}
	}

	// Verify asset links are not broken.
	for _, e := range got {
		if e.Check == "broken_link" && e.Path == "arch/with-asset/index.md" {
			t.Errorf("oracle: unexpected broken_link for asset link: %s", e.Message)
		}
	}

	// Run again: deterministic — same findings.
	findings2, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run (second): %v", err)
	}
	got2 := make([]entry, 0, len(findings2))
	for _, f := range findings2 {
		got2 = append(got2, entry{f.Path, f.Check, f.Severity, f.Message})
	}
	sort.Slice(got2, func(i, j int) bool {
		if got2[i].Path != got2[j].Path {
			return got2[i].Path < got2[j].Path
		}
		if got2[i].Check != got2[j].Check {
			return got2[i].Check < got2[j].Check
		}
		return got2[i].Message < got2[j].Message
	})

	if len(got) != len(got2) {
		t.Fatalf("oracle: second run returned %d findings, first had %d", len(got2), len(got))
	}
	for i := range got {
		if got[i] != got2[i] {
			t.Errorf("oracle: finding %d differs:\n  first:  %+v\n  second: %+v", i, got[i], got2[i])
		}
	}
}

// BenchmarkLintRun generates a KB of 1,000 concepts with ~7 links each and
// benchmarks a whole-KB lint.Run.
func BenchmarkLintRun(b *testing.B) {
	dir := b.TempDir()
	k, err := kb.Init(dir)
	if err != nil {
		b.Fatal(err)
	}
	const maps = 10
	const conceptsPerMap = 100
	for m := 0; m < maps; m++ {
		name := fmt.Sprintf("m%d", m)
		if err := k.CreateMapWithContract(name, name, "map", nil, "", kb.MapContract{}); err != nil {
			b.Fatal(err)
		}
		for i := 0; i < conceptsPerMap; i++ {
			fm := newBenchFM()
			fm.Set("type", "Note")
			fm.Set("title", fmt.Sprintf("C%d-%d", m, i))
			// ~7 links: 5 intra-map + 2 cross-map.
			var links []string
			for l := 1; l <= 5; l++ {
				target := (i + l) % conceptsPerMap
				links = append(links, fmt.Sprintf("[c%d](c%d.md)", target, target))
			}
			otherMap := (m + 1) % maps
			links = append(links,
				fmt.Sprintf("[cross1](../m%d/c%d.md)", otherMap, i%conceptsPerMap),
				fmt.Sprintf("[cross2](../m%d/c%d.md)", (m+2)%maps, (i+1)%conceptsPerMap),
			)
			body := fmt.Sprintf("# C\n\n%s\n", joinLinks(links))
			if _, err := k.WriteConcept(okf.ConceptID(fmt.Sprintf("%s/c%d", name, i)), fm, body, ""); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := Run(k, "", false)
		if err != nil {
			b.Fatal(err)
		}
		_ = f
	}
}

func newBenchFM() *okf.Frontmatter {
	fm, _ := okf.ParseFrontmatter("")
	return fm
}

func joinLinks(links []string) string {
	out := ""
	for _, l := range links {
		out += l + "\n"
	}
	return out
}
