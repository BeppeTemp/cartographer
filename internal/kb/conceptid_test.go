package kb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// #1280: the write path applies the slug rule the repair applies, refuses a
// case-only twin and a NUL byte, so a caller never holds an ID that changes.
func TestWriteConceptRefusesNonCanonicalNewIDs(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: T")

	refused := map[string]string{
		"notes/with space": "notes/with-space",
		"notes/.hid":       "notes/hid",
		"notes/a.b":        "notes/a-b",
		"notes/CON":        "notes/con",
		"notes/Upper":      "notes/upper",
		"notes/日本":         "no letter or digit",
	}
	for id, hint := range refused {
		_, err := k.WriteConcept(okf.ConceptID(id), fm, "x\n", "")
		if !errors.Is(err, okf.ErrInvalidPath) || !strings.Contains(err.Error(), hint) {
			t.Errorf("%s: err = %v, want ErrInvalidPath naming %q", id, err, hint)
		}
	}

	if _, err := k.WriteConcept("notes/ok", fm, "x\n", ""); err != nil {
		t.Fatalf("canonical id refused: %v", err)
	}
	if _, err := k.WriteConcept("notes/bad", fm, "x\x00y\n", ""); !errors.Is(err, okf.ErrInvalidConcept) {
		t.Errorf("NUL byte: err = %v, want ErrInvalidConcept", err)
	}
	if _, err := os.Stat(filepath.Join(k.DataRoot(), "notes", "bad.md")); err == nil {
		t.Error("a body with a NUL byte was written")
	}
}

func TestWriteConceptRefusesCaseOnlyTwin(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: T")
	// A legacy page whose name is not a slug, as a checkout can carry.
	if err := os.MkdirAll(filepath.Join(k.DataRoot(), "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "notes", "Upper.md"), []byte("---\ntype: Note\n---\nold\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := k.WriteConcept("notes/upper", fm, "new\n", "")
	if !errors.Is(err, okf.ErrInvalidPath) || !strings.Contains(err.Error(), "differs only in case") {
		t.Fatalf("err = %v, want a case-only collision", err)
	}
	got, _ := os.ReadFile(filepath.Join(k.DataRoot(), "notes", "Upper.md"))
	if !strings.Contains(string(got), "old") {
		t.Errorf("Upper.md was overwritten: %q", got)
	}
	// The existing page itself stays writable (a repair, a patch).
	if _, err := k.WriteConcept("notes/Upper", fm, "edited\n", ""); err != nil {
		t.Errorf("existing non-slug page refused: %v", err)
	}
}
