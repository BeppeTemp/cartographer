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

// repairOnWriteChecks are the fixes a write applies to what it wrote (D349),
// always since D907: no setting turns them off or shortens the list. They are
// the checks whose fix is safe to run unattended (D354) and stays inside the
// concept. broken_link and reciprocal_link_item drop or rewrite links (D309),
// and a single write has no cross-concept state for the mutual-pair guard, so
// they stay with kb_repair; artifact and map checks never concern a concept.
func repairOnWriteChecks() []string {
	var out []string
	for _, s := range lint.Checks() {
		if !s.AutoRepairSafe || s.CrossConcept || lint.ArtifactRepairCheck(s.Name) || lint.MapRepairCheck(s.Name) {
			continue
		}
		out = append(out, s.Name)
	}
	return out
}

// repairWritten is what the write handlers report findings through (D349): it
// applies the safe mechanical fixes to the concepts the call left on
// disk, then reports what remains. It must run inside gitWrap (the handler
// holds the KB lock and the repair lands in the same commit; a handler still
// never locks or commits itself). It never fails the write: an error leaves
// the content as written and the finding in the response.
//
// hashes maps a repaired concept to its new content hash. Neighbours that
// ScopedCheck reports are never rewritten.
func repairWritten(k *kb.KB, written, gone []string, view *callerView) (findings []findingOut, repaired []repairedOut, hashes map[string]string) {
	found := writeLintFindings(k, written, gone)
	checks := repairOnWriteChecks()
	if len(checks) == 0 || len(found) == 0 {
		return findingsOrNil(view.filter(found)), nil, nil
	}
	inChecks := map[string]bool{}
	for _, c := range checks {
		inChecks[c] = true
	}
	isWritten := map[string]bool{}
	for _, id := range written {
		isWritten[id] = true
	}
	byConcept := map[string][]lint.Finding{} // concept -> its repairable findings
	for _, f := range found {
		if f.Fix == nil || f.Artifact || !inChecks[f.Check] {
			continue
		}
		id := uiFindingConcept(f.Path)
		if id == "" || !isWritten[id] {
			continue
		}
		byConcept[id] = append(byConcept[id], f)
	}
	if len(byConcept) == 0 {
		return findingsOrNil(view.filter(found)), nil, nil
	}

	ids := make([]string, 0, len(byConcept))
	for id := range byConcept {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	counts := map[string]int{}
	for _, id := range ids {
		// The stage-1 applier (D355): the concept is repaired to a fixpoint
		// and written once, so a chain (rename, split, normalise) is one write.
		out, skip := repairConceptFixpoint(k, okf.ConceptID(id), checks, byConcept[id])
		if skip != nil {
			fmt.Fprintf(os.Stderr, "cartographer: repair-on-write %s: %s\n", id, skip.Reason)
			continue
		}
		if out.Hash == "" {
			continue
		}
		if hashes == nil {
			hashes = map[string]string{}
		}
		hashes[id] = out.Hash
		for c, n := range out.Changed {
			counts[c] += n
		}
	}
	if len(hashes) == 0 {
		return findingsOrNil(view.filter(found)), nil, nil
	}
	for c, n := range counts {
		repaired = append(repaired, repairedOut{Check: c, Count: n})
	}
	sort.Slice(repaired, func(i, j int) bool { return repaired[i].Check < repaired[j].Check })
	return findingsOrNil(view.filter(writeLintFindings(k, written, gone))), repaired, hashes
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
