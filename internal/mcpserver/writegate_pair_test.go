package mcpserver

import (
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// A page git reports as a delete plus an add (too small to survive its rewrite
// under the rename threshold) is still the same page moved, when no other
// deleted or added page shares its file name: its findings are not new.
func TestPairUnmatchedMoves(t *testing.T) {
	t.Parallel()
	renamed := map[string]string{"x/kept": "w/kept"}
	pairUnmatchedMoves([]gitx.FileChange{
		{Status: "D", Path: "data/src/topic/detail.md"},
		{Status: "A", Path: "data/archive/topic/detail.md"},
		{Status: "D", Path: "data/src/a/dup.md"},
		{Status: "D", Path: "data/src/b/dup.md"},
		{Status: "A", Path: "data/dst/dup.md"},
		{Status: "D", Path: "data/src/only-gone.md"},
		{Status: "A", Path: "data/dst/only-new.md"},
	}, renamed)
	if len(renamed) != 2 || renamed["archive/topic/detail"] != "src/topic/detail" || renamed["x/kept"] != "w/kept" {
		t.Fatalf("renamed = %v", renamed)
	}
}

// A finding's fix hint names the page by file name, so a page that was only
// renamed keeps the finding it had: the gate does not call it new.
func TestWriteGate_RenamedExpandedConceptKeepsItsFindings(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	seedIndexMaps(t, k, map[string]string{
		"notes/_map.md":         "---\ntype: Map\nkind: map\ntitle: Notes\nindex: generated\n---\n# Notes\n",
		"notes/topic/index.md":  "---\ntype: Note\ntitle: Topic\n---\n[detail](detail.md)\n\n[[anchor]]\n",
		"notes/topic/detail.md": "---\ntype: Note\ntitle: Detail\n---\n[index](index.md)\n",
	})
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	if res := callTool(t, s, "concept_move", `{"source_id":"notes/topic","target_id":"notes/renamed"}`); res.IsError {
		t.Fatalf("a rename was refused for a finding the page already had: %+v", res.Content)
	}
}
