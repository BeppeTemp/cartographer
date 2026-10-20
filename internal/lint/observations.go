package lint

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// ReviewObservation (D947) is the kb_review kind of a state of a KB that has no
// single right answer: a shape a reasonable maintainer may look at and leave as
// it is. It is not a lint finding. `lint`, `gate_check`, a write's response and
// the background repair never return one; only the doctor's list does, where a
// person decides.
const ReviewObservation = "observation"

// ObservationSpec describes one observation (D947). A lint check stays in the
// lint only if it has a definite fix: a state of the KB that makes it
// disappear and is always better than the current one. An observation fails
// that test, yet each one has a page state that closes its item (D907), so the
// doctor can work it without a dismissal.
type ObservationSpec struct {
	Name string
	// Category is the reader's group (Categories); it orders the items of the
	// kind, earlier categories first.
	Category string
	// WholeGraph: the item reads concepts other than the ones it names (a count
	// over a map, a graph component), so a caller who cannot see the whole KB
	// never receives it.
	WholeGraph bool
	// Why says why no single state is always better than the current one.
	Why string
	// Closes is the page state that closes the item.
	Closes string
	// Advice is the item's suggested_action.
	Advice string
}

// observations is the single list of observations, in reading order (D947).
var observations = []ObservationSpec{
	{
		Name: "cut_concept", Category: CategoryLinks, WholeGraph: true,
		Why:    "a hub that holds pages together is often the right shape of a wiki",
		Closes: "removing or unlinking the concept disconnects fewer than 3 concepts",
		Advice: "decide whether the pages it holds up should also link to each other or to the rest of the graph; leave it when the hub is the point",
	},
	{
		Name: "island", Category: CategoryLinks, WholeGraph: true,
		Why:    "two pages that belong only to each other are a legitimate pair",
		Closes: "a page outside the group links into it, or one of its pages links out",
		Advice: "link the group to the page that should point to it, or leave it when the pages stand alone",
	},
	{
		Name: "map_misfit", Category: CategoryLinks, WholeGraph: true,
		Why:    "where a page belongs is a matter of taste once its links point elsewhere",
		Closes: "the concept moves to the map its links point to, or its links no longer mostly point to another map",
		Advice: "concept_move it to the named map, or rebalance its links; leave it when its map is the right home",
	},
	{
		Name: "concept_oversize", Category: CategoryPages,
		Why:    "a reference table or a long procedure may be one thing at any length",
		Closes: "the body is at most 30000 bytes",
		Advice: "split it: concept_expand into a dossier, or sibling satellites for a satellite; leave it when it is one reference",
	},
	{
		Name: "map_oversize", Category: CategoryMaps, WholeGraph: true,
		Why:    "a map grows by construction and none of its pages is the one that is wrong",
		Closes: "the map has at most 50 top-level concepts",
		Advice: "split the map thematically into a new map (map_create, concept_move)",
	},
	{
		Name: "facet_sprawl", Category: CategoryMaps, WholeGraph: true,
		Why:    "tags are a free vocabulary, and which values to merge is an editorial call",
		Closes: "fewer than 30 distinct tag values, or fewer than half of them used once",
		Advice: "converge the tags on a small vocabulary: reuse the most used values, drop the rest",
	},
	{
		Name: "missing_value_contract", Category: CategoryMaps, WholeGraph: true,
		Why:    "whether a field is a closed vocabulary is the map's choice, not a defect",
		Closes: "the map declares field_values for the field",
		Advice: "declare the proposed vocabulary in the map's contract (map_update field_values), then kb_repair invalid_field_value converges the pages",
	},
}

var observationIndex = func() map[string]ObservationSpec {
	m := make(map[string]ObservationSpec, len(observations))
	for _, o := range observations {
		if _, dup := m[o.Name]; dup {
			panic("lint: observation registered twice: " + o.Name)
		}
		m[o.Name] = o
	}
	return m
}()

// Observations returns every observation in reading order.
func Observations() []ObservationSpec { return append([]ObservationSpec(nil), observations...) }

// ObservationSpecOf returns the descriptor of an observation.
func ObservationSpecOf(name string) (ObservationSpec, bool) {
	o, ok := observationIndex[name]
	return o, ok
}

// newObservation builds the carrier of an observation. It is a Finding only
// inside this package, on the way to a review item: no lint entry point returns
// one. A name that is not an observation panics under test, so a check cannot be
// emitted as an observation by mistake, nor an observation as a finding.
func newObservation(check string, f Finding) Finding {
	f.Check = check
	if _, ok := ObservationSpecOf(check); !ok && testing.Testing() {
		panic("lint: " + check + " is not an observation")
	}
	return f
}

// categoryOrder is the position of a category in Categories, len(Categories)
// for one the list does not know.
func categoryOrder(category string) int {
	for i, c := range Categories {
		if c == category {
			return i
		}
	}
	return len(Categories)
}

// categoryWeight is the Weight of a review item that comes from a check or an
// observation of the given category: earlier categories weigh more, so the
// doctor starts where the lint lists first. It is an order, not a gravity.
func categoryWeight(category string) int { return len(Categories) - categoryOrder(category) }

// conceptOversizeObservation is concept_oversize for one page: a body over the
// threshold. The threshold is derived from the read guard rather than an
// independent number (D159): the observation fires at half the size at which a
// plain concept_read degrades to an outline, so the doctor hears of it before
// reads change shape.
func conceptOversizeObservation(id okf.ConceptID, body string) (Finding, bool) {
	if len(body) <= conceptOversizeThreshold {
		return Finding{}, false
	}
	// concept_expand requires exactly two segments and the write path caps depth
	// at three, so for a satellite the remedy this advice used to name is
	// structurally unavailable — and the two largest concepts in the reporting KB
	// were satellites (D159).
	remedy := "consider concept_expand to split it into a dossier"
	if len(strings.Split(string(id), "/")) > 2 {
		remedy = "this is a satellite, so concept_expand does not apply: split it into sibling satellites of the same expanded concept and link them from its index"
	}
	return newObservation("concept_oversize", Finding{
		Path:    string(id),
		Message: fmt.Sprintf("%d bytes in one concept (threshold %d; concept_read returns an outline instead of the body above %d) — %s", len(body), conceptOversizeThreshold, okf.ConceptReadSizeGuard, remedy),
	}), true
}

// observationItems builds the observation items of the whole KB (D947),
// deterministic like the rest of Review.
func observationItems(k *kb.KB, concepts []*reviewConcept, contracts map[string]kb.MapContract, archives []string) ([]ReviewItem, error) {
	found, err := observationFindings(k, concepts, contracts, archives)
	if err != nil {
		return nil, err
	}
	out := make([]ReviewItem, 0, len(found))
	for _, f := range found {
		if it, ok := observationItem(f); ok {
			out = append(out, it)
		}
	}
	return out, nil
}

// observationFindings runs the observation detectors. They are the ones the
// lint ran before these became observations; they read the graph, the
// contracts and the concepts Review already walked.
func observationFindings(k *kb.KB, concepts []*reviewConcept, contracts map[string]kb.MapContract, archives []string) ([]Finding, error) {
	lg, err := k.LinkGraph(nil)
	if err != nil {
		return nil, fmt.Errorf("lint.Review: link graph: %w", err)
	}
	st, err := analyseStructure(k, lg, archives, contracts)
	if err != nil {
		return nil, fmt.Errorf("lint.Review: structure: %w", err)
	}
	found := st.observations()

	content := make(map[okf.ConceptID]string, len(concepts))
	for _, c := range concepts {
		content[c.id] = c.content
		if f, ok := conceptOversizeObservation(c.id, c.body); ok {
			found = append(found, f)
		}
	}
	sortedArchives := append([]string(nil), archives...)
	sort.Strings(sortedArchives)
	for _, a := range sortedArchives {
		c, ok := contracts[a]
		if !ok {
			continue
		}
		found = append(found, valueContractFindings(a, c, content)...)
		found = append(found, facetSprawlFindings(a, content)...)
		if f, ok := mapOversizeFinding(a, c, mapOversizeCount(lg, a)); ok {
			found = append(found, f)
		}
	}
	return found, nil
}

// observationItem turns an observation carrier into its kb_review item. The
// item names the concepts the observation is about: a page, the members of an
// island, or a map's pseudo-ID for the ones that read a whole map.
func observationItem(f Finding) (ReviewItem, bool) {
	spec, ok := ObservationSpecOf(f.Check)
	if !ok {
		return ReviewItem{}, false
	}
	var ids []string
	switch {
	case len(f.members) > 0:
		for _, m := range f.members {
			ids = append(ids, string(m))
		}
	case strings.HasSuffix(f.Path, "/_map.md"):
		ids = []string{strings.TrimSuffix(f.Path, ".md")}
	default:
		// A concept path by construction: the carriers that reach here are a
		// concept's own (cut_concept, map_misfit, concept_oversize). Not
		// findingConceptID, which answers "" for any slashless path so that
		// KB-level files such as paths.yaml are not taken for concepts — and
		// so would drop a root-level concept's observation.
		id := strings.TrimSuffix(filepath.ToSlash(f.Path), ".md")
		if id == "" {
			return ReviewItem{}, false
		}
		ids = []string{id}
	}
	return ReviewItem{
		Kind:            ReviewObservation,
		Concepts:        ids,
		Check:           f.Check,
		Evidence:        f.Check + ": " + f.Message,
		SuggestedAction: spec.Advice,
		Proposal:        f.Proposal,
		Weight:          categoryWeight(spec.Category),
		wholeGraph:      spec.WholeGraph,
	}, true
}
