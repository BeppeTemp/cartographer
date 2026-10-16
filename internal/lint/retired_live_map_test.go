package lint

import (
	"testing"
)

// A retired concept left in a live map gets neither "list it" (index_incomplete)
// nor "link it" (orphan): link_to_retired and index_lists_retired push the
// other way, and their default auto-repair would undo whatever the agent did
// (D826).

var retiredStatuses = []string{"deprecated", "superseded", "archived"}

const retiredMapContract = "---\ntype: Map\ntitle: Ops\nkind: map\nrequire_index_entry: true\n---\n"

func TestRetiredConcept_NoIndexIncomplete(t *testing.T) {
	t.Parallel()
	for _, status := range retiredStatuses {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			k := tempKB(t)
			writeFile(t, k.DataRoot(), "ops/_map.md", retiredMapContract)
			writeFile(t, k.DataRoot(), "ops/index.md", "---\ntype: Index\ntitle: Ops\n---\n\n- [B](b.md)\n")
			writeFile(t, k.DataRoot(), "ops/a.md", "---\ntype: Note\ntitle: A\nstatus: "+status+"\n---\n# A\n")
			writeFile(t, k.DataRoot(), "ops/b.md", "---\ntype: Note\ntitle: B\n---\n# B\n")

			got, err := Run(k, "", false)
			if err != nil {
				t.Fatal(err)
			}
			if hasCheck(got, "ops/index.md", "index_incomplete") {
				t.Fatalf("a %s concept must not be asked into the curated index: %v", status, got)
			}
			if sc := ScopedCheck(k, ids("ops/a")); hasCheck(sc, "ops/index.md", "index_incomplete") {
				t.Fatalf("ScopedCheck: a %s concept must not be asked into the curated index: %v", status, sc)
			}
		})
	}
}

// The loop of the issue: the entry is listed, index_lists_retired reports it,
// the auto-repair drops the line, and the next lint must stay quiet.
func TestRetiredConcept_ListedThenUnlistedIsStable(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", retiredMapContract)
	writeFile(t, k.DataRoot(), "ops/a.md", "---\ntype: Note\ntitle: A\nstatus: superseded\nsuperseded_by: ops/b\n---\n# A\n")
	writeFile(t, k.DataRoot(), "ops/b.md", "---\ntype: Note\ntitle: B\n---\n# B\n")
	writeFile(t, k.DataRoot(), "ops/c.md", "---\ntype: Note\ntitle: C\n---\n# C\n")

	writeFile(t, k.DataRoot(), "ops/index.md", "---\ntype: Index\ntitle: Ops\n---\n\n- [A](a.md)\n- [B](b.md)\n- [C](c.md)\n")
	got, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(got, "ops/index.md", "index_lists_retired") {
		t.Fatalf("a listed retired concept must raise index_lists_retired: %v", got)
	}

	writeFile(t, k.DataRoot(), "ops/index.md", "---\ntype: Index\ntitle: Ops\n---\n\n- [B](b.md)\n- [C](c.md)\n")
	got, err = Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if hasCheck(got, "ops/index.md", "index_incomplete") || hasCheck(got, "ops/index.md", "index_lists_retired") {
		t.Fatalf("after the entry is dropped, no index finding may remain: %v", got)
	}
}

func TestRetiredConcept_NoIndexIncompleteInExpandedIndex(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", retiredMapContract)
	writeFile(t, k.DataRoot(), "ops/index.md", "---\ntype: Index\ntitle: Ops\n---\n\n- [[ops/owner]]\n")
	writeFile(t, k.DataRoot(), "ops/owner/index.md", "---\ntype: Note\ntitle: Owner\n---\n# Owner\n\n- [Live](live.md)\n")
	writeFile(t, k.DataRoot(), "ops/owner/live.md", "---\ntype: Note\ntitle: Live\n---\n# Live\n")
	writeFile(t, k.DataRoot(), "ops/owner/old.md", "---\ntype: Note\ntitle: Old\nstatus: deprecated\n---\n# Old\n")

	got, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if hasCheck(got, "ops/owner/index.md", "index_incomplete") {
		t.Fatalf("a retired satellite must not be asked into the expanded index: %v", got)
	}
	if sc := ScopedCheck(k, ids("ops/owner/old")); hasCheck(sc, "ops/owner/index.md", "index_incomplete") {
		t.Fatalf("ScopedCheck: a retired satellite must not be asked into the expanded index: %v", sc)
	}
}

// Retired is the whole vocabulary of kb.IsRetired, not "not active": a
// disputed concept is still live and still owed its entry.
func TestRetiredConcept_DisputedStillAskedIntoIndex(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", retiredMapContract)
	writeFile(t, k.DataRoot(), "ops/index.md", "---\ntype: Index\ntitle: Ops\n---\n\n- [B](b.md)\n")
	writeFile(t, k.DataRoot(), "ops/a.md", "---\ntype: Note\ntitle: A\nstatus: disputed\n---\n# A\n")
	writeFile(t, k.DataRoot(), "ops/b.md", "---\ntype: Note\ntitle: B\n---\n# B\n")

	got, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(got, "ops/index.md", "index_incomplete") {
		t.Fatalf("a disputed concept is live and must stay in the index contract: %v", got)
	}
}

func TestRetiredConcept_NoOrphan(t *testing.T) {
	t.Parallel()
	for _, status := range retiredStatuses {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			k := tempKB(t)
			writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\ntitle: Ops\nkind: map\n---\n")
			writeFile(t, k.DataRoot(), "ops/old.md", "---\ntype: Note\ntitle: Old\nstatus: "+status+"\n---\n# Old\n")
			writeFile(t, k.DataRoot(), "ops/live.md", "---\ntype: Note\ntitle: Live\n---\n# Live\n")

			got, err := Run(k, "", false)
			if err != nil {
				t.Fatal(err)
			}
			if hasCheck(got, "ops/old.md", "orphan") {
				t.Fatalf("a %s concept nothing links must not be an orphan: %v", status, got)
			}
			if !hasCheck(got, "ops/live.md", "orphan") {
				t.Fatalf("a live unlinked concept stays an orphan: %v", got)
			}
			if sc := ScopedCheck(k, ids("ops/old")); hasCheck(sc, "ops/old.md", "orphan") {
				t.Fatalf("ScopedCheck: a %s concept nothing links must not be an orphan: %v", status, sc)
			}
			if sc := ScopedCheck(k, ids("ops/live")); !hasCheck(sc, "ops/live.md", "orphan") {
				t.Fatalf("ScopedCheck: a live unlinked concept stays an orphan: %v", sc)
			}
		})
	}
}

// A satellite is not at its map's top level: it was reported on the plain
// "no incoming links" rule, which retired skips like the archive-top one.
func TestRetiredConcept_NoOrphanOnSatellite(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\ntitle: Ops\nkind: map\n---\n")
	writeFile(t, k.DataRoot(), "ops/owner/index.md", "---\ntype: Note\ntitle: Owner\n---\n# Owner\n")
	writeFile(t, k.DataRoot(), "ops/owner/old.md", "---\ntype: Note\ntitle: Old\nstatus: archived\n---\n# Old\n")

	got, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if hasCheck(got, "ops/owner/old.md", "orphan") {
		t.Fatalf("a retired satellite nothing links must not be an orphan: %v", got)
	}
}
