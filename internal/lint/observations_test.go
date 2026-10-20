package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// observed runs the observation detectors on k, as kb_review does, and returns
// the carriers before they become items (D947): their Path and Message are what
// the detectors word.
func observed(t *testing.T, k *kb.KB) []Finding {
	t.Helper()
	var concepts []*reviewConcept
	if err := k.WalkConceptPaths(func(id okf.ConceptID, physicalPath, content string) error {
		_, body, _ := okf.SplitFrontmatter(content)
		concepts = append(concepts, &reviewConcept{id: id, body: body, content: content, path: physicalPath})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	archives, err := k.ListArchives()
	if err != nil {
		t.Fatal(err)
	}
	contracts := map[string]kb.MapContract{}
	for _, a := range archives {
		if c, err := k.ReadMapContract(a); err == nil {
			contracts[a] = c
		}
	}
	found, err := observationFindings(k, concepts, contracts, archives)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// obsFixtures build, per observation, a KB with one positive item and name the
// concept it is about; obsClosers write the page state that closes it (D907).
var obsFixtures = map[string]func(t *testing.T) (*kb.KB, string){
	"cut_concept": func(t *testing.T) (*kb.KB, string) {
		s := newStructKB(t, map[string]string{"ops": "map"})
		s.mainGraph(nil)
		return s.k, "ops/h"
	},
	"island": func(t *testing.T) (*kb.KB, string) {
		s := newStructKB(t, map[string]string{"ops": "map", "notes": "map"})
		s.mainGraph(nil)
		s.concept("notes/a", "", "notes/b")
		s.concept("notes/b", "")
		return s.k, "notes/a"
	},
	"map_misfit": func(t *testing.T) (*kb.KB, string) {
		s := newStructKB(t, map[string]string{"ops": "map", "net": "map"})
		s.concept("ops/router", "", "net/a", "net/b", "net/c", "ops/x")
		for _, id := range []string{"net/a", "net/b", "net/c", "ops/x"} {
			s.concept(id, "")
		}
		return s.k, "ops/router"
	},
	"concept_oversize": func(t *testing.T) (*kb.KB, string) {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n")
		writeFile(t, k.DataRoot(), "m/big.md", "---\ntype: Note\ntitle: Big\n---\n"+strings.Repeat("a", conceptOversizeThreshold+1)+"\n")
		return k, "m/big"
	},
	"map_oversize": func(t *testing.T) (*kb.KB, string) {
		k := tempKB(t)
		oversizeMap(t, k, "kind: map\n", mapOversizeThreshold+1, 0)
		return k, "arch/_map"
	},
	"facet_sprawl": func(t *testing.T) (*kb.KB, string) {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
		for i := 0; i < 40; i++ {
			writeFile(t, k.DataRoot(), fmt.Sprintf("m/c%d.md", i), fmt.Sprintf("---\ntype: Note\ntitle: C\ntags: [common, t%d]\n---\n# C\n", i))
		}
		return k, "m/_map"
	},
	"missing_value_contract": func(t *testing.T) (*kb.KB, string) {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "kb-a/_map.md", "---\ntype: Map\nkind: map\ntitle: A\n---\n# A\n")
		for i, v := range []string{"open", "open", "done", "done", "done"} {
			writeFile(t, k.DataRoot(), fmt.Sprintf("kb-a/t%d.md", i), fmt.Sprintf("---\ntype: Task\ntitle: T%d\nstatus: %s\n---\nx\n", i, v))
		}
		return k, "kb-a/_map"
	},
}

var obsClosers = map[string]func(t *testing.T, k *kb.KB){
	// A second path around the hub: its leaves link each other.
	"cut_concept": func(t *testing.T, k *kb.KB) {
		writeFile(t, k.DataRoot(), "ops/h.md", "---\ntype: Note\ntitle: ops/h\n---\nSee [[ops/l1]].\n")
		writeFile(t, k.DataRoot(), "ops/l1.md", "---\ntype: Note\ntitle: ops/l1\n---\nSee [[ops/l2]] and [[ops/m1]].\n")
		writeFile(t, k.DataRoot(), "ops/l2.md", "---\ntype: Note\ntitle: ops/l2\n---\nSee [[ops/l3]] and [[ops/m2]].\n")
		writeFile(t, k.DataRoot(), "ops/l3.md", "---\ntype: Note\ntitle: ops/l3\n---\nSee [[ops/m3]].\n")
	},
	// A page outside the group links into it.
	"island": func(t *testing.T, k *kb.KB) {
		writeFile(t, k.DataRoot(), "ops/m1.md", "---\ntype: Note\ntitle: ops/m1\n---\nSee [[ops/m2]], [[ops/p]] and [[notes/a]].\n")
	},
	// The page moves to the map its links point to.
	"map_misfit": func(t *testing.T, k *kb.KB) {
		if err := os.Remove(filepath.Join(k.DataRoot(), "ops/router.md")); err != nil {
			t.Fatal(err)
		}
		writeFile(t, k.DataRoot(), "net/router.md", "---\ntype: Note\ntitle: router\n---\nSee [[net/a]], [[net/b]], [[net/c]] and [[ops/x]].\n")
	},
	// Split the page: the body is under the threshold.
	"concept_oversize": func(t *testing.T, k *kb.KB) {
		writeFile(t, k.DataRoot(), "m/big.md", "---\ntype: Note\ntitle: Big\n---\nA short summary.\n")
	},
	// Archive one page: archived pages do not count.
	"map_oversize": func(t *testing.T, k *kb.KB) {
		writeFile(t, k.DataRoot(), "arch/concept-0.md", "---\ntype: Note\nstatus: archived\n---\nOld.\n")
	},
	// Converge the tags on a small vocabulary.
	"facet_sprawl": func(t *testing.T, k *kb.KB) {
		for i := 0; i < 40; i++ {
			writeFile(t, k.DataRoot(), fmt.Sprintf("m/c%d.md", i), "---\ntype: Note\ntitle: C\ntags: [common]\n---\n# C\n")
		}
	},
	// Declare the field's values in the map.
	"missing_value_contract": func(t *testing.T, k *kb.KB) {
		writeFile(t, k.DataRoot(), "kb-a/_map.md", "---\ntype: Map\nkind: map\ntitle: A\nfield_values.Task.status: [open, done]\n---\n# A\n")
	},
}

// TestObservationsCloseOnPageState is the D907 trap for the D947 observations:
// every observation has a fixture that yields a kb_review item of kind
// observation naming its concept, and a page state that makes the item go, with
// nothing kept outside the pages. A new observation without both fails here.
func TestObservationsCloseOnPageState(t *testing.T) {
	for _, o := range Observations() {
		t.Run(o.Name, func(t *testing.T) {
			fixture, ok := obsFixtures[o.Name]
			if !ok {
				t.Fatalf("no fixture for observation %s", o.Name)
			}
			closer, ok := obsClosers[o.Name]
			if !ok {
				t.Fatalf("no closing page state for observation %s", o.Name)
			}
			k, id := fixture(t)
			has := func() bool {
				for _, it := range itemsOf(review(t, k), ReviewObservation) {
					if it.Check == o.Name && names([]ReviewItem{it}, id) {
						return true
					}
				}
				return false
			}
			if !has() {
				t.Fatalf("fixture produced no %s observation naming %s: %+v", o.Name, id, itemsOf(review(t, k), ReviewObservation))
			}
			closer(t, k)
			if has() {
				t.Fatalf("%s still listed after its closing state", o.Name)
			}
		})
	}
}

// An observation is never a finding (D947): a lint run over a KB that trips
// every one returns none of them, and kb_review returns all of them.
func TestObservationsAreNeverFindings(t *testing.T) {
	t.Parallel()
	for name, fixture := range obsFixtures {
		k, _ := fixture(t)
		findings, err := Run(k, "", false)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range findings {
			if _, isObservation := ObservationSpecOf(f.Check); isObservation {
				t.Errorf("%s: Run returned the observation %s: %+v", name, f.Check, f)
			}
		}
		for _, f := range CheckConcept(k, "m/big", "---\ntype: Note\ntitle: Big\n---\n"+strings.Repeat("a", conceptOversizeThreshold+1)+"\n") {
			if _, isObservation := ObservationSpecOf(f.Check); isObservation {
				t.Errorf("CheckConcept returned the observation %s", f.Check)
			}
		}
	}
}

// Every observation of the registry is produced by some fixture, so the table
// cannot list a name no detector emits.
func TestObservationsAreAllEmitted(t *testing.T) {
	t.Parallel()
	for _, o := range Observations() {
		fixture, ok := obsFixtures[o.Name]
		if !ok {
			t.Errorf("no fixture for %s", o.Name)
			continue
		}
		k, _ := fixture(t)
		seen := false
		for _, f := range observed(t, k) {
			if f.Check == o.Name {
				seen = true
			}
		}
		if !seen {
			t.Errorf("%s: the detectors did not emit it for its own fixture", o.Name)
		}
	}
}

// An observation item keeps the proposal a missing_value_contract carries
// (D296): the vocabulary to declare and the values that converge on it.
func TestObservationItemKeepsTheProposal(t *testing.T) {
	t.Parallel()
	k, _ := obsFixtures["missing_value_contract"](t)
	for _, it := range itemsOf(review(t, k), ReviewObservation) {
		if it.Check == "missing_value_contract" {
			if it.Proposal == nil || it.Proposal.Field != "status" {
				t.Fatalf("proposal = %+v", it.Proposal)
			}
			if !it.wholeGraph {
				t.Error("a map-wide observation reaches only a caller that sees the whole KB")
			}
			return
		}
	}
	t.Fatal("no missing_value_contract observation")
}

// The order of the items of the kind is the registry's category order (pages,
// then links, then maps), never a gravity (D947).
func TestObservationItemsFollowCategoryOrder(t *testing.T) {
	t.Parallel()
	pages, links, maps := categoryWeight(CategoryPages), categoryWeight(CategoryLinks), categoryWeight(CategoryMaps)
	if !(pages > links && links > maps) {
		t.Fatalf("weights pages=%d links=%d maps=%d: earlier categories must weigh more", pages, links, maps)
	}
}
