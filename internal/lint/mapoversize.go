package lint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// mapOversizeCount counts a map's top-level concepts (D313): exactly one "/"
// after the map prefix, because an expanded concept's satellites are the
// concept's own content, not siblings to split. Archived concepts (D322) are
// closed and harvested: they do not count.
func mapOversizeCount(lg *kb.LinkGraph, mapName string) int {
	n := 0
	for i, id := range lg.IDs {
		rest, ok := strings.CutPrefix(string(id), mapName+"/")
		if !ok || strings.Contains(rest, "/") {
			continue
		}
		if lg.Facets[i].Status == kb.StatusArchived {
			continue
		}
		n++
	}
	return n
}

// mapOversizeThresholdOf resolves a map's threshold: its oversize_concepts,
// else the built-in default.
func mapOversizeThresholdOf(c kb.MapContract) int {
	if c.OversizeConcepts > 0 {
		return c.OversizeConcepts
	}
	return mapOversizeThreshold
}

// mapOversizeFinding is the one place that decides and words map_oversize,
// for the whole-KB pass and for the write path alike. A journal grows by
// construction and needs harvest, not a split: the check is on contract.Kind,
// never on the map's name.
func mapOversizeFinding(mapName string, c kb.MapContract, count int) (Finding, bool) {
	if c.Kind == "journal" {
		return Finding{}, false
	}
	threshold := mapOversizeThresholdOf(c)
	if count <= threshold {
		return Finding{}, false
	}
	return newFinding("map_oversize", Finding{
		Path:    mapName + "/_map.md",
		Message: fmt.Sprintf("%d top-level concepts in one map (threshold %d) — consider a thematic split into a new map, raise oversize_concepts, or accept with lint_ignore: [map_oversize] in _map.md", count, threshold),
	}), true
}

// MapOversizeCrossings reports map_oversize for the maps that the given newly
// created concepts took from at most the threshold to above it (D716). It is
// the write-time half of the check: ScopedCheck has no "before" state and sees
// one ID at a time, so the creating handlers pass the whole created set here
// once. Only top-level creations count; updates and satellites never do, and a
// map already over the threshold stays silent (D312). lint_ignore in the
// map's _map.md applies. Advisory: nil on any error.
func MapOversizeCrossings(k *kb.KB, created []okf.ConceptID) []Finding {
	perMap := map[string]int{}
	for _, id := range created {
		m, rest, ok := strings.Cut(string(id), "/")
		if !ok || rest == "" || strings.Contains(rest, "/") {
			continue
		}
		perMap[m]++
	}
	if len(perMap) == 0 {
		return nil
	}
	lg, err := k.LinkGraph(nil)
	if err != nil {
		return nil
	}
	maps := make([]string, 0, len(perMap))
	for m := range perMap {
		maps = append(maps, m)
	}
	sort.Strings(maps)
	var findings []Finding
	for _, m := range maps {
		c, err := k.ReadMapContract(m)
		if err != nil {
			continue
		}
		after := mapOversizeCount(lg, m)
		before := after - perMap[m]
		if before > mapOversizeThresholdOf(c) {
			continue
		}
		if f, ok := mapOversizeFinding(m, c, after); ok {
			findings = append(findings, f)
		}
	}
	if len(findings) == 0 {
		return nil
	}
	findings = applyMapIgnores(k, findings)
	out := findings[:0]
	for _, f := range findings {
		if f.Check == "lint_ignore_invalid" && strings.HasSuffix(f.Path, "/_map.md") {
			continue
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
