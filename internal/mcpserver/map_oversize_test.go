package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// D716: a write that takes a map from at most its threshold to above it
// carries one map_oversize finding; later writes into the oversize map and
// updates do not.

func oversizeServer(t *testing.T, existing int) (*Server, *kb.KB) {
	t.Helper()
	k := setupTestKB(t)
	writeKBFile(t, k, "m/_map.md", "---\ntype: Map\ntitle: M\nkind: map\noversize_concepts: 3\n---\n")
	for i := 0; i < existing; i++ {
		writeKBFile(t, k, fmt.Sprintf("m/old-%d.md", i), "---\ntype: Note\ntitle: Old\n---\nBody.\n")
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return s, k
}

func oversizeCount(t *testing.T, raw string) int {
	t.Helper()
	return strings.Count(raw, `"map_oversize"`)
}

func TestConceptWrite_MapOversizeOnCrossingOnly(t *testing.T) {
	t.Parallel()
	s, _ := oversizeServer(t, 3)
	create := func(id string) string {
		return callTool(t, s, "concept_write", fmt.Sprintf(`{"id":%q,"frontmatter":{"type":"Note","title":"T"},"body":"Body.\n"}`, id)).Content[0].Text
	}
	if got := oversizeCount(t, create("m/n1")); got != 1 {
		t.Fatalf("crossing write: %d map_oversize findings, want 1", got)
	}
	if got := oversizeCount(t, create("m/n2")); got != 0 {
		t.Fatalf("write into an already-oversize map: %d findings, want 0", got)
	}
	upd := callTool(t, s, "concept_write", `{"id":"m/n1","frontmatter":{"type":"Note","title":"T2"},"body":"Body 2.\n"}`).Content[0].Text
	if oversizeCount(t, upd) != 0 {
		t.Fatalf("update reported map_oversize: %s", upd)
	}
}

func TestConceptNew_MapOversizeOnCrossing(t *testing.T) {
	t.Parallel()
	s, k := oversizeServer(t, 3)
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTpl := func(content string) {
		if err := os.WriteFile(filepath.Join(k.Root, "templates", "note.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeTpl("---\ntype: Note\ntitle: \"{{title}}\"\n---\n# {{title}}\n")
	res := callTool(t, s, "concept_new", `{"template":"note","id":"m/fresh","vars":{"title":"Fresh"}}`)
	if res.IsError || oversizeCount(t, res.Content[0].Text) != 1 {
		t.Fatalf("concept_new crossing: %+v", res)
	}
}

func TestConceptBatch_MapOversizeOnceOnLastCreated(t *testing.T) {
	t.Parallel()
	s, k := oversizeServer(t, 2)
	ops := []map[string]any{
		{"op": "write", "id": "m/a", "frontmatter": map[string]any{"type": "Note", "title": "A"}, "body": "A\n"},
		{"op": "write", "id": "m/b", "frontmatter": map[string]any{"type": "Note", "title": "B"}, "body": "B\n"},
		{"op": "patch", "id": "m/old-0", "old_string": "Body.", "new_string": "Patched."},
		{"op": "write", "id": "m/c", "frontmatter": map[string]any{"type": "Note", "title": "C"}, "body": "C\n"},
	}
	for i := range ops {
		if ops[i]["op"] == "patch" {
			ops[i]["if_match"] = readHash(t, k, "m/old-0")
		}
	}
	raw, _ := json.Marshal(map[string]any{"operations": ops})
	res := callTool(t, s, "concept_batch", string(raw))
	if res.IsError {
		t.Fatalf("%+v", res.Content)
	}
	var out struct {
		Results []struct {
			ID       string `json:"id"`
			Findings []struct {
				Check string `json:"check"`
			} `json:"findings"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	for _, r := range out.Results {
		n := 0
		for _, f := range r.Findings {
			if f.Check == "map_oversize" {
				n++
			}
		}
		want := 0
		if r.ID == "m/c" { // last created op of the map; the patch is not a creation
			want = 1
		}
		if n != want {
			t.Errorf("%s: %d map_oversize findings, want %d", r.ID, n, want)
		}
	}
}
