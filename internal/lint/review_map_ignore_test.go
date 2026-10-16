package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// addMapIgnore adds lint_ignore: [kinds] to the existing frontmatter of
// <mapName>/_map.md, keeping the keys the fixture put there.
func addMapIgnore(t *testing.T, k *kb.KB, mapName string, kinds ...string) {
	t.Helper()
	path := filepath.Join(k.DataRoot(), mapName, "_map.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := "lint_ignore: [" + strings.Join(kinds, ", ") + "]\n"
	if !strings.HasPrefix(string(raw), "---\n") {
		t.Fatalf("%s has no frontmatter", path)
	}
	out := "---\n" + line + strings.TrimPrefix(string(raw), "---\n")
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lintFindings(t *testing.T, k *kb.KB) []Finding {
	t.Helper()
	f, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestMapLintIgnoreDismissesReviewKinds (D306, #829): a map's lint_ignore
// takes every check a concept can silence, the kb_review kinds included. For
// every kind, naming it in the _map.md of the item's map drops the item and
// is not reported as lint_ignore_invalid.
func TestMapLintIgnoreDismissesReviewKinds(t *testing.T) {
	for _, kind := range ReviewKinds {
		t.Run(kind, func(t *testing.T) {
			fixture, ok := reviewFixtures[kind]
			if !ok {
				t.Fatalf("no review fixture for %s", kind)
			}
			k, id := fixture(t, false)
			if got := itemsOf(review(t, k), kind); !names(got, id) {
				t.Fatalf("fixture produced no %s item naming %s: %+v", kind, id, got)
			}
			mapName, _, _ := strings.Cut(id, "/")
			addMapIgnore(t, k, mapName, kind)
			if hasCheck(lintFindings(t, k), mapName+"/_map.md", "lint_ignore_invalid") {
				t.Fatalf("lint_ignore: [%s] in _map.md reported invalid", kind)
			}
			if got := itemsOf(review(t, k), kind); names(got, id) {
				t.Fatalf("lint_ignore: [%s] in _map.md did not dismiss: %+v", kind, got)
			}
		})
	}
}

// TestMapLintIgnoreDuplicateRepro is the issue's reproduction: two
// near-identical concepts in notes/, the map accepting duplicate_candidate.
func TestMapLintIgnoreDuplicateRepro(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "notes/_map.md", "---\ntype: Map\ntitle: Notes\n---\n")
	writeFile(t, k.DataRoot(), "notes/backup-restore.md", "---\ntype: Note\ntitle: Backup restore procedure\n---\n# A\n")
	writeFile(t, k.DataRoot(), "notes/restore-backup.md", "---\ntype: Note\ntitle: The restore of a backup procedure\n---\n# B\n")
	if got := itemsOf(review(t, k), ReviewDuplicate); len(got) != 1 {
		t.Fatalf("want the pair before the map accepts it: %+v", got)
	}
	writeFile(t, k.DataRoot(), "notes/_map.md", "---\ntype: Map\ntitle: Notes\nlint_ignore: [duplicate_candidate]\n---\n")
	if got := itemsOf(review(t, k), ReviewDuplicate); len(got) != 0 {
		t.Fatalf("the map's lint_ignore dismisses nothing: %+v", got)
	}
	if f := lintFindings(t, k); hasCheck(f, "notes/_map.md", "lint_ignore_invalid") {
		t.Fatalf("a review kind in a map's lint_ignore is valid: %+v", f)
	}
}

// TestMapLintIgnoreReviewScope pins where a map's dismissal reaches: items
// that name a concept of the map, and nothing else.
func TestMapLintIgnoreReviewScope(t *testing.T) {
	t.Parallel()
	const dup = "type: Service\nresource: https://example.com/gw\n"
	build := func(t *testing.T, mapIgnore string) *kb.KB {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
		writeFile(t, k.DataRoot(), "n/_map.md", "---\ntype: Map\ntitle: N\n"+mapIgnore+"---\n")
		// A pair inside m, a pair across m and n, a pair at the KB root.
		writeFile(t, k.DataRoot(), "m/a1.md", "---\n"+dup+"title: Alpha one\n---\n# A\n")
		writeFile(t, k.DataRoot(), "m/a2.md", "---\n"+dup+"title: Alpha two\n---\n# A\n")
		writeFile(t, k.DataRoot(), "m/x.md", "---\ntype: Database\nresource: https://example.com/db\ntitle: Orders database\n---\n# X\n")
		writeFile(t, k.DataRoot(), "n/x.md", "---\ntype: Database\nresource: https://example.com/db\ntitle: Orders store\n---\n# X\n")
		writeFile(t, k.DataRoot(), "r1.md", "---\ntype: Cache\nresource: https://example.com/cache\ntitle: Session cache one\n---\n# R\n")
		writeFile(t, k.DataRoot(), "r2.md", "---\ntype: Cache\nresource: https://example.com/cache\ntitle: Session cache two\n---\n# R\n")
		return k
	}
	pairs := func(k *kb.KB) map[string]bool {
		out := map[string]bool{}
		for _, it := range itemsOf(review(t, k), ReviewDuplicate) {
			out[strings.Join(it.Concepts, "|")] = true
		}
		return out
	}

	base := pairs(build(t, ""))
	for _, p := range []string{"m/a1|m/a2", "m/x|n/x", "r1|r2"} {
		if !base[p] {
			t.Fatalf("fixture lacks the pair %s: %v", p, base)
		}
	}

	// The map of one member dismisses a pair, as a concept's own ignore does;
	// a pair that names no concept of n, or none of any map, stays.
	got := pairs(build(t, "lint_ignore: [duplicate_candidate]\n"))
	if got["m/x|n/x"] || !got["m/a1|m/a2"] || !got["r1|r2"] {
		t.Fatalf("n's lint_ignore: %v", got)
	}

	// Another kind in the map's lint_ignore leaves duplicate_candidate alone.
	if got := pairs(build(t, "lint_ignore: [zombie_work]\n")); len(got) != len(base) {
		t.Fatalf("zombie_work dismissed duplicate_candidate: %v", got)
	}
}

// TestMapLintIgnoreKeepsConceptIgnore: a concept's own lint_ignore still
// dismisses its item when the map names other kinds.
func TestMapLintIgnoreKeepsConceptIgnore(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\nlint_ignore: [zombie_work]\n---\n")
	writeFile(t, k.DataRoot(), "m/a.md", "---\ntype: Service\ntitle: Alpha gateway\nresource: https://example.com/x\nlint_ignore: [duplicate_candidate]\n---\n# A\n")
	writeFile(t, k.DataRoot(), "m/b.md", "---\ntype: Service\ntitle: Beta gateway\nresource: https://example.com/x\n---\n# B\n")
	if got := itemsOf(review(t, k), ReviewDuplicate); len(got) != 0 {
		t.Fatalf("the concept's own lint_ignore stopped working: %+v", got)
	}
}

// TestMapLintIgnoreDismissOnFirst: a group item dismissed by its first
// concept only is not dismissed by the map of a later member. The shared
// retired origin is in another map than the tasks linking it.
func TestMapLintIgnoreDismissOnFirst(t *testing.T) {
	t.Parallel()
	build := func(t *testing.T, legacyIgnore, tasksIgnore string) *kb.KB {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "legacy/_map.md", "---\ntype: Map\ntitle: Legacy\n"+legacyIgnore+"---\n")
		writeFile(t, k.DataRoot(), "tasks/_map.md", "---\ntype: Map\ntitle: Tasks\n"+tasksIgnore+"---\n")
		writeFile(t, k.DataRoot(), "legacy/oldlist.md", "---\ntype: Topic\ntitle: Old list\nstatus: deprecated\n---\n# Old list\n")
		for _, n := range []string{"t1", "t2", "t3"} {
			writeFile(t, k.DataRoot(), "tasks/"+n+".md", "---\ntype: Task\ntitle: "+n+"\nstatus: open\n---\n# "+n+"\n\n## Origin\n\nFrom [the old list](../legacy/oldlist.md).\n")
		}
		return k
	}
	shared := func(k *kb.KB) []ReviewItem {
		var out []ReviewItem
		for _, it := range itemsOf(review(t, k), ReviewZombie) {
			if it.Concepts[0] == "legacy/oldlist" {
				out = append(out, it)
			}
		}
		return out
	}
	const ignore = "lint_ignore: [zombie_work]\n"
	if got := shared(build(t, "", "")); len(got) != 1 || len(got[0].Concepts) != 4 {
		t.Fatalf("fixture: want one shared-origin item over the origin and 3 tasks: %+v", got)
	}
	if got := shared(build(t, "", ignore)); len(got) != 1 {
		t.Fatalf("the tasks' map dismissed an item whose first concept is in legacy: %+v", got)
	}
	if got := shared(build(t, ignore, "")); len(got) != 0 {
		t.Fatalf("the first concept's map did not dismiss its group item: %+v", got)
	}
}

// TestMapLintIgnoreMapLevelItems: map_naming names every map by its
// pseudo-ID, and a map's lint_ignore dismisses it the same way it did before
// concepts inherited their map's: by naming the kind, nothing else.
func TestMapLintIgnoreMapLevelItems(t *testing.T) {
	t.Parallel()
	build := func(t *testing.T, bIgnore string) *kb.KB {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "a/_map.md", "---\ntype: Map\ntitle: Alpha — first notes\nlint_ignore: [duplicate_candidate, zombie_work]\n---\n")
		writeFile(t, k.DataRoot(), "b/_map.md", "---\ntype: Map\ntitle: Beta\n"+bIgnore+"---\n")
		writeFile(t, k.DataRoot(), "c/_map.md", "---\ntype: Map\ntitle: Gamma\n---\n")
		return k
	}
	if got := itemsOf(review(t, build(t, "")), ReviewMapNaming); len(got) != 1 || !names(got, "a/_map") {
		t.Fatalf("other kinds in a map's lint_ignore dismissed map_naming: %+v", got)
	}
	if got := itemsOf(review(t, build(t, "lint_ignore: [map_naming]\n")), ReviewMapNaming); len(got) != 0 {
		t.Fatalf("a map naming map_naming no longer dismisses it: %+v", got)
	}
}

// TestMapLintIgnoreGlossaryGap: for a glossary_gap the map's lint_ignore
// stops counting its concepts, as a concept's own does, instead of dropping
// an item that also names concepts of other maps.
func TestMapLintIgnoreGlossaryGap(t *testing.T) {
	t.Parallel()
	build := func(t *testing.T, inM, inN int) *kb.KB {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\nlint_ignore: [glossary_gap]\n---\n")
		writeFile(t, k.DataRoot(), "n/_map.md", "---\ntype: Map\ntitle: N\n---\n")
		for i := 0; i < inM; i++ {
			writeFile(t, k.DataRoot(), fmt.Sprintf("m/c%02d.md", i), fmt.Sprintf("---\ntype: Note\ntitle: M%d\n---\n# M\n\nThe ZFS pool.\n", i))
		}
		for i := 0; i < inN; i++ {
			writeFile(t, k.DataRoot(), fmt.Sprintf("n/c%02d.md", i), fmt.Sprintf("---\ntype: Note\ntitle: N%d\n---\n# N\n\nThe ZFS pool.\n", i))
		}
		return k
	}
	// Under the threshold once m's concepts stop counting.
	if got := itemsOf(review(t, build(t, 4, glossaryMinConcepts-1)), ReviewGlossary); len(got) != 0 {
		t.Fatalf("m's concepts still count: %+v", got)
	}
	// Over it from n alone: the item stays and names no concept of m.
	got := itemsOf(review(t, build(t, 4, glossaryMinConcepts)), ReviewGlossary)
	if len(got) != 1 || len(got[0].Concepts) != glossaryMinConcepts || strings.Contains(strings.Join(got[0].Concepts, " "), "m/") {
		t.Fatalf("n alone carries the term: %+v", got)
	}
}
