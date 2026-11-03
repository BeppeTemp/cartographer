package mcpserver

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// #1503 WP9: a concept whose only fix is a removal does not take a heartbeat
// slot, a stuck one is counted as skipped, and the slots rotate.
func TestAutoRepairQuotaSkipsRemovalsAndRotates(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	if err := k.CreateMapWithContract("ops", "Ops", "map", nil, kb.MapContract{FieldValues: map[string][]string{"tags": {"a"}}}); err != nil {
		t.Fatal(err)
	}
	write := func(id string, fields map[string]any) {
		fm := newFM()
		fm.Set("type", "Note")
		fm.Set("title", id)
		for f, v := range fields {
			fm.Set(f, v)
		}
		if _, err := k.WriteConcept(okf.ConceptID("ops/"+id), fm, "# "+id+"\n", ""); err != nil {
			t.Fatal(err)
		}
	}
	// ops/a: only a removal (a tag the contract does not list, no synonym).
	write("a", map[string]any{"tags": []string{"zzz"}})
	// ops/b, ops/c: a rename the heartbeat applies.
	write("b", map[string]any{"updated": "2026-01-02"})
	write("c", map[string]any{"updated": "2026-01-03"})
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}

	// The removal is not a seed, so quota 1 reaches ops/b in the first run.
	var cursor string
	r := stagedAutoRepairFrom(context.Background(), k, lint.AutoRepairChecks(), 1, &cursor)
	if r.applied["nonstandard_field"] != 1 || cursor != "ops/b" {
		t.Fatalf("run 1: applied=%v skipped=%v cursor=%q", r.applied, r.skipped, cursor)
	}
	// The next run resumes after ops/b.
	r = stagedAutoRepairFrom(context.Background(), k, lint.AutoRepairChecks(), 1, &cursor)
	if r.applied["nonstandard_field"] != 1 {
		t.Fatalf("run 2: applied=%v skipped=%v cursor=%q", r.applied, r.skipped, cursor)
	}
	if d, _ := k.ReadConcept("ops/c"); strings.Contains(d.FrontmatterRaw, "updated:") {
		t.Errorf("ops/c was not repaired: %q", d.FrontmatterRaw)
	}
}

func TestRotateIDs(t *testing.T) {
	t.Parallel()
	ids := []string{"a", "b", "c", "d", "e"}
	cur := ""
	got := rotateIDs(ids, 2, &cur)
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("first = %v", got)
	}
	cur = "b"
	if got = rotateIDs(ids, 2, &cur); !reflect.DeepEqual(got, []string{"c", "d"}) {
		t.Fatalf("after b = %v", got)
	}
	cur = "e"
	if got = rotateIDs(ids, 2, &cur); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("wrap = %v", got)
	}
	cur = "d"
	if got = rotateIDs(ids, 2, &cur); !reflect.DeepEqual(got, []string{"e", "a"}) {
		t.Fatalf("wrap in the middle = %v", got)
	}
	// A cursor on a concept that is gone resumes at the next one.
	cur = "bb"
	if got = rotateIDs(ids, 2, &cur); !reflect.DeepEqual(got, []string{"c", "d"}) {
		t.Fatalf("vanished cursor = %v", got)
	}
	cur = "c"
	if got = rotateIDs(ids, 9, &cur); len(got) != 5 || cur != "" {
		t.Fatalf("everything fits: %v cursor %q", got, cur)
	}
}
