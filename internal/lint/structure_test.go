package lint

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
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
	s := newStructKB(t, map[string]string{"ops": "map", "notes": "map"})
	s.mainGraph(nil)
	// An island of three (b is its anchor, degree 2) and a lone concept.
	s.concept("notes/a", "", "notes/b")
	s.concept("notes/b", "", "notes/c")
	s.concept("notes/c", "")
	s.concept("notes/lone", "")
	findings := s.run("")

	islands := findingsOf(findings, "island")
	if len(islands) != 1 || islands[0].Path != "notes/b.md" || islands[0].Severity != SevInfo {
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

func TestStructuralIgnoresAndScope(t *testing.T) {
	s := newStructKB(t, map[string]string{"ops": "map", "notes": "map"})
	s.mainGraph(map[string]string{"h": "lint_ignore: [cut_concept]\n", "m1": "lint_ignore: [island]\n"})
	s.concept("notes/a", "", "notes/b")
	s.concept("notes/b", "")
	findings := s.run("")
	if hasCheck(findings, "ops/h.md", "cut_concept") {
		t.Fatal("lint_ignore: [cut_concept] was not honoured")
	}
	// island is accepted by a member (D306): m1 is not in the island, so
	// it is valid and changes nothing.
	if invalid := findingsOf(findings, "lint_ignore_invalid"); len(invalid) != 0 {
		t.Fatalf("lint_ignore: [island] = %+v", invalid)
	}
	if len(findingsOf(findings, "island")) != 1 {
		t.Fatal("a non-member's lint_ignore accepted the island")
	}

	// Scoped lint: computed on the whole graph, emitted only in scope.
	whole := s.run("")
	scoped := s.run("notes")
	if got, want := findingsOf(scoped, "island"), findingsOf(whole, "island"); !reflect.DeepEqual(got, want) {
		t.Fatalf("scoped island %+v, whole %+v", got, want)
	}
	if len(findingsOf(scoped, "cut_concept")) != 0 {
		t.Fatal("an out-of-scope cut concept was reported")
	}
	if got, want := findingsOf(s.run("ops"), "cut_concept"), findingsOf(whole, "cut_concept"); !reflect.DeepEqual(got, want) {
		t.Fatalf("scoped cut_concept %+v, whole %+v", got, want)
	}
}

func TestLinkToRetiredAndBrokenRelation(t *testing.T) {
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
	if len(broken) != 2 || broken[0].Path != "ops/dangling.md" || broken[1].Path != "ops/selfish.md" || broken[0].Severity != SevWarning {
		t.Fatalf("broken_relation = %+v", broken)
	}
	if hasCheck(findings, "ops/old-dns.md", "broken_relation") {
		t.Fatal("a valid successor was reported broken")
	}
}

func TestMapMisfit(t *testing.T) {
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
	findings := s.run("")
	misfits := findingsOf(findings, "map_misfit")
	if len(misfits) != 1 || misfits[0].Path != "ops/router.md" {
		t.Fatalf("map_misfit = %+v", misfits)
	}
	if misfits[0].Message != "3 of 4 linked concepts are in map net: consider concept_move" {
		t.Fatalf("message = %q", misfits[0].Message)
	}
}

func TestMapMisfit_StrictMapRefusingType_NoFinding(t *testing.T) {
	// When the candidate map is strict and doesn't admit the concept's type,
	// map_misfit should not fire (D295 WP3).
	s := newStructKB(t, map[string]string{"ops": "map", "net": "map"})
	// Make net strict with concept_types that do NOT include "Note".
	writeFile(t, s.k.DataRoot(), "net/_map.md", "---\ntype: Map\ntitle: net\nkind: map\nontology_mode: strict\nconcept_types: [Service, Runbook]\n---\n")
	// Create a Note in ops with 3 of 4 neighbors in net → normally fires.
	s.concept("ops/router", "", "net/a", "net/b", "net/c", "ops/x")
	for _, id := range []string{"net/a", "net/b", "net/c", "ops/x"} {
		s.concept(id, "")
	}
	findings := s.run("")
	misfits := findingsOf(findings, "map_misfit")
	if len(misfits) != 0 {
		t.Fatalf("strict map refusing the type should not produce map_misfit: %+v", misfits)
	}
}

func TestMapMisfit_FlexibleMapAdmitsAnyType(t *testing.T) {
	s := newStructKB(t, map[string]string{"ops": "map", "net": "map"})
	// net is flexible (default) → admits any type.
	s.concept("ops/router", "", "net/a", "net/b", "net/c", "ops/x")
	for _, id := range []string{"net/a", "net/b", "net/c", "ops/x"} {
		s.concept(id, "")
	}
	findings := s.run("")
	misfits := findingsOf(findings, "map_misfit")
	if len(misfits) != 1 || misfits[0].Path != "ops/router.md" {
		t.Fatalf("flexible map should produce map_misfit: %+v", misfits)
	}
}

// TestMapLintIgnore (D306): lint_ignore in a _map.md accepts a check for the
// whole map, including the checks no concept owns; an island goes when one
// member accepts it; errors never go; a name a map cannot accept is reported.
func TestMapLintIgnore(t *testing.T) {
	s := newStructKB(t, map[string]string{"ops": "map", "notes": "map"})
	s.mainGraph(nil)
	s.concept("notes/a", "", "notes/b")
	s.concept("notes/b", "lint_ignore: [island]\n")
	if got := findingsOf(s.run(""), "island"); len(got) != 0 {
		t.Fatalf("a member accepted the island, still: %+v", got)
	}
	writeFile(t, s.k.DataRoot(), "ops/_map.md", "---\ntype: Map\ntitle: Ops\nkind: map\nlint_ignore: [cut_concept, tool_param_field]\n---\n")
	findings := s.run("")
	if len(findingsOf(findings, "cut_concept")) != 0 {
		t.Fatalf("map-level cut_concept kept: %+v", findingsOf(findings, "cut_concept"))
	}
	invalid := findingsOf(findings, "lint_ignore_invalid")
	if len(invalid) != 1 || invalid[0].Path != "ops/_map.md" || !strings.Contains(invalid[0].Message, "tool_param_field") {
		t.Fatalf("invalid map-level name: %+v", invalid)
	}
}

func TestLinkToRetired_SuccessorExempt(t *testing.T) {
	s := newStructKB(t, map[string]string{"ops": "map"})
	s.concept("ops/adr-old", "status: superseded\nsuperseded_by: ops/adr-new\n")
	s.concept("ops/adr-new", "", "ops/adr-old")
	if got := findingsOf(s.run(""), "link_to_retired"); len(got) != 0 {
		t.Fatalf("the successor linking its predecessor is not a finding: %+v", got)
	}
}

func TestLinkToRetired_AcceptedOnTarget(t *testing.T) {
	s := newStructKB(t, map[string]string{"ops": "map"})
	s.concept("ops/gone", "status: deprecated\nlint_ignore: [link_to_retired]\n")
	s.concept("ops/a", "", "ops/gone")
	if got := findingsOf(s.run(""), "link_to_retired"); len(got) != 0 {
		t.Fatalf("accepted on the retired concept: %+v", got)
	}
	s.concept("ops/b", "", "ops/gone") // a new linker does not reopen it
	if got := findingsOf(s.run(""), "link_to_retired"); len(got) != 0 {
		t.Fatalf("a new linker reopened an accepted finding: %+v", got)
	}
}

func TestLinkToRetired_ManyLinkersOneFinding(t *testing.T) {
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
	s := newStructKB(t, map[string]string{"ops": "map"})
	s.chainTo("ops/svc")
	s.concept("ops/svc/index", "", "ops/svc/api", "ops/svc/db", "ops/svc/ui")
	for _, sat := range []string{"api", "db", "ui"} {
		s.concept("ops/svc/"+sat, "")
	}
	if got := findingsOf(s.run(""), "cut_concept"); hasCheck(got, "ops/svc/index.md", "cut_concept") {
		t.Fatalf("an expanded index cutting off its own satellites is not fragility: %+v", got)
	}
}

func TestCutConcept_JournalLeavesExempt(t *testing.T) {
	s := newStructKB(t, map[string]string{"ops": "map", "incidents": "journal"})
	s.chainTo("ops/svc")
	s.concept("ops/svc", "")
	for _, n := range []string{"a", "b", "c"} {
		s.concept("incidents/"+n, "", "ops/svc")
	}
	if got := findingsOf(s.run(""), "cut_concept"); hasCheck(got, "ops/svc.md", "cut_concept") {
		t.Fatalf("journal notes linked only to their subject are not fragility: %+v", got)
	}
}

func TestCutConcept_MixedStillFires(t *testing.T) {
	s := newStructKB(t, map[string]string{"ops": "map", "incidents": "journal"})
	s.chainTo("ops/svc")
	s.concept("ops/svc", "", "ops/leaf1", "ops/leaf2", "ops/leaf3")
	for _, n := range []string{"leaf1", "leaf2", "leaf3"} {
		s.concept("ops/"+n, "")
	}
	s.concept("incidents/a", "", "ops/svc")
	got := findingsOf(s.run(""), "cut_concept")
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
