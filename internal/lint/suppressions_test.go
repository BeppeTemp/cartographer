package lint

import (
	"strings"
	"testing"
)

func supsOf(t *testing.T, s *structKB, scope string) ([]Finding, []Suppression) {
	t.Helper()
	findings, sups, err := RunWithSuppressions(s.k, scope, false, runOptions(s.k))
	if err != nil {
		t.Fatal(err)
	}
	return findings, sups
}

func supFor(sups []Suppression, source, path, check string) (Suppression, bool) {
	for _, sp := range sups {
		if sp.Source == source && sp.Path == path && sp.Check == check {
			return sp, true
		}
	}
	return Suppression{}, false
}

// brokenLinks fires broken_link twice.
const brokenLinks = "See [[ops/gone-one]] and [[ops/gone-two]].\n"

func TestSuppressionsConceptCountsWhatItHides(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map"})
	writeFile(t, s.k.DataRoot(), "ops/a.md", "---\ntype: Note\ntitle: A\nlint_ignore: [broken_link]\n---\n"+brokenLinks+"[[ops/b]]\n")
	writeFile(t, s.k.DataRoot(), "ops/b.md", "---\ntype: Note\ntitle: B\n---\n[[ops/a]]\n")
	findings, sups := supsOf(t, s, "")
	if len(findingsOf(findings, "broken_link")) != 0 {
		t.Fatal("broken_link not suppressed")
	}
	sp, ok := supFor(sups, SourceConcept, "ops/a.md", "broken_link")
	if !ok || sp.Hidden != 2 || sp.Stale {
		t.Fatalf("concept entry = %+v (%v), want Hidden >= 2", sp, sups)
	}
	if len(findingsOf(findings, "lint_ignore_invalid")) != 0 {
		t.Fatalf("a working ignore was reported: %+v", findingsOf(findings, "lint_ignore_invalid"))
	}
}

func TestSuppressionsMapSumsConcepts(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map"})
	writeFile(t, s.k.DataRoot(), "ops/_map.md", "---\ntype: Map\ntitle: ops\nkind: map\nlint_ignore: [broken_link]\n---\n")
	writeFile(t, s.k.DataRoot(), "ops/a.md", "---\ntype: Note\ntitle: A\n---\nSee [[ops/gone-a]].\n[[ops/b]]\n")
	writeFile(t, s.k.DataRoot(), "ops/b.md", "---\ntype: Note\ntitle: B\n---\nSee [[ops/gone-b]].\n[[ops/a]]\n")
	_, sups := supsOf(t, s, "")
	sp, ok := supFor(sups, SourceMap, "ops/_map.md", "broken_link")
	if !ok || sp.Hidden != 2 || sp.Stale {
		t.Fatalf("map entry = %+v, want Hidden 2 (%v)", sp, sups)
	}
}

func TestSuppressionsIslandCountedOnMap(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map", "notes": "map"})
	writeFile(t, s.k.DataRoot(), "notes/_map.md", "---\ntype: Map\ntitle: notes\nkind: map\nlint_ignore: [island]\n---\n")
	s.mainGraph(nil)
	s.concept("notes/a", "", "notes/b")
	s.concept("notes/b", "")
	findings, sups := supsOf(t, s, "")
	if len(findingsOf(findings, "island")) != 0 {
		t.Fatalf("island not accepted by the map: %+v", findingsOf(findings, "island"))
	}
	if sp, ok := supFor(sups, SourceMap, "notes/_map.md", "island"); !ok || sp.Hidden != 1 {
		t.Fatalf("island entry = %+v (%v)", sp, sups)
	}
}

func TestSuppressionsStale(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map"})
	writeFile(t, s.k.DataRoot(), "ops/_map.md", "---\ntype: Map\ntitle: ops\nkind: map\nlint_ignore: [broken_link]\n---\n")
	writeFile(t, s.k.DataRoot(), "ops/a.md", "---\ntype: Note\ntitle: A\nlint_ignore: [broken_link, repeated_fact, duplicate_candidate, not_a_check]\n---\n[[ops/b]]\n")
	writeFile(t, s.k.DataRoot(), "ops/b.md", "---\ntype: Note\ntitle: B\n---\n[[ops/a]]\n")
	findings, sups := supsOf(t, s, "")
	stale := findingsOf(findings, "lint_ignore_invalid")
	got := map[string]string{}
	for _, f := range stale {
		got[f.Path+" "+f.Message] = ""
	}
	wantConcept := `ops/a.md lint_ignore names "broken_link", which hides no finding here, so the entry is stale`
	wantMap := `ops/_map.md lint_ignore names "broken_link", which hides no finding here, so the entry is stale`
	if _, ok := got[wantConcept]; !ok {
		t.Errorf("concept stale not reported: %v", got)
	}
	if _, ok := got[wantMap]; !ok {
		t.Errorf("map stale not reported: %v", got)
	}
	for _, f := range stale {
		// repeated_fact / duplicate_candidate are review kinds, never stale; the
		// unknown name is reported invalid once, not stale too.
		if f.Path == "ops/a.md" && f.Message != `lint_ignore names "broken_link", which hides no finding here, so the entry is stale` && !strings.Contains(f.Message, "not_a_check") {
			t.Errorf("unexpected finding %+v", f)
		}
		if strings.Contains(f.Message, "not_a_check") && strings.Contains(f.Message, "stale") {
			t.Errorf("an unknown name reported stale too: %+v", f)
		}
	}
	for _, name := range []string{"repeated_fact", "duplicate_candidate"} {
		if sp, ok := supFor(sups, SourceConcept, "ops/a.md", name); !ok || sp.Stale {
			t.Errorf("%s = %+v (%v), want listed and not stale", name, sp, ok)
		}
	}

	// A scoped run never reports stale entries.
	scoped, sups := supsOf(t, s, "ops")
	for _, f := range findingsOf(scoped, "lint_ignore_invalid") {
		if strings.Contains(f.Message, "stale") {
			t.Errorf("scoped run reported stale: %+v", f)
		}
	}
	for _, sp := range sups {
		if sp.Stale {
			t.Errorf("scoped run marked stale: %+v", sp)
		}
	}
}

func TestSuppressionsAcceptCounts(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.Root, "skills/committer/SKILL.md", skillMD("committer", "c", "Then `git commit -m \"save\"`.\n"))
	writeFile(t, k.Root, "skills/other/SKILL.md", skillMD("other", "c", "Then `git pull`.\n"))
	writeFile(t, k.Root, "skills/other/x.md", "x")
	writeFile(t, k.Root, "instructions.md", "---\nlint_accept:\n  skills/committer/SKILL.md: [skill_git_command]\n  skills/other/SKILL.md: [artifact_unused]\n---\n# I\n")
	_, sups, err := RunWithSuppressions(k, "", false, runOptions(k))
	if err != nil {
		t.Fatal(err)
	}
	if sp, ok := supFor(sups, SourceAccept, "skills/committer/SKILL.md", "skill_git_command"); !ok || sp.Hidden < 1 {
		t.Fatalf("accept hit = %+v (%v)", sp, sups)
	}
	if sp, ok := supFor(sups, SourceAccept, "skills/other/SKILL.md", "artifact_unused"); !ok || sp.Hidden != 0 || !sp.Stale {
		t.Fatalf("accept stale = %+v (%v)", sp, sups)
	}
}

// An entry is credited with every finding it would hide on its own: a
// redundant but valid entry is not stale.
func TestSuppressionsEveryAcceptorIsCredited(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map", "notes": "map"})
	writeFile(t, s.k.DataRoot(), "notes/_map.md", "---\ntype: Map\ntitle: notes\nkind: map\nlint_ignore: [island]\n---\n")
	s.mainGraph(nil)
	s.concept("notes/a", "lint_ignore: [island]\n", "notes/b")
	s.concept("notes/b", "lint_ignore: [island]\n")
	findings, sups := supsOf(t, s, "")
	if len(findingsOf(findings, "island")) != 0 {
		t.Fatal("island not accepted")
	}
	for _, want := range [][2]string{{SourceMap, "notes/_map.md"}, {SourceConcept, "notes/a.md"}, {SourceConcept, "notes/b.md"}} {
		if sp, ok := supFor(sups, want[0], want[1], "island"); !ok || sp.Hidden != 1 || sp.Stale {
			t.Errorf("%v = %+v (%v)", want, sp, ok)
		}
	}
	if inv := findingsOf(findings, "lint_ignore_invalid"); len(inv) != 0 {
		t.Errorf("stale reported: %+v", inv)
	}
}

func TestSuppressionsConceptAndMapOverlap(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map"})
	writeFile(t, s.k.DataRoot(), "ops/_map.md", "---\ntype: Map\ntitle: ops\nkind: map\nlint_ignore: [broken_link]\n---\n")
	writeFile(t, s.k.DataRoot(), "ops/a.md", "---\ntype: Note\ntitle: A\nlint_ignore: [broken_link]\n---\n"+brokenLinks+"[[ops/b]]\n")
	writeFile(t, s.k.DataRoot(), "ops/b.md", "---\ntype: Note\ntitle: B\n---\n[[ops/a]]\n")
	findings, sups := supsOf(t, s, "")
	for _, want := range [][2]string{{SourceConcept, "ops/a.md"}, {SourceMap, "ops/_map.md"}} {
		if sp, ok := supFor(sups, want[0], want[1], "broken_link"); !ok || sp.Hidden != 2 || sp.Stale {
			t.Errorf("%v = %+v (%v)", want, sp, ok)
		}
	}
	if inv := findingsOf(findings, "lint_ignore_invalid"); len(inv) != 0 {
		t.Errorf("stale reported: %+v", inv)
	}
}

func TestSuppressionsNeverSuppressIsInvalidNotStale(t *testing.T) {
	t.Parallel()
	s := newStructKB(t, map[string]string{"ops": "map"})
	writeFile(t, s.k.DataRoot(), "ops/a.md", "---\ntype: Note\ntitle: A\nlint_ignore: [stringified_list]\n---\nbody\n")
	findings, _ := supsOf(t, s, "")
	inv := findingsOf(findings, "lint_ignore_invalid")
	if len(inv) != 1 || !strings.Contains(inv[0].Message, "a check lint_ignore cannot suppress") || strings.Contains(inv[0].Message, "stale") {
		t.Fatalf("got %+v", inv)
	}
}
