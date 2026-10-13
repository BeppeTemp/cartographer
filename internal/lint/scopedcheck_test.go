package lint

import (
	"fmt"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

func ids(s ...string) []okf.ConceptID {
	out := make([]okf.ConceptID, len(s))
	for i, v := range s {
		out[i] = okf.ConceptID(v)
	}
	return out
}

const noteHead = "---\ntype: Note\ntitle: T\n"

func TestScopedCheck_BrokenLinkOnWrittenConcept(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/x.md", noteHead+"---\nSee [gone](gone.md).\n")
	got := ScopedCheck(k, ids("ops/x"))
	if !hasCheck(got, "ops/x.md", "broken_link") {
		t.Fatalf("want broken_link on ops/x.md: %v", got)
	}
}

func TestScopedCheck_IndexIncomplete(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\nrequire_index_entry: true\n---\n")
	writeFile(t, k.DataRoot(), "ops/index.md", "# Ops\n")
	writeFile(t, k.DataRoot(), "ops/x.md", noteHead+"---\nBody.\n")
	got := ScopedCheck(k, ids("ops/x"))
	if !hasCheck(got, "ops/index.md", "index_incomplete") {
		t.Fatalf("want index_incomplete: %v", got)
	}
	writeFile(t, k.DataRoot(), "ops/index.md", "# Ops\n\n- [X](x.md)\n")
	if got := ScopedCheck(k, ids("ops/x")); hasCheck(got, "ops/index.md", "index_incomplete") {
		t.Fatalf("listed concept must not be incomplete: %v", got)
	}
}

func TestScopedCheck_Orphan(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\n---\n")
	writeFile(t, k.DataRoot(), "notes/deep/x.md", noteHead+"---\nBody.\n")
	got := ScopedCheck(k, ids("notes/deep/x"))
	if !hasCheck(got, "notes/deep/x.md", "orphan") {
		t.Fatalf("want orphan: %v", got)
	}
	writeFile(t, k.DataRoot(), "ops/y.md", noteHead+"---\n[x](../notes/deep/x.md)\n")
	if got := ScopedCheck(k, ids("notes/deep/x")); hasCheck(got, "notes/deep/x.md", "orphan") {
		t.Fatalf("linked concept is no orphan: %v", got)
	}
}

// A retired concept still linked by live ones reports link_to_retired on
// itself (D313); a page that links a retired concept gets the finding too, as
// one of its linkers.
func TestScopedCheck_LinkToRetired(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\nkind: map\n---\n")
	writeFile(t, k.DataRoot(), "ops/old.md", "---\ntype: Note\ntitle: Old\nstatus: deprecated\n---\nOld.\n")
	writeFile(t, k.DataRoot(), "ops/y.md", noteHead+"---\nUses [old](old.md).\n")
	got := ScopedCheck(k, ids("ops/old"))
	if !hasCheck(got, "ops/old.md", "link_to_retired") {
		t.Fatalf("want link_to_retired on the retired concept: %v", got)
	}
	got = ScopedCheck(k, ids("ops/y"))
	if !hasCheck(got, "ops/old.md", "link_to_retired") {
		t.Fatalf("the linker's write must surface link_to_retired on its target: %v", got)
	}
}

// A neighbour's pre-existing broken link, unrelated to the written concept, is
// not part of this write's answer.
func TestScopedCheck_NeighbourPreexistingFindingExcluded(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/a.md", noteHead+"---\nSee [b](b.md) and [nowhere](nowhere.md).\n")
	writeFile(t, k.DataRoot(), "ops/b.md", noteHead+"---\nBack to [a](a.md).\n")
	got := ScopedCheck(k, ids("ops/b"))
	if hasCheck(got, "ops/a.md", "broken_link") {
		t.Fatalf("neighbour's own broken link leaked: %v", got)
	}
}

// A page still linking an ID that is gone (moved without rewriting) is a
// finding the write introduced.
func TestScopedCheck_GoneIDReportsLinkers(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/a.md", noteHead+"---\nSee [old](old.md).\n")
	got := ScopedCheck(k, ids("ops/old"))
	if !hasCheck(got, "ops/a.md", "broken_link") {
		t.Fatalf("want broken_link on the linker: %v", got)
	}
	if len(got) != 1 {
		t.Fatalf("only the linker's finding is expected: %v", got)
	}
}

func TestScopedCheck_LintIgnore(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/x.md", "---\ntype: Note\ntitle: T\nlint_ignore: [broken_link]\n---\nSee [gone](gone.md).\n")
	if got := ScopedCheck(k, ids("ops/x")); hasCheck(got, "ops/x.md", "broken_link") {
		t.Fatalf("concept lint_ignore must suppress: %v", got)
	}
	writeFile(t, k.DataRoot(), "ops/x.md", noteHead+"---\nSee [gone](gone.md).\n")
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\nlint_ignore: [broken_link]\n---\n")
	if got := ScopedCheck(k, ids("ops/x")); hasCheck(got, "ops/x.md", "broken_link") {
		t.Fatalf("map lint_ignore must suppress: %v", got)
	}
}

// A hub linked from far more pages than the cap: only the capped, sorted
// prefix of linkers is examined, so a write never becomes a full lint.
func TestScopedCheck_NeighbourCap(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	for i := 0; i < scopedNeighbourCap+50; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("ops/p%03d.md", i), noteHead+"---\nSee [hub](hub.md).\n")
	}
	got := ScopedCheck(k, ids("ops/hub"))
	n := 0
	for _, f := range got {
		if f.Check == "broken_link" {
			n++
		}
	}
	if n != scopedNeighbourCap {
		t.Fatalf("examined %d linkers, want the cap %d", n, scopedNeighbourCap)
	}
	if !hasCheck(got, "ops/p000.md", "broken_link") || hasCheck(got, fmt.Sprintf("ops/p%03d.md", scopedNeighbourCap), "broken_link") {
		t.Fatalf("the cap must keep the sorted prefix")
	}
}

func TestScopedCheck_CleanIsNil(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\n---\n")
	writeFile(t, k.DataRoot(), "ops/a.md", noteHead+"---\nSee [b](b.md).\n")
	writeFile(t, k.DataRoot(), "ops/b.md", noteHead+"---\nSee [a](a.md).\n")
	if got := ScopedCheck(k, ids("ops/a")); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}

// A Source cited only from another concept's provenance is no orphan.
func TestScopedCheck_Orphan_ProvenanceCited(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\n---\n")
	writeFile(t, k.DataRoot(), "notes/deep/src.md", noteHead+"---\nBody.\n")
	if got := ScopedCheck(k, ids("notes/deep/src")); !hasCheck(got, "notes/deep/src.md", "orphan") {
		t.Fatalf("want orphan before any citation: %v", got)
	}
	writeFile(t, k.DataRoot(), "ops/y.md", "---\ntype: Note\ntitle: Y\nprovenance:\n  - notes/deep/src\n---\nBody.\n")
	if got := ScopedCheck(k, ids("notes/deep/src")); hasCheck(got, "notes/deep/src.md", "orphan") {
		t.Fatalf("provenance-cited concept is no orphan: %v", got)
	}
}

// --- MapOversizeCrossings (D716) ---

func crossingMap(t *testing.T, k *kb.KB, front string, existing int, extra ...string) {
	t.Helper()
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n"+front+"---\n")
	for i := 0; i < existing; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("m/old-%d.md", i), "---\ntype: Note\n---\nBody.\n")
	}
	for _, e := range extra {
		writeFile(t, k.DataRoot(), e, "---\ntype: Note\n---\nBody.\n")
	}
}

func TestMapOversizeCrossings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		front    string
		existing int
		extra    []string
		created  []okf.ConceptID
		want     bool
	}{
		{"crossing", "kind: map\noversize_concepts: 3\n", 3, []string{"m/n1.md", "m/n2.md"}, ids("m/n1", "m/n2"), true},
		{"already over", "kind: map\noversize_concepts: 3\n", 5, []string{"m/n1.md"}, ids("m/n1"), false},
		{"update only", "kind: map\noversize_concepts: 3\n", 5, nil, nil, false},
		{"satellite not counted", "kind: map\noversize_concepts: 3\n", 3, []string{"m/a/index.md", "m/a/b.md"}, ids("m/a/b"), false},
		{"journal exempt", "kind: journal\noversize_concepts: 3\n", 3, []string{"m/n1.md"}, ids("m/n1"), false},
		{"lint_ignore", "kind: map\noversize_concepts: 3\nlint_ignore: [map_oversize]\n", 3, []string{"m/n1.md"}, ids("m/n1"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := tempKB(t)
			crossingMap(t, k, tc.front, tc.existing, tc.extra...)
			got := MapOversizeCrossings(k, tc.created)
			if has := hasCheck(got, "m/_map.md", "map_oversize"); has != tc.want {
				t.Fatalf("map_oversize = %v, want %v: %v", has, tc.want, got)
			}
			if tc.want && len(got) != 1 {
				t.Fatalf("want exactly one finding: %v", got)
			}
		})
	}
}

func TestMapOversizeCrossings_ArchivedNotCounted(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	crossingMap(t, k, "kind: map\noversize_concepts: 3\n", 2)
	writeFile(t, k.DataRoot(), "m/done.md", "---\ntype: Note\nstatus: archived\n---\nBody.\n")
	writeFile(t, k.DataRoot(), "m/n1.md", "---\ntype: Note\n---\nBody.\n")
	// 3 live + 1 archived: after = 3, not over 3.
	if got := MapOversizeCrossings(k, ids("m/n1")); hasCheck(got, "m/_map.md", "map_oversize") {
		t.Fatalf("archived pages must not count: %v", got)
	}
}
