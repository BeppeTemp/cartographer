package docskb

import (
	"os"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// proseChecks are the lint checks about the words of the documentation, which
// this generator does not own: a documented example token or path is the docs'
// own choice. They are logged and never fail the build; every other finding is a
// defect of the generator or of a link in the docs, and fails it.
var proseChecks = map[string]bool{
	"machine_path":     true, // a client-local path written in a page
	"potential_secret": true, // a documented example token
	"open_marker":      true, // a TODO-like marker in a page
	"title_quality":    true, // a heading the quality heuristics dislike
}

// TestGeneratedKBIsClean generates the KB from the repository's own docs and
// lints it, so a docs change cannot break it: a link to a missing page or
// heading, a new placeholder, a page nothing links to. The fix is a generator
// rule, or the docs where the docs are wrong, never a special case for a page.
func TestGeneratedKBIsClean(t *testing.T) {
	files, err := Generate(os.DirFS("../.."), "main")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := WriteTree(files, dir); err != nil {
		t.Fatal(err)
	}
	k, err := kb.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := lint.Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	logged := map[string]int{}
	shown := map[string]int{}
	for _, f := range findings {
		if proseChecks[f.Check] {
			logged[f.Check]++
			continue
		}
		if shown[f.Check]++; shown[f.Check] <= 5 {
			t.Errorf("%s: %s: %s", f.Check, f.Path, strings.TrimSpace(f.Message))
		}
	}
	for c, n := range logged {
		t.Logf("%s: %d finding(s) about the docs' own prose, not failing", c, n)
	}
}
