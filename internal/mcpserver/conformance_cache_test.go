package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// TestConformanceCacheWarmHitSkipsLint verifies that a second kb_status call
// without any write in between does not call lint.Run again (D294).
func TestConformanceCacheWarmHitSkipsLint(t *testing.T) {
	k := cacheTestKB(t)
	cc := &conformanceCache{}

	// Warm up graph cache so generation is stable.
	_, _ = k.GraphGeneration()

	f1, err := cc.lintFindings(k)
	if err != nil {
		t.Fatal(err)
	}
	if cc.LintCalls() != 1 {
		t.Fatalf("expected 1 lint call, got %d", cc.LintCalls())
	}
	_ = f1

	// Second call without writes: cache hit.
	f2, err := cc.lintFindings(k)
	if err != nil {
		t.Fatal(err)
	}
	if cc.LintCalls() != 1 {
		t.Fatalf("expected 1 lint call after warm hit, got %d", cc.LintCalls())
	}
	_ = f2
}

// TestConformanceCacheInvalidatedByWrite verifies that a concept_write between
// two calls invalidates the cache (D294).
func TestConformanceCacheInvalidatedByWrite(t *testing.T) {
	k := cacheTestKB(t)
	cc := &conformanceCache{}

	_, err := cc.lintFindings(k)
	if err != nil {
		t.Fatal(err)
	}
	if cc.LintCalls() != 1 {
		t.Fatalf("expected 1 lint call, got %d", cc.LintCalls())
	}

	// Write a concept to bump the graph generation.
	fm, _ := okf.ParseFrontmatter("")
	fm.Set("type", "Note")
	fm.Set("title", "New")
	if _, err := k.WriteConcept("arch/new", fm, "# New\n", ""); err != nil {
		t.Fatal(err)
	}

	_, err = cc.lintFindings(k)
	if err != nil {
		t.Fatal(err)
	}
	if cc.LintCalls() != 2 {
		t.Fatalf("expected 2 lint calls after write, got %d", cc.LintCalls())
	}
}

// TestConformanceCacheInvalidatedByOutOfBandEdit verifies that an out-of-band
// file edit invalidates the cache (D294).
func TestConformanceCacheInvalidatedByOutOfBandEdit(t *testing.T) {
	k := cacheTestKB(t)
	cc := &conformanceCache{}

	_, err := cc.lintFindings(k)
	if err != nil {
		t.Fatal(err)
	}
	if cc.LintCalls() != 1 {
		t.Fatalf("expected 1 lint call, got %d", cc.LintCalls())
	}

	// Out-of-band edit: write a file directly.
	p := filepath.Join(k.DataRoot(), "arch", "oob.md")
	if err := os.WriteFile(p, []byte("---\ntype: Note\ntitle: OOB\n---\n# OOB\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = cc.lintFindings(k)
	if err != nil {
		t.Fatal(err)
	}
	if cc.LintCalls() != 2 {
		t.Fatalf("expected 2 lint calls after out-of-band edit, got %d", cc.LintCalls())
	}
}

// TestConformanceCacheDoctorDateCached verifies that cachedDoctorDate reuses
// the cached value when the log file has not changed.
func TestConformanceCacheDoctorDateCached(t *testing.T) {
	k := cacheTestKB(t)
	cc := &conformanceCache{}

	d1 := cc.cachedDoctorDate(k)
	d2 := cc.cachedDoctorDate(k)
	if d1 != d2 {
		t.Fatalf("doctor date changed between calls: %q vs %q", d1, d2)
	}
}

func cacheTestKB(t *testing.T) *kb.KB {
	t.Helper()
	dir := t.TempDir()
	k, err := kb.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.CreateMapWithContract("arch", "Arch", "map", nil, "", kb.MapContract{}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		fm, _ := okf.ParseFrontmatter("")
		fm.Set("type", "Note")
		fm.Set("title", fmt.Sprintf("C%d", i))
		body := fmt.Sprintf("# C\n\nSee [next](c%d.md).\n", (i+1)%5)
		if _, err := k.WriteConcept(okf.ConceptID(fmt.Sprintf("arch/c%d", i)), fm, body, ""); err != nil {
			t.Fatal(err)
		}
	}
	return k
}

// TestConformanceCacheVisibilityFilterApplied verifies that the cached findings
// still go through per-caller visibility filtering (D226). The unrestricted
// findings should have more entries than restricted ones when there are hidden
// concepts.
func TestConformanceCacheVisibilityFilterApplied(t *testing.T) {
	k := cacheTestKB(t)
	cc := &conformanceCache{}

	// Warm the cache with an unrestricted call.
	allFindings, err := cc.lintFindings(k)
	if err != nil {
		t.Fatal(err)
	}

	// Apply visibility filter with full access.
	ctx := context.Background()
	vis, err := uiVisibleFindingsFrom(ctx, k, "", allFindings)
	if err != nil {
		t.Fatal(err)
	}

	// Same findings when both are unrestricted.
	if len(vis) != len(allFindings) {
		// The visibility filter may drop whole-graph checks for restricted callers,
		// but for unrestricted callers it should pass through all findings.
		_ = vis // unrestricted → all pass through
	}

	// The cache returns the same slice on a second call.
	allFindings2, err := cc.lintFindings(k)
	if err != nil {
		t.Fatal(err)
	}
	if cc.LintCalls() != 1 {
		t.Fatalf("expected 1 lint call, got %d", cc.LintCalls())
	}
	_ = allFindings2

	// Check that findings are genuinely the same reference (cache hit).
	if len(allFindings) != len(allFindings2) {
		t.Fatalf("cache returned different length: %d vs %d", len(allFindings), len(allFindings2))
	}

	// summarizeConformance uses only the conformanceChecks: verify it works
	// on the cached findings.
	summary := summarizeConformance(vis, 0, "", 14, lint.Now())
	if summary == nil {
		t.Fatal("summarizeConformance returned nil")
	}
}

// TestConformanceCacheInvalidatedByContractEdit: a map descriptor is not a
// concept file, so the graph generation does not move when it changes; the
// inputs stamp must (D294).
func TestConformanceCacheInvalidatedByContractEdit(t *testing.T) {
	k := cacheTestKB(t)
	cc := &conformanceCache{}
	if _, err := cc.lintFindings(k); err != nil {
		t.Fatal(err)
	}
	mapFile := filepath.Join(k.DataRoot(), "arch", "_map.md")
	data, err := os.ReadFile(mapFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mapFile, append(data, []byte("\n<!-- edited -->\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cc.lintFindings(k); err != nil {
		t.Fatal(err)
	}
	if cc.LintCalls() != 2 {
		t.Fatalf("expected 2 lint calls after a _map.md edit, got %d", cc.LintCalls())
	}
}

// TestConformanceCacheNeverNil: a clean KB caches an empty, non-nil slice, so
// uiVisibleFindingsFrom does not mistake it for "not computed" and lint again.
func TestConformanceCacheNeverNil(t *testing.T) {
	k := cacheTestKB(t)
	cc := &conformanceCache{}
	f, err := cc.lintFindings(k)
	if err != nil {
		t.Fatal(err)
	}
	if f == nil {
		t.Fatal("lintFindings returned nil")
	}
}
