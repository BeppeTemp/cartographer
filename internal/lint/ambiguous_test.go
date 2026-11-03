package lint

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// TestAmbiguousExpandedConcept_DirectFileWinsEverywhere pins #1503 WP5: with
// both arch/both.md and arch/both/index.md, Run, Work and the shared walk read
// the direct file, content and path together.
func TestAmbiguousExpandedConcept_DirectFileWinsEverywhere(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.Root, "data/arch/both.md", "---\ntype: Note\ntitle: Direct\n---\n# Direct\n\nSee [o](other.md).\n")
	writeFile(t, k.Root, "data/arch/both/index.md", "---\ntype: Note\ntitle: Expanded\n---\n# Expanded\n\nNothing.\n")
	writeFile(t, k.Root, "data/arch/other.md", "---\ntype: Note\ntitle: Other\n---\n# Other\n")

	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(findings, "broken_link"); len(got) != 0 {
		t.Errorf("false broken_link on the direct file's links: %v", got)
	}

	var gotPath, gotContent string
	if err := walkConceptsDirectWins(k, func(id okf.ConceptID, p, c string) error {
		if id == "arch/both" {
			gotPath, gotContent = p, c
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "arch/both.md" || !strings.Contains(gotContent, "Direct") {
		t.Errorf("walk served %q / %q", gotPath, gotContent)
	}

	entries, err := Work(k)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.ID == "arch/both" && e.Title != "Direct" {
			t.Errorf("Work read %q, want the direct file", e.Title)
		}
	}
}
