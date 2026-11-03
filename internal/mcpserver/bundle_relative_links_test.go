package mcpserver

import "testing"

// A link starting with "/" is a path from the KB root (docs/conventions.md):
// it resolves like any link, shows in the graph, and concept_move rewrites it
// keeping the bundle-relative form (#1503 WP8).
func TestBundleRelativeLinks(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	files := satMapFiles("arch", "ops", "area")
	files["arch/a.md"] = satPage("See [b](/ops/b.md#top) and [c](/ops/c) and [home](//example.com/ops/b.md).\n")
	files["ops/b.md"] = satPage("b\n")
	files["ops/c.md"] = satPage("c\n")
	satWriteFiles(t, k, files)

	if got := satBrokenLinks(t, k); len(got) != 0 {
		t.Fatalf("a bundle-relative link is broken: %v", got)
	}
	lg, err := k.LinkGraph(nil)
	if err != nil {
		t.Fatal(err)
	}
	from, to := lg.Index["arch/a"], lg.Index["ops/b"]
	found := false
	for _, o := range lg.Graph.Out[from] {
		found = found || o == to
	}
	if !found {
		t.Fatalf("no graph edge arch/a -> ops/b")
	}

	satMove(t, s, `{"source_id":"ops/b","target_id":"area/b"}`)
	satWantBody(t, k, "arch/a", "See [b](/area/b.md#top) and [c](/ops/c) and [home](//example.com/ops/b.md).\n")
	if got := satBrokenLinks(t, k); len(got) != 0 {
		t.Fatalf("broken after the move: %v", got)
	}
}
