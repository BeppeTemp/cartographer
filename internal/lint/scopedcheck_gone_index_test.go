package lint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kbtest"
)

// #1764 backstop: a deleted concept whose line is still in the curated index
// is a broken_link on that index.
func TestScopedCheck_GoneConceptStillInCuratedIndex(t *testing.T) {
	t.Parallel()
	k := kbtest.New(t)
	write := func(rel, content string) {
		abs := filepath.Join(k.DataRoot(), rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("zz/_map.md", "---\ntype: Map\nkind: map\ntitle: zz\n---\n# zz\n")
	write("zz/index.md", "---\ntype: Index\ntitle: zz\n---\n# zz\n\n- [Gone](gone.md)\n")
	got := ScopedCheck(k, ids("zz/gone"))
	if !hasCheck(got, "zz/index.md", "broken_link") {
		t.Fatalf("findings = %+v", got)
	}
	write("zz/index.md", "---\ntype: Index\ntitle: zz\n---\n# zz\n")
	if got := ScopedCheck(k, ids("zz/gone")); hasCheck(got, "zz/index.md", "broken_link") {
		t.Fatalf("findings = %+v", got)
	}
}
