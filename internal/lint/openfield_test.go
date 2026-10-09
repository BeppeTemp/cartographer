package lint

import (
	"strings"
	"testing"
)

// D347: a journal whose open/closed state lives in a domain field.
const ofMap = "---\ntype: Map\ntitle: J\nkind: journal\nopen_field: outcome\nopen_statuses: [open, mitigated]\n---\n"

func ofEntry(extra, body string) string {
	return "---\ntype: Incident\ntitle: E\nstatus: active\ntimestamp: 2026-01-02\n" + extra + "---\n# E\n" + body
}

func TestOpenField_DecayAndWork(t *testing.T) {
	withNow(t, "2026-06-01")
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "j/_map.md", ofMap)
	writeFile(t, k.DataRoot(), "j/open.md", ofEntry("outcome: open\n", ""))
	writeFile(t, k.DataRoot(), "j/resolved.md", ofEntry("outcome: resolved\n", "\n- [ ] leftover\n"))
	writeFile(t, k.DataRoot(), "j/archived.md", "---\ntype: Incident\ntitle: A\nstatus: archived\noutcome: open\ntimestamp: 2026-01-02\n---\n# A\n\n- [ ] x\n")
	writeFile(t, k.DataRoot(), "j/task.md", "---\ntype: Task\ntitle: T\nstatus: open\ntimestamp: 2026-01-02\n---\n# T\n")
	writeFile(t, k.DataRoot(), "j/empty.md", ofEntry("outcome: \"\"\n", ""))
	writeFile(t, k.DataRoot(), "j/list.md", ofEntry("outcome: [open]\n", ""))

	f, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	msg := func(path, check string) string {
		for _, x := range f {
			if x.Path == path && x.Check == check {
				return x.Message
			}
		}
		return ""
	}
	if m := msg("j/open.md", "stale_open"); !strings.Contains(m, `outcome "open"`) {
		t.Errorf("open outcome must be stale_open naming the field: %q", m)
	}
	if m := msg("j/resolved.md", "closed_with_open_items"); !strings.Contains(m, `outcome "resolved"`) {
		t.Errorf("resolved outcome with items: %q", m)
	}
	if hasCheck(f, "j/archived.md", "stale_open") || !hasCheck(f, "j/archived.md", "closed_with_open_items") {
		t.Errorf("archived wins over an open outcome: %v", f)
	}
	if m := msg("j/task.md", "stale_open"); !strings.Contains(m, `status "open"`) {
		t.Errorf("a concept without the field falls back to status: %q", m)
	}
	for _, id := range []string{"empty", "list"} {
		if hasCheck(f, "j/"+id+".md", "stale_open") {
			t.Errorf("%s: empty or non-string field falls back to status active, not open", id)
		}
		if hasCheck(f, "j/"+id+".md", "status_semantics") {
			t.Errorf("%s: status_semantics must not fire with open_field", id)
		}
	}
	if hasCheck(f, "j/open.md", "status_semantics") {
		t.Error("status_semantics must not fire with open_field")
	}

	entries, err := Work(k)
	if err != nil {
		t.Fatal(err)
	}
	got := workByID(t, entries)
	if e := got["j/open"]; !e.OpenPhase || e.State != "open" || e.Status != "active" {
		t.Errorf("open entry: %+v", e)
	}
	if e := got["j/resolved"]; e.OpenPhase || e.State != "resolved" {
		t.Errorf("resolved entry: %+v", e)
	}
	if e := got["j/task"]; !e.OpenPhase || e.State != "" {
		t.Errorf("task entry: %+v", e)
	}

	items := review(t, k)
	if names(itemsOf(items, ReviewStatusReclassify), "j/open") {
		t.Error("status_reclassify must not fire with open_field")
	}
}

func TestOpenField_Harvest(t *testing.T) {
	withNow(t, "2026-06-01")
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "j/_map.md", ofMap)
	writeFile(t, k.DataRoot(), "j/old.md", ofEntry("outcome: resolved\n", ""))
	if !names(itemsOf(review(t, k), ReviewHarvestCandidate), "j/old") {
		t.Error("a resolved outcome older than 45 days is a harvest candidate")
	}
}

func TestOpenField_ContractMalformed(t *testing.T) {
	for _, v := range []string{`"a b"`, "a.b"} {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "j/_map.md", "---\ntype: Map\ntitle: J\nkind: journal\nopen_field: "+v+"\n---\n")
		c, err := k.ReadMapContract("j")
		if err != nil || c.OpenField != "" || len(c.Malformed) != 1 || c.Malformed[0].Key != "open_field" {
			t.Errorf("%s: %+v %v", v, c, err)
		}
	}
	for v, want := range map[string]int{"0": -1, "-3": 0} {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "j/_map.md", "---\ntype: Map\ntitle: J\nkind: journal\nharvest_after: "+v+"\n---\n")
		c, _ := k.ReadMapContract("j")
		if c.HarvestAfterDays != want || (v == "-3") != (len(c.Malformed) == 1) {
			t.Errorf("harvest_after %s: %+v", v, c)
		}
	}
}
