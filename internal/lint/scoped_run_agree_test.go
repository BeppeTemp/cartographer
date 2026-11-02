package lint

import (
	"testing"
)

// TestRunAndScopedCheckAgree pins #1503 WP6: the same finding from the whole-KB
// lint and from the write path (D312).
func TestRunAndScopedCheckAgree(t *testing.T) {
	t.Parallel()
	t.Run("index_incomplete on the satellite of an expanded concept in a generated map", func(t *testing.T) {
		t.Parallel()
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\nindex: generated\n---\n")
		writeFile(t, k.DataRoot(), "m/c/index.md", noteHead+"---\nHub.\n")
		writeFile(t, k.DataRoot(), "m/c/s.md", noteHead+"---\nSatellite.\n")
		run, err := Run(k, "", false)
		if err != nil {
			t.Fatal(err)
		}
		if !hasCheck(run, "m/c/index.md", "index_incomplete") {
			t.Fatalf("Run: want index_incomplete on m/c/index.md: %v", run)
		}
		if got := ScopedCheck(k, ids("m/c/s")); !hasCheck(got, "m/c/index.md", "index_incomplete") {
			t.Fatalf("ScopedCheck: want index_incomplete on m/c/index.md: %v", got)
		}
		if _, off := Inactive(k)["index_incomplete"]; off {
			t.Error("index_incomplete is reported inactive while it runs on an expanded concept")
		}
	})
	t.Run("a link with a backslash is broken in both", func(t *testing.T) {
		t.Parallel()
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "ops/x.md", noteHead+"---\nSee [b](sub\\b.md).\n")
		run, err := Run(k, "", false)
		if err != nil {
			t.Fatal(err)
		}
		if !hasCheck(run, "ops/x.md", "broken_link") {
			t.Fatalf("Run: want broken_link: %v", run)
		}
		if got := ScopedCheck(k, ids("ops/x")); !hasCheck(got, "ops/x.md", "broken_link") {
			t.Fatalf("ScopedCheck: want broken_link: %v", got)
		}
	})
}
