package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// D366: the mechanical part of index_lists_retired, link_to_retired and
// title_quality, end to end.

func TestRepairIndexListsRetired_DropsOnlyTheSingleLinkLine(t *testing.T) {
	k, s := repairKB(t, 0)
	driftWrite(t, k, "idx/_map.md", "---\ntype: Map\nkind: map\ntitle: Idx\n---\n# Idx\n")
	for _, n := range []string{"live1", "live2", "live3"} {
		driftWrite(t, k, "idx/"+n+".md", "---\ntype: Note\ntitle: "+n+"\n---\n# "+n+"\n")
	}
	for _, n := range []string{"old", "old2"} {
		driftWrite(t, k, "idx/"+n+".md", "---\ntype: Note\ntitle: "+n+"\nstatus: deprecated\n---\n# "+n+"\n")
	}
	index := "---\ntype: Index\ntitle: Idx\n---\n- [live1](live1.md)\n- [old](old.md): was the first\n- [old2](old2.md) replaced by [live2](live2.md)\n- [live3](live3.md)\n"
	driftWrite(t, k, "idx/index.md", index)
	before := commitCount(t, k)

	dry := repairCall(t, s, `{"check":"index_lists_retired"}`)
	if dry["planned_total"].(float64) != 1 || commitCount(t, k) != before {
		t.Fatalf("dry run = %v", dry)
	}
	out := repairApply(t, s, "index_lists_retired")
	if out["applied"].(float64) != 1 || commitCount(t, k) != before+1 {
		t.Fatalf("out = %v", out)
	}
	raw, err := os.ReadFile(filepath.Join(k.DataRoot(), "idx", "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(index, "- [old](old.md): was the first\n", "", 1)
	if string(raw) != want {
		t.Fatalf("index:\n%s\nwant:\n%s", raw, want)
	}
	if again := repairApply(t, s, "index_lists_retired"); again["applied"].(float64) != 0 || commitCount(t, k) != before+1 {
		t.Fatalf("not idempotent: %v", again)
	}
}

// retireKB is a map with a retired concept that names a live successor, and
// the live concepts that still link the retired one.
func retireKB(t *testing.T) (k *kb.KB, run func() *stagedRepair) {
	t.Helper()
	kk, _ := repairKB(t, 0)
	driftWrite(t, kk, "rt/_map.md", "---\ntype: Map\nkind: map\ntitle: Rt\n---\n# Rt\n")
	page := func(id, extra, body string) {
		driftWrite(t, kk, "rt/"+id+".md", "---\ntype: Note\ntitle: "+id+"\n"+extra+"---\n# "+id+"\n\n"+body+"\n")
	}
	page("old", "status: deprecated\nsuperseded_by: rt/new\n", "Replaced.")
	page("new", "", "The successor.")
	page("l1", "", "Runs on [the old box](old.md#setup) and [[rt/old|legacy]].")
	page("l2", "", "See [new](new.md) and [old](old.md).")
	// No successor: whether the mention is historical is judgement.
	page("gone", "status: deprecated\n", "Gone.")
	page("l3", "", "Was on [gone](gone.md).")
	// A chain: a1 -> a2 (retired) -> a3 (live). a1's links wait for a2 to be live.
	page("a1", "status: deprecated\nsuperseded_by: rt/a2\n", "First.")
	page("a2", "status: deprecated\nsuperseded_by: rt/a3\n", "Second.")
	page("a3", "", "Third.")
	page("l4", "", "Uses [a1](a1.md).")
	return kk, func() *stagedRepair {
		return stagedAutoRepair(context.Background(), kk, config.DefaultAutoRepair, 50)
	}
}

func TestStagedAutoRepair_LinkToRetiredFollowsSupersededBy(t *testing.T) {
	k, run := retireKB(t)
	read := func(id string) string {
		b, err := os.ReadFile(filepath.Join(k.DataRoot(), "rt", id+".md"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	before := map[string]string{"gone": read("gone"), "l3": read("l3"), "a1": read("a1"), "l4": read("l4")}
	r := run()
	if r.applied["link_to_retired"] != 2 {
		t.Fatalf("applied = %v, skipped = %v, errs = %v", r.applied, r.skipped, r.errs)
	}
	// Anchor, alias and label stay; only the target changes.
	if got := read("l1"); !strings.Contains(got, "[the old box](new.md#setup)") || !strings.Contains(got, "[[rt/new|legacy]]") || strings.Contains(got, "old.md") {
		t.Fatalf("l1:\n%s", got)
	}
	// l2 already linked the successor: the retargeted link is the repeat the
	// existing repeated_link fix unlinks on the same run.
	if got := read("l2"); strings.Contains(got, "old.md") || strings.Count(got, "](new.md)") != 1 || !strings.Contains(got, "and old.") {
		t.Fatalf("l2:\n%s", got)
	}
	// No successor, and a successor that is itself retired: untouched.
	for id, want := range before {
		if got := read(id); got != want {
			t.Errorf("%s changed:\n%s", id, got)
		}
	}
	// The retired concept's own relation keeps pointing where it did.
	if got := read("old"); !strings.Contains(got, "superseded_by: rt/new") {
		t.Fatalf("old:\n%s", got)
	}
	// A second run finds nothing to do.
	if again := run(); again.applied["link_to_retired"] != 0 {
		t.Fatalf("not idempotent: %v", again.applied)
	}
}

func TestStagedAutoRepair_StripsDecorativeTitleAndSyncsHeading(t *testing.T) {
	k, _ := repairKB(t, 0)
	driftWrite(t, k, "ops/deco.md", "---\ntype: Note\ntitle: \"🚀 Deploy notes ✨\"\nupdated: 2026-01-02\n---\n# 🚀 Deploy notes ✨\n")
	driftWrite(t, k, "ops/only.md", "---\ntype: Note\ntitle: \"🚀\"\nupdated: 2026-01-02\n---\n# 🚀\n")
	r := stagedAutoRepair(context.Background(), k, config.DefaultAutoRepair, 50)
	if r.applied["title_quality"] != 1 {
		t.Fatalf("applied = %v, skipped = %v, errs = %v", r.applied, r.skipped, r.errs)
	}
	got := readRaw(t, k, "ops/deco")
	if !strings.Contains(got, "title: Deploy notes\n") || !strings.Contains(got, "# Deploy notes\n") || strings.Contains(got, "🚀") {
		t.Fatalf("page:\n%s", got)
	}
	// A title that is only decoration has no unique replacement.
	if only := readRaw(t, k, "ops/only"); !strings.Contains(only, "🚀") {
		t.Fatalf("a title with nothing left was rewritten:\n%s", only)
	}
	again := stagedAutoRepair(context.Background(), k, config.DefaultAutoRepair, 50)
	if again.applied["title_quality"] != 0 {
		t.Fatalf("not idempotent: %v", again.applied)
	}
}
