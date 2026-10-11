package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

func TestIndexGet_WithHashReturnsContentOverGuard(t *testing.T) {
	k := setupTestKB(t)
	writeBigIndex(t, k)
	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(callOK(t, s, "index_get", `{"with_hash":true}`)), &out); err != nil {
		t.Fatal(err)
	}
	c, _ := out["content"].(string)
	if len(c) <= conceptReadSizeGuard || out["content_hash"] == "" || out["outline"] != nil {
		t.Fatalf("with_hash must return the whole content: keys=%v", out)
	}
}

func TestConceptMove_CuratedEntryHasNoMovedFromSuffix(t *testing.T) {
	k, s := archiveFixture(t, "", "- [mover](mover.md)\n")
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "archive/_map.md"),
		[]byte("---\ntype: Map\nkind: map\ntitle: Archive\nrequire_index_entry: true\n---\n# Archive\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := callTool(t, s, "concept_move", `{"source_id":"src/mover","target_id":"archive/mover"}`); res.IsError {
		t.Fatalf("concept_move: %+v", res.Content)
	}
	idx, _ := k.ReadIndex("archive")
	if !strings.Contains(idx, "## Moved here") || !strings.Contains(idx, "- [Mover](mover.md)\n") || strings.Contains(idx, "moved from") {
		t.Fatalf("unexpected curated entry:\n%s", idx)
	}
}

func TestSourceRegisterDuplicateLocatorMismatch(t *testing.T) {
	_, s := sourceFixture(t, true)
	registerSource(t, s, `{"title":"A","source_kind":"document","sha256":"`+shaA+`","locator":"https://example.com/a","timestamp":"2026-01-01"}`)
	out := registerSource(t, s, `{"title":"B","source_kind":"document","sha256":"`+shaA+`","locator":"https://example.com/b"}`)
	if out["duplicate"] != true || out["locator_mismatch"] != true || out["existing_locator"] != "https://example.com/a" {
		t.Fatalf("got %v", out)
	}
	out = registerSource(t, s, `{"title":"B","source_kind":"document","sha256":"`+shaA+`","locator":"https://example.com/a"}`)
	if _, ok := out["locator_mismatch"]; ok {
		t.Fatalf("same locator must not flag: %v", out)
	}
}

func TestSourceListCitedByIDs(t *testing.T) {
	k, s := sourceFixture(t, true)
	registerSource(t, s, `{"title":"Done","source_kind":"web","timestamp":"2026-03-01"}`)
	for _, id := range []string{"area/z", "area/b"} {
		fm, _ := okf.ParseFrontmatter("type: Note\ntitle: N\nprovenance: [sources/2026-03-01-done]")
		if _, err := k.WriteConcept(okf.ConceptID(id), fm, "# N\n", ""); err != nil {
			t.Fatal(err)
		}
	}
	var out []struct {
		CitedBy    int      `json:"cited_by"`
		CitedByIDs []string `json:"cited_by_ids"`
	}
	if err := json.Unmarshal([]byte(callOK(t, s, "source_list", `{}`)), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].CitedBy != 2 || strings.Join(out[0].CitedByIDs, ",") != "area/b,area/z" {
		t.Fatalf("got %+v", out)
	}
}
