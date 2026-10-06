package lint

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func withNow(t *testing.T, day string) {
	t.Helper()
	prev := Now
	d, _ := time.Parse("2006-01-02", day)
	Now = func() time.Time { return d }
	t.Cleanup(func() { Now = prev })
}

func TestStaleOpen(t *testing.T) {
	withNow(t, "2026-10-01")
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "j/_map.md", "---\ntype: Map\ntitle: J\nkind: journal\n---\n")
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	writeFile(t, k.DataRoot(), "c/_map.md", "---\ntype: Map\ntitle: C\nkind: journal\nopen_statuses: [doing]\nstale_after: 10\n---\n")
	page := func(rel, status, ts string) {
		writeFile(t, k.DataRoot(), rel, fmt.Sprintf("---\ntype: Task\ntitle: T\nstatus: %s\ntimestamp: %s\n---\n# T\n", status, ts))
	}
	writeFile(t, k.DataRoot(), "a/_map.md", "---\ntype: Map\ntitle: A\nkind: journal\nopen_statuses: [active, in-progress]\n---\n")
	page("a/old-active.md", "active", "2026-06-01")
	page("j/old-active.md", "active", "2026-06-01")
	page("j/old-done.md", "done", "2026-06-01")
	page("j/recent.md", "in-progress", "2026-09-20")
	page("m/old-active.md", "active", "2026-01-01")
	page("c/doing.md", "doing", "2026-09-01")
	page("c/active.md", "active", "2026-01-01")
	f, _ := Run(k, "", false)
	for rel, want := range map[string]bool{"a/old-active.md": true, "j/old-active.md": false, "j/old-done.md": false, "j/recent.md": false, "m/old-active.md": false, "c/doing.md": true, "c/active.md": false} {
		if got := hasCheck(f, rel, "stale_open"); got != want {
			t.Errorf("%s: stale_open = %v, want %v", rel, got, want)
		}
	}
}

// D321: a future review_after suspends stale_open; a past one does not.
func TestStaleOpenSuspendedByReviewAfter(t *testing.T) {
	withNow(t, "2026-10-01")
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "j/_map.md", "---\ntype: Map\ntitle: J\nkind: journal\nopen_statuses: [open, in-progress, blocked]\n---\n")
	page := func(rel, extra string) {
		writeFile(t, k.DataRoot(), rel, "---\ntype: Task\ntitle: T\nstatus: in-progress\ntimestamp: 2026-07-03\n"+extra+"---\n# T\n")
	}
	page("j/future.md", "waiting_on: vendor\nreview_after: 2026-10-31\n")
	page("j/today.md", "review_after: 2026-10-01\n")
	page("j/past.md", "review_after: 2026-09-01\n")
	page("j/none.md", "")
	f, _ := Run(k, "", false)
	for rel, want := range map[string]bool{"j/future.md": false, "j/today.md": false, "j/past.md": true, "j/none.md": true} {
		if got := hasCheck(f, rel, "stale_open"); got != want {
			t.Errorf("%s: stale_open = %v, want %v", rel, got, want)
		}
	}
	if hasCheck(f, "j/future.md", "nonstandard_field") {
		t.Error("waiting_on flagged")
	}
	entries, _ := Work(k)
	for _, e := range entries {
		if e.ID == "j/future" && (!e.OpenPhase || e.Stale) {
			t.Errorf("suspended work must stay open, not stale: %+v", e)
		}
		if e.ID == "j/past" && !e.Stale {
			t.Errorf("past review_after must not suspend: %+v", e)
		}
	}
}

// D321: active in a journal that does not list it open gets status_semantics.
func TestStatusSemantics(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "j/_map.md", "---\ntype: Map\ntitle: J\nkind: journal\n---\n")
	writeFile(t, k.DataRoot(), "o/_map.md", "---\ntype: Map\ntitle: O\nkind: journal\nopen_statuses: [active]\n---\n")
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	page := func(rel, status string) {
		writeFile(t, k.DataRoot(), rel, "---\ntype: Note\ntitle: T\nstatus: "+status+"\n---\n# T\n")
	}
	page("j/a.md", "active")
	page("o/a.md", "active")
	page("j/b.md", "in-progress")
	page("m/a.md", "active")
	f, _ := Run(k, "", false)
	for rel, want := range map[string]bool{"j/a.md": true, "o/a.md": false, "j/b.md": false, "m/a.md": false} {
		if got := hasCheck(f, rel, "status_semantics"); got != want {
			t.Errorf("%s: status_semantics = %v, want %v", rel, got, want)
		}
	}
}

func TestClosedWithOpenItems(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "j/_map.md", "---\ntype: Map\ntitle: J\nkind: journal\n---\n")
	writeFile(t, k.DataRoot(), "j/a.md", "---\ntype: Task\ntitle: A\nstatus: completato\n---\n# A\n\n- [x] one\n- [ ] two\n")
	writeFile(t, k.DataRoot(), "j/b.md", "---\ntype: Task\ntitle: B\nstatus: done\n---\n# B\n\n```\n- [ ] in code\n```\n")
	f, _ := Run(k, "", false)
	if !hasCheck(f, "j/a.md", "closed_with_open_items") || hasCheck(f, "j/b.md", "closed_with_open_items") {
		t.Fatalf("%+v", f)
	}
}

// D313: checkboxes under a procedure heading are a reusable checklist.
func TestClosedWithOpenItems_ProcedureExempt(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "r/_map.md", "---\ntype: Map\ntitle: R\nprocedure_headings: [Procedura]\n---\n")
	writeFile(t, k.DataRoot(), "r/a.md", "---\ntype: Task\ntitle: A\nstatus: done\n---\n# A\n\n## Procedura\n\n- [ ] step one\n- [ ] step two\n")
	writeFile(t, k.DataRoot(), "r/b.md", "---\ntype: Task\ntitle: B\nstatus: done\n---\n# B\n\n## Procedura\n\n- [ ] step one\n\n## Follow-up\n\n- [ ] real leftover\n")
	f, _ := Run(k, "", false)
	if hasCheck(f, "r/a.md", "closed_with_open_items") {
		t.Fatalf("procedure checklist fired: %+v", f)
	}
	got := findingFor(f, "r/b.md", "closed_with_open_items")
	if got == nil || !contains(got.Message, "1 unchecked") {
		t.Fatalf("leftover outside the procedure must fire once: %+v", got)
	}
}

func TestTemplateSectionMissing(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "templates/service.md", "---\ntype: Service\ntitle: \"{{title}}\"\n---\n# {{title}}\n\n## Endpoint\n\n## Source of truth\n\n## Città\n")
	writeFile(t, k.DataRoot(), "s/_map.md", "---\ntype: Map\ntitle: S\ntemplate_sections: true\n---\n")
	writeFile(t, k.DataRoot(), "o/_map.md", "---\ntype: Map\ntitle: O\n---\n")
	writeFile(t, k.DataRoot(), "s/a.md", "---\ntype: Service\ntitle: A\n---\n# A\n\n## endpoint\n\n## Citta\n")
	writeFile(t, k.DataRoot(), "o/a.md", "---\ntype: Service\ntitle: A\n---\n# A\n")
	writeFile(t, k.DataRoot(), "s/n.md", "---\ntype: Note\ntitle: N\n---\n# N\n")
	f, _ := Run(k, "", false)
	got := findingFor(f, "s/a.md", "template_section_missing")
	if got == nil || !contains(got.Message, "Source of truth") || contains(got.Message, "Endpoint,") {
		t.Fatalf("s/a: %+v", got)
	}
	if hasCheck(f, "o/a.md", "template_section_missing") || hasCheck(f, "s/n.md", "template_section_missing") {
		t.Fatal("opt-in only, and only for types with a template")
	}
}

func TestOpenMarker(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	writeFile(t, k.DataRoot(), "l/_map.md", "---\ntype: Map\ntitle: L\nopen_markers: [da verificare]\n---\n")
	writeFile(t, k.DataRoot(), "m/a.md", "---\ntype: Note\ntitle: A\n---\n# A\n\nTODO: check. Also TBD.\n\n`TODO in code`\n\nTODOLIST is not one.\n\n3. ~~A TODO that was cancelled~~ — dropped.\n")
	writeFile(t, k.DataRoot(), "l/a.md", "---\ntype: Note\ntitle: A\n---\n# A\n\nQuesto è Da Verificare.\nTODO here is not a marker in this map.\n")
	f, _ := Run(k, "", false)
	if got := findingFor(f, "m/a.md", "open_marker"); got == nil || got.Count != 2 {
		t.Fatalf("m/a: %+v", got)
	}
	if got := findingFor(f, "l/a.md", "open_marker"); got == nil || got.Count != 1 {
		t.Fatalf("l/a: %+v", got)
	}
}

// D313: headings are section names, table rows of an open concept are tracked.
func TestOpenMarker_HeadingAndOpenTableExempt(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "j/_map.md", "---\ntype: Map\ntitle: J\nkind: journal\n---\n")
	writeFile(t, k.DataRoot(), "j/h.md", "---\ntype: Note\ntitle: H\n---\n# H\n\n## TODO list\n\n- [x] done\n")
	writeFile(t, k.DataRoot(), "j/open.md", "---\ntype: Task\ntitle: O\nstatus: in-progress\n---\n# O\n\n| item | state |\n|---|---|\n| TBD | pending |\n")
	writeFile(t, k.DataRoot(), "j/closed.md", "---\ntype: Task\ntitle: C\nstatus: done\n---\n# C\n\n| item | state |\n|---|---|\n| TBD | pending |\n")
	f, _ := Run(k, "", false)
	if hasCheck(f, "j/h.md", "open_marker") || hasCheck(f, "j/open.md", "open_marker") {
		t.Fatalf("heading / open-concept table marker fired: %+v", f)
	}
	if got := findingFor(f, "j/closed.md", "open_marker"); got == nil || got.Count != 1 {
		t.Fatalf("a TBD in a closed concept's table must count: %+v", got)
	}
}

func TestFacetSprawl(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	for i := 0; i < 40; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("m/c%d.md", i), fmt.Sprintf("---\ntype: Note\ntitle: C\ntags: [common, t%d]\n---\n# C\n", i))
	}
	f, _ := Run(k, "", false)
	if !hasCheck(f, "m/_map.md", "facet_sprawl") {
		t.Fatal("facet_sprawl not reported")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestTemplateSectionsFromFencedSample(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "templates/repo.md", "---\ntype: repo\ntitle: x\n---\nIntro.\n\n```markdown\n# <name>\n\n## One\n## Two\nRules.\n\n```markdown\n## Other\n```\n")
	if got := k.TemplateSections("repo"); len(got) != 2 || got[0] != "One" || got[1] != "Two" {
		t.Fatalf("got %v", got)
	}
}
