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
	dir := tempKB(t)
	kb, _ := Init(dir)

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
	dir := tempKB(t)
	kb, _ := Init(dir)

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
	dir := tempKB(t)
	kb, _ := Init(dir)

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
	dir := tempKB(t)
	kb, _ := Init(dir)

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
	dir := tempKB(t)
	kb, _ := Init(dir)

	if err := kb.CreateMap("entities", "Entities", "map", nil, ""); err != nil {
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
	dir := tempKB(t)
	kb, _ := Init(dir)

	if err := kb.CreateMap("entities", "Entities", "map", nil, ""); err != nil {
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
	dir := tempKB(t)
	kb, _ := Init(dir)

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
	dir := tempKB(t)
	kb, _ := Init(dir)

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
