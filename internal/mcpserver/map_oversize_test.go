package mcpserver

import (
	"fmt"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// D947: map_oversize is an observation, not a finding. A write that takes a map
// past its threshold is neither refused nor told of it; kb_review, where a
// person decides, lists the map.

// oversizeThreshold mirrors lint's fixed per-map concept limit (mapOversizeThreshold).
const oversizeThreshold = 50

func oversizeServer(t *testing.T, existing int) (*Server, *kb.KB) {
	t.Helper()
	k := setupTestKB(t)
	writeKBFile(t, k, "m/_map.md", "---\ntype: Map\ntitle: M\nkind: map\n---\n")
	for i := 0; i < existing; i++ {
		writeKBFile(t, k, fmt.Sprintf("m/old-%d.md", i), "---\ntype: Note\ntitle: Old\n---\nBody.\n")
	}
	forwardLinks(t, k, "m/n1", "m/n2", "m/fresh", "m/a", "m/b", "m/c")
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	// A generated index lists the new page itself (D907).
	if res := callTool(t, s, "map_update", `{"map":"m","index":"generated"}`); res.IsError {
		t.Fatalf("map_update: %+v", res.Content)
	}
	return s, k
}

func TestMapOversizeIsAnObservationNotAWriteFinding(t *testing.T) {
	t.Parallel()
	s, _ := oversizeServer(t, oversizeThreshold)
	res := callTool(t, s, "concept_write", `{"id":"m/n1","frontmatter":{"type":"Note","title":"T"},"body":"Body.\n"}`)
	if res.IsError {
		t.Fatalf("a write crossing the map threshold was refused: %s", res.Content[0].Text)
	}
	if strings.Contains(res.Content[0].Text, "map_oversize") {
		t.Fatalf("the write reported map_oversize: %s", res.Content[0].Text)
	}
	// The doctor's list holds it.
	out := callTool(t, s, "kb_review", `{"kind":"observation"}`).Content[0].Text
	if !strings.Contains(out, `"check":"map_oversize"`) || !strings.Contains(out, "m/_map") {
		t.Fatalf("kb_review lists no map_oversize observation for m: %s", out)
	}
	// And a lint over the same KB does not.
	if lintOut := callTool(t, s, "lint", `{"check":"map_oversize"}`).Content[0].Text; strings.Contains(lintOut, `"check":"map_oversize"`) {
		t.Fatalf("lint returned the map_oversize observation: %s", lintOut)
	}
}
