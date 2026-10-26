package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// helpers

func tempKB(t *testing.T) *kb.KB {
	t.Helper()
	return kbtest.New(t)
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

func countCheck(findings []Finding, path, check string) int {
	count := 0
	for _, f := range findings {
		if f.Path == path && f.Check == check {
			count++
		}
	}
	return count
}

// --- broken_link ---

func TestRun_BrokenLink_Detected(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// --- missing_title (#322) ---

func TestRun_MissingTitle(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/untitled.md",
		"---\ntype: Note\n---\n# Backup of the laptop\n\nBody.\n")
	writeFile(t, k.DataRoot(), "arch/blank-title.md",
		"---\ntype: Note\ntitle: \"  \"\n---\nNo heading at all.\n")
	writeFile(t, k.DataRoot(), "arch/titled.md",
		"---\ntype: Note\ntitle: Titled\n---\n# Titled\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var untitled *Finding
	for i := range findings {
		if findings[i].Path == "arch/untitled.md" && findings[i].Check == "missing_title" {
			untitled = &findings[i]
		}
	}
	if untitled == nil {
		t.Fatalf("expected missing_title for arch/untitled.md, got: %v", findings)
	}
	if !strings.Contains(untitled.Message, `title: "Backup of the laptop"`) {
		t.Errorf("missing_title should be a warning suggesting the first H1, got %+v", *untitled)
	}
	if !hasCheck(findings, "arch/blank-title.md", "missing_title") {
		t.Errorf("a blank title is a missing title: %v", findings)
	}
	if hasCheck(findings, "arch/titled.md", "missing_title") {
		t.Errorf("unexpected missing_title for arch/titled.md: %v", findings)
	}
}

// --- machine_path (D75 WP6) ---

func TestRun_MachinePath_MacHome_Detected(t *testing.T) {
	t.Parallel()
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
	// The message offers the portable spellings, and never a key the contract
	// reports as legacy (D907, #837).
	for _, f := range findings {
		if f.Check != "machine_path" {
			continue
		}
		for _, want := range []string{"$HOME/", "{{repo:<key>}}", "{{path:<name>}}"} {
			if !strings.Contains(f.Message, want) {
				t.Errorf("machine_path message lacks %q: %s", want, f.Message)
			}
		}
		if strings.Contains(f.Message, "machine_path_allow_prefixes") || strings.Contains(f.Message, "<nome>") {
			t.Errorf("machine_path message names a removed key or a typo: %s", f.Message)
		}
	}
}

func TestRun_MachinePath_LinuxHome_Detected(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

// TestRun_MachinePath_NamesEveryPath: one finding per concept, naming every
// disallowed path, so fixing the first does not reveal the next one.
func TestRun_MachinePath_NamesEveryPath(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/concept-m.md",
		"---\ntype: Note\n---\nSee ~/.zprofile, then ~/.codex/hooks.json and ~/.zprofile again.\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	n := 0
	for _, f := range findings {
		if f.Check == "machine_path" {
			n++
			if !strings.Contains(f.Message, "and 1 more here: ~/.codex/hooks.json") {
				t.Errorf("message does not name the other path: %s", f.Message)
			}
		}
	}
	if n != 1 {
		t.Errorf("want one machine_path finding, got %d", n)
	}
}

func TestRun_MachinePath_WindowsUsers_Detected(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// --- machine_path conventional prefixes (D313) ---

func TestRun_MachinePath_InsideURL_Clean(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/concept-url.md",
		"---\ntype: Note\n---\nSee https://example.com/home/bob/profile for details.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hasCheck(findings, "arch/concept-url.md", "machine_path") {
		t.Errorf("unexpected machine_path for a path inside a URL: %v", findings)
	}
}

func TestRun_MachinePath_AllowedFirst_DisallowedLater_Flagged(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "infra/mixed.md",
		"---\ntype: Note\n---\nThe ssh config ~/.ssh/config is fine, but the author's own /home/carol/notes.md is not.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if countCheck(findings, "infra/mixed.md", "machine_path") != 1 {
		t.Fatalf("expected exactly one machine_path finding once the disallowed path is reached: %v", findings)
	}
	found := false
	for _, f := range findings {
		if f.Path == "infra/mixed.md" && f.Check == "machine_path" {
			found = strings.Contains(f.Message, "/home/carol/notes.md")
		}
	}
	if !found {
		t.Errorf("expected the disallowed path to be the one reported, got: %v", findings)
	}
}

func TestRun_MachinePath_PrefixBoundaryCollision_Flagged(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "infra/collision.md",
		"---\ntype: Note\n---\nUnrelated ~/.sshx/data is not covered by the conventional ~/.ssh/ prefix.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "infra/collision.md", "machine_path") {
		t.Errorf("expected machine_path: ~/.sshx must not match the ~/.ssh/ conventional prefix (segment boundary): %v", findings)
	}
}

// --- orphan ---

func TestRun_Orphan_Detected(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/dossier/index.md",
		"---\ntype: Index\ntitle: Dossier\n---\n")
	writeFile(t, k.DataRoot(), "arch/dossier/orphan-concept.md",
		"---\ntype: Note\n---\nNo one links here.\n")
	// Also write _archive.md so arch appears as an archive.
	writeFile(t, k.DataRoot(), "arch/_archive.md",
		"---\ntype: Archive\ntitle: Arch\narchive_type: ops\n---\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "arch/dossier/orphan-concept.md", "orphan") {
		t.Errorf("expected orphan for arch/dossier/orphan-concept.md, got: %v", findings)
	}
}

func TestRun_Orphan_LinkedConcept_Clean(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_archive.md",
		"---\ntype: Archive\ntitle: Arch\narchive_type: ops\n---\n")
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

// Self-links have always counted as incoming links for the orphan check. The
// derived D108 adjacency retains that compatibility even though traversal
// excludes self-edges.
func TestRun_Orphan_SelfLinkRemainsClean(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/self.md",
		"---\ntype: Note\n---\nSee [[arch/self]].\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hasCheck(findings, "arch/self.md", "orphan") {
		t.Errorf("self-link must retain pre-D108 orphan behaviour: %v", findings)
	}
}

func TestRun_Orphan_ArchiveTopLevel_Skipped(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_archive.md",
		"---\ntype: Archive\ntitle: Arch\narchive_type: ops\n---\n")
	// concept-a is directly under archive arch/ (depth=1) and leads somewhere
	// — an entry point, not an orphan.
	writeFile(t, k.DataRoot(), "arch/concept-a.md",
		"---\ntype: Note\n---\nEntry point for archive, see [[arch/concept-b]].\n")
	writeFile(t, k.DataRoot(), "arch/concept-b.md",
		"---\ntype: Note\n---\nB.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hasCheck(findings, "arch/concept-a.md", "orphan") {
		t.Errorf("unexpected orphan for archive top-level concept: %v", findings)
	}
}

// TestRun_Orphan_ArchiveTopLevel_Isolated: a top-level concept with no link
// in or out is a node connected to nothing — the index listing it is not an
// edge of the graph the Atlas draws (found on a real KB).
func TestRun_Orphan_ArchiveTopLevel_Isolated(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md",
		"---\ntype: Map\ntitle: Arch\nkind: map\n---\n")
	writeFile(t, k.DataRoot(), "arch/lonely.md",
		"---\ntype: Note\n---\nNothing links here, and this links nowhere.\n")
	// A second live page: the only live page of an archive is exempt (D1099).
	writeFile(t, k.DataRoot(), "arch/other.md",
		"---\ntype: Note\n---\nNothing links here either.\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCheck(findings, "arch/lonely.md", "orphan") {
		t.Errorf("isolated top-level concept not reported: %v", findings)
	}
	// link_suggest has no graph evidence for an isolated node: the message
	// must not promise what only the title fallback can give (D317).
	for _, f := range findings {
		if f.Check == "orphan" && strings.Contains(f.Message, "proposes some") {
			t.Errorf("orphan message overpromises: %q", f.Message)
		}
	}
}

// --- expanded_missing_index (D72 WP4, renamed in D77 WP4) ---

func TestRun_ExpandedMissingIndex_Detected(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md",
		"---\ntype: Map\ntitle: Arch\nkind: map\n---\n")
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
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md",
		"---\ntype: Map\ntitle: Arch\nkind: map\n---\n")
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

func TestRun_AssetsOnlyProduceOrphanAsset(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md", "---\ntype: Map\ntitle: Arch\nkind: map\n---\n")
	writeFile(t, k.DataRoot(), "arch/owner/index.md", "---\ntype: Note\ntitle: Owner\n---\n[report](report.csv)\n")
	if _, err := k.WriteAsset("arch/owner", "report.csv", []byte("a,b\n"), "", nil); err != nil {
		t.Fatalf("WriteAsset report: %v", err)
	}
	if _, err := k.WriteAsset("arch/owner", "evidence/screen.png", []byte{0, 1}, "", nil); err != nil {
		t.Fatalf("WriteAsset evidence: %v", err)
	}
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var orphans []Finding
	for _, finding := range findings {
		if finding.Check == "broken_link" {
			t.Fatalf("asset link became broken_link: %v", finding)
		}
		if finding.Check == "orphan_asset" {
			orphans = append(orphans, finding)
		}
	}
	if len(orphans) != 1 || orphans[0].Path != "arch/owner/evidence/screen.png" {
		t.Fatalf("orphan assets: %v", orphans)
	}
}

// --- declarative lint contracts (D107) ---

func TestRun_MissingRequiredFieldContract(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\nrequired_fields: [timestamp, provenance]\nrequired_fields.Runbook: [owner]\n---\n")
	writeFile(t, k.DataRoot(), "ops/a.md", "---\ntype: Runbook\ntimestamp: 2026-01-01\nprovenance: [, ]\nowner: Alice\n---\n")
	writeFile(t, k.DataRoot(), "ops/b.md", "---\ntype: Note\ntimestamp: 2026-01-01\n---\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var missing []Finding
	for _, f := range findings {
		if f.Check == "missing_required_field" {
			missing = append(missing, f)
		}
	}
	if len(missing) != 2 {
		t.Fatalf("required-field findings = %#v", missing)
	}
}

func TestRun_MissingRequiredFieldsWithoutFrontmatter(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\nrequired_fields: [timestamp, provenance]\n---\n")
	writeFile(t, k.DataRoot(), "ops/no-frontmatter.md", "plain body\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if countCheck(findings, "ops/no-frontmatter.md", "missing_required_field") != 2 {
		t.Fatalf("no-frontmatter findings = %#v", findings)
	}
}

func TestRun_ContractMalformedAndServicesSkipped(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\nrequired_fields: [timestamp, timestamp, ]\n---\n")
	writeFile(t, k.Root, "services/demo.md", "---\ntype: Service\n---\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	malformed := 0
	for _, f := range findings {
		if f.Check == "contract_malformed" {
			malformed++
		}
		if f.Check == "missing_required_field" && strings.HasPrefix(f.Path, "services/") {
			t.Fatalf("service matched a map contract: %#v", f)
		}
	}
	if malformed != 1 {
		t.Fatalf("contract_malformed = %d, findings=%#v", malformed, findings)
	}
}

func TestRun_EmptyPerTypeContractSuffixMalformedOnce(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\nrequired_fields.: [owner]\n---\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if countCheck(findings, "ops/_map.md", "contract_malformed") != 1 {
		t.Fatalf("empty type suffix findings = %#v", findings)
	}
}

func TestRun_IndexContract_MapAndExpanded(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\n---\n")
	writeFile(t, k.DataRoot(), "ops/index.md", "---\ntype: Index\n---\n[alpha](alpha.md)\n[[ops/owner]]\n[missing](missing.md)\n")
	writeFile(t, k.DataRoot(), "ops/alpha.md", "---\ntype: Note\n---\n")
	writeFile(t, k.DataRoot(), "ops/owner/index.md", "---\ntype: Note\n---\n")
	writeFile(t, k.DataRoot(), "ops/owner/child.md", "---\ntype: Note\n---\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(findings, "ops/index.md", "broken_link") || !hasCheck(findings, "ops/owner/index.md", "index_incomplete") {
		t.Fatalf("index contract findings = %#v", findings)
	}
	if hasCheck(findings, "ops/index.md", "index_incomplete") {
		t.Fatalf("map index should include alpha and expanded owner: %#v", findings)
	}
}

func TestRun_IndexContract_ScopedOnlyOwnMembership(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\n---\n")
	writeFile(t, k.DataRoot(), "ops/index.md", "---\ntype: Index\n---\n[[ops/a]]\n")
	writeFile(t, k.DataRoot(), "ops/a.md", "---\ntype: Note\n---\n")
	writeFile(t, k.DataRoot(), "ops/b.md", "---\ntype: Note\n---\n")
	findings, err := Run(k, "ops/a", false)
	if err != nil {
		t.Fatal(err)
	}
	if hasCheck(findings, "ops/index.md", "index_incomplete") {
		t.Fatalf("scoped lint reported unrelated sibling: %#v", findings)
	}
	findings, err = Run(k, "ops/b", false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(findings, "ops/index.md", "index_incomplete") {
		t.Fatalf("scoped lint missed own membership: %#v", findings)
	}
}

func TestRun_IndexContract_EmptyMapStillValidatesMapIndex(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, index string
		check       string
	}{
		{"dangling", "---\ntype: Index\n---\n[[ops/missing]]\n", "broken_link"},
		{"missing", "", "index_incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := tempKB(t)
			writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\n---\n")
			if tc.index != "" {
				writeFile(t, k.DataRoot(), "ops/index.md", tc.index)
			}
			findings, err := Run(k, "", false)
			if err != nil {
				t.Fatal(err)
			}
			if countCheck(findings, "ops/index.md", tc.check) != 1 {
				t.Fatalf("empty map findings = %#v", findings)
			}
			findings, err = Run(k, "ops", false)
			if err != nil || countCheck(findings, "ops/index.md", tc.check) != 1 {
				t.Fatalf("explicit map scope findings = %#v, %v", findings, err)
			}
		})
	}
}

func TestRun_IndexContract_ExpandedIndexBrokenLinkOnceAndScopedMapIsolation(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\n---\n")
	writeFile(t, k.DataRoot(), "ops/index.md", "---\ntype: Index\n---\n[[ops/missing-map-target]]\n")
	writeFile(t, k.DataRoot(), "ops/owner/index.md", "---\ntype: Note\n---\n[[ops/missing-owner-target]]\n[[ops/owner/child]]\n")
	writeFile(t, k.DataRoot(), "ops/owner/child.md", "---\ntype: Note\n---\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if countCheck(findings, "ops/owner.md", "broken_link") != 1 {
		t.Fatalf("expanded-index broken links = %#v", findings)
	}
	findings, err = Run(k, "ops/owner/child", false)
	if err != nil {
		t.Fatal(err)
	}
	if hasCheck(findings, "ops/index.md", "broken_link") || hasCheck(findings, "ops/index.md", "index_incomplete") {
		t.Fatalf("expanded scope leaked map-index findings: %#v", findings)
	}
}

func TestRun_MissingIndexIsOneFinding(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "on/_map.md", "---\ntype: Map\n---\n")
	writeFile(t, k.DataRoot(), "on/a.md", "---\ntype: Note\n---\n")
	writeFile(t, k.DataRoot(), "on/b.md", "---\ntype: Note\n---\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if countCheck(findings, "on/index.md", "index_incomplete") != 1 {
		t.Fatalf("a missing index must be one finding = %#v", findings)
	}
}

// --- expanded_ambiguous (D77 WP4) ---

func TestRun_ExpandedAmbiguous_Detected(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md",
		"---\ntype: Map\ntitle: Arch\nkind: map\n---\n")
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
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md",
		"---\ntype: Map\ntitle: Arch\nkind: map\n---\n")
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
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md",
		"---\ntype: Map\ntitle: Arch\nkind: map\n---\n")
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

func oversizeMap(t *testing.T, k *kb.KB, front string, top, satellites int) {
	t.Helper()
	writeFile(t, k.DataRoot(), "arch/_map.md", "---\ntype: Map\ntitle: Arch\n"+front+"---\n")
	for i := 0; i < top; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("arch/concept-%d.md", i), "---\ntype: Note\n---\nContent.\n")
	}
	if satellites > 0 {
		writeFile(t, k.DataRoot(), "arch/big/index.md", "---\ntype: Note\n---\nIndex.\n")
		for i := 0; i < satellites; i++ {
			writeFile(t, k.DataRoot(), fmt.Sprintf("arch/big/sat-%d.md", i), "---\ntype: Note\n---\nSat.\n")
		}
	}
}

func TestRun_MapOversize_Detected(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	oversizeMap(t, k, "kind: map\n", mapOversizeThreshold+1, 0)
	findings := observed(t, k)
	if !hasCheck(findings, "arch/_map.md", "map_oversize") {
		t.Errorf("expected map_oversize for arch, got: %v", findings)
	}
}

// Satellites of an expanded concept are its own content: only top-level
// concepts count (D313), checked by the segment after the map prefix, not by a
// naive prefix count.
func TestRun_MapOversize_SatellitesNotCounted(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	oversizeMap(t, k, "kind: map\n", 7, mapOversizeThreshold*3)
	findings := observed(t, k)
	if hasCheck(findings, "arch/_map.md", "map_oversize") {
		t.Errorf("satellites must not inflate map_oversize: %v", findings)
	}
}

// A journal grows by construction: it needs harvest, not a split.
func TestRun_MapOversize_JournalExempt(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	oversizeMap(t, k, "kind: journal\n", mapOversizeThreshold+10, 0)
	findings := observed(t, k)
	if hasCheck(findings, "arch/_map.md", "map_oversize") {
		t.Errorf("a journal never fires map_oversize: %v", findings)
	}
}

// --- concept_oversize (D78) ---

func TestRun_ConceptOversize_Detected(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	body := strings.Repeat("a", conceptOversizeThreshold+1)
	writeFile(t, k.DataRoot(), "arch/big-concept.md",
		"---\ntype: Note\n---\n"+body+"\n")

	findings := observed(t, k)
	if !hasCheck(findings, "arch/big-concept", "concept_oversize") {
		t.Errorf("expected concept_oversize for arch/big-concept, got: %v", findings)
	}
}

func TestRun_ConceptOversize_UnderThreshold_Clean(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/small-concept.md",
		"---\ntype: Note\n---\nContenuto breve.\n")

	findings := observed(t, k)
	if hasCheck(findings, "arch/small-concept", "concept_oversize") {
		t.Errorf("did not expect concept_oversize for a small concept, got: %v", findings)
	}
}

// --- legacy_archive_descriptor (D77 WP4) ---

func TestRun_LegacyArchiveDescriptor_Detected(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_archive.md",
		"---\ntype: Archive\ntitle: Arch\narchive_type: ops\n---\n")
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
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md",
		"---\ntype: Map\ntitle: Arch\nkind: map\n---\n")
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
		"---\ntype: Archive\ntitle: Alpha\narchive_type: ops\n---\n")
	writeFile(t, k.DataRoot(), "alpha/dossier/index.md",
		"---\ntype: Index\ntitle: Dossier\n---\n")
	writeFile(t, k.DataRoot(), "alpha/dossier/concept-a.md",
		"---\ntype: Note\nreview_after: 2020-01-01\n---\n")
	writeFile(t, k.DataRoot(), "beta/_archive.md",
		"---\ntype: Archive\ntitle: Beta\narchive_type: ops\n---\n")
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
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_archive.md",
		"---\ntype: Archive\ntitle: Arch\narchive_type: ops\n---\n")
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

// scope_neighbors used to call GraphNeighbors once per concept in scope, each
// call a full walk of the KB. One lint run is now one graph validation (D241).
func TestRun_ScopeNeighborsValidatesTheGraphOnce(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_archive.md",
		"---\ntype: Archive\ntitle: Arch\narchive_type: ops\n---\n")
	for i := 0; i < 50; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("arch/c%02d.md", i),
			fmt.Sprintf("---\ntype: Note\n---\nSee [next](c%02d.md) and [out](../ext/e%02d.md).\n", (i+1)%50, i))
		writeFile(t, k.DataRoot(), fmt.Sprintf("ext/e%02d.md", i), "---\ntype: Note\n---\nLeaf.\n")
	}
	before := k.GraphValidations()
	if _, err := Run(k, "arch", true); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Which neighbours are pulled in is TestRun_ScopeNeighbors' concern.
	if got := k.GraphValidations() - before; got != 1 {
		t.Fatalf("graph validations = %d, want 1", got)
	}
}

// --- D149: one link base, one concept resolver ---

// The natural form inside an expanded concept's index.md is a bare sibling
// filename, and it is the form that resolves correctly on disk and in any
// markdown viewer. lint used to resolve it against the ID's base and report a
// broken link; the graph resolved it against the file and was right. This test
// pins the direction of the alignment: making the graph use the ID base instead
// would make this KB stop rendering, and the satellite would go orphan.
func TestLint_ExpandedIndexRelativeLinkToSatellite(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n")
	writeFile(t, k.DataRoot(), "m/index.md", "---\ntype: Index\ntitle: M\n---\n- [c](c.md)\n")
	writeFile(t, k.DataRoot(), "m/c/index.md", "---\ntype: Note\ntitle: C\n---\n- [s](s.md)\n")
	writeFile(t, k.DataRoot(), "m/c/s.md", "---\ntype: Note\ntitle: S\n---\n# S\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Check == "broken_link" {
			t.Errorf("unexpected broken_link: %s — %s", f.Path, f.Message)
		}
		if f.Check == "orphan" && strings.Contains(f.Path, "m/c/s") {
			t.Errorf("satellite falsely orphaned: %s — %s", f.Path, f.Message)
		}
	}
}

// The mirror image, kept as an explicit test so the behaviour change cannot be
// reverted by accident: the previously-accepted form now resolves one level too
// deep, which is what it always did on disk.
func TestLint_ExpandedIndexPrefixedLinkIsNowBroken(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n")
	writeFile(t, k.DataRoot(), "m/index.md", "---\ntype: Index\ntitle: M\n---\n- [c](c.md)\n")
	writeFile(t, k.DataRoot(), "m/c/index.md", "---\ntype: Note\ntitle: C\n---\n- [s](c/s.md)\n")
	writeFile(t, k.DataRoot(), "m/c/s.md", "---\ntype: Note\ntitle: S\n---\n# S\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range findings {
		if f.Check == "broken_link" && strings.Contains(f.Message, "m/c/c/s.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("want a broken_link naming m/c/c/s.md, got %+v", findings)
	}
}

// lint's existence check now goes through the same resolver concept_read uses,
// so a link to the canonical ID of an expanded concept is not broken.
func TestLint_LinkToExpandedConceptCanonicalIDIsNotBroken(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n")
	writeFile(t, k.DataRoot(), "m/index.md", "---\ntype: Index\ntitle: M\n---\n- [a](a.md)\n- [c](c.md)\n")
	writeFile(t, k.DataRoot(), "m/a.md", "---\ntype: Note\ntitle: A\n---\n- [c](c.md)\n")
	writeFile(t, k.DataRoot(), "m/c/index.md", "---\ntype: Note\ntitle: C\n---\n# C\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Check == "broken_link" {
			t.Errorf("unexpected broken_link: %s — %s", f.Path, f.Message)
		}
	}
}

func TestLint_LinkToMissingConceptStillBroken(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n")
	writeFile(t, k.DataRoot(), "m/a.md", "---\ntype: Note\ntitle: A\n---\n- [gone](gone.md)\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range findings {
		if f.Check == "broken_link" && strings.Contains(f.Message, "m/gone.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("want broken_link to m/gone.md, got %+v", findings)
	}
}

// In a curated index an expanded concept may be linked either way. The
// index.md form is the inverse of the inter-concept one, and requiring exactly
// one of them produced 58 false index_incomplete findings in the field.
func TestLint_CuratedIndexAcceptsBothExpandedForms(t *testing.T) {
	t.Parallel()
	for _, link := range []string{"- [c](c.md)\n", "- [c](c/index.md)\n"} {
		k := tempKB(t)
		writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n")
		writeFile(t, k.DataRoot(), "m/index.md", "---\ntype: Index\ntitle: M\n---\n"+link)
		writeFile(t, k.DataRoot(), "m/c/index.md", "---\ntype: Note\ntitle: C\n---\n# C\n")

		findings, err := Run(k, "", false)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range findings {
			if f.Check == "index_incomplete" {
				t.Errorf("link %q: unexpected index_incomplete: %s — %s", strings.TrimSpace(link), f.Path, f.Message)
			}
		}
	}
}

// --- D158: secrets on a concept the service tools cannot see ---

func TestLint_SecretsOnNonServiceConcept(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		frontmat  string
		wantCheck bool
	}{
		{"lowercase service type is fine", "type: service\ntitle: S\nsecrets_source: secrets/x.sops.yaml\n", false},
		{"canonical Service type is fine", "type: Service\ntitle: S\nsecrets_source: secrets/x.sops.yaml\n", false},
		{"other type with secrets_source is flagged", "type: functional\ntitle: F\nsecrets_source: secrets/x.sops.yaml\n", true},
		{"other type with secret_refs is flagged", "type: functional\ntitle: F\nsecret_refs: [TOKEN=secrets/x.sops.yaml#/t]\n", true},
		{"other type with no secrets is fine", "type: functional\ntitle: F\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := tempKB(t)
			writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n")
			writeFile(t, k.DataRoot(), "m/c.md", "---\n"+tc.frontmat+"---\n# C\n")

			findings, err := Run(k, "", false)
			if err != nil {
				t.Fatal(err)
			}
			var got bool
			for _, f := range findings {
				if f.Check == "secrets_on_non_service" {
					got = true
				}
			}
			for _, f := range findings {
				if f.Check == "secrets_on_non_service" && (!strings.Contains(f.Message, "secret_resolve")) {
					t.Errorf("secrets_on_non_service must be info and name secret_resolve: %+v", f)
				}
			}
			if got != tc.wantCheck {
				t.Errorf("secrets_on_non_service = %v, want %v (findings %+v)", got, tc.wantCheck, findings)
			}
		})
	}
}

// --- D150: an extensionless asset can be cited ---

// Citing a Dockerfile produced a link to a nonexistent concept and left the
// asset orphan_asset forever, and renaming the file to satisfy the linter would
// have been worse than the finding.
func TestLint_ExtensionlessAssetCitationClearsOrphanAsset(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n")
	writeFile(t, k.DataRoot(), "m/index.md", "---\ntype: Index\ntitle: M\n---\n- [c](c.md)\n")
	writeFile(t, k.DataRoot(), "m/c/index.md", "---\ntype: Note\ntitle: C\n---\n- [Dockerfile](Dockerfile)\n")
	writeFile(t, k.DataRoot(), "m/c/Dockerfile", "FROM scratch\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Check == "orphan_asset" && strings.Contains(f.Path, "Dockerfile") {
			t.Errorf("the cited asset is still reported orphan: %s — %s", f.Path, f.Message)
		}
		if f.Check == "broken_link" {
			t.Errorf("unexpected broken_link: %s — %s", f.Path, f.Message)
		}
	}
}

// A concept documenting the Mermaid/POSIX collision must be writable without
// generating the findings it describes.
func TestLint_DocumentingCodeExamplesRaisesNoBrokenLink(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n")
	body := "---\ntype: Note\ntitle: FP\n---\n" +
		"```mermaid\nflowchart LR\n  N1[[\"a label\"]]\n```\n" +
		"```sh\ngrep -nE \"listen[[:space:]]\"\n```\n"
	writeFile(t, k.DataRoot(), "m/fp.md", body)

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Check == "broken_link" {
			t.Errorf("unexpected broken_link: %s — %s", f.Path, f.Message)
		}
	}
}

// concept_expand requires two segments, so for a satellite the old advice named
// an operation the product refuses.
func TestLint_OversizeRemedyIsDepthAware(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", 30001)
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n")
	writeFile(t, k.DataRoot(), "m/index.md", "---\ntype: Index\ntitle: M\n---\n- [c](c.md)\n")
	writeFile(t, k.DataRoot(), "m/c/index.md", "---\ntype: Note\ntitle: C\n---\n"+big+"\n- [s](s.md)\n")
	writeFile(t, k.DataRoot(), "m/c/s.md", "---\ntype: Note\ntitle: S\n---\n"+big+"\n")

	findings := observed(t, k)
	var forConcept, forSatellite string
	for _, f := range findings {
		if f.Check != "concept_oversize" {
			continue
		}
		switch f.Path {
		case "m/c":
			forConcept = f.Message
		case "m/c/s":
			forSatellite = f.Message
		}
	}
	if !strings.Contains(forConcept, "concept_expand") {
		t.Errorf("an expandable concept should still be told to expand: %q", forConcept)
	}
	// It may mention concept_expand only to say it does not apply.
	if strings.Contains(forSatellite, "consider concept_expand") {
		t.Errorf("a satellite must not be advised to concept_expand: %q", forSatellite)
	}
	if !strings.Contains(forSatellite, "concept_expand does not apply") {
		t.Errorf("a satellite should be told why the usual remedy is unavailable: %q", forSatellite)
	}
	if !strings.Contains(forSatellite, "sibling satellites") {
		t.Errorf("a satellite should be told what is actually available: %q", forSatellite)
	}
	// Both messages name the read bound as well as the split threshold.
	if !strings.Contains(forConcept, "60000") {
		t.Errorf("the message should name the read guard too: %q", forConcept)
	}
}

// The two thresholds are now related rather than independent numbers.
func TestOversizeThresholdIsDerivedFromTheReadGuard(t *testing.T) {
	t.Parallel()
	if conceptOversizeThreshold*2 != okf.ConceptReadSizeGuard {
		t.Errorf("conceptOversizeThreshold=%d, ConceptReadSizeGuard=%d: the two have drifted apart",
			conceptOversizeThreshold, okf.ConceptReadSizeGuard)
	}
}

// TestLint_MapIndexBrokenLink: a dead link in a map's index.md is reported,
// and so is the entry the index lacks: completeness is always on (D907).
func TestLint_MapIndexBrokenLink(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n")
	writeFile(t, k.DataRoot(), "m/index.md", "---\ntype: Index\ntitle: M\n---\n- [[m/live]]\n- [[m/moved-away]]\n")
	writeFile(t, k.DataRoot(), "m/live.md", "---\ntype: Note\ntitle: Live\n---\n# Live\n")
	writeFile(t, k.DataRoot(), "m/unlisted.md", "---\ntype: Note\ntitle: Unlisted\n---\n# Unlisted\n")
	writeFile(t, k.DataRoot(), "bare/_map.md", "---\ntype: Map\nkind: map\ntitle: Bare\n---\n# Bare\n")

	for _, scope := range []string{"", "m"} {
		findings, err := Run(k, scope, false)
		if err != nil {
			t.Fatal(err)
		}
		if !hasCheck(findings, "m/index.md", "broken_link") {
			t.Errorf("scope %q: expected broken_link on m/index.md, got %v", scope, findings)
		}
		for _, f := range findings {
			if f.Check == "broken_link" && f.Path == "m/index.md" && !strings.Contains(f.Message, "m/moved-away") {
				t.Errorf("scope %q: broken_link names the wrong target: %s", scope, f.Message)
			}
			if f.Check == "index_incomplete" && f.Path == "m/index.md" && !strings.Contains(f.Message, "m/unlisted") {
				t.Errorf("scope %q: index_incomplete names the wrong entry: %s", scope, f.Message)
			}
		}
		if !hasCheck(findings, "m/index.md", "index_incomplete") {
			t.Errorf("scope %q: expected index_incomplete for m/unlisted, got %v", scope, findings)
		}
	}
	findings, err := Run(k, "m/live", false)
	if err != nil {
		t.Fatal(err)
	}
	if hasCheck(findings, "m/index.md", "broken_link") {
		t.Errorf("a concept scope must not surface map-index findings: %v", findings)
	}
}

// A file that reached an expanded concept through git — above the size cap,
// or hidden — must not abort the whole lint run (D270).
func TestLint_AssetsThatUsedToAbortTheRun(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n")
	writeFile(t, k.DataRoot(), "m/index.md", "---\ntype: Index\ntitle: M\n---\n- [c](c.md)\n")
	writeFile(t, k.DataRoot(), "m/c/index.md", "---\ntype: Note\ntitle: C\n---\n[big](big.pdf)\n")
	writeFile(t, k.DataRoot(), "m/c/big.pdf", strings.Repeat("x", kb.AssetMaxFileSize+1))
	writeFile(t, k.DataRoot(), "m/c/.DS_Store", "x")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("lint.Run: %v", err)
	}
	if !hasCheck(findings, "m/c/big.pdf", "oversized_asset") {
		t.Errorf("want oversized_asset on m/c/big.pdf, got %+v", findings)
	}
	for _, f := range findings {
		if strings.Contains(f.Path, ".DS_Store") {
			t.Errorf("a hidden file is not an asset, got %+v", f)
		}
	}
}

func TestLint_UnlistableAssetsIsAFinding(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n")
	writeFile(t, k.DataRoot(), "m/index.md", "---\ntype: Index\ntitle: M\n---\n- [c](c.md)\n")
	writeFile(t, k.DataRoot(), "m/c/index.md", "---\ntype: Note\ntitle: C\n---\n# C\n")
	if err := os.Symlink("/etc/hosts", filepath.Join(k.DataRoot(), "m/c/link.txt")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("lint.Run: %v", err)
	}
	if !hasCheck(findings, "m/c", "unlistable_assets") {
		t.Errorf("want unlistable_assets on m/c, got %+v", findings)
	}
}

// --- field_values / forbidden_fields contract (D275) ---

func fieldContractKB(t *testing.T) *kb.KB {
	t.Helper()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: I\n"+
		"field_values.status: [active, draft, deprecated, superseded]\n"+
		"field_values.Incident.status: [open, closed]\n"+
		"forbidden_fields: [state]\n---\n# I\n")
	return k
}

func TestRun_FieldValuesContract(t *testing.T) {
	t.Parallel()
	k := fieldContractKB(t)
	writeFile(t, k.DataRoot(), "infra/ok.md", "---\ntype: Note\ntitle: OK\nstatus: active\n---\n")
	writeFile(t, k.DataRoot(), "infra/bad.md", "---\ntype: Note\ntitle: Bad\nstatus: deprecate\n---\n")
	writeFile(t, k.DataRoot(), "infra/list-bad.md", "---\ntype: Note\ntitle: L\nstatus: [active, nope]\n---\n")
	writeFile(t, k.DataRoot(), "infra/list-ok.md", "---\ntype: Note\ntitle: L\nstatus: [active, draft]\n---\n")
	writeFile(t, k.DataRoot(), "infra/absent.md", "---\ntype: Note\ntitle: A\n---\n")
	writeFile(t, k.DataRoot(), "infra/inc-ok.md", "---\ntype: Incident\ntitle: I\nstatus: open\n---\n")
	writeFile(t, k.DataRoot(), "infra/inc-bad.md", "---\ntype: Incident\ntitle: I\nstatus: active\n---\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{
		"infra/ok.md": 0, "infra/bad.md": 1, "infra/list-bad.md": 1, "infra/list-ok.md": 0,
		"infra/absent.md": 0, "infra/inc-ok.md": 0, "infra/inc-bad.md": 1,
	} {
		if got := countCheck(findings, path, "invalid_field_value"); got != want {
			t.Errorf("%s: invalid_field_value = %d, want %d", path, got, want)
		}
	}
	for _, f := range findings {
		if f.Path == "infra/bad.md" && f.Check == "invalid_field_value" {
			if !strings.Contains(f.Message, `field "status" has value "deprecate", allowed by map "infra" (all types): active, deprecated, draft, superseded`) {
				t.Errorf("finding = %#v", f)
			}
		}
	}
}

func TestRun_ForbiddenFieldContract(t *testing.T) {
	t.Parallel()
	k := fieldContractKB(t)
	writeFile(t, k.DataRoot(), "infra/legacy.md", "---\ntype: Note\ntitle: L\nstate: active\n---\n")
	writeFile(t, k.DataRoot(), "infra/clean.md", "---\ntype: Note\ntitle: C\n---\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if countCheck(findings, "infra/legacy.md", "forbidden_field") != 1 || countCheck(findings, "infra/clean.md", "forbidden_field") != 0 {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestLint_ExpandRebasesLinks_NoBrokenLink(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n- [c](c.md)\n- [other](other.md)\n")
	writeFile(t, k.DataRoot(), "m/other.md", "---\ntype: Note\ntitle: Other\n---\n# Other\n")
	// Write c.md with relative links before expanding.
	writeFile(t, k.DataRoot(), "m/c.md", "---\ntype: Note\ntitle: C\n---\nSee [other](other.md).\n")
	// Expand via the KB API — this should rebase the link.
	if err := k.ExpandConcept("m/c"); err != nil {
		t.Fatal(err)
	}
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Check == "broken_link" && strings.Contains(f.Path, "m/c") {
			t.Errorf("broken_link after expand: %s — %s", f.Path, f.Message)
		}
	}
}

func TestLint_BrokenLinkRebaseFix_ExpandedConcept(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n- [c](c/index.md)\n- [other](other.md)\n- [x](x.md)\n")
	writeFile(t, k.DataRoot(), "m/other.md", "---\ntype: Note\ntitle: Other\n---\n# Other\n")
	writeFile(t, k.DataRoot(), "m/x.md", "---\ntype: Note\ntitle: X\n---\n# X\n")
	// Simulate a concept expanded WITHOUT link rebasing (the old bug):
	// c/index.md still has links written for the old base "m/c.md".
	os.MkdirAll(filepath.Join(k.DataRoot(), "m", "c"), 0o755)
	writeFile(t, k.DataRoot(), "m/c/index.md", "---\ntype: Note\ntitle: C\n---\nSee [other](other.md) and [x](../m2/nowhere.md).\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var fixable, unfixable int
	for _, f := range findings {
		if f.Check != "broken_link" || f.Path != "m/c.md" {
			continue
		}
		if f.Fix != nil && f.Fix.Kind == FixRebaseLink {
			fixable++
			if f.Fix.Field != "other.md" || f.Fix.To != "../other.md" {
				t.Errorf("unexpected fix: %+v", f.Fix)
			}
		} else {
			unfixable++
		}
	}
	if fixable != 1 {
		t.Errorf("expected 1 fixable broken_link, got %d", fixable)
	}
	// The link to "../m2/nowhere.md" doesn't resolve from either base → no fix.
	if unfixable != 1 {
		t.Errorf("expected 1 unfixable broken_link, got %d", unfixable)
	}
}

func TestLint_DuplicateLinkFix_LinkOnlyItem(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n- [a](a.md)\n- [b](b.md)\n")
	writeFile(t, k.DataRoot(), "m/a.md", "---\ntype: Note\ntitle: A\n---\n# A\n")
	writeFile(t, k.DataRoot(), "m/b.md", "---\ntype: Note\ntitle: B\n---\n# B\n")
	// A concept with links in text AND in the links section.
	writeFile(t, k.DataRoot(), "m/page.md", "---\ntype: Note\ntitle: Page\n---\nUses [[m/a]] daily.\nAlso [[m/b]] — runtime dependency.\n\n## Collegamenti\n\n- [[m/a]]\n- [[m/b]] — runtime dependency\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var fixable, notFixable int
	for _, f := range findings {
		if f.Check != "duplicate_link" || f.Path != "m/page.md" {
			continue
		}
		if f.Fix != nil && f.Fix.Kind == FixDropLinkItem {
			fixable++
		} else {
			notFixable++
		}
	}
	// Both are fixable since D307: the text already links them, so the item
	// goes with its reason (the text says why).
	if fixable != 2 || notFixable != 0 {
		t.Errorf("expected 2 fixable duplicate_link, got %d fixable and %d not (findings: %+v)", fixable, notFixable, findings)
	}
}

// D313: conventional tool paths are never a machine_path finding.
func TestRun_MachinePath_BuiltinConventionalPaths_Clean(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n")
	writeFile(t, k.DataRoot(), "m/ok.md", "---\ntype: Note\ntitle: OK\n---\nSee ~/.ssh/config, ~/.kube/config, ~/.m2/settings.xml and ~/.config/sops/age.\n")
	writeFile(t, k.DataRoot(), "m/bad.md", "---\ntype: Note\ntitle: Bad\n---\nSee ~/Documents/work/secret.txt.\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if hasCheck(findings, "m/ok.md", "machine_path") {
		t.Errorf("conventional paths flagged: %+v", findings)
	}
	if !hasCheck(findings, "m/bad.md", "machine_path") {
		t.Errorf("a non-conventional home path must still fire: %+v", findings)
	}
	// CheckConcept (write responses) shares the allowlist.
	for _, f := range CheckConcept(k, "m/ok", "---\ntype: Note\ntitle: OK\n---\nSee ~/.ssh/config.\n") {
		if f.Check == "machine_path" {
			t.Errorf("CheckConcept flagged a conventional path: %+v", f)
		}
	}
}

func TestOrder_DeterministicOrder(t *testing.T) {
	t.Parallel()
	// Registry category order first (pages, templates, validity, links, ...),
	// then check, path and message; a name the registry does not know is last.
	in := []Finding{
		{Path: "b.md", Check: "orphan", Message: "m"},
		{Path: "b.md", Check: "broken_link", Message: "z"},
		{Path: "a.md", Check: "broken_link", Message: "z"},
		{Path: "a.md", Check: "broken_link", Message: "a"},
		{Path: "z.md", Check: "malformed_frontmatter", Message: "m"},
		{Path: "a.md", Check: "template_missing", Message: "m"},
		{Path: "a.md", Check: "unknown_check", Message: "m"},
	}
	want := []string{
		"z.md/malformed_frontmatter/m",
		"a.md/template_missing/m",
		"a.md/broken_link/a", "a.md/broken_link/z", "b.md/broken_link/z", "b.md/orphan/m",
		"a.md/unknown_check/m",
	}
	for round := 0; round < 3; round++ {
		shuffled := append([]Finding(nil), in...)
		for i := range shuffled { // a fixed permutation per round
			j := (i*7 + round*3) % len(shuffled)
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		}
		kept := Order(shuffled)
		if len(kept) != len(in) {
			t.Fatalf("round %d: %d findings, want %d", round, len(kept), len(in))
		}
		for i, f := range kept {
			if got := f.Path + "/" + f.Check + "/" + f.Message; got != want[i] {
				t.Fatalf("round %d pos %d: got %s want %s", round, i, got, want[i])
			}
		}
	}
	if got := CountsByCheck(in); got["broken_link"] != 3 || got["orphan"] != 1 || len(got) != 5 {
		t.Errorf("CountsByCheck = %v", got)
	}
}

func indexFormFindings(t *testing.T, k *kb.KB) []Finding {
	t.Helper()
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var out []Finding
	for _, f := range findings {
		if f.Check == "index_link_form" {
			out = append(out, f)
		}
	}
	return out
}

func seedIndexForm(t *testing.T, linker, exp string) *kb.KB {
	t.Helper()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md", "---\ntype: Map\nkind: map\ntitle: A\n---\n# A\n")
	writeFile(t, k.DataRoot(), "arch/exp/index.md", "---\ntype: Note\ntitle: Exp\n---\n"+exp)
	writeFile(t, k.DataRoot(), "arch/linker.md", "---\ntype: Note\ntitle: Linker\n---\n"+linker)
	return k
}

// D310: a link spelled <concept>/index gets the canonical spelling.
func TestLint_IndexLinkForm(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, linker, exp string
		path              string
		field, to         string
		kind              string
	}{
		{"markdown", "See [exp](exp/index.md).\n", "# Exp\n", "arch/linker.md", "exp/index.md", "exp.md", FixRebaseLink},
		{"wiki", "See [[arch/exp/index]].\n", "# Exp\n", "arch/linker.md", "arch/exp/index", "arch/exp", FixRewriteWikiLink},
		{"wiki in the expanded index", "# L\n", "See [[arch/exp/index]].\n", "arch/exp.md", "arch/exp/index", "arch/exp", FixRewriteWikiLink},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k := seedIndexForm(t, c.linker, c.exp)
			got := indexFormFindings(t, k)
			if len(got) != 1 {
				t.Fatalf("findings = %+v", got)
			}
			f := got[0]
			if f.Fix == nil || f.Fix.Kind != c.kind || f.Fix.Field != c.field || f.Fix.To != c.to {
				t.Errorf("finding = %+v fix = %+v", f, f.Fix)
			}
		})
	}
	t.Run("no concept behind it", func(t *testing.T) {
		k := seedIndexForm(t, "See [m](missing/index.md) and [[arch/missing/index]].\n", "# Exp\n")
		if got := indexFormFindings(t, k); len(got) != 0 {
			t.Errorf("findings = %+v", got)
		}
	})
}

// D310: a link to the concept itself is dropped, not rebased to index.md.
func TestLint_BrokenLinkSelfLinkIsDropped(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "catalog/_map.md", "---\ntype: Map\nkind: map\ntitle: C\n---\n# C\n")
	writeFile(t, k.DataRoot(), "catalog/proj/index.md", "---\ntype: Note\ntitle: P\n---\nSee [proj](proj/index.md).\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Check == "broken_link" {
			if f.Fix == nil || f.Fix.Kind != FixRebaseLink || f.Fix.Field != "proj/index.md" || f.Fix.To != "" {
				t.Errorf("fix = %+v", f.Fix)
			}
			return
		}
	}
	t.Fatal("no broken_link finding")
}

func TestRun_Orphan_ProvenanceCited_Clean(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_archive.md",
		"---\ntype: Archive\ntitle: Arch\narchive_type: ops\n---\n")
	writeFile(t, k.DataRoot(), "arch/dossier/index.md", "---\ntype: Index\ntitle: Dossier\n---\n")
	writeFile(t, k.DataRoot(), "arch/dossier/src.md", "---\ntype: Note\n---\nA source.\n")
	writeFile(t, k.DataRoot(), "arch/dossier/user.md",
		"---\ntype: Note\nprovenance:\n  - arch/dossier/src\n---\nCites it.\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hasCheck(findings, "arch/dossier/src.md", "orphan") {
		t.Errorf("provenance-cited concept flagged orphan: %v", findings)
	}
}
