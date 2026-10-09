package lint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

const (
	// repeatedFactLookupCap bounds the index lookups one write call spends on
	// added lines (D351). It is the knob that keeps D312's cost envelope: lines
	// past the cap are silently not examined. Measured at about 6 ms a lookup
	// on a 1,000-concept KB, so 5 keeps a 50-patch batch near the 50 ms budget
	// (decision D351); raise it only with a new measurement.
	repeatedFactLookupCap = 5
	// repeatedFactPerConceptCap bounds the repeated_fact findings one written
	// concept gets.
	repeatedFactPerConceptCap = 5
	// repeatedFactOwnersShown bounds the other concepts a finding names.
	repeatedFactOwnersShown = 5
)

// RepeatedFactLookupCap is the lookup budget of one write call, for callers
// that share it across a batch.
const RepeatedFactLookupCap = repeatedFactLookupCap

// AddedRepeatedFacts is the write-time sibling of the kb_review repeated_fact
// item (D351): the fact lines the write added to id (present in newBody, absent
// from prevBody; a created concept has an empty prevBody) that the same
// threshold of other concepts already carry. It uses the review's own
// definition of a fact line (factLines), so the two cannot disagree.
//
// lookup returns candidate concept IDs for a normalised line. It is injected so
// lint does not import the search index. Trap: a hit is a candidate, not a
// proof (the index ANDs trigram terms and ranks), so every candidate is
// re-read and kept only when factLines of its body holds the same key.
//
// budget is the number of lookups this call may still spend (shared across a
// batch); the second result is the number it used. Advice only: severity info,
// no Fix (judgement, D14), nothing on any error. It needs the previous body
// and an index, so it is not part of ScopedCheck and not of gate_check.
func AddedRepeatedFacts(k *kb.KB, id okf.ConceptID, prevBody, newBody string, budget int, lookup func(line string) []okf.ConceptID) ([]Finding, int) {
	if lookup == nil || budget <= 0 {
		return nil, 0
	}
	boilerplate := templateBoilerplate(k.TemplateTexts())
	prev := factLines(prevBody, boilerplate)
	cur := factLines(newBody, boilerplate)
	if len(cur) == 0 {
		return nil, 0
	}
	mapName, _, _ := strings.Cut(string(id), "/")
	min := repeatedFactMinConcepts
	if contract, err := k.ReadMapContract(mapName); err == nil && contract.RepeatedFactMin > 0 {
		min = contract.RepeatedFactMin
	}
	if min < 2 {
		min = 2
	}
	var ignores map[string]bool
	if data, err := k.ReadConcept(id); err == nil {
		if fm, _ := okf.ParseFrontmatter(data.FrontmatterRaw); fm != nil {
			ignores = lintIgnoreSet(fm)
		}
	}
	if meta, err := k.ReadArchiveMeta(mapName); err == nil {
		for c := range lintIgnoreSet(meta) {
			if ignores == nil {
				ignores = map[string]bool{}
			}
			ignores[c] = true
		}
	}
	if ignores[ReviewRepeatedFact] {
		return nil, 0
	}
	path := okf.IDToPath(id)

	// Added lines in order of appearance in the new body.
	var added []string
	seen := map[string]bool{}
	masked := factLinesInOrder(newBody, boilerplate)
	for _, key := range masked {
		if _, old := prev[key]; old || seen[key] {
			continue
		}
		seen[key] = true
		added = append(added, key)
	}

	var out []Finding
	used := 0
	bodies := map[okf.ConceptID]map[string]string{} // candidate -> its fact lines
	for _, key := range added {
		if used >= budget || len(out) >= repeatedFactPerConceptCap {
			break
		}
		used++
		var owners []string
		dup := map[okf.ConceptID]bool{}
		for _, cand := range lookup(key) {
			if cand == id || dup[cand] {
				continue
			}
			dup[cand] = true
			lines, ok := bodies[cand]
			if !ok {
				if data, err := k.ReadConcept(cand); err == nil {
					lines = factLines(data.Body, boilerplate)
				}
				bodies[cand] = lines
			}
			if _, same := lines[key]; same {
				owners = append(owners, string(cand))
			}
		}
		if len(owners)+1 < min {
			continue
		}
		sort.Strings(owners)
		shown := owners
		more := ""
		if len(shown) > repeatedFactOwnersShown {
			more = fmt.Sprintf(" and %d more", len(shown)-repeatedFactOwnersShown)
			shown = shown[:repeatedFactOwnersShown]
		}
		out = append(out, newFinding(ReviewRepeatedFact, Finding{
			Path: path,
			Message: fmt.Sprintf("the same line is already in %s%s: %q; keep the fact in one concept (the one whose subject it is) and link to it from the others",
				strings.Join(shown, ", "), more, cutBytes(cur[key], repeatedFactEvidenceBytes)),
		}))
	}
	return out, used
}

// factLinesInOrder is the keys of factLines in order of first appearance.
func factLinesInOrder(body string, boilerplate map[string]bool) []string {
	set := factLines(body, boilerplate)
	var out []string
	done := map[string]bool{}
	for _, raw := range strings.Split(body, "\n") {
		one := factLines(raw, boilerplate)
		for key := range one {
			if _, ok := set[key]; ok && !done[key] {
				done[key] = true
				out = append(out, key)
			}
		}
	}
	// Multi-line constructs (a table header decided by the next row) may not
	// reproduce line by line: append what the per-line pass missed, sorted.
	var rest []string
	for key := range set {
		if !done[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}
