package lint

import (
	"fmt"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// D346: one function decides the threshold; a map that holds work defaults to
// 30 days, a reference map stays never-stale, an explicit value always wins.
func TestEffectiveStaleAfter(t *testing.T) {
	cases := []struct {
		name      string
		c         *kb.MapContract
		days      int
		defaulted bool
	}{
		{"nil", nil, 0, false},
		{"reference map", &kb.MapContract{Kind: "map"}, 0, false},
		{"closed-only status vocabulary", &kb.MapContract{FieldValues: map[string][]string{"status": {"active", "deprecated"}}}, 0, false},
		{"journal", &kb.MapContract{Kind: "journal"}, 60, true},
		{"open_statuses", &kb.MapContract{OpenStatuses: []string{"active"}}, 30, true},
		{"field_values status", &kb.MapContract{FieldValues: map[string][]string{"status": {"open", "done"}}}, 30, true},
		{"field_values_by_type status", &kb.MapContract{FieldValuesByType: map[string]map[string][]string{"Task": {"status": {"open", "done"}}}}, 30, true},
		{"explicit wins", &kb.MapContract{OpenStatuses: []string{"open"}, StaleAfterDays: 90}, 90, false},
		{"explicit off in a work map", &kb.MapContract{OpenStatuses: []string{"open"}, StaleAfterOff: true}, 0, false},
		{"explicit off in a journal", &kb.MapContract{Kind: "journal", StaleAfterOff: true}, 0, false},
	}
	for _, tc := range cases {
		if d, def := EffectiveStaleAfter(tc.c); d != tc.days || def != tc.defaulted {
			t.Errorf("%s: got (%d,%v), want (%d,%v)", tc.name, d, def, tc.days, tc.defaulted)
		}
	}
}

func TestStaleOpenWorkMapDefault(t *testing.T) {
	withNow(t, "2026-10-01")
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "w/_map.md", "---\ntype: Map\ntitle: W\nfield_values.status: [open, done]\n---\n")
	writeFile(t, k.DataRoot(), "r/_map.md", "---\ntype: Map\ntitle: R\n---\n")
	writeFile(t, k.DataRoot(), "o/_map.md", "---\ntype: Map\ntitle: O\nfield_values.status: [open, done]\nstale_after: 0\n---\n")
	page := func(rel, ts string) {
		writeFile(t, k.DataRoot(), rel, fmt.Sprintf("---\ntype: Task\ntitle: T\nstatus: open\ntimestamp: %s\n---\n# T\n", ts))
	}
	page("w/old.md", "2026-08-30") // 32 days
	page("w/new.md", "2026-09-10") // 21 days
	page("r/old.md", "2026-01-01")
	page("o/old.md", "2026-01-01")
	f, _ := Run(k, "", false)
	for rel, want := range map[string]bool{"w/old.md": true, "w/new.md": false, "r/old.md": false, "o/old.md": false} {
		if got := hasCheck(f, rel, "stale_open"); got != want {
			t.Errorf("%s: stale_open = %v, want %v", rel, got, want)
		}
	}
	entries, err := Work(k)
	if err != nil {
		t.Fatal(err)
	}
	got := workByID(t, entries)
	if !got["w/old"].Stale || got["w/new"].Stale || got["r/old"].Stale || got["o/old"].Stale {
		t.Fatalf("work_list stale flags: %+v", got)
	}
}
