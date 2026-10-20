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
