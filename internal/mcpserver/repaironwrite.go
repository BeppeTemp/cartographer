package mcpserver

import (
	"fmt"
	"os"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// repairedOut is one entry of the `repaired` array a write answers with
// (D349): how many fixes of one check the write applied to the concepts it
// wrote.
type repairedOut struct {
	Check string `json:"check"`
	Count int    `json:"count"`
}

// repairOnWriteChecks is the auto_repair list a write may apply (D349):
// broken_link and reciprocal_link_item drop or rewrite links (D309), and a
// single write has no cross-concept state for the mutual-pair guard, so they
// stay with the timer and kb_repair; artifact checks never concern a concept.
func repairOnWriteChecks(k *kb.KB) []string {
	if !k.RepairOnWrite {
		return nil
	}
	var out []string
	for _, c := range k.AutoRepair {
		if c == "broken_link" || c == "reciprocal_link_item" || artifactRepairChecks[c] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// repairWritten replaces writeFindingsFor in the write handlers (D349): it
// applies the mechanical auto_repair fixes of the concepts the call left on
// disk, then reports what remains. It must run inside gitWrap (the handler
// holds the KB lock and the repair lands in the same commit; a handler still
// never locks or commits itself). It never fails the write: an error leaves
// the content as written and the finding in the response.
//
// hashes maps a repaired concept to its new content hash. Neighbours that
// ScopedCheck reports are never rewritten.
func repairWritten(k *kb.KB, written, gone []string) (findings []findingOut, repaired []repairedOut, hashes map[string]string) {
	found := writeLintFindings(k, written, gone)
	checks := repairOnWriteChecks(k)
	if len(checks) == 0 || len(found) == 0 {
		return findingsOrNil(found), nil, nil
	}
	inChecks := map[string]bool{}
	for _, c := range checks {
		inChecks[c] = true
	}
	isWritten := map[string]bool{}
	for _, id := range written {
		isWritten[id] = true
	}
	byConcept := map[string]map[string][]int{} // concept -> check -> indexes into found
	for i, f := range found {
		if f.Fix == nil || f.Artifact || !inChecks[f.Check] {
			continue
		}
		id := uiFindingConcept(f.Path)
		if id == "" || !isWritten[id] {
			continue
		}
		if byConcept[id] == nil {
			byConcept[id] = map[string][]int{}
		}
		byConcept[id][f.Check] = append(byConcept[id][f.Check], i)
	}
	if len(byConcept) == 0 {
		return findingsOrNil(found), nil, nil
	}

	ids := make([]string, 0, len(byConcept))
	for id := range byConcept {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	counts := map[string]int{}
	for _, id := range ids {
		cd, err := k.ReadConcept(okf.ConceptID(id))
		if err != nil {
			fmt.Fprintf(os.Stderr, "cartographer: repair-on-write %s: %v\n", id, err)
			continue
		}
		fm, err := okf.ParseFrontmatter(cd.FrontmatterRaw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cartographer: repair-on-write %s: %v\n", id, err)
			continue
		}
		body := cd.Body
		perCheck := map[string]int{}
		total := 0
		for _, check := range checks { // k.AutoRepair order
			idx := byConcept[id][check]
			if len(idx) == 0 {
				continue
			}
			fixes := make([]*lint.Fix, 0, len(idx))
			for _, i := range idx {
				fixes = append(fixes, found[i].Fix)
			}
			savedFM, savedBody := fm.Serialize(), body
			changed, partial, fatal := applyFixes(fm, &body, fixes)
			if fatal != "" || len(partial) > 0 {
				// Not a clean mechanical fix: leave it to a person, finding stays.
				if restored, perr := okf.ParseFrontmatter(savedFM); perr == nil {
					fm, body = restored, savedBody
				}
				continue
			}
			if changed > 0 {
				perCheck[check] += changed
				total += changed
			}
		}
		if total == 0 {
			continue
		}
		newHash, err := k.WriteConcept(okf.ConceptID(id), fm, body, cd.ContentHash)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cartographer: repair-on-write %s: %v\n", id, err)
			continue
		}
		if hashes == nil {
			hashes = map[string]string{}
		}
		hashes[id] = newHash
		for c, n := range perCheck {
			counts[c] += n
		}
	}
	if len(hashes) == 0 {
		return findingsOrNil(found), nil, nil
	}
	for c, n := range counts {
		repaired = append(repaired, repairedOut{Check: c, Count: n})
	}
	sort.Slice(repaired, func(i, j int) bool { return repaired[i].Check < repaired[j].Check })
	return findingsOrNil(writeLintFindings(k, written, gone)), repaired, hashes
}

// applyRepairResult adds what repairWritten did to a single-concept write
// result: the post-repair content_hash and the repaired array (omitted when
// nothing was repaired).
func applyRepairResult(result map[string]interface{}, id string, repaired []repairedOut, hashes map[string]string) {
	if h, ok := hashes[id]; ok {
		result["content_hash"] = h
	}
	if len(repaired) > 0 {
		result["repaired"] = repaired
	}
}
