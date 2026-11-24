package lint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

func writeUnits(t *testing.T, dir func(string, string), n int, shape string) {
	t.Helper()
	fm := "type: IngestUnit\n"
	if shape != "" {
		fm += "shape: " + shape + "\n"
	}
	for i := 0; i < n; i++ {
		dir(fmt.Sprintf("ingest/namespace-web-%02d.md", i), "---\n"+fm+"title: Namespace web\nstatus: open\n---\n# Namespace web\n\n"+sharedFact+"\n")
	}
}

// TestReviewIngestUnitsSkipDuplicateAndRepeated (D-new-ingest-units-skip-duplicate-and-repeated-checks): units of one group
// share title and instruction text by construction; ordinary pages still pair.
func TestReviewIngestUnitsSkipDuplicateAndRepeated(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		shape     string
		wantItems bool
	}{{"ingest-unit", false}, {"", true}} {
		t.Run("shape="+tc.shape, func(t *testing.T) {
			t.Parallel()
			k := tempKB(t)
			writeFile(t, k.DataRoot(), "ingest/_map.md", "---\ntype: Map\ntitle: Ingest\n---\n")
			writeUnits(t, func(p, c string) { writeFile(t, k.DataRoot(), p, c) }, 4, tc.shape)
			r := review(t, k)
			dup, rep := itemsOf(r, ReviewDuplicate), itemsOf(r, ReviewRepeatedFact)
			if tc.wantItems != (len(dup) > 0) || tc.wantItems != (len(rep) > 0) {
				t.Fatalf("shape %q: duplicates=%d repeated=%d", tc.shape, len(dup), len(rep))
			}
		})
	}
}

// A unit does not count as an owner of a line an ordinary page carries.
func TestReviewIngestUnitIsNotARepeatedFactOwner(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	writeFile(t, k.DataRoot(), "ingest/_map.md", "---\ntype: Map\ntitle: Ingest\n---\n")
	for i := 0; i < 2; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("m/p%d.md", i), "---\ntype: Note\ntitle: P"+fmt.Sprint(i)+"\n---\n"+sharedFact+"\n")
	}
	writeFile(t, k.DataRoot(), "ingest/u.md", "---\ntype: IngestUnit\nshape: ingest-unit\ntitle: U\n---\n"+sharedFact+"\n")
	if got := itemsOf(review(t, k), ReviewRepeatedFact); len(got) != 0 {
		t.Fatalf("unit counted as owner: %+v", got)
	}
}

// The write-time twin: no advice on a unit, and a unit is not an owner.
func TestAddedRepeatedFactsIngestUnit(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ingest/u1.md", "---\ntype: IngestUnit\nshape: ingest-unit\ntitle: U1\n---\n"+addedFactLine+"\n")
	writeFile(t, k.DataRoot(), "ingest/u2.md", "---\ntype: IngestUnit\nshape: ingest-unit\ntitle: U2\n---\n"+addedFactLine+"\n")
	putConcept(t, k, "m/a", addedFactLine)
	putConcept(t, k, "m/b", addedFactLine)
	lookup := func(string) []okf.ConceptID { return []okf.ConceptID{"ingest/u2", "m/a", "m/b"} }
	if got, _ := AddedRepeatedFacts(k, "ingest/u1", "", addedFactLine, 20, lookup); len(got) != 0 {
		t.Fatalf("unit advised: %+v", got)
	}
	putConcept(t, k, "m/c", addedFactLine)
	// m/a and m/b are two owners; the unit would have made three if counted.
	only := func(string) []okf.ConceptID { return []okf.ConceptID{"ingest/u1", "m/a"} }
	if got, _ := AddedRepeatedFacts(k, "m/c", "", addedFactLine, 20, only); len(got) != 0 {
		t.Fatalf("unit counted: %+v", got)
	}
}

// The hour of an ISO timestamp ("T07") is not a glossary term, on any page.
func TestReviewGlossaryGapIgnoresISOHour(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	for i := 0; i < glossaryMinConcepts; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("m/d%02d.md", i), "---\ntype: Note\ntitle: D\n---\n# D\n\nSeen 2026-11-21T07:00:00Z and 2026-11-21T23:15Z, again 2026-11-22T08.\n")
	}
	for _, it := range itemsOf(review(t, k), ReviewGlossary) {
		if strings.HasPrefix(it.Term, "T0") || strings.HasPrefix(it.Term, "T2") {
			t.Fatalf("timestamp hour as a term: %+v", it)
		}
	}
}
