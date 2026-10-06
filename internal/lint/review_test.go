package lint

import (
	"fmt"
	"math/rand"
	"reflect"
	"slices"
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

// sharedFact is a prose line long enough to count as a fact (D301).
const sharedFact = "The backup job runs nightly at 02:00 and keeps fourteen copies."

// reviewFixtures build, per kind, a KB with one positive item whose first
// concept carries lint_ignore: [kind] when dismiss is true. The trap test
// iterates ReviewKinds over this map, so a new kind without a fixture fails.
var reviewFixtures = map[string]func(t *testing.T, dismiss bool) (*kb.KB, string){
	ReviewDuplicate: func(t *testing.T, dismiss bool) (*kb.KB, string) {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
		writeFile(t, k.DataRoot(), "m/a.md", "---\ntype: Service\ntitle: Alpha gateway\nresource: https://example.com/x\n"+ignoreLine(ReviewDuplicate, dismiss)+"---\n# A\n")
		writeFile(t, k.DataRoot(), "m/b.md", "---\ntype: Service\ntitle: Beta gateway\nresource: https://example.com/x\n---\n# B\n")
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
	ReviewRepeatedFact: func(t *testing.T, dismiss bool) (*kb.KB, string) {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
		for i := 0; i < repeatedFactMinConcepts; i++ {
			ign := ""
			if i == 0 {
				ign = ignoreLine(ReviewRepeatedFact, dismiss)
			}
			writeFile(t, k.DataRoot(), fmt.Sprintf("m/r%d.md", i), fmt.Sprintf("---\ntype: Note\ntitle: R%d\n%s---\n# R\n\n%s\n", i, ign, sharedFact))
		}
		return k, "m/r0"
	},
	ReviewReadHotspot: func(t *testing.T, dismiss bool) (*kb.KB, string) {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\nhotspot_in_degree: 3\nhotspot_bytes: 100\n---\n")
		writeFile(t, k.DataRoot(), "m/hub.md", "---\ntype: Note\ntitle: Hub\n"+ignoreLine(ReviewReadHotspot, dismiss)+"---\n# Hub\n\n"+strings.Repeat("word ", 40)+"\n")
		for i := 0; i < 3; i++ {
			writeFile(t, k.DataRoot(), fmt.Sprintf("m/l%d.md", i), fmt.Sprintf("---\ntype: Note\ntitle: L%d\n---\n# L\n\n[hub](hub.md)\n", i))
		}
		return k, "m/hub"
	},
	ReviewScatteredWork: func(t *testing.T, dismiss bool) (*kb.KB, string) {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "backlog/_map.md", "---\ntype: Map\ntitle: Backlog\n---\n")
		writeFile(t, k.DataRoot(), "ref/_map.md", "---\ntype: Map\ntitle: Ref\nwork_map: backlog\n---\n")
		writeFile(t, k.DataRoot(), "ref/page.md", "---\ntype: Topic\ntitle: Page\n"+ignoreLine(ReviewScatteredWork, dismiss)+"---\n# Page\n\n## Follow-up\n\n- [ ] migrate the host\n")
		return k, "ref/page"
	},
	ReviewMapNaming: func(t *testing.T, dismiss bool) (*kb.KB, string) {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "a/_map.md", "---\ntype: Map\ntitle: Alpha — first notes\n"+ignoreLine(ReviewMapNaming, dismiss)+"---\n")
		writeFile(t, k.DataRoot(), "b/_map.md", "---\ntype: Map\ntitle: Beta\n---\n")
		writeFile(t, k.DataRoot(), "c/_map.md", "---\ntype: Map\ntitle: Gamma\n---\n")
		return k, "a/_map"
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

// D313: the same resource with unrelated titles is not a duplicate.
func TestReviewDuplicateCandidate_ResourceNeedsSimilarTitle(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	for name, title := range map[string]string{"a": "Rotate certificates", "b": "Upgrade storage nodes", "c": "Tune ingress limits"} {
		writeFile(t, k.DataRoot(), "m/"+name+".md", "---\ntype: Task\ntitle: "+title+"\nresource: https://example.com/cluster\n---\n")
	}
	if dups := itemsOf(review(t, k), ReviewDuplicate); len(dups) != 0 {
		t.Fatalf("unrelated titles on one resource: %+v", dups)
	}
	writeFile(t, k.DataRoot(), "m/d.md", "---\ntype: Task\ntitle: Rotate the certificates\nresource: https://example.com/cluster\n---\n")
	if dups := itemsOf(review(t, k), ReviewDuplicate); len(dups) == 0 {
		t.Fatal("similar titles on one resource must be a candidate")
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
		// API and JSON are common terms; NON is "non" shouted (D307); HA,
		// two letters, stays a term even though "ha" is an ordinary word.
		writeFile(t, k.DataRoot(), fmt.Sprintf("m/d%02d.md", i), "---\ntype: Note\ntitle: D\n---\n# D\n\nCPU and PostgreSQL and TODO, `NAS` in code. NFS mount. API, JSON. NON farlo: non serve. HA ha.\n")
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
	if !reflect.DeepEqual(terms, map[string]int{"ZFS": glossaryMinConcepts, "PostgreSQL": glossaryMinConcepts, "HA": glossaryMinConcepts}) {
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

// TestReviewRepeatedFact pins what is and is not a repeated fact (D301).
func TestReviewRepeatedFact(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	writeFile(t, k.Root, "templates/note.md", "---\ntype: Note\n---\n> Fill in the details of this note before saving it anywhere.\n")
	row := "| backup-host | nightly at 02:00 | fourteen copies kept |"
	body := strings.Join([]string{
		"# Title that is a heading and long enough to count otherwise",
		"> Fill in the details of this note before saving it anywhere.",
		"- [[m/target-of-a-link-only-list-item-long-enough]]",
		"`an inline code span that is long enough to be a fact line`",
		"```",
		"a fenced code line that is also long enough to be a fact",
		"```",
		"| Host name of the machine | Schedule | Retention policy |",
		"| --- | --- | --- |",
		row,
	}, "\n")
	for i := 0; i < 3; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("m/c%d.md", i), fmt.Sprintf("---\ntype: Note\ntitle: C%d\n---\n%s\n", i, body))
	}
	// A line in only two concepts is not an item.
	for i := 0; i < 2; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("m/d%d.md", i), fmt.Sprintf("---\ntype: Note\ntitle: D%d\n---\n%s\n", i, sharedFact))
	}
	got := itemsOf(review(t, k), ReviewRepeatedFact)
	if len(got) != 1 || !strings.Contains(got[0].Evidence, "backup-host | nightly at 02:00") || got[0].Weight != 3 {
		t.Fatalf("want only the table row: %+v", got)
	}
	// A map may lower the threshold: two copies are then an item.
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\nrepeated_fact_min: 2\n---\n")
	if got := itemsOf(review(t, k), ReviewRepeatedFact); !names(got, "m/d0") {
		t.Fatalf("repeated_fact_min: 2 ignored: %+v", got)
	}
}

// TestReviewReadHotspot: both conditions are needed (D301).
func TestReviewReadHotspot(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	big := strings.Repeat("word ", hotspotMinBytes/5+10)
	// A hub of 60 small pages is not a hotspot.
	writeFile(t, k.DataRoot(), "m/hub.md", "---\ntype: Note\ntitle: Hub\n---\n# Hub\n")
	// A 20 KB page with 10 inbound links is not one either.
	writeFile(t, k.DataRoot(), "m/big.md", "---\ntype: Note\ntitle: Big\n---\n"+big+"\n")
	// A 20 KB page with 60 inbound links is.
	writeFile(t, k.DataRoot(), "m/hot.md", "---\ntype: Note\ntitle: Hot\n---\n"+big+"\n")
	for i := 0; i < 60; i++ {
		extra := ""
		if i < 10 {
			extra = " [big](big.md)"
		}
		writeFile(t, k.DataRoot(), fmt.Sprintf("m/p%02d.md", i), fmt.Sprintf("---\ntype: Note\ntitle: P%d\n---\n[hub](hub.md) [hot](hot.md)%s\n", i, extra))
	}
	items := review(t, k)
	got := itemsOf(items, ReviewReadHotspot)
	if len(got) != 1 || got[0].Concepts[0] != "m/hot" {
		t.Fatalf("hotspots: %+v", got)
	}
	if vis := FilterReview(got, func(string) bool { return true }, false); len(vis) != 0 {
		t.Fatal("a whole-graph item reached a narrowed caller")
	}
}

// TestConceptOversizePerMap: oversize_bytes lowers the threshold for its own
// map only (D301); the default stays tied to the read guard.
func TestConceptOversizePerMap(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "small/_map.md", "---\ntype: Map\ntitle: S\noversize_bytes: 100\n---\n")
	writeFile(t, k.DataRoot(), "other/_map.md", "---\ntype: Map\ntitle: O\n---\n")
	body := strings.Repeat("word ", 60)
	writeFile(t, k.DataRoot(), "small/a.md", "---\ntype: Note\ntitle: A\n---\n"+body+"\n")
	writeFile(t, k.DataRoot(), "other/a.md", "---\ntype: Note\ntitle: A\n---\n"+body+"\n")
	f, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(f, "small/a", "concept_oversize") || hasCheck(f, "other/a", "concept_oversize") {
		t.Fatalf("per-map oversize: %+v", f)
	}
}

// TestReciprocalLinkItem (D301): only a link-only item whose target links
// back; an item with prose is kept; a duplicate_link is not reported twice.
func TestReciprocalLinkItem(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	writeFile(t, k.DataRoot(), "m/a.md", "---\ntype: Note\ntitle: A\n---\n# A\n\nUses [[m/d]].\n\n## Links\n\n- [[m/b]]\n- [[m/c]] — why it matters\n- [[m/d]]\n- [[m/e]]\n")
	for _, id := range []string{"b", "c", "d"} {
		writeFile(t, k.DataRoot(), "m/"+id+".md", "---\ntype: Note\ntitle: "+id+"\n---\nBack to [[m/a]].\n")
	}
	writeFile(t, k.DataRoot(), "m/e.md", "---\ntype: Note\ntitle: e\n---\nNo link back.\n")
	f, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, x := range f {
		if x.Check == "reciprocal_link_item" {
			got = append(got, x.Fix.Field)
		}
	}
	if len(got) != 1 || got[0] != "- [[m/b]]" {
		t.Fatalf("reciprocal items: %v", got)
	}
	if countCheck(f, "m/a.md", "duplicate_link") != 1 {
		t.Fatalf("duplicate_link lost: %+v", f)
	}
}

// TestReviewScatteredWork (D302): no item without work_map; an item for a
// reference page with an unchecked item; none once it links into work_map.
func TestReviewScatteredWork(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "backlog/_map.md", "---\ntype: Map\ntitle: Backlog\n---\n")
	writeFile(t, k.DataRoot(), "backlog/t.md", "---\ntype: Task\ntitle: T\nstatus: open\n---\n# T\n")
	writeFile(t, k.DataRoot(), "ref/_map.md", "---\ntype: Map\ntitle: Ref\n---\n")
	writeFile(t, k.DataRoot(), "ref/page.md", "---\ntype: Topic\ntitle: Page\n---\n# Page\n\n## Follow-up\n\n- [ ] one\n- [ ] two\n")
	if got := itemsOf(review(t, k), ReviewScatteredWork); len(got) != 0 {
		t.Fatalf("no work_map, yet: %+v", got)
	}
	writeFile(t, k.DataRoot(), "ref/_map.md", "---\ntype: Map\ntitle: Ref\nwork_map: backlog\n---\n")
	got := itemsOf(review(t, k), ReviewScatteredWork)
	if len(got) != 1 || got[0].Weight != 2 || !strings.Contains(got[0].Evidence, `"Follow-up"`) {
		t.Fatalf("scattered work: %+v", got)
	}
	writeFile(t, k.DataRoot(), "ref/page.md", "---\ntype: Topic\ntitle: Page\n---\n# Page\n\nTracked in [[backlog/t]].\n\n- [ ] one\n")
	if got := itemsOf(review(t, k), ReviewScatteredWork); len(got) != 0 {
		t.Fatalf("linked into work_map, yet: %+v", got)
	}
	// A work_map that names no map is malformed.
	writeFile(t, k.DataRoot(), "ref/_map.md", "---\ntype: Map\ntitle: Ref\nwork_map: nowhere\n---\n")
	f, _ := Run(k, "", false)
	if !hasCheck(f, "ref/_map.md", "contract_malformed") {
		t.Fatal("work_map naming no map is not contract_malformed")
	}
}

// TestReviewMapNaming (D304): one item for a set of map titles that mixes
// shapes or capitalisation, naming every map; none for a consistent set, nor
// below three maps, and never for a caller who cannot see the whole KB.
func TestReviewMapNaming(t *testing.T) {
	for name, tc := range map[string]struct {
		titles []string
		want   string
	}{
		"consistent":    {[]string{"Infrastructure", "Smart Home", "Dev Tools"}, ""},
		"two maps":      {[]string{"Infra — the cluster", "Notes"}, ""},
		"subtitle mix":  {[]string{"Infra — the cluster", "Notes", "Clients"}, "1 with a subtitle and 2 without"},
		"case mix":      {[]string{"Smart Home", "Operational backlog", "Clients"}, "1 in Title Case and 1 in sentence case"},
		"acronyms skip": {[]string{"AI Tools", "DNS and VPN", "Home Lab"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			k := tempKB(t)
			// Folders follow the titles (D315 flags a title its folder does not echo).
			folders := make([]string, len(tc.titles))
			for i, title := range tc.titles {
				folders[i] = slugOf(subtitleSep.Split(title, 2)[0])
			}
			for i, title := range tc.titles {
				writeFile(t, k.DataRoot(), folders[i]+"/_map.md", "---\ntype: Map\ntitle: "+title+"\n---\n")
			}
			got := itemsOf(review(t, k), ReviewMapNaming)
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("unexpected: %+v", got)
				}
				return
			}
			if len(got) != 1 || len(got[0].Concepts) != len(tc.titles) || !slices.Contains(got[0].Concepts, folders[0]+"/_map") ||
				!strings.Contains(got[0].Evidence, tc.want) || !strings.Contains(got[0].Evidence, fmt.Sprintf("%q", tc.titles[0])) {
				t.Fatalf("item: %+v", got)
			}
			if vis := FilterReview(got, func(string) bool { return true }, false); len(vis) != 0 {
				t.Fatalf("a restricted caller sees map titles: %+v", vis)
			}
		})
	}
}

// TestReviewMapNamingIndividual (D315): a map title poor on its own is
// flagged even when the set is consistent, with the reason in the evidence.
func TestReviewMapNamingIndividual(t *testing.T) {
	long := "Everything we know about the cluster and its nodes"
	for name, tc := range map[string]struct {
		folders, titles []string
		want            string
	}{
		"clean":           {[]string{"infra", "clients", "notes"}, []string{"Infra", "Clients", "Notes"}, ""},
		"long title":      {[]string{"infra", "clients", "notes"}, []string{"Infra", "Clients", long}, "50 characters; map titles are 1-3 words"},
		"long subtitles":  {[]string{"infra", "clients", "notes"}, []string{"Infra — the cluster and its nodes", "Clients — who we work for here", "Notes — whatever comes to mind"}, "subtitle is a description, not a qualifier"},
		"folder mismatch": {[]string{"infra", "clients", "notes"}, []string{"Infra", "Clients", "Ricerche"}, `does not match folder "notes"`},
		"folder prefix":   {[]string{"managed-services", "clients", "notes"}, []string{"Managed", "Clients", "Notes"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			k := tempKB(t)
			for i, title := range tc.titles {
				writeFile(t, k.DataRoot(), tc.folders[i]+"/_map.md", "---\ntype: Map\ntitle: "+title+"\n---\n")
			}
			got := itemsOf(review(t, k), ReviewMapNaming)
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("unexpected: %+v", got)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0].Evidence, tc.want) {
				t.Fatalf("item: %+v", got)
			}
		})
	}
}

// TestReviewZombieSharedOrigin: a retired concept three open ones link is
// their origin. They get one item naming it first, which lint_ignore on it
// dismisses, while a concept's own retired subject still gets its own item.
func TestReviewZombieSharedOrigin(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "b/_map.md", "---\ntype: Map\ntitle: B\n---\n")
	writeFile(t, k.DataRoot(), "b/oldlist.md", "---\ntype: Topic\ntitle: Old list\nstatus: deprecated\n---\n# Old list\n")
	writeFile(t, k.DataRoot(), "b/gone.md", "---\ntype: Service\ntitle: Gone\nstatus: deprecated\n---\n# Gone\n")
	for _, n := range []string{"t1", "t2", "t3"} {
		extra := ""
		if n == "t1" {
			extra = " About [gone](gone.md)."
		}
		writeFile(t, k.DataRoot(), "b/"+n+".md", "---\ntype: Task\ntitle: "+n+"\nstatus: open\n---\n# "+n+"\n\nFrom [the old list](oldlist.md)."+extra+"\n")
	}
	got := itemsOf(review(t, k), ReviewZombie)
	if len(got) != 2 {
		t.Fatalf("want one shared item and one own item: %+v", got)
	}
	var shared, own ReviewItem
	for _, it := range got {
		if it.Concepts[0] == "b/oldlist" {
			shared = it
		} else {
			own = it
		}
	}
	if len(shared.Concepts) != 4 || shared.Weight != 3 {
		t.Fatalf("shared origin item: %+v", shared)
	}
	if own.Concepts[0] != "b/t1" || !strings.Contains(own.Evidence, "b/gone") || strings.Contains(own.Evidence, "oldlist") {
		t.Fatalf("own subject item: %+v", own)
	}
	// A member dismissing zombie_work on itself does not dismiss the group
	// (found on a real KB: two members' own dismissals hid the group item).
	writeFile(t, k.DataRoot(), "b/t2.md", "---\ntype: Task\ntitle: t2\nstatus: open\nlint_ignore: [zombie_work]\n---\n# t2\n\nFrom [the old list](oldlist.md).\n")
	if got := itemsOf(review(t, k), ReviewZombie); len(got) != 2 {
		t.Fatalf("a member's dismissal hid the group: %+v", got)
	}
	writeFile(t, k.DataRoot(), "b/oldlist.md", "---\ntype: Topic\ntitle: Old list\nstatus: deprecated\nlint_ignore: [zombie_work]\n---\n# Old list\n")
	if got := itemsOf(review(t, k), ReviewZombie); len(got) != 1 || got[0].Concepts[0] != "b/t1" {
		t.Fatalf("dismissing the origin once: %+v", got)
	}
}

// TestReviewDuplicateSkipsNamedSatellite: a satellite titled "<parent> — part"
// is the split concept_expand made, not a duplicate of its parent.
func TestReviewDuplicateSkipsNamedSatellite(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	writeFile(t, k.DataRoot(), "m/plan/index.md", "---\ntype: Topic\ntitle: Operational evolution plan\n---\n# Plan\n")
	writeFile(t, k.DataRoot(), "m/plan/phases.md", "---\ntype: Topic\ntitle: Operational evolution plan — Phases\n---\n# Phases\n")
	if got := itemsOf(review(t, k), ReviewDuplicate); len(got) != 0 {
		t.Fatalf("satellite named after its parent: %+v", got)
	}
}

// D314: a template's table row is boilerplate, and the evidence keeps the
// code spans the comparison key masks.
func TestRepeatedFact_TemplateTableRowExempt(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	writeFile(t, k.Root, "templates/svc.md", "---\ntype: Note\n---\n| Header one of it | Header two of it |\n| --- | --- |\n| image | on the registry (see the template note) |\n")
	for i := 0; i < 3; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("m/c%d.md", i), fmt.Sprintf("---\ntype: Note\ntitle: C%d\n---\n| Header one of it | Header two of it |\n| --- | --- |\n| image | on the registry (see the template note) |\n| backup-host | nightly at 02:00 and kept fourteen days |\n", i))
	}
	got := itemsOf(review(t, k), ReviewRepeatedFact)
	if len(got) != 1 || !strings.Contains(got[0].Evidence, "backup-host") {
		t.Fatalf("want only the non-template row: %+v", got)
	}
}

func TestRepeatedFact_EvidencePreservesCodeSpans(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	for i := 0; i < 3; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("m/c%d.md", i), fmt.Sprintf("---\ntype: Note\ntitle: C%d\n---\n| image | pulled on the shared registry host `nexus.example.com` |\n| other | row |\n", i))
	}
	got := itemsOf(review(t, k), ReviewRepeatedFact)
	if len(got) != 1 || !strings.Contains(got[0].Evidence, "nexus.example.com") {
		t.Fatalf("evidence lost the code span: %+v", got)
	}
}
