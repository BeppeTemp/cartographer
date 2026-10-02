package lint

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

func review(t *testing.T, k *kb.KB) []ReviewItem {
	t.Helper()
	f, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	items, err := Review(k, f)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func itemsOf(items []ReviewItem, kind string) []ReviewItem {
	var out []ReviewItem
	for _, it := range items {
		if it.Kind == kind {
			out = append(out, it)
		}
	}
	return out
}

func names(items []ReviewItem, id string) bool {
	for _, it := range items {
		for _, c := range it.Concepts {
			if c == id {
				return true
			}
		}
	}
	return false
}

// ignoreLine is the frontmatter line that dismisses kind, or "" when off.
func ignoreLine(kind string, on bool) string {
	if !on {
		return ""
	}
	return "lint_ignore: [" + kind + "]\n"
}

// reviewFixtures build, per kind, a KB with one positive item whose first
// concept carries lint_ignore: [kind] when dismiss is true. The trap test
// iterates ReviewKinds over this map, so a new kind without a fixture fails.
var reviewFixtures = map[string]func(t *testing.T, dismiss bool) (*kb.KB, string){
	ReviewDuplicate: func(t *testing.T, dismiss bool) (*kb.KB, string) {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
		writeFile(t, k.DataRoot(), "m/a.md", "---\ntype: Service\ntitle: Alpha\nresource: https://example.com/x\n"+ignoreLine(ReviewDuplicate, dismiss)+"---\n# A\n")
		writeFile(t, k.DataRoot(), "m/b.md", "---\ntype: Service\ntitle: Beta\nresource: https://example.com/x\n---\n# B\n")
		return k, "m/a"
	},
	ReviewZombie: func(t *testing.T, dismiss bool) (*kb.KB, string) {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "b/_map.md", "---\ntype: Map\ntitle: B\n---\n")
		writeFile(t, k.DataRoot(), "b/old.md", "---\ntype: Service\ntitle: Old\nstatus: deprecated\n---\n# Old\n")
		writeFile(t, k.DataRoot(), "b/task.md", "---\ntype: Task\ntitle: Task\nstatus: open\n"+ignoreLine(ReviewZombie, dismiss)+"---\n# Task\n\nSee [old](old.md).\n")
		return k, "b/task"
	},
	ReviewPromotion: func(t *testing.T, dismiss bool) (*kb.KB, string) {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "p/_map.md", "---\ntype: Map\ntitle: P\n---\n")
		writeFile(t, k.DataRoot(), "j/_map.md", "---\ntype: Map\ntitle: J\nkind: journal\npromote_to: p\n---\n")
		writeFile(t, k.DataRoot(), "j/e1.md", "---\ntype: Note\ntitle: E1\n"+ignoreLine(ReviewPromotion, dismiss)+"---\n# E1\n\n1. one\n2. two\n\n   detail\n3. three\n4. four\n5. five\n")
		return k, "j/e1"
	},
	ReviewGlossary: func(t *testing.T, dismiss bool) (*kb.KB, string) {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
		for i := 0; i < glossaryMinConcepts; i++ {
			ign := ""
			if i == 0 {
				ign = ignoreLine(ReviewGlossary, dismiss)
			}
			writeFile(t, k.DataRoot(), fmt.Sprintf("m/c%02d.md", i), fmt.Sprintf("---\ntype: Note\ntitle: C%d\n%s---\n# C\n\nThe ZFS pool.\n", i, ign))
		}
		return k, "m/c00"
	},
	ReviewLintJudgement: func(t *testing.T, dismiss bool) (*kb.KB, string) {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
		writeFile(t, k.DataRoot(), "m/a.md", "---\ntype: Note\ntitle: A\n"+ignoreLine(ReviewLintJudgement, dismiss)+"---\n# A\n\nSee [gone](gone.md).\n")
		return k, "m/a"
	},
}

// TestReviewKindsDismissible is the D298 trap: every review kind is a
// lint_ignore name (lint does not call it invalid) and lint_ignore on a
// concept the item names dismisses the item.
func TestReviewKindsDismissible(t *testing.T) {
	for _, kind := range ReviewKinds {
		t.Run(kind, func(t *testing.T) {
			if !perConceptChecks[kind] {
				t.Fatalf("%s is not in perConceptChecks: lint_ignore cannot dismiss it", kind)
			}
			fixture, ok := reviewFixtures[kind]
			if !ok {
				t.Fatalf("no review fixture for %s", kind)
			}
			k, id := fixture(t, false)
			if got := itemsOf(review(t, k), kind); !names(got, id) {
				t.Fatalf("fixture produced no %s item naming %s: %+v", kind, id, got)
			}
			k, id = fixture(t, true)
			f, _ := Run(k, "", false)
			if hasCheck(f, id+".md", "lint_ignore_invalid") {
				t.Fatalf("lint_ignore: [%s] reported invalid", kind)
			}
			if got := itemsOf(review(t, k), kind); names(got, id) {
				t.Fatalf("lint_ignore: [%s] did not dismiss: %+v", kind, got)
			}
		})
	}
}

func TestReviewDuplicateCandidate(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	writeFile(t, k.DataRoot(), "n/_map.md", "---\ntype: Map\ntitle: N\n---\n")
	// Same resource, same type → item; same resource, different type → none.
	writeFile(t, k.DataRoot(), "m/svc-a.md", "---\ntype: Service\ntitle: Gateway one\nresource: https://example.com/gw\n---\n")
	writeFile(t, k.DataRoot(), "n/svc-b.md", "---\ntype: Service\ntitle: Gateway two\nresource: https://example.com/gw\n---\n")
	writeFile(t, k.DataRoot(), "n/note-c.md", "---\ntype: Note\ntitle: Gateway notes\nresource: https://example.com/gw\n---\n")
	// Near-identical titles in one map → item; one shared word → none;
	// identical titles in two maps → none.
	writeFile(t, k.DataRoot(), "m/backup-restore.md", "---\ntype: Note\ntitle: Backup restore procedure\n---\n")
	writeFile(t, k.DataRoot(), "m/restore-backup.md", "---\ntype: Note\ntitle: The restore of a backup procedure\n---\n")
	writeFile(t, k.DataRoot(), "m/grafana-alerts.md", "---\ntype: Note\ntitle: Grafana alerting\n---\n")
	writeFile(t, k.DataRoot(), "m/grafana-dash.md", "---\ntype: Note\ntitle: Grafana dashboards\n---\n")
	writeFile(t, k.DataRoot(), "n/backup-restore.md", "---\ntype: Note\ntitle: Backup restore procedure\n---\n")

	dups := itemsOf(review(t, k), ReviewDuplicate)
	want := map[string]bool{"m/svc-a|n/svc-b": true, "m/backup-restore|m/restore-backup": true}
	got := map[string]bool{}
	for _, it := range dups {
		got[strings.Join(it.Concepts, "|")] = true
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("duplicate items = %v, want %v", got, want)
	}
	if dups[0].Concepts[0] != "m/svc-a" {
		t.Fatalf("resource duplicate must rank first: %+v", dups)
	}
}

func TestReviewDuplicateActionSatellite(t *testing.T) {
	if got := duplicateAction(idsOf("m/x", "m/x/y")); !strings.Contains(got, "concept_merge m/x/y into its parent m/x") {
		t.Fatalf("satellite pair: %q", got)
	}
	if got := duplicateAction(idsOf("m/x", "m/z")); strings.Contains(got, "concept_merge") {
		t.Fatalf("sibling pair: %q", got)
	}
}

func TestReviewZombieWork(t *testing.T) {
	k, _ := reviewFixtures[ReviewZombie](t, false)
	writeFile(t, k.DataRoot(), "b/closed.md", "---\ntype: Task\ntitle: Closed\nstatus: done\n---\n# Closed\n\nSee [old](old.md).\n")
	items := itemsOf(review(t, k), ReviewZombie)
	if !names(items, "b/task") || names(items, "b/closed") {
		t.Fatalf("zombie items: %+v", items)
	}
}

func TestReviewPromotionCandidate(t *testing.T) {
	k, _ := reviewFixtures[ReviewPromotion](t, false)
	writeFile(t, k.DataRoot(), "p/x.md", "---\ntype: Procedure\ntitle: X\n---\n# X\n")
	writeFile(t, k.DataRoot(), "j/linked.md", "---\ntype: Note\ntitle: L\n---\n# L\n\n1. a\n2. b\n3. c\n4. d\n5. e\n\nNow in [x](../p/x.md).\n")
	writeFile(t, k.DataRoot(), "j/heading.md", "---\ntype: Note\ntitle: H\n---\n# H\n\n## How to restart the gateway\n\nText.\n")
	writeFile(t, k.DataRoot(), "j/short.md", "---\ntype: Note\ntitle: S\n---\n# S\n\n1. a\n2. b\n3. c\n4. d\n")
	writeFile(t, k.DataRoot(), "j/broken-run.md", "---\ntype: Note\ntitle: B\n---\n# B\n\n1. a\n2. b\n3. c\nProse.\n4. d\n5. e\n")
	writeFile(t, k.DataRoot(), "j/code.md", "---\ntype: Note\ntitle: C\n---\n# C\n\n```\n1. a\n2. b\n3. c\n4. d\n5. e\n```\n")
	// A journal without promote_to is never a promotion source.
	writeFile(t, k.DataRoot(), "q/_map.md", "---\ntype: Map\ntitle: Q\nkind: journal\n---\n")
	writeFile(t, k.DataRoot(), "q/e.md", "---\ntype: Note\ntitle: E\n---\n# E\n\n1. a\n2. b\n3. c\n4. d\n5. e\n")
	items := itemsOf(review(t, k), ReviewPromotion)
	for id, want := range map[string]bool{"j/e1": true, "j/heading": true, "j/linked": false, "j/short": false, "j/broken-run": false, "j/code": false, "q/e": false} {
		if got := names(items, id); got != want {
			t.Errorf("%s: promotion = %v, want %v", id, got, want)
		}
	}

	// A KB-declared heading replaces the English default.
	writeFile(t, k.DataRoot(), "j/_map.md", "---\ntype: Map\ntitle: J\nkind: journal\npromote_to: p\nprocedure_headings: [Procedura]\n---\n")
	writeFile(t, k.DataRoot(), "j/it.md", "---\ntype: Note\ntitle: I\n---\n# I\n\n## Procedùra di ripristino\n")
	items = itemsOf(review(t, k), ReviewPromotion)
	if !names(items, "j/it") || names(items, "j/heading") {
		t.Fatalf("procedure_headings: %+v", items)
	}
}

func TestReviewGlossaryGap(t *testing.T) {
	k, _ := reviewFixtures[ReviewGlossary](t, false)
	for i := 0; i < glossaryMinConcepts; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("m/d%02d.md", i), "---\ntype: Note\ntitle: D\n---\n# D\n\nCPU and PostgreSQL and TODO, `NAS` in code. NFS mount.\n")
	}
	// NFS is used in 10 concepts but one glossary-map page defines it.
	writeFile(t, k.DataRoot(), "g/_map.md", "---\ntype: Map\ntitle: G\nglossary: true\n---\n")
	writeFile(t, k.DataRoot(), "g/nfs.md", "---\ntype: Term\ntitle: NFS\n---\n# NFS\n\nNFS is a protocol.\n")
	writeFile(t, k.Root, "glossary.yaml", "terms:\n  - canonical: CPU\n    aliases: [processor]\n")
	items := itemsOf(review(t, k), ReviewGlossary)
	terms := map[string]int{}
	for _, it := range items {
		terms[it.Term] = len(it.Concepts)
	}
	if !reflect.DeepEqual(terms, map[string]int{"ZFS": glossaryMinConcepts, "PostgreSQL": glossaryMinConcepts}) {
		t.Fatalf("glossary terms = %v", terms)
	}
}

func TestReviewLintJudgement(t *testing.T) {
	k, _ := reviewFixtures[ReviewLintJudgement](t, false)
	items := itemsOf(review(t, k), ReviewLintJudgement)
	if len(items) != 1 || items[0].Check != "broken_link" || items[0].Concepts[0] != "m/a" {
		t.Fatalf("lint_judgement items: %+v", items)
	}
	// orphan is not a judgement the doctor works from this list.
	for _, it := range items {
		if it.Check == "orphan" {
			t.Fatalf("orphan reported: %+v", it)
		}
	}
}

// TestReviewOrderStable pins the ranking: kind priority, then weight, then
// IDs, whatever order the generators emitted.
func TestReviewOrderStable(t *testing.T) {
	items := []ReviewItem{
		{Kind: ReviewLintJudgement, Concepts: []string{"m/a"}, Weight: 1},
		{Kind: ReviewGlossary, Concepts: []string{"m/b"}, Weight: 12},
		{Kind: ReviewGlossary, Concepts: []string{"m/a"}, Weight: 30},
		{Kind: ReviewDuplicate, Concepts: []string{"m/c", "m/d"}, Weight: 70},
		{Kind: ReviewDuplicate, Concepts: []string{"m/a", "m/b"}, Weight: 70},
		{Kind: ReviewZombie, Concepts: []string{"m/z"}, Weight: 1},
	}
	want := []string{"m/a|m/b", "m/c|m/d", "m/z", "m/a", "m/b", "m/a"}
	for i := 0; i < 20; i++ {
		shuffled := append([]ReviewItem(nil), items...)
		rand.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		SortReview(shuffled)
		var got []string
		for _, it := range shuffled {
			got = append(got, strings.Join(it.Concepts, "|"))
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestFilterReviewVisibility(t *testing.T) {
	var many []string
	for i := 0; i < glossaryMinConcepts+1; i++ {
		many = append(many, fmt.Sprintf("m/c%02d", i))
	}
	items := []ReviewItem{
		{Kind: ReviewDuplicate, Concepts: []string{"m/a", "h/b"}},
		{Kind: ReviewDuplicate, Concepts: []string{"m/a", "m/b"}},
		{Kind: ReviewZombie, Concepts: []string{"m/z"}, wholeGraph: true},
		{Kind: ReviewGlossary, Term: "ZFS", Concepts: append([]string{"h/x"}, many...)},
		{Kind: ReviewGlossary, Term: "NAS", Concepts: append([]string{"h/x", "h/y"}, many[:glossaryMinConcepts-1]...)},
	}
	visible := func(id string) bool { return strings.HasPrefix(id, "m/") }
	got := FilterReview(items, visible, false)
	if len(got) != 2 || got[0].Concepts[1] != "m/b" || got[1].Term != "ZFS" {
		t.Fatalf("filtered = %+v", got)
	}
	if len(got[1].Concepts) != glossaryMinConcepts+1 || strings.Contains(got[1].Evidence, "12") {
		t.Fatalf("glossary not recounted: %+v", got[1])
	}
	if all := FilterReview(items, func(string) bool { return true }, true); len(all) != len(items) {
		t.Fatalf("whole caller lost items: %d", len(all))
	}
}

func TestTitleJaccardThreshold(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"Backup restore procedure", "Restore backup procedure", true},
		{"Grafana", "Grafana", true},
		{"Grafana", "Grafana alerting", false},
		{"Grafana alerting", "Grafana dashboards", false},
		{"NAS backup", "NAS backup schedule", true},
	} {
		if got := Jaccard(TitleTokens(c.a), TitleTokens(c.b)) >= TitleJaccardMin; got != c.want {
			t.Errorf("%q vs %q: %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func idsOf(ids ...string) []okf.ConceptID {
	out := make([]okf.ConceptID, len(ids))
	for i, id := range ids {
		out[i] = okf.ConceptID(id)
	}
	return out
}
