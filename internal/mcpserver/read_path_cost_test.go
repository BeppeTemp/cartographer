package mcpserver

import (
	"fmt"
	"strings"
	"testing"
)

// Read paths on a large KB: every helper asks once, not once per map or pass.
// Counters, never timings: k.ArchiveMetaReads and k.RawReads are the test
// seams of the kb package.

// manyMapsKB returns the server and a counter of the concept reads a call makes
// itself: ReadRaw calls minus the graph cache's own (a file written a moment
// ago is re-read by every validation until its mtime is trusted).
func manyMapsKB(t *testing.T, maps, perMap int) (*Server, func() int, func() int) {
	t.Helper()
	files := map[string]string{}
	for m := 0; m < maps; m++ {
		for i := 0; i < perMap; i++ {
			files[fmt.Sprintf("ops%d/page%02d.md", m, i)] = "Walrus text.\n"
		}
	}
	s, k := graphToolKBWithKB(t, files)
	return s, func() int { return k.RawReads() - k.GraphFileReads() }, k.ArchiveMetaReads
}

func TestReadPathCost_AtlasOverviewDoesNotReadConceptsPerMap(t *testing.T) {
	t.Parallel()
	const maps, perMap = 6, 20
	s, raw, _ := manyMapsKB(t, maps, perMap)
	for name, ctx := range map[string]requestContext{"admin": adminCtx, "narrowed": narrowCtx} {
		callJSON(t, s, ctx, "atlas_overview", `{}`) // warm
		before := raw()
		out, _ := callJSON(t, s, ctx, "atlas_overview", `{}`)
		// Each map's descriptor (a few times) and the root index are read; no
		// concept is. A narrowed token also reads the descriptor of each
		// concept's map once, to decide its visibility: N reads, not M x N.
		limit := 2 * maps
		if name == "narrowed" {
			limit += maps * perMap
		}
		if got := raw() - before; got > limit {
			t.Errorf("%s: atlas_overview read %d files for %d maps x %d concepts", name, got, maps, perMap)
		}
		if name == "admin" && !strings.Contains(out, "**ops3** (20 concepts)") {
			t.Errorf("counts changed: %s", out)
		}
	}
}

func TestReadPathCost_ConceptListScopeReadsOnlyTheScope(t *testing.T) {
	t.Parallel()
	const maps, perMap = 6, 20
	s, raw, _ := manyMapsKB(t, maps, perMap)
	mustText(t, s, "concept_list", `{"scope":"ops2"}`) // warm
	before := raw()
	out := mustText(t, s, "concept_list", `{"scope":"ops2"}`)
	if got := raw() - before; got > perMap {
		t.Errorf("concept_list scope read %d files, %d in scope", got, perMap)
	}
	if strings.Count(out, "ops2/page") < perMap || strings.Contains(out, "ops1/page") {
		t.Errorf("scope result changed: %.200s", out)
	}
}

// A narrowed token's visibility is decided once per concept for the request,
// not once per pass: graph_context with a query asks for the visible graph and
// then for the seed search's candidates.
func TestReadPathCost_NarrowedGraphContextAsksVisibilityOncePerConcept(t *testing.T) {
	t.Parallel()
	files := hubKB()
	files["ops/alpha.md"] = "Walrus [beta](beta.md) and [hub](hub.md).\n"
	for i := 0; i < 10; i++ {
		files[fmt.Sprintf("hidden/h%02d.md", i)] = "Walrus [hub](../ops/hub.md).\n"
	}
	s, k := graphToolKBWithKB(t, files)
	args := `{"ids":["ops/hub"],"query":"walrus"}`
	callJSON(t, s, narrowCtx, "graph_context", args) // warm
	before := k.ArchiveMetaReads()
	callJSON(t, s, narrowCtx, "graph_context", args)
	if got, limit := k.ArchiveMetaReads()-before, len(files)+3; got > limit { // +3: the maps themselves
		t.Errorf("%d map descriptor reads for %d concepts and maps: visibility asked more than once per concept", got, limit)
	}
}
