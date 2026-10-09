package lint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// D322: archived is a reserved lifecycle stage after done: finished, retired,
// never open, and in no synonym family.
func TestArchivedLifecycle(t *testing.T) {
	fm, _ := okf.ParseFrontmatter("type: Note\nstatus: archived")
	if !closedPhase(fm, nil) {
		t.Error("archived must be closed")
	}
	if openPhase(fm, nil) {
		t.Error("archived must never be open")
	}
	if !retired("archived") {
		t.Error("archived must be retired")
	}
	for fam, members := range ValueSynonymFamilies {
		for _, m := range members {
			if NormValue(m) == "archived" {
				t.Errorf("archived must stay out of the synonym families, found in %s", fam)
			}
		}
	}
}

func TestLinkToRetired_Archived(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\nkind: map\n---\n")
	writeFile(t, k.DataRoot(), "ops/old.md", "---\ntype: Note\ntitle: Old\nstatus: archived\n---\nOld.\n")
	writeFile(t, k.DataRoot(), "ops/live.md", "---\ntype: Note\ntitle: Live\n---\nSee [old](old.md).\n")
	got := ScopedCheck(k, ids("ops/old"))
	if !hasCheck(got, "ops/old.md", "link_to_retired") {
		t.Fatalf("a live page linking an archived one must raise link_to_retired: %v", got)
	}
}

// A digest records what it archived, and a journal's own index lists its
// entries: neither is a live page relying on a retired one.
func TestLinkToRetired_DigestAndJournalIndexExempt(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "j/_map.md", "---\ntype: Map\ntitle: J\nkind: journal\n---\n")
	writeFile(t, k.DataRoot(), "j/e.md", "---\ntype: Note\ntitle: E\nstatus: archived\n---\n# E\n")
	writeFile(t, k.DataRoot(), "j/index.md", "---\ntype: Index\ntitle: J\n---\n# J\n\n- [E](e.md)\n")
	writeFile(t, k.DataRoot(), "j/archive-2026-q3.md", "---\ntype: Note\ntitle: Digest\nstatus: reference\n---\n# D\n\n[E](e.md)\n")
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\ntitle: Ops\nkind: map\n---\n")
	writeFile(t, k.DataRoot(), "ops/q3.md", "---\ntype: digest\ntitle: Q3\n---\n# Q3\n\n[E](../j/e.md)\n")
	got, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if hasCheck(got, "j/e.md", "link_to_retired") {
		t.Fatalf("digest and journal index must not raise link_to_retired: %v", got)
	}
}

func TestMapOversize_ArchivedNotCounted(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md", "---\ntype: Map\ntitle: Arch\nkind: map\n---\n")
	for i := 0; i < mapOversizeThreshold; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("arch/c-%d.md", i), "---\ntype: Note\n---\nContent.\n")
	}
	for i := 0; i < 5; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("arch/old-%d.md", i), "---\ntype: Note\nstatus: archived\n---\nOld.\n")
	}
	got, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if hasCheck(got, "arch/_map.md", "map_oversize") {
		t.Fatalf("archived concepts must not count toward map_oversize: %v", got)
	}
}

func harvestKB(t *testing.T, contract, entry string) *kb.KB {
	t.Helper()
	withNow(t, "2026-10-01")
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "j/_map.md", "---\ntype: Map\ntitle: J\nkind: journal\n"+contract+"---\n")
	writeFile(t, k.DataRoot(), "j/e.md", entry)
	return k
}

func TestHarvestCandidate(t *testing.T) {
	old := "---\ntype: Note\ntitle: E\nstatus: done\ntimestamp: 2026-08-02\n---\n# E\n\n## Root cause\n\nx\n\n## Timeline\n\ny\n"
	cases := []struct {
		name, contract, entry string
		want                  bool
	}{
		{"closed and 60 days old", "", old, true},
		{"archived", "", "---\ntype: Note\ntitle: E\nstatus: archived\ntimestamp: 2026-08-02\n---\n# E\n", false},
		{"only 30 days old", "", "---\ntype: Note\ntitle: E\nstatus: done\ntimestamp: 2026-09-01\n---\n# E\n", false},
		{"contract harvest_after 90", "harvest_after: 90\n", old, false},
		{"open entry", "", "---\ntype: Note\ntitle: E\nstatus: open\ntimestamp: 2026-08-02\n---\n# E\n", false},
		{"harvest_after 0 is off", "harvest_after: 0\n", old, false},
		{"resolved outcome", "open_field: outcome\nopen_statuses: [open]\n", "---\ntype: Note\ntitle: E\nstatus: active\noutcome: resolved\ntimestamp: 2026-08-02\n---\n# E\n", true},
		{"open outcome, active status", "open_field: outcome\nopen_statuses: [open]\n", "---\ntype: Note\ntitle: E\nstatus: active\noutcome: open\ntimestamp: 2026-08-02\n---\n# E\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := harvestKB(t, tc.contract, tc.entry)
			got := itemsOf(review(t, k), ReviewHarvestCandidate)
			if tc.want != names(got, "j/e") {
				t.Fatalf("want candidate=%v, got %+v", tc.want, got)
			}
			if tc.want && got[0].Evidence == "" {
				t.Fatal("evidence is empty")
			}
		})
	}
}

func TestHarvestCandidate_EvidenceNamesDurableSections(t *testing.T) {
	k := harvestKB(t, "", "---\ntype: Note\ntitle: E\nstatus: resolved\ntimestamp: 2026-08-02\n---\n# E\n\n## Root cause\n\nx\n\n## Lezioni\n\ny\n\n## Timeline\n\nz\n")
	got := itemsOf(review(t, k), ReviewHarvestCandidate)
	if len(got) != 1 || got[0].Evidence == "" {
		t.Fatalf("want one item: %+v", got)
	}
	for _, want := range []string{"2 durable-looking", "Root cause", "Lezioni"} {
		if !strings.Contains(got[0].Evidence, want) {
			t.Errorf("evidence %q lacks %q", got[0].Evidence, want)
		}
	}
}
