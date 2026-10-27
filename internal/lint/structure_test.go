package lint

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// structKB writes maps and concepts; a concept body is its links, and fm is
// extra frontmatter lines.
type structKB struct {
	t *testing.T
	k *kb.KB
}

func newStructKB(t *testing.T, maps map[string]string) *structKB {
	t.Helper()
	s := &structKB{t: t, k: tempKB(t)}
	for name, kind := range maps {
		writeFile(t, s.k.DataRoot(), name+"/_map.md", fmt.Sprintf("---\ntype: Map\ntitle: %s\nkind: %s\n---\n", name, kind))
	}
	return s
}

func (s *structKB) concept(id, fm string, links ...string) {
	s.t.Helper()
	var body strings.Builder
	for _, l := range links {
		fmt.Fprintf(&body, "See [[%s]].\n", l)
	}
	writeFile(s.t, s.k.DataRoot(), id+".md", fmt.Sprintf("---\ntype: Note\ntitle: %s\n%s---\n%s", id, fm, body.String()))
}

func (s *structKB) run(scope string) []Finding {
	s.t.Helper()
	findings, err := Run(s.k, scope, false)
	if err != nil {
		s.t.Fatal(err)
	}
	return findings
}

func findingsOf(findings []Finding, check string) []Finding {
	var out []Finding
	for _, f := range findings {
		if f.Check == check {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Message < out[j].Message
	})
	return out
}

// mainGraph is a component of 8 with a hub h cutting off three leaves and a
// node p cutting off two: m1-m2-m3-m4-h, h-l1, h-l2, h-l3, m1-p, p-q1, p-q2.
func (s *structKB) mainGraph(fm map[string]string) {
	s.concept("ops/m1", fm["m1"], "ops/m2", "ops/p")
	s.concept("ops/m2", "", "ops/m3")
	s.concept("ops/m3", "", "ops/m4")
	s.concept("ops/m4", "", "ops/h")
	s.concept("ops/h", fm["h"], "ops/l1", "ops/l2", "ops/l3")
	for _, l := range []string{"l1", "l2", "l3"} {
		s.concept("ops/"+l, "")
	}
	s.concept("ops/p", "", "ops/q1", "ops/q2")
	s.concept("ops/q1", "")
	s.concept("ops/q2", "")
}

func TestIslandAndCutConcept(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map", "notes": "map"})
	s.mainGraph(nil)
	// An island of three (b is its anchor, degree 2) and a lone concept.
	s.concept("notes/a", "", "notes/b")
	s.concept("notes/b", "", "notes/c")
	s.concept("notes/c", "")
	s.concept("notes/lone", "")
	findings := observed(t, s.k)

	islands := findingsOf(findings, "island")
	if len(islands) != 1 || islands[0].Path != "notes/b.md" {
		t.Fatalf("islands = %+v", islands)
	}
	if want := "3 concepts form an island disconnected from the main graph: notes/a, notes/b, notes/c"; islands[0].Message != want {
		t.Fatalf("island message = %q", islands[0].Message)
	}
	cuts := findingsOf(findings, "cut_concept")
	// m4 (cuts h and its leaves: 4), h (3 leaves); p cuts only two; m1..m3
	// are on the path and cut off the tail below them.
	byPath := map[string]string{}
	for _, f := range cuts {
		byPath[f.Path] = f.Message
	}
	if msg := byPath["ops/h.md"]; msg != "removing or unlinking this concept disconnects 3 concepts (e.g. ops/l1, ops/l2, ops/l3)" {
		t.Fatalf("h: %q (all: %v)", msg, byPath)
	}
	if _, ok := byPath["ops/p.md"]; ok {
		t.Fatal("a concept cutting off two is the normal shape of a page, not a finding")
	}
}

// The graph-shape observations are not findings (D947): a whole-KB or scoped
// lint never returns one, and kb_review computes them on the whole graph.
func TestStructuralObservationsAreNotFindings(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map", "notes": "map"})
	s.mainGraph(nil)
	s.concept("notes/a", "", "notes/b")
	s.concept("notes/b", "")
	obs := observed(t, s.k)
	if !hasCheck(obs, "ops/h.md", "cut_concept") {
		t.Fatal("cut_concept missing on the hub")
	}
	if len(findingsOf(obs, "island")) != 1 {
		t.Fatal("island missing")
	}
	for _, scope := range []string{"", "notes", "ops"} {
		for _, f := range s.run(scope) {
			if _, isObservation := ObservationSpecOf(f.Check); isObservation {
				t.Fatalf("scope %q: lint returned the observation %s: %+v", scope, f.Check, f)
			}
		}
	}
}

func TestLinkToRetiredAndBrokenRelation(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map", "incidents": "journal"})
	s.concept("ops/new-dns", "")
	s.concept("ops/old-dns", "status: superseded\nsuperseded_by: ops/new-dns\n")
	s.concept("ops/legacy", "status: deprecated\n")
	s.concept("ops/live", "", "ops/old-dns", "ops/legacy", "ops/new-dns")
	s.concept("ops/retired-src", "status: deprecated\n", "ops/legacy")
	s.concept("incidents/2026-01-01-outage", "", "ops/legacy")
	s.concept("ops/dangling", "superseded_by: ops/nowhere\n")
	s.concept("ops/selfish", "superseded_by: ops/selfish\n")
	findings := s.run("")

	// One finding per retired concept, on the retired concept (D313): the
	// successor, a retired linker and a journal entry are never counted.
	retiredLinks := findingsOf(findings, "link_to_retired")
	if len(retiredLinks) != 2 {
		t.Fatalf("link_to_retired = %+v", retiredLinks)
	}
	if retiredLinks[0].Path != "ops/legacy.md" || retiredLinks[1].Path != "ops/old-dns.md" {
		t.Fatalf("link_to_retired = %+v", retiredLinks)
	}
	if !strings.Contains(retiredLinks[0].Message, "(status: deprecated) but still linked by 1 live concepts: ops/live —") ||
		!strings.Contains(retiredLinks[1].Message, "(status: superseded) but still linked by 1 live concepts: ops/live —") {
		t.Fatalf("link_to_retired = %+v", retiredLinks)
	}

	broken := findingsOf(findings, "broken_relation")
	if len(broken) != 2 || broken[0].Path != "ops/dangling.md" || broken[1].Path != "ops/selfish.md" {
		t.Fatalf("broken_relation = %+v", broken)
	}
	if hasCheck(findings, "ops/old-dns.md", "broken_relation") {
		t.Fatal("a valid successor was reported broken")
	}
}

func TestMapMisfit(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map", "net": "map", "diary": "journal"})
	// fires: 3 of 4 neighbours in net.
	s.concept("ops/router", "", "net/a", "net/b", "net/c", "ops/x")
	// silent: 3 neighbours only (journal and root concepts do not vote).
	s.concept("ops/switch", "", "net/a", "net/b", "net/c", "diary/day", "rootpage")
	// silent: 4 neighbours split 2/2.
	s.concept("ops/firewall", "", "net/a", "net/b", "ops/x", "ops/y")
	// silent: retired, and an archive holds retired things of every domain (D307).
	s.concept("ops/oldrouter", "status: deprecated\n", "net/a", "net/b", "net/c", "ops/x")
	for _, id := range []string{"net/a", "net/b", "net/c", "ops/x", "ops/y", "diary/day"} {
		s.concept(id, "")
	}
	writeFile(t, s.k.DataRoot(), "rootpage.md", "---\ntype: Note\n---\nRoot.\n")
	findings := observed(t, s.k)
	misfits := findingsOf(findings, "map_misfit")
	if len(misfits) != 1 || misfits[0].Path != "ops/router.md" {
		t.Fatalf("map_misfit = %+v", misfits)
	}
	if misfits[0].Message != "3 of 4 linked concepts are in map net: consider concept_move" {
		t.Fatalf("message = %q", misfits[0].Message)
	}
}

func TestMapMisfit_StrictMapRefusingType_NoFinding(t *testing.T) {
	t.Parallel()
	// When the candidate map is strict and doesn't admit the concept's type,
	// map_misfit should not fire (D295 WP3).
	s := newStructKB(t, map[string]string{"ops": "map", "net": "map"})
	// Make net strict with concept_types that do NOT include "Note".
	writeFile(t, s.k.DataRoot(), "net/_map.md", "---\ntype: Map\ntitle: net\nkind: map\nconcept_types: [Service, Runbook]\n---\n")
	// Create a Note in ops with 3 of 4 neighbors in net → normally fires.
	s.concept("ops/router", "", "net/a", "net/b", "net/c", "ops/x")
	for _, id := range []string{"net/a", "net/b", "net/c", "ops/x"} {
		s.concept(id, "")
	}
	findings := observed(t, s.k)
	misfits := findingsOf(findings, "map_misfit")
	if len(misfits) != 0 {
		t.Fatalf("strict map refusing the type should not produce map_misfit: %+v", misfits)
	}
}

func TestMapMisfit_FlexibleMapAdmitsAnyType(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map", "net": "map"})
	// net is flexible (default) → admits any type.
	s.concept("ops/router", "", "net/a", "net/b", "net/c", "ops/x")
	for _, id := range []string{"net/a", "net/b", "net/c", "ops/x"} {
		s.concept(id, "")
	}
	findings := observed(t, s.k)
	misfits := findingsOf(findings, "map_misfit")
	if len(misfits) != 1 || misfits[0].Path != "ops/router.md" {
		t.Fatalf("flexible map should produce map_misfit: %+v", misfits)
	}
}

func TestLinkToRetired_SuccessorExempt(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map"})
	s.concept("ops/adr-old", "status: superseded\nsuperseded_by: ops/adr-new\n")
	s.concept("ops/adr-new", "", "ops/adr-old")
	if got := findingsOf(s.run(""), "link_to_retired"); len(got) != 0 {
		t.Fatalf("the successor linking its predecessor is not a finding: %+v", got)
	}
}

func TestLinkToRetired_ManyLinkersOneFinding(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map"})
	s.concept("ops/gone", "status: deprecated\n")
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		s.concept("ops/"+n, "", "ops/gone")
	}
	got := findingsOf(s.run(""), "link_to_retired")
	if len(got) != 1 || got[0].Path != "ops/gone.md" || !strings.Contains(got[0].Message, "linked by 7 live concepts: ops/a, ops/b, ops/c, ops/d, ops/e, …") {
		t.Fatalf("7 linkers must be one finding: %+v", got)
	}
}

// chainTo builds r1-r2-r3-r4-hub, the large side every cut vertex needs.
func (s *structKB) chainTo(hub string) {
	s.concept("ops/r1", "", "ops/r2")
	s.concept("ops/r2", "", "ops/r3")
	s.concept("ops/r3", "", "ops/r4")
	s.concept("ops/r4", "", hub)
}

func TestCutConcept_ExpandedIndexExempt(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map"})
	s.chainTo("ops/svc")
	s.concept("ops/svc/index", "", "ops/svc/api", "ops/svc/db", "ops/svc/ui")
	for _, sat := range []string{"api", "db", "ui"} {
		s.concept("ops/svc/"+sat, "")
	}
	if got := findingsOf(observed(t, s.k), "cut_concept"); hasCheck(got, "ops/svc/index.md", "cut_concept") {
		t.Fatalf("an expanded index cutting off its own satellites is not fragility: %+v", got)
	}
}

func TestCutConcept_JournalLeavesExempt(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map", "incidents": "journal"})
	s.chainTo("ops/svc")
	s.concept("ops/svc", "")
	for _, n := range []string{"a", "b", "c"} {
		s.concept("incidents/"+n, "", "ops/svc")
	}
	if got := findingsOf(observed(t, s.k), "cut_concept"); hasCheck(got, "ops/svc.md", "cut_concept") {
		t.Fatalf("journal notes linked only to their subject are not fragility: %+v", got)
	}
}

func TestCutConcept_MixedStillFires(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map", "incidents": "journal"})
	s.chainTo("ops/svc")
	s.concept("ops/svc", "", "ops/leaf1", "ops/leaf2", "ops/leaf3")
	for _, n := range []string{"leaf1", "leaf2", "leaf3"} {
		s.concept("ops/"+n, "")
	}
	s.concept("incidents/a", "", "ops/svc")
	got := findingsOf(observed(t, s.k), "cut_concept")
	var msg string
	for _, f := range got {
		if f.Path == "ops/svc.md" {
			msg = f.Message
		}
	}
	if !strings.Contains(msg, "disconnects 3 concepts (e.g. ops/leaf1, ops/leaf2, ops/leaf3)") || strings.Contains(msg, "incidents") {
		t.Fatalf("a real separation must fire, naming only map concepts: %q", msg)
	}
}

// D1069: superseded_target_retired points at the live end of the chain.
func TestSupersededTargetRetired(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map"})
	sup := func(to string) string { return "status: superseded\nsuperseded_by: " + to + "\n" }
	s.concept("ops/a", sup("ops/b"))
	s.concept("ops/b", sup("ops/c"))
	s.concept("ops/c", "")
	// two superseded hops: a2 and m1 both end at live
	s.concept("ops/a2", sup("ops/m1"))
	s.concept("ops/m1", sup("ops/m2"))
	s.concept("ops/m2", sup("ops/live"))
	s.concept("ops/live", "")
	// deprecated / archived targets are history, not findings
	s.concept("ops/d1", sup("ops/dep"))
	s.concept("ops/dep", "status: deprecated\n")
	s.concept("ops/d2", sup("ops/arc"))
	s.concept("ops/arc", "status: archived\n")
	// dead end and cycle
	s.concept("ops/e1", sup("ops/e2"))
	s.concept("ops/e2", "status: superseded\n")
	s.concept("ops/y1", sup("ops/y2"))
	s.concept("ops/y2", sup("ops/y3"))
	s.concept("ops/y3", sup("ops/y2"))
	// missing target is broken_relation
	s.concept("ops/x", sup("ops/nowhere"))
	f := s.run("")
	got := map[string]string{}
	for _, x := range findingsOf(f, "superseded_target_retired") {
		if x.Fix == nil || x.Fix.Kind != FixSetValue || x.Fix.Field != "superseded_by" {
			t.Fatalf("fix = %+v", x.Fix)
		}
		got[x.Path] = x.Fix.To
	}
	want := map[string]string{"ops/a.md": "ops/c", "ops/a2.md": "ops/live", "ops/m1.md": "ops/live"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if hasCheck(f, "ops/x.md", "superseded_target_retired") || !hasCheck(f, "ops/x.md", "broken_relation") {
		t.Error("a missing target is broken_relation only")
	}
	if AutoRepairSafe("superseded_target_retired") {
		t.Error("must not be auto-repair safe")
	}
}

// ScopedCheck examines the written concept's own pointer only.
func TestSupersededTargetRetired_Scoped(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map"})
	s.concept("ops/a", "status: superseded\nsuperseded_by: ops/b\n")
	s.concept("ops/b", "status: superseded\nsuperseded_by: ops/c\n")
	s.concept("ops/c", "")
	if got := findingsOf(ScopedCheck(s.k, []okf.ConceptID{"ops/a"}), "superseded_target_retired"); len(got) != 1 {
		t.Fatalf("written a: %+v", got)
	}
	if got := findingsOf(ScopedCheck(s.k, []okf.ConceptID{"ops/b"}), "superseded_target_retired"); len(got) != 0 {
		t.Fatalf("retiring b must not report on untouched a: %+v", got)
	}
}

func TestDigestMissing(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"diary": "journal", "log": "journal", "ops": "map"})
	entry := func(id, ts string) {
		fm := "status: archived\n"
		if ts != "" {
			fm += "timestamp: " + ts + "\n"
		}
		s.concept(id, fm)
	}
	entry("diary/e1", "2026-01-05")
	entry("diary/e2", "2026-02-05")
	entry("diary/e3", "2026-03-05")
	entry("diary/q2", "2026-05-05")
	entry("diary/nodate", "")
	entry("ops/arch", "2026-01-05") // not a journal
	f := findingsOf(s.run(""), "digest_missing")
	if len(f) != 3 {
		t.Fatalf("want 3 groups (q1, q2, undated): %+v", f)
	}
	if f[0].Path != "diary/e1.md" || !strings.Contains(f[0].Message, "3 archived entries") || !strings.Contains(f[0].Message, "diary/archive-2026-q1") {
		t.Errorf("q1 group: %+v", f[0])
	}
	if fmt.Sprint(f) != fmt.Sprint(findingsOf(s.run(""), "digest_missing")) {
		t.Error("not deterministic")
	}

	// A digest of the same journal covering two of three.
	writeFile(t, s.k.DataRoot(), "diary/archive-2026-q1.md", "---\ntype: digest\ntitle: d\nstatus: reference\n---\nSee [[diary/e1]] [[diary/e2]].\n")
	f = findingsOf(s.run(""), "digest_missing")
	if len(f) != 3 || !strings.Contains(f[0].Message, "1 archived entries") || f[0].Path != "diary/e3.md" {
		t.Fatalf("partial coverage: %+v", f)
	}
	// Another journal's digest does not cover; a non-digest type does not.
	writeFile(t, s.k.DataRoot(), "log/archive-2026-q1.md", "---\ntype: digest\ntitle: d\nstatus: reference\n---\nSee [[diary/e3]].\n")
	writeFile(t, s.k.DataRoot(), "diary/ref.md", "---\ntype: reference\ntitle: r\n---\nSee [[diary/e3]].\n")
	if f = findingsOf(s.run(""), "digest_missing"); len(f) != 3 {
		t.Fatalf("foreign digest / reference page must not cover: %+v", f)
	}
	writeFile(t, s.k.DataRoot(), "diary/archive-2026-q1.md", "---\ntype: digest\ntitle: d\nstatus: reference\n---\nSee [[diary/e1]] [[diary/e2]] [[diary/e3]].\n")
	if f = findingsOf(s.run(""), "digest_missing"); len(f) != 2 {
		t.Fatalf("q1 covered: %+v", f)
	}
}

func TestDigestMissing_InactiveWithoutJournal(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map"})
	if r := Inactive(s.k)["digest_missing"]; r != "no map is a journal" {
		t.Fatalf("inactive reason = %q", r)
	}
	s2 := newStructKB(t, map[string]string{"ops": "map", "diary": "journal"})
	if r, ok := Inactive(s2.k)["digest_missing"]; ok {
		t.Fatalf("active with a journal, got %q", r)
	}
}
