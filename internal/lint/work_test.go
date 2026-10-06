package lint

import (
	"strings"
	"testing"
	"time"
)

func workByID(t *testing.T, entries []WorkEntry) map[string]WorkEntry {
	t.Helper()
	out := map[string]WorkEntry{}
	for _, e := range entries {
		out[e.ID] = e
	}
	return out
}

// TestWork pins what a work item is (D302).
func TestWork(t *testing.T) {
	old := Now
	Now = func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { Now = old })

	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	writeFile(t, k.DataRoot(), "j/_map.md", "---\ntype: Map\ntitle: J\nkind: journal\n---\n")
	writeFile(t, k.DataRoot(), "c/_map.md", "---\ntype: Map\ntitle: C\nopen_statuses: [waiting, draft]\n---\n")
	// An open-phase concept of a non-Task type.
	writeFile(t, k.DataRoot(), "m/plan.md", "---\ntype: Topic\ntitle: Plan\nstatus: in-progress\n---\n# Plan\n")
	// A done concept with unchecked items; a checkbox in code is not work.
	writeFile(t, k.DataRoot(), "m/done.md", "---\ntype: Note\ntitle: Done\nstatus: done\ntimestamp: 2026-01-01\n---\n# Done\n\n## Next steps\n\n- [ ] ship "+strings.Repeat("é", 150)+"\n- [x] closed\n\n```\n- [ ] in code\n```\n")
	// active is valid, not open, in a journal and in a map (D321) unless the
	// journal lists it in open_statuses.
	writeFile(t, k.DataRoot(), "a/_map.md", "---\ntype: Map\ntitle: A\nkind: journal\nopen_statuses: [active]\n---\n")
	writeFile(t, k.DataRoot(), "a/e.md", "---\ntype: Note\ntitle: E\nstatus: active\ntimestamp: 2026-01-02\n---\n# E\n")
	writeFile(t, k.DataRoot(), "m/svc.md", "---\ntype: Service\ntitle: Svc\nstatus: active\n---\n# Svc\n")
	writeFile(t, k.DataRoot(), "j/2026-01-02-e.md", "---\ntype: Note\ntitle: E\nstatus: active\ntimestamp: 2026-01-02\n---\n# E\n")
	// A contract's open_statuses override the defaults.
	writeFile(t, k.DataRoot(), "c/w.md", "---\ntype: Note\ntitle: W\nstatus: waiting\n---\n")
	// A draft is a page being written, not work, unless the map lists it;
	// its unchecked items are work all the same.
	writeFile(t, k.DataRoot(), "m/study.md", "---\ntype: Topic\ntitle: Study\nstatus: draft\n---\n# Study\n")
	writeFile(t, k.DataRoot(), "m/study2.md", "---\ntype: Topic\ntitle: Study 2\nstatus: draft\n---\n# Study 2\n\n- [ ] open point\n")
	writeFile(t, k.DataRoot(), "c/d.md", "---\ntype: Note\ntitle: D\nstatus: draft\n---\n")
	writeFile(t, k.DataRoot(), "c/p.md", "---\ntype: Note\ntitle: P\nstatus: in-progress\n---\n")

	entries, err := Work(k)
	if err != nil {
		t.Fatal(err)
	}
	got := workByID(t, entries)
	if e, ok := got["m/plan"]; !ok || !e.OpenPhase || len(e.Items) != 0 {
		t.Fatalf("open-phase Topic: %+v", e)
	}
	d, ok := got["m/done"]
	if !ok || d.OpenPhase || len(d.Items) != 1 {
		t.Fatalf("done with items: %+v", d)
	}
	if it := d.Items[0]; it.Section != "Next steps" || it.Line != 5 || len(it.Text) > workItemTextMax || !strings.HasPrefix(it.Text, "ship é") {
		t.Fatalf("item: %+v (%d bytes)", it, len(it.Text))
	}
	if d.Stale || d.AgeDays == nil || *d.AgeDays != 151 {
		t.Fatalf("a map without stale_after never makes work stale: %+v", d)
	}
	if _, ok := got["m/svc"]; ok {
		t.Fatal("active counted as open in a map")
	}
	if _, ok := got["j/2026-01-02-e"]; ok {
		t.Fatal("active counted as open in a journal without open_statuses")
	}
	if e, ok := got["a/e"]; !ok || !e.OpenPhase || !e.Stale {
		t.Fatalf("active listed in open_statuses, 150 days old: %+v", e)
	}
	if _, ok := got["c/w"]; !ok {
		t.Fatal("open_statuses value not open")
	}
	if _, ok := got["m/study"]; ok {
		t.Fatal("draft counted as work by default")
	}
	if e, ok := got["m/study2"]; !ok || e.OpenPhase || len(e.Items) != 1 {
		t.Fatalf("draft with items: %+v", e)
	}
	if e, ok := got["c/d"]; !ok || !e.OpenPhase {
		t.Fatalf("draft listed in open_statuses: %+v", e)
	}
	if _, ok := got["c/p"]; ok {
		t.Fatal("default open status kept despite open_statuses")
	}
}
