package lint

import (
	"fmt"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
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

// mapOversizeFinding is the one place that decides and words the map_oversize
// observation (D947). A journal grows by construction and needs harvest, not a
// split: the test is on contract.Kind, never on the map's name.
func mapOversizeFinding(mapName string, c kb.MapContract, count int) (Finding, bool) {
	if c.Kind == "journal" {
		return Finding{}, false
	}
	threshold := mapOversizeThreshold
	if count <= threshold {
		return Finding{}, false
	}
	return newObservation("map_oversize", Finding{
		Path:    mapName + "/_map.md",
		Message: fmt.Sprintf("%d top-level concepts in one map (threshold %d) — split it thematically into a new map", count, threshold),
	}), true
}
