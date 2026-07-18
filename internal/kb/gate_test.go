package kb

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// writeTestFile creates a file at root/rel with given content, creating dirs as needed.
func writeTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(abs), err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", abs, err)
	}
}

// --- CommitGate ---

func TestCommitGate_NoContradictions(t *testing.T) {
	dir := tempKB(t)
	k, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := k.CommitGate([]okf.ConceptID{"arch/concept-a"})
	if err != nil {
		t.Fatalf("CommitGate: %v", err)
	}
	if !result.Pass {
		t.Errorf("expected Pass=true with no contradictions, got blockers: %v", result.Blockers)
	}
}

func TestCommitGate_OpenContradiction_NotInvolving(t *testing.T) {
	dir := tempKB(t)
	k, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, k.DataRoot(), "conflicts/c1.md", `---
type: Contradiction
resolution_status: open
involves: [arch/concept-x, arch/concept-y]
contradiction_kind: conflict
reason: They disagree
---
Body.
`)

	result, err := k.CommitGate([]okf.ConceptID{"arch/unrelated"})
	if err != nil {
		t.Fatalf("CommitGate: %v", err)
	}
	if !result.Pass {
		t.Errorf("expected Pass=true when contradiction does not involve changed ID, blockers: %v", result.Blockers)
	}
}

func TestCommitGate_OpenContradiction_Involving(t *testing.T) {
	dir := tempKB(t)
	k, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, k.DataRoot(), "conflicts/c1.md", `---
type: Contradiction
resolution_status: open
involves: [arch/concept-a, arch/concept-b]
contradiction_kind: conflict
reason: Conflicting requirements
---
Body.
`)

	result, err := k.CommitGate([]okf.ConceptID{"arch/concept-a"})
	if err != nil {
		t.Fatalf("CommitGate: %v", err)
	}
	if result.Pass {
		t.Error("expected Pass=false when contradiction involves changed ID")
	}
	if len(result.Blockers) != 1 {
		t.Fatalf("expected 1 blocker, got %d: %v", len(result.Blockers), result.Blockers)
	}
	b := result.Blockers[0]
	if b.ConceptPath != "conflicts/c1.md" {
		t.Errorf("expected blocker path 'conflicts/c1.md', got %q", b.ConceptPath)
	}
	if b.Kind != "conflict" {
		t.Errorf("expected kind 'conflict', got %q", b.Kind)
	}
	if b.Reason != "Conflicting requirements" {
		t.Errorf("expected reason 'Conflicting requirements', got %q", b.Reason)
	}
	if len(b.Involves) != 2 {
		t.Errorf("expected 2 involves entries, got %d", len(b.Involves))
	}
}

func TestCommitGate_ResolvedContradiction_NoBlocker(t *testing.T) {
	dir := tempKB(t)
	k, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, k.DataRoot(), "conflicts/c1.md", `---
type: Contradiction
resolution_status: resolved
involves: [arch/concept-a, arch/concept-b]
contradiction_kind: conflict
reason: Was a problem
---
Body.
`)

	result, err := k.CommitGate([]okf.ConceptID{"arch/concept-a"})
	if err != nil {
		t.Fatalf("CommitGate: %v", err)
	}
	if !result.Pass {
		t.Errorf("expected Pass=true for resolved contradiction, blockers: %v", result.Blockers)
	}
}
