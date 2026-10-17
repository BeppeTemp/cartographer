package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// D853: the page source_register writes is not an unknown type, even in a KB
// that declares a type palette.
func TestSourceRegisterLeavesNoUnknownTypeFinding(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	tdir := filepath.Join(k.Root, "templates")
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tdir, "runbook.md"), []byte("---\ntype: Runbook\ntitle: Runbook\n---\n# {{title}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registerSource(t, s, `{"title":"Kickoff","source_kind":"transcript","sha256":"`+shaA+`","timestamp":"2026-03-04"}`)
	out := callOK(t, s, "lint", `{}`)
	if strings.Contains(out, "unknown_type") {
		t.Fatalf("lint reports unknown_type for a Source page: %s", out)
	}
}
