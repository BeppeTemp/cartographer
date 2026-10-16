package testkbs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

var day = time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)

// A generated KB opens, has a history, and plants the findings the Health
// view is judged on: if a lint change stops one of them firing, the test KBs
// no longer show that state and this test says which.
func TestGeneratedKBShowsEveryPlantedState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := filepath.Join(t.TempDir(), "test-300")
	if err := Generate(root, Spec{Name: "test-300", Concepts: 300, Seed: 7, Conflicts: true}, day); err != nil {
		t.Fatal(err)
	}
	k, err := kb.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", root, "log", "--format=%s").Output()
	if err != nil {
		t.Fatal(err)
	}
	log := string(out)
	for _, subject := range []string{"concept_batch:", "concept_patch:", "concept_move:", "concept_delete:", "supersede:", "auto-repair (background):"} {
		if !strings.Contains(log, subject) {
			t.Errorf("no %q commit in the history", subject)
		}
	}
	findings, err := lint.Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	fired := map[string]bool{}
	for _, f := range findings {
		fired[f.Check] = true
	}
	for _, check := range []string{"broken_link", "island", "invalid_field_value", "missing_required_field", "forbidden_term",
		"closed_with_open_items", "contract_malformed", "unmapped_folder", "hook_invalid", "link_to_retired"} {
		if !fired[check] {
			t.Errorf("planted %s does not fire", check)
		}
	}
	for _, name := range []string{"lint-history.jsonl", "search-misses.jsonl", "usage.json", "conflicts.json"} {
		if _, err := os.Stat(filepath.Join(root, ".cartographer", name)); err != nil {
			t.Errorf("local state %s: %v", name, err)
		}
	}
}

// The bare KB has no git and no artifacts: the empty states.
func TestBareKBHasNoHistoryNorArtifacts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "test-30")
	if err := Generate(root, Spec{Name: "test-30", Concepts: 30, Seed: 1, Bare: true}, day); err != nil {
		t.Fatal(err)
	}
	if _, err := kb.Open(root); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".git", "skills", "services", "paths.yaml"} {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			t.Errorf("%s exists in the bare KB", p)
		}
	}
}

// The fleet is ten KBs, the largest at 5,000 concepts (#861).
func TestFleetScale(t *testing.T) {
	f := Fleet()
	if len(f) != 10 || f[0].Concepts != 5000 {
		t.Fatalf("fleet = %+v", f)
	}
}
