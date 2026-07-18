package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// helpers

func tempKB(t *testing.T) *kb.KB {
	t.Helper()
	dir, err := os.MkdirTemp("", "lint-test-*")
	if err != nil {
		t.Fatalf("tempKB: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	k, err := kb.Init(dir)
	if err != nil {
		t.Fatalf("kb.Init: %v", err)
	}
	return k
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(abs), err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", abs, err)
	}
}

func hasCheck(findings []Finding, path, check string) bool {
	for _, f := range findings {
		if f.Path == path && f.Check == check {
			return true
		}
	}
	return false
}

// --- broken_link ---

func TestRun_BrokenLink_Detected(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/concept-a.md",
		"---\ntype: Note\n---\nSee [missing](missing.md).\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "arch/concept-a.md", "broken_link") {
		t.Errorf("expected broken_link for arch/concept-a.md, got: %v", findings)
	}
}

func TestRun_BrokenLink_ExistingTarget_Clean(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/concept-a.md",
		"---\ntype: Note\n---\nSee [concept-b](concept-b.md).\n")
	writeFile(t, k.DataRoot(), "arch/concept-b.md",
		"---\ntype: Note\n---\nContent.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hasCheck(findings, "arch/concept-a.md", "broken_link") {
		t.Errorf("unexpected broken_link for arch/concept-a.md: %v", findings)
	}
}

// --- stale_claim ---

func TestRun_StaleClaim_Detected(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/old-concept.md",
		"---\ntype: Note\nreview_after: 2020-01-01\n---\nOld content.\n")

	// Override Now to a point after the review date.
	orig := Now
	Now = func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) }
	defer func() { Now = orig }()

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "arch/old-concept.md", "stale_claim") {
		t.Errorf("expected stale_claim for arch/old-concept.md, got: %v", findings)
	}
}

func TestRun_StaleClaim_FutureDate_Clean(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/future-concept.md",
		"---\ntype: Note\nreview_after: 2099-12-31\n---\nFuture content.\n")

	orig := Now
	Now = func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) }
	defer func() { Now = orig }()

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hasCheck(findings, "arch/future-concept.md", "stale_claim") {
		t.Errorf("unexpected stale_claim for future date: %v", findings)
	}
}

func TestRun_StaleClaim_NoField_Clean(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/no-date.md",
		"---\ntype: Note\n---\nNo review_after here.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hasCheck(findings, "arch/no-date.md", "stale_claim") {
		t.Errorf("unexpected stale_claim for concept without review_after: %v", findings)
	}
}

// --- imported_draft (D74 WP1) ---

func TestRun_ImportedDraft_Detected(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/imported-concept.md",
		"---\ntype: Note\nstatus: imported\n---\nRaw content awaiting curation.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "arch/imported-concept.md", "imported_draft") {
		t.Errorf("expected imported_draft for arch/imported-concept.md, got: %v", findings)
	}
}

func TestRun_ImportedDraft_OtherStatus_Clean(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/curated-concept.md",
		"---\ntype: Note\nstatus: active\n---\nCurated content.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hasCheck(findings, "arch/curated-concept.md", "imported_draft") {
		t.Errorf("unexpected imported_draft for status: active: %v", findings)
	}
}

func TestRun_ImportedDraft_NoStatusField_Clean(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/no-status.md",
		"---\ntype: Note\n---\nNo status field here.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hasCheck(findings, "arch/no-status.md", "imported_draft") {
		t.Errorf("unexpected imported_draft for concept without status: %v", findings)
	}
}

// --- machine_path (D75 WP6) ---

func TestRun_MachinePath_MacHome_Detected(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/concept-a.md",
		"---\ntype: Note\n---\nRepo is at /Users/alice/repos/cartographer.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "arch/concept-a.md", "machine_path") {
		t.Errorf("expected machine_path for arch/concept-a.md, got: %v", findings)
	}
}

func TestRun_MachinePath_LinuxHome_Detected(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/concept-b.md",
		"---\ntype: Note\n---\nRepo is at /home/bob/repos/cartographer.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "arch/concept-b.md", "machine_path") {
		t.Errorf("expected machine_path for arch/concept-b.md, got: %v", findings)
	}
}

func TestRun_MachinePath_Tilde_Detected(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/concept-c.md",
		"---\ntype: Note\n---\nSee ~/Documents/notes.md for details.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "arch/concept-c.md", "machine_path") {
		t.Errorf("expected machine_path for arch/concept-c.md, got: %v", findings)
	}
}

func TestRun_MachinePath_WindowsUsers_Detected(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/concept-d.md",
		"---\ntype: Note\n---\nOn Windows: C:\\Users\\carol\\repos\\cartographer.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "arch/concept-d.md", "machine_path") {
		t.Errorf("expected machine_path for arch/concept-d.md, got: %v", findings)
	}
}

func TestRun_MachinePath_ContainerPaths_Clean(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/concept-e.md",
		"---\ntype: Note\n---\nConfig lives at /etc/cartographer/config.yaml, logs in /var/log/cartographer.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hasCheck(findings, "arch/concept-e.md", "machine_path") {
		t.Errorf("unexpected machine_path for container/cluster paths: %v", findings)
	}
}

func TestRun_MachinePath_Placeholder_Clean(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/concept-f.md",
		"---\ntype: Note\n---\nRepo is at {{repo:cartographer}}, assets in {{path:design}}.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hasCheck(findings, "arch/concept-f.md", "machine_path") {
		t.Errorf("unexpected machine_path for D75 placeholders: %v", findings)
	}
}

// --- orphan ---

func TestRun_Orphan_Detected(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/dossier/index.md",
		"---\ntype: Index\ntitle: Dossier\n---\n")
	writeFile(t, k.DataRoot(), "arch/dossier/orphan-concept.md",
		"---\ntype: Note\n---\nNo one links here.\n")
	// Also write _archive.md so arch appears as an archive.
	writeFile(t, k.DataRoot(), "arch/_archive.md",
		"---\ntype: Archive\ntitle: Arch\narchive_type: ops\nontology_mode: flexible\n---\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "arch/dossier/orphan-concept.md", "orphan") {
		t.Errorf("expected orphan for arch/dossier/orphan-concept.md, got: %v", findings)
	}
}

func TestRun_Orphan_LinkedConcept_Clean(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_archive.md",
		"---\ntype: Archive\ntitle: Arch\narchive_type: ops\nontology_mode: flexible\n---\n")
	writeFile(t, k.DataRoot(), "arch/concept-a.md",
		"---\ntype: Note\n---\nSee [concept-b](concept-b.md).\n")
	writeFile(t, k.DataRoot(), "arch/concept-b.md",
		"---\ntype: Note\n---\nLinked from concept-a.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// concept-b has an incoming link from concept-a; must not be orphan.
	if hasCheck(findings, "arch/concept-b.md", "orphan") {
		t.Errorf("unexpected orphan for linked concept-b: %v", findings)
	}
}

func TestRun_Orphan_ArchiveTopLevel_Skipped(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_archive.md",
		"---\ntype: Archive\ntitle: Arch\narchive_type: ops\nontology_mode: flexible\n---\n")
	// concept-a is directly under archive arch/ (depth=1) — must not be orphan.
	writeFile(t, k.DataRoot(), "arch/concept-a.md",
		"---\ntype: Note\n---\nEntry point for archive.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hasCheck(findings, "arch/concept-a.md", "orphan") {
		t.Errorf("unexpected orphan for archive top-level concept: %v", findings)
	}
}

// --- expanded_missing_index (D72 WP4, renamed in D77 WP4) ---

func TestRun_ExpandedMissingIndex_Detected(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md",
		"---\ntype: Map\ntitle: Arch\nkind: map\nontology_mode: flexible\n---\n")
	// Expanded directory created by hand (not via WriteConcept/ExpandConcept): no index.md.
	writeFile(t, k.DataRoot(), "arch/sub-concept/concept-a.md",
		"---\ntype: Note\n---\nContent.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "arch/sub-concept/index.md", "expanded_missing_index") {
		t.Errorf("expected expanded_missing_index for arch/sub-concept, got: %v", findings)
	}
}

func TestRun_ExpandedMissingIndex_WithIndex_Clean(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md",
		"---\ntype: Map\ntitle: Arch\nkind: map\nontology_mode: flexible\n---\n")
	writeFile(t, k.DataRoot(), "arch/sub-concept/index.md",
		"---\ntype: Index\ntitle: Sub Concept\n---\n")
	writeFile(t, k.DataRoot(), "arch/sub-concept/concept-a.md",
		"---\ntype: Note\n---\nContent.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hasCheck(findings, "arch/sub-concept/index.md", "expanded_missing_index") {
		t.Errorf("unexpected expanded_missing_index for expanded concept with index.md: %v", findings)
	}
}

// --- expanded_ambiguous (D77 WP4) ---

func TestRun_ExpandedAmbiguous_Detected(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md",
		"---\ntype: Map\ntitle: Arch\nkind: map\nontology_mode: flexible\n---\n")
	writeFile(t, k.DataRoot(), "arch/dup.md",
		"---\ntype: Note\n---\nDirect form.\n")
	writeFile(t, k.DataRoot(), "arch/dup/index.md",
		"---\ntype: Note\n---\nExpanded form.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "arch/dup.md", "expanded_ambiguous") {
		t.Errorf("expected expanded_ambiguous for arch/dup, got: %v", findings)
	}
}

// --- expanded_as_category (D77 WP4) ---

func TestRun_ExpandedAsCategory_Detected(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md",
		"---\ntype: Map\ntitle: Arch\nkind: map\nontology_mode: flexible\n---\n")
	// Index that links none of its many children: a taxonomy bucket.
	writeFile(t, k.DataRoot(), "arch/bucket/index.md",
		"---\ntype: Index\ntitle: Bucket\n---\nNothing linked here.\n")
	for i := 0; i < expandedAsCategoryMinChildren+1; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("arch/bucket/child-%d.md", i),
			"---\ntype: Note\n---\nUnrelated content.\n")
	}

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "arch/bucket", "expanded_as_category") {
		t.Errorf("expected expanded_as_category for arch/bucket, got: %v", findings)
	}
}

func TestRun_ExpandedAsCategory_LinkedChildren_Clean(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md",
		"---\ntype: Map\ntitle: Arch\nkind: map\nontology_mode: flexible\n---\n")
	n := expandedAsCategoryMinChildren + 1
	var links strings.Builder
	for i := 0; i < n; i++ {
		links.WriteString(fmt.Sprintf("- [[arch/coherent/child-%d]]\n", i))
	}
	writeFile(t, k.DataRoot(), "arch/coherent/index.md",
		"---\ntype: Index\ntitle: Coherent\n---\n"+links.String())
	for i := 0; i < n; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("arch/coherent/child-%d.md", i),
			"---\ntype: Note\n---\nPart of the same case.\n")
	}

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hasCheck(findings, "arch/coherent", "expanded_as_category") {
		t.Errorf("unexpected expanded_as_category for arch/coherent: %v", findings)
	}
}

// --- map_oversize (D77 WP4) ---

func TestRun_MapOversize_Detected(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md",
		"---\ntype: Map\ntitle: Arch\nkind: map\nontology_mode: flexible\n---\n")
	for i := 0; i < mapOversizeThreshold+1; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("arch/concept-%d.md", i),
			"---\ntype: Note\n---\nContent.\n")
	}

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "arch", "map_oversize") {
		t.Errorf("expected map_oversize for arch, got: %v", findings)
	}
}

// --- concept_oversize (D78) ---

func TestRun_ConceptOversize_Detected(t *testing.T) {
	k := tempKB(t)
	body := strings.Repeat("a", conceptOversizeThreshold+1)
	writeFile(t, k.DataRoot(), "arch/big-concept.md",
		"---\ntype: Note\n---\n"+body+"\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "arch/big-concept", "concept_oversize") {
		t.Errorf("expected concept_oversize for arch/big-concept, got: %v", findings)
	}
}

func TestRun_ConceptOversize_UnderThreshold_Clean(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/small-concept.md",
		"---\ntype: Note\n---\nContenuto breve.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hasCheck(findings, "arch/small-concept", "concept_oversize") {
		t.Errorf("did not expect concept_oversize for a small concept, got: %v", findings)
	}
}

// --- legacy_archive_descriptor (D77 WP4) ---

func TestRun_LegacyArchiveDescriptor_Detected(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_archive.md",
		"---\ntype: Archive\ntitle: Arch\narchive_type: ops\nontology_mode: flexible\n---\n")
	writeFile(t, k.DataRoot(), "arch/concept-a.md",
		"---\ntype: Note\n---\nContent.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "arch/_archive.md", "legacy_archive_descriptor") {
		t.Errorf("expected legacy_archive_descriptor for arch, got: %v", findings)
	}
}

func TestRun_LegacyArchiveDescriptor_MapDescriptor_Clean(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md",
		"---\ntype: Map\ntitle: Arch\nkind: map\nontology_mode: flexible\n---\n")
	writeFile(t, k.DataRoot(), "arch/concept-a.md",
		"---\ntype: Note\n---\nContent.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hasCheck(findings, "arch/_archive.md", "legacy_archive_descriptor") {
		t.Errorf("unexpected legacy_archive_descriptor for arch with _map.md: %v", findings)
	}
}

// --- scope filtering ---

func TestRun_ScopeFiltering(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "alpha/_archive.md",
		"---\ntype: Archive\ntitle: Alpha\narchive_type: ops\nontology_mode: flexible\n---\n")
	writeFile(t, k.DataRoot(), "alpha/dossier/index.md",
		"---\ntype: Index\ntitle: Dossier\n---\n")
	writeFile(t, k.DataRoot(), "alpha/dossier/concept-a.md",
		"---\ntype: Note\nreview_after: 2020-01-01\n---\n")
	writeFile(t, k.DataRoot(), "beta/_archive.md",
		"---\ntype: Archive\ntitle: Beta\narchive_type: ops\nontology_mode: flexible\n---\n")
	writeFile(t, k.DataRoot(), "beta/dossier/index.md",
		"---\ntype: Index\ntitle: Dossier\n---\n")
	writeFile(t, k.DataRoot(), "beta/dossier/concept-b.md",
		"---\ntype: Note\nreview_after: 2020-01-01\n---\n")

	orig := Now
	Now = func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) }
	defer func() { Now = orig }()

	// Lint only the alpha scope.
	findings, err := Run(k, "alpha", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "alpha/dossier/concept-a.md", "stale_claim") {
		t.Errorf("expected stale_claim in alpha scope, got: %v", findings)
	}
	if hasCheck(findings, "beta/dossier/concept-b.md", "stale_claim") {
		t.Errorf("unexpected stale_claim outside scope: %v", findings)
	}
}

func TestRun_ScopeNeighbors(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_archive.md",
		"---\ntype: Archive\ntitle: Arch\narchive_type: ops\nontology_mode: flexible\n---\n")
	// concept-a (in scope) links to concept-b (neighbor).
	// concept-b has a broken link.
	writeFile(t, k.DataRoot(), "arch/concept-a.md",
		"---\ntype: Note\n---\nSee [concept-b](concept-b.md).\n")
	writeFile(t, k.DataRoot(), "arch/concept-b.md",
		"---\ntype: Note\n---\nSee [nonexistent](nonexistent.md).\n")

	// Without scopeNeighbors: only concept-a is checked, no broken_link for concept-b.
	findings, err := Run(k, "arch/concept-a", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hasCheck(findings, "arch/concept-b.md", "broken_link") {
		t.Errorf("unexpected finding for concept-b without scopeNeighbors: %v", findings)
	}

	// With scopeNeighbors: concept-b is included, broken_link found.
	findings, err = Run(k, "arch/concept-a", true)
	if err != nil {
		t.Fatalf("Run scopeNeighbors: %v", err)
	}
	if !hasCheck(findings, "arch/concept-b.md", "broken_link") {
		t.Errorf("expected broken_link for concept-b with scopeNeighbors, got: %v", findings)
	}
}
