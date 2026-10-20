package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
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
	}, renamed, func(string, string) bool { return true })
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

// Two pages are paired as one moved only when they share title and type: an
// unrelated delete plus create under the same file name is judged as new.
func TestPairUnmatchedMoves_NeedsSamePage(t *testing.T) {
	t.Parallel()
	renamed := map[string]string{}
	pairUnmatchedMoves([]gitx.FileChange{
		{Status: "D", Path: "data/src/topic/index.md"},
		{Status: "A", Path: "data/dst/other/index.md"},
	}, renamed, func(string, string) bool { return false })
	if len(renamed) != 0 {
		t.Fatalf("paired pages that are not the same: %v", renamed)
	}
}

func TestWriteGate_UnrelatedSameNameCreateIsJudgedAsNew(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	old := "---\ntype: Note\ntitle: Old\n---\n# Old\n\n" + strings.Repeat("Old has its own words here. ", 8) + "\n\nSee [gone](../gone.md).\n"
	gateSeed(t, k, map[string]string{"arch/a/page": old})
	root := k.DataRoot()
	swap := func(title string) {
		if err := os.Remove(filepath.Join(root, "arch", "a", "page.md")); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "arch", "b"), 0o755); err != nil {
			t.Fatal(err)
		}
		// Different words per title, so git sees a delete plus an add, not a rename.
		page := "---\ntype: Note\ntitle: " + title + "\n---\n# " + title + "\n\n" + strings.Repeat(title+" has its own words here. ", 8) + "\n\nSee [gone](../gone.md).\n"
		if err := os.WriteFile(filepath.Join(root, "arch", "b", "page.md"), []byte(page), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The same page moved keeps the finding it had.
	swap("Old")
	if res := judgeWrite(k, "concept_batch", nil); res != nil {
		t.Fatalf("a moved page was refused: %+v", res.Content)
	}
	if _, err := k.CommitOp("test: moved"); err != nil {
		t.Fatal(err)
	}
	// Reset to the seed, then an unrelated page under the same file name.
	if err := os.Rename(filepath.Join(root, "arch", "b", "page.md"), filepath.Join(root, "arch", "b", "keep.md")); err != nil {
		t.Fatal(err)
	}
	_ = os.Rename(filepath.Join(root, "arch", "b", "keep.md"), filepath.Join(root, "arch", "b", "page.md"))
	_ = os.MkdirAll(filepath.Join(root, "arch", "a"), 0o755)
	if err := os.Rename(filepath.Join(root, "arch", "b", "page.md"), filepath.Join(root, "arch", "a", "page.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("test: back"); err != nil {
		t.Fatal(err)
	}
	swap("Unrelated")
	res := judgeWrite(k, "concept_batch", nil)
	if res == nil || !strings.Contains(res.Content[0].Text, "broken_link") {
		t.Fatalf("an unrelated same-name page was forgiven: %+v", res)
	}
}
