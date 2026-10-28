package kb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// --- WriteConceptBatch (D125 WP2): atomic multi-concept write + rollback ---

func TestWriteConceptBatch_AppliesAllAndOneLogEntry(t *testing.T) {
	t.Parallel()
	kb := newTestKB(t)

	fm1, _ := okf.ParseFrontmatter("type: Runbook\ntitle: One")
	fm2, _ := okf.ParseFrontmatter("type: Runbook\ntitle: Two")
	ops := []BatchWriteOp{
		{ID: okf.ConceptID("alpha"), FM: fm1, Body: "# One\n"},
		{ID: okf.ConceptID("beta"), FM: fm2, Body: "# Two\n"},
	}

	results, err := kb.WriteConceptBatch(ops, "concept_batch: 2 op(s)", nil)
	if err != nil {
		t.Fatalf("WriteConceptBatch: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != "alpha" || results[1].ID != "beta" {
		t.Fatalf("results out of order: %+v", results)
	}

	for _, id := range []string{"alpha", "beta"} {
		if _, err := kb.ReadConcept(okf.ConceptID(id)); err != nil {
			t.Errorf("ReadConcept(%s): %v", id, err)
		}
	}

	logContent, err := kb.ReadRaw("log.md")
	if err != nil {
		t.Fatalf("ReadRaw log.md: %v", err)
	}
	if n := strings.Count(logContent, "## "); n != 1 {
		t.Errorf("expected exactly one log entry for the whole batch, got %d in:\n%s", n, logContent)
	}
	if !strings.Contains(logContent, "2 op(s)") {
		t.Errorf("expected the summary batch message in log.md, got: %q", logContent)
	}
}

func TestWriteConceptBatch_RollsBackOnMidBatchFailure(t *testing.T) {
	t.Parallel()
	kb := newTestKB(t)

	fmExisting, _ := okf.ParseFrontmatter("type: Runbook\ntitle: Existing")
	if _, err := kb.WriteConcept(okf.ConceptID("existing"), fmExisting, "# V1\n", ""); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	preLog, err := kb.ReadRaw("log.md")
	if err != nil {
		t.Fatalf("ReadRaw log.md: %v", err)
	}

	fm1, _ := okf.ParseFrontmatter("type: Runbook\ntitle: New")
	fm2, _ := okf.ParseFrontmatter("type: Runbook\ntitle: Existing V2")
	ops := []BatchWriteOp{
		{ID: okf.ConceptID("brand-new"), FM: fm1, Body: "# New\n"},
		// Stale if_match: the second op must fail preparation before any
		// file is touched.
		{ID: okf.ConceptID("existing"), FM: fm2, Body: "# V2\n", IfMatch: "wrong-hash"},
	}

	_, err = kb.WriteConceptBatch(ops, "concept_batch: 2 op(s)", nil)
	if !errors.Is(err, okf.ErrStaleWrite) {
		t.Fatalf("expected ErrStaleWrite, got: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(kb.DataRoot(), "brand-new.md")); !os.IsNotExist(statErr) {
		t.Error("first op's file must not exist after a later op's preparation failure")
	}
	data, err := kb.ReadRaw("existing.md")
	if err != nil {
		t.Fatalf("ReadRaw existing.md: %v", err)
	}
	if !strings.Contains(data, "title: Existing") || strings.Contains(data, "Existing V2") {
		t.Errorf("existing concept must be untouched, got: %q", data)
	}
	postLog, err := kb.ReadRaw("log.md")
	if err != nil {
		t.Fatalf("ReadRaw log.md: %v", err)
	}
	if postLog != preLog {
		t.Errorf("log.md must be unchanged on preparation failure:\nbefore: %q\nafter:  %q", preLog, postLog)
	}
}

func TestWriteConceptBatch_RollsBackOnWriteFailureAfterFirstOpSucceeded(t *testing.T) {
	t.Parallel()
	kb := newTestKB(t)

	fmExisting, _ := okf.ParseFrontmatter("type: Runbook\ntitle: Existing")
	firstHash, err := kb.WriteConcept(okf.ConceptID("existing"), fmExisting, "# V1\n", "")
	if err != nil {
		t.Fatalf("seed write: %v", err)
	}

	fm1, _ := okf.ParseFrontmatter("type: Runbook\ntitle: New")
	fm2, _ := okf.ParseFrontmatter("type: Runbook\ntitle: Existing V2")
	ops := []BatchWriteOp{
		{ID: okf.ConceptID("brand-new"), FM: fm1, Body: "# New\n"},
		{ID: okf.ConceptID("existing"), FM: fm2, Body: "# V2\n", IfMatch: firstHash},
	}

	// afterFiles simulates an index-sync failure discovered only once every
	// file (both ops here) has already been written to disk.
	injected := errors.New("simulated index failure")
	_, err = kb.WriteConceptBatch(ops, "concept_batch: 2 op(s)", func(_ []BatchWriteResult) error {
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("expected the afterFiles error to surface, got: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(kb.DataRoot(), "brand-new.md")); !os.IsNotExist(statErr) {
		t.Error("op committed before the afterFiles failure must be rolled back (new-file cleanup)")
	}
	data, err := kb.ReadRaw("existing.md")
	if err != nil {
		t.Fatalf("ReadRaw existing.md: %v", err)
	}
	if !strings.Contains(data, "title: Existing") || strings.Contains(data, "Existing V2") {
		t.Errorf("previously-existing concept must be restored to its original content, got: %q", data)
	}
}

func TestWriteConceptBatch_RollbackRestoresOriginalFileMode(t *testing.T) {
	t.Parallel()
	kb := newTestKB(t)

	fm1, _ := okf.ParseFrontmatter("type: Runbook\ntitle: Existing")
	if _, err := kb.WriteConcept(okf.ConceptID("existing"), fm1, "# V1\n", ""); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	targetPath := filepath.Join(kb.DataRoot(), "existing.md")
	if err := os.Chmod(targetPath, 0o640); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	info, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	wantMode := info.Mode()

	fm2, _ := okf.ParseFrontmatter("type: Runbook\ntitle: V2")
	ops := []BatchWriteOp{
		{ID: okf.ConceptID("existing"), FM: fm2, Body: "# V2\n", IfMatch: ""},
	}
	injected := errors.New("simulated failure")
	_, err = kb.WriteConceptBatch(ops, "concept_batch: 1 op(s)", func(_ []BatchWriteResult) error {
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("expected injected error, got: %v", err)
	}

	gotInfo, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("stat after rollback: %v", err)
	}
	if gotInfo.Mode() != wantMode {
		t.Errorf("mode not restored: got %v, want %v", gotInfo.Mode(), wantMode)
	}
}

func TestWriteConceptBatch_NewExpandedStubRolledBackAndPruned(t *testing.T) {
	t.Parallel()
	kb := newTestKB(t)

	if err := kb.CreateMap("entities", "Entities", "map", nil); err != nil {
		t.Fatalf("CreateMap: %v", err)
	}

	fm1, _ := okf.ParseFrontmatter("type: Runbook\ntitle: Foo")
	ops := []BatchWriteOp{
		// A brand-new 3-segment child implicitly stubs entities/smart-home/index.md.
		{ID: okf.ConceptID("entities/smart-home/foo"), FM: fm1, Body: "# Foo\n"},
		// This op fails after the first has already been committed (and its
		// implicit stub with it).
		{ID: okf.ConceptID("no-type"), FM: mustEmptyType(t), Body: "# Bad\n"},
	}

	_, err := kb.WriteConceptBatch(ops, "concept_batch: 2 op(s)", nil)
	if !errors.Is(err, okf.ErrInvalidConcept) {
		t.Fatalf("expected ErrInvalidConcept from the second op, got: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(kb.DataRoot(), "entities", "smart-home")); !os.IsNotExist(statErr) {
		t.Error("the whole newly created expanded directory (concept + implicit stub) must be pruned on rollback")
	}
}

func TestWriteConceptBatch_ExpandedOwnerUpdate(t *testing.T) {
	t.Parallel()
	kb := newTestKB(t)

	if err := kb.CreateMap("entities", "Entities", "map", nil); err != nil {
		t.Fatalf("CreateMap: %v", err)
	}
	fm, _ := okf.ParseFrontmatter("type: Entity\ntitle: Owner")
	if err := kb.ExpandConcept(okf.ConceptID("entities/owner")); err == nil {
		t.Fatal("ExpandConcept on a non-existent concept should fail")
	}
	if _, err := kb.WriteConcept(okf.ConceptID("entities/owner"), fm, "# Owner\n", ""); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	if err := kb.ExpandConcept(okf.ConceptID("entities/owner")); err != nil {
		t.Fatalf("ExpandConcept: %v", err)
	}
	owner, err := kb.ReadConcept(okf.ConceptID("entities/owner"))
	if err != nil {
		t.Fatalf("ReadConcept owner: %v", err)
	}

	fm2, _ := okf.ParseFrontmatter("type: Entity\ntitle: Owner Updated")
	ops := []BatchWriteOp{
		{ID: okf.ConceptID("entities/owner"), FM: fm2, Body: "# Owner Updated\n", IfMatch: owner.ContentHash},
	}
	results, err := kb.WriteConceptBatch(ops, "concept_batch: 1 op(s)", nil)
	if err != nil {
		t.Fatalf("WriteConceptBatch: %v", err)
	}
	if len(results) != 1 || results[0].ID != "entities/owner" {
		t.Fatalf("unexpected results: %+v", results)
	}

	// Resolved through the expanded index.md, not a new direct file.
	if _, statErr := os.Stat(filepath.Join(kb.DataRoot(), "entities", "owner.md")); !os.IsNotExist(statErr) {
		t.Error("expanded owner update must not create a direct entities/owner.md")
	}
	updated, err := kb.ReadRaw("entities/owner/index.md")
	if err != nil {
		t.Fatalf("ReadRaw entities/owner/index.md: %v", err)
	}
	if !strings.Contains(updated, "Owner Updated") {
		t.Errorf("expanded owner's index.md was not updated in place: %q", updated)
	}
}

func TestWriteConceptBatch_RestartReadbackAfterRollback(t *testing.T) {
	t.Parallel()
	kb := newTestKB(t)
	dir := kb.Root

	fm1, _ := okf.ParseFrontmatter("type: Runbook\ntitle: Existing")
	if _, err := kb.WriteConcept(okf.ConceptID("existing"), fm1, "# V1\n", ""); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	before, err := kb.ReadRaw("existing.md")
	if err != nil {
		t.Fatalf("ReadRaw before: %v", err)
	}

	fm2, _ := okf.ParseFrontmatter("type: Runbook\ntitle: V2")
	ops := []BatchWriteOp{
		{ID: okf.ConceptID("existing"), FM: fm2, Body: "# V2\n"},
	}
	injected := errors.New("simulated failure")
	if _, err := kb.WriteConceptBatch(ops, "concept_batch: 1 op(s)", func(_ []BatchWriteResult) error { return injected }); !errors.Is(err, injected) {
		t.Fatalf("expected injected error, got: %v", err)
	}

	// Re-open the KB from disk (simulates a server restart) and confirm the
	// rollback is durable, not just an in-memory artifact of this kb value.
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("Open after rollback: %v", err)
	}
	after, err := reopened.ReadRaw("existing.md")
	if err != nil {
		t.Fatalf("ReadRaw after reopen: %v", err)
	}
	if after != before {
		t.Errorf("content not restored after restart:\nbefore: %q\nafter:  %q", before, after)
	}
}

func TestWriteConceptBatch_EmptyBatchRejected(t *testing.T) {
	t.Parallel()
	kb := newTestKB(t)

	if _, err := kb.WriteConceptBatch(nil, "concept_batch: 0 op(s)", nil); !errors.Is(err, okf.ErrInvalidConcept) {
		t.Fatalf("expected ErrInvalidConcept for an empty batch, got: %v", err)
	}
}

// mustEmptyType returns a Frontmatter with no "type" field, so
// prepareWriteConcept rejects it deterministically (used to force a
// mid-batch failure in rollback tests).
func mustEmptyType(t *testing.T) *okf.Frontmatter {
	t.Helper()
	fm, err := okf.ParseFrontmatter("title: No Type")
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	return fm
}

// #1276, #1301: a concept file (serialized frontmatter + body) over
// MaxConceptBytes is refused before anything is written, by WriteConcept and by
// a batch, whichever half is big; a file exactly at the limit is accepted.
func TestWriteConceptRefusesAnOversizeBody(t *testing.T) {
	t.Parallel()
	kb := newTestKB(t)
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: Big")

	big := strings.Repeat("a", MaxConceptBytes+1)
	_, err := kb.WriteConcept("big", fm, big, "")
	if !errors.Is(err, okf.ErrInvalidConcept) || !strings.Contains(err.Error(), "concept_too_large") || !strings.Contains(err.Error(), "1048576") {
		t.Fatalf("WriteConcept oversize err = %v", err)
	}
	_, err = kb.WriteConceptBatch([]BatchWriteOp{{ID: "big", FM: fm, Body: big}}, "x", nil)
	if err == nil || !strings.Contains(err.Error(), "concept_too_large") {
		t.Fatalf("batch oversize err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(kb.DataRoot(), "big.md")); !os.IsNotExist(statErr) {
		t.Fatalf("an oversize write left a file: %v", statErr)
	}
	// A huge frontmatter value with a small body is the same concept (#1301).
	bigFM, _ := okf.ParseFrontmatter("type: Note\ntitle: Big")
	bigFM.Set("blob", strings.Repeat("a", MaxConceptBytes))
	_, err = kb.WriteConcept("bigfm", bigFM, "small\n", "")
	if !errors.Is(err, okf.ErrInvalidConcept) || !strings.Contains(err.Error(), "concept_too_large") {
		t.Fatalf("oversize frontmatter err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(kb.DataRoot(), "bigfm.md")); !os.IsNotExist(statErr) {
		t.Fatalf("an oversize frontmatter left a file: %v", statErr)
	}
	// Exactly at the limit: the whole file, header included.
	overhead := len("---\n" + fm.Serialize() + "\n---\n")
	if _, err := kb.WriteConcept("ok", fm, strings.Repeat("a", MaxConceptBytes-overhead-1)+"\n", ""); err != nil {
		t.Fatalf("a file at the limit: %v", err)
	}
	if _, err := kb.WriteConcept("ok2", fm, strings.Repeat("a", MaxConceptBytes-overhead)+"\n", ""); err == nil {
		t.Fatal("a file one byte over the limit was accepted")
	}
}

// A delete in a batch is rolled back with the rest when a later step fails, and
// a missing concept or an expanded one is refused before anything is touched (D1327).
func TestWriteConceptBatch_DeleteRollsBackAndRefusesMissing(t *testing.T) {
	t.Parallel()
	kb := newTestKB(t)
	fm, _ := okf.ParseFrontmatter("type: Runbook\ntitle: Keep")
	if _, err := kb.WriteConcept(okf.ConceptID("keep"), fm, "# Keep\n", ""); err != nil {
		t.Fatal(err)
	}
	before, _ := kb.ReadRaw("keep.md")
	fmNew, _ := okf.ParseFrontmatter("type: Runbook\ntitle: New")
	failAfter := errors.New("boom")
	// The delete and a later write are both applied when the failure comes.
	_, err := kb.WriteConceptBatch([]BatchWriteOp{
		{ID: "keep", Delete: true},
		{ID: "fresh", FM: fmNew, Body: "# New\n"},
	}, "concept_batch", func([]BatchWriteResult) error { return failAfter })
	if !errors.Is(err, failAfter) {
		t.Fatalf("err = %v, want the afterFiles failure", err)
	}
	if after, _ := kb.ReadRaw("keep.md"); after != before {
		t.Fatalf("a rolled-back delete must restore the exact bytes:\n%q\n%q", before, after)
	}
	if _, err := kb.ReadConcept("fresh"); !errors.Is(err, okf.ErrNotFound) {
		t.Fatalf("the write after the delete must be rolled back too: %v", err)
	}
	if _, err := kb.ReadConcept("keep"); err != nil {
		t.Fatalf("a rolled-back delete must restore the concept: %v", err)
	}
	if _, err := kb.WriteConceptBatch([]BatchWriteOp{{ID: "keep", Delete: true}, {ID: "nope", Delete: true}}, "concept_batch", nil); !errors.Is(err, okf.ErrNotFound) {
		t.Fatalf("err = %v, want not found", err)
	}
	if _, err := kb.ReadConcept("keep"); err != nil {
		t.Fatalf("a refused batch must delete nothing: %v", err)
	}
}

// seedExpandedWithSatellites builds entities/owner (expanded) with two satellites.
func seedExpandedWithSatellites(t *testing.T, kb *KB) {
	t.Helper()
	if err := kb.CreateMap("entities", "Entities", "map", nil); err != nil {
		t.Fatalf("CreateMap: %v", err)
	}
	fm, _ := okf.ParseFrontmatter("type: Entity\ntitle: T")
	if _, err := kb.WriteConcept("entities/owner", fm, "# Owner\n", ""); err != nil {
		t.Fatal(err)
	}
	if err := kb.ExpandConcept("entities/owner"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"entities/owner/a", "entities/owner/b"} {
		if _, err := kb.WriteConcept(okf.ConceptID(id), fm, "# S\n", ""); err != nil {
			t.Fatal(err)
		}
	}
}

// A batch deletes an expanded concept together with every satellite under it,
// leaves no empty directory, and a failure after the deletes puts it all back (#1359).
func TestWriteConceptBatch_DeleteExpandedWithSatellites(t *testing.T) {
	t.Parallel()
	kb := newTestKB(t)
	seedExpandedWithSatellites(t, kb)
	dir := filepath.Join(kb.DataRoot(), "entities", "owner")
	ops := []BatchWriteOp{
		{ID: "entities/owner", Delete: true},
		{ID: "entities/owner/a", Delete: true},
		{ID: "entities/owner/b", Delete: true},
	}

	if _, err := kb.WriteConceptBatch(ops, "concept_batch: 3 op(s)", func([]BatchWriteResult) error { return errors.New("boom") }); err == nil {
		t.Fatal("afterFiles failure must fail the batch")
	}
	for _, id := range []string{"entities/owner", "entities/owner/a", "entities/owner/b"} {
		if _, err := kb.ReadConcept(okf.ConceptID(id)); err != nil {
			t.Errorf("%s must be restored by the rollback: %v", id, err)
		}
	}

	if _, err := kb.WriteConceptBatch(ops, "concept_batch: 3 op(s)", nil); err != nil {
		t.Fatalf("WriteConceptBatch: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the emptied expanded directory must be removed: %v", err)
	}
}

// What the caller did not list is never deleted with an expanded concept, and
// nothing is written below a concept the batch deletes (#1359).
func TestWriteConceptBatch_DeleteExpandedRefusals(t *testing.T) {
	t.Parallel()
	fm, _ := okf.ParseFrontmatter("type: Entity\ntitle: T")
	tests := []struct {
		name  string
		setup func(t *testing.T, kb *KB)
		ops   []BatchWriteOp
		want  string
	}{
		{"satellite not listed", func(*testing.T, *KB) {}, []BatchWriteOp{
			{ID: "entities/owner", Delete: true}, {ID: "entities/owner/a", Delete: true},
		}, "entities/owner/b"},
		{"asset owned", func(t *testing.T, kb *KB) {
			if _, err := kb.WriteAsset("entities/owner", "data.csv", []byte("x"), "", nil); err != nil {
				t.Fatal(err)
			}
		}, []BatchWriteOp{
			{ID: "entities/owner", Delete: true}, {ID: "entities/owner/a", Delete: true}, {ID: "entities/owner/b", Delete: true},
		}, "data.csv"},
		{"write below deleted", func(*testing.T, *KB) {}, []BatchWriteOp{
			{ID: "entities/owner", Delete: true}, {ID: "entities/owner/a", Delete: true}, {ID: "entities/owner/b", Delete: true},
			{ID: "entities/owner/c", FM: fm, Body: "# C\n"},
		}, "entities/owner/c"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			kb := newTestKB(t)
			seedExpandedWithSatellites(t, kb)
			tc.setup(t, kb)
			_, err := kb.WriteConceptBatch(tc.ops, "concept_batch", nil)
			if !errors.Is(err, okf.ErrInvalidConcept) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want ErrInvalidConcept naming %q, got %v", tc.want, err)
			}
			for _, id := range []string{"entities/owner", "entities/owner/a", "entities/owner/b"} {
				if _, err := kb.ReadConcept(okf.ConceptID(id)); err != nil {
					t.Errorf("%s must survive a refused batch: %v", id, err)
				}
			}
		})
	}
}
