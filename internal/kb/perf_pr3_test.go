package kb

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// ReadCost filters on the validated view: no ConceptFacets lookup per concept,
// and archived concepts are still left out.
func TestReadCost_NoFacetLookups(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	for i := 0; i < 30; i++ {
		status := "active"
		if i%3 == 0 {
			status = "archived"
		}
		writeTestFile(t, k.DataRoot(), fmt.Sprintf("arch/c%d.md", i),
			fmt.Sprintf("---\ntype: Note\ntitle: C%d\nstatus: %s\n---\n# C\n\n[next](c%d.md)\n", i, status, (i+1)%30))
	}
	if _, err := k.ReadCost(nil); err != nil { // warm
		t.Fatal(err)
	}
	before := k.GraphFacetLookups()
	rc, err := k.ReadCost(nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := k.GraphFacetLookups() - before; n != 0 {
		t.Fatalf("ReadCost made %d ConceptFacets lookups, want 0", n)
	}
	if rc.ConceptBytes.Max == 0 {
		t.Fatalf("read cost = %+v", rc)
	}
	// The archived ones do not count: hiding every live one leaves nothing.
	only, err := k.ReadCost(func(id string) bool {
		return strings.HasPrefix(id, "arch/c") && k.ConceptFacets(okf.ConceptID(id)).Status == "archived"
	})
	if err != nil {
		t.Fatal(err)
	}
	if only != (ReadCost{}) {
		t.Fatalf("archived concepts counted: %+v", only)
	}
}

// WalkConceptPathsIn and CommitGate read only the files that can matter.
// Not parallel: the read hook is process-wide.
func TestCommitGate_ReadsOnlyContradictions(t *testing.T) {
	k := newTestKB(t)
	for i := 0; i < 20; i++ {
		writeTestFile(t, k.DataRoot(), fmt.Sprintf("arch/n%d.md", i), fmt.Sprintf("---\ntype: Note\ntitle: N%d\n---\n# N\n", i))
	}
	for i := 0; i < 2; i++ {
		writeTestFile(t, k.DataRoot(), fmt.Sprintf("conflicts/c%d.md", i), fmt.Sprintf(`---
type: Contradiction
resolution_status: open
involves: [arch/n%d, arch/n5]
contradiction_kind: conflict
reason: r
---
# C
`, i))
	}
	writeTestFile(t, k.DataRoot(), "conflicts/gap.md", "---\ntype: Contradiction\nresolution_status: open\ninvolves: [arch/n0]\ncontradiction_kind: open_question\n---\n# G\n")
	time.Sleep(2200 * time.Millisecond) // past the graph cache's racy window
	if _, err := k.GraphGeneration(); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var reads []string
	restore := SetReadRawHook(func(rel string) { mu.Lock(); reads = append(reads, rel); mu.Unlock() })
	defer restore()

	res, err := k.CommitGate([]okf.ConceptID{"arch/n0", "arch/n5"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass || len(res.Blockers) != 2 || res.Blockers[0].ConceptPath != "conflicts/c0.md" || res.Blockers[1].ConceptPath != "conflicts/c1.md" {
		t.Fatalf("gate = %+v", res)
	}
	mu.Lock()
	n := len(reads)
	mu.Unlock()
	if n != 3 { // the three Contradiction concepts, the gap included
		t.Fatalf("CommitGate read %d files %v, want the 3 contradictions", n, reads)
	}

	mu.Lock()
	reads = nil
	mu.Unlock()
	var seen []string
	if err := k.WalkConceptPathsIn(map[string]bool{"arch/n1": true, "arch/n2": true, "gone/x": true}, func(id okf.ConceptID, _, _ string) error {
		seen = append(seen, string(id))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || len(reads) != 2 {
		t.Fatalf("walked %v, read %v, want the 2 named concepts", seen, reads)
	}
}
