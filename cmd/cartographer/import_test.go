package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

func writeSourceFile(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(abs), err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", abs, err)
	}
}

func mustReadConcept(t *testing.T, kbDir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(kbDir, "data", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read concept %s: %v", rel, err)
	}
	return string(data)
}

// TestCmdImport_HappyPath imports a flat source directory into a single
// default map and verifies the written concept has synthesized
// frontmatter (title from filename, type/status defaults).
func TestCmdImport_HappyPath(t *testing.T) {
	src := t.TempDir()
	writeSourceFile(t, src, "my-note.md", "Just a note, no frontmatter.\n")

	kbDir := t.TempDir()
	if _, err := kb.Init(kbDir); err != nil {
		t.Fatalf("kb.Init: %v", err)
	}

	out := withStdout(t, func() {
		code := cmdImport([]string{"--source", src, "--kb", kbDir, "--default-map", "notes"})
		if code != 0 {
			t.Errorf("cmdImport = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "imported: 1, skipped: 0, errors: 0") {
		t.Errorf("unexpected summary: %q", out)
	}

	content := mustReadConcept(t, kbDir, "notes/my-note.md")
	if !strings.Contains(content, "status: imported") {
		t.Errorf("expected status: imported, got:\n%s", content)
	}
	if !strings.Contains(content, "type: Note") {
		t.Errorf("expected type: Note, got:\n%s", content)
	}
	if !strings.Contains(content, "title: my-note") {
		t.Errorf("expected fallback title, got:\n%s", content)
	}
	if !strings.Contains(content, "Just a note, no frontmatter.") {
		t.Errorf("expected body preserved, got:\n%s", content)
	}
}

// TestCmdImport_TitleFromH1 verifies the first H1 in the body wins over the
// filename fallback.
func TestCmdImport_TitleFromH1(t *testing.T) {
	src := t.TempDir()
	writeSourceFile(t, src, "raw.md", "# My Real Title\n\nBody text.\n")

	kbDir := t.TempDir()
	if _, err := kb.Init(kbDir); err != nil {
		t.Fatalf("kb.Init: %v", err)
	}

	withStdout(t, func() {
		if code := cmdImport([]string{"--source", src, "--kb", kbDir, "--default-map", "notes"}); code != 0 {
			t.Fatalf("cmdImport = %d, want 0", code)
		}
	})

	content := mustReadConcept(t, kbDir, "notes/raw.md")
	if !strings.Contains(content, "title: My Real Title") {
		t.Errorf("expected title from H1, got:\n%s", content)
	}
}

// TestCmdImport_DryRun verifies --dry-run prints the plan without writing
// anything to the KB.
func TestCmdImport_DryRun(t *testing.T) {
	src := t.TempDir()
	writeSourceFile(t, src, "a.md", "Content A.\n")

	kbDir := t.TempDir()
	if _, err := kb.Init(kbDir); err != nil {
		t.Fatalf("kb.Init: %v", err)
	}

	out := withStdout(t, func() {
		code := cmdImport([]string{"--source", src, "--kb", kbDir, "--default-map", "notes", "--dry-run"})
		if code != 0 {
			t.Errorf("cmdImport = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "a.md -> notes/a") {
		t.Errorf("expected plan line for a.md, got: %q", out)
	}
	if !strings.Contains(out, "would import: 1, skipped: 0") {
		t.Errorf("expected plan summary, got: %q", out)
	}

	if _, err := os.Stat(filepath.Join(kbDir, "data", "notes", "a.md")); !os.IsNotExist(err) {
		t.Errorf("dry-run must not write, but %v", err)
	}
}

// TestCmdImport_PreservesExistingFrontmatter verifies that an existing
// frontmatter field is left untouched, and only missing fields (status) are
// added.
func TestCmdImport_PreservesExistingFrontmatter(t *testing.T) {
	src := t.TempDir()
	writeSourceFile(t, src, "curated.md",
		"---\ntype: Runbook\ntitle: Already Curated\n---\nBody.\n")

	kbDir := t.TempDir()
	if _, err := kb.Init(kbDir); err != nil {
		t.Fatalf("kb.Init: %v", err)
	}

	withStdout(t, func() {
		if code := cmdImport([]string{"--source", src, "--kb", kbDir, "--default-map", "notes"}); code != 0 {
			t.Fatalf("cmdImport = %d, want 0", code)
		}
	})

	content := mustReadConcept(t, kbDir, "notes/curated.md")
	if !strings.Contains(content, "type: Runbook") {
		t.Errorf("expected existing type preserved, got:\n%s", content)
	}
	if !strings.Contains(content, "title: Already Curated") {
		t.Errorf("expected existing title preserved, got:\n%s", content)
	}
	if !strings.Contains(content, "status: imported") {
		t.Errorf("expected status: imported added, got:\n%s", content)
	}
}

// TestCmdImport_ExistingStatusPreserved verifies that a pre-existing status
// field (e.g. from a previous curation pass) is not overwritten with
// "imported".
func TestCmdImport_ExistingStatusPreserved(t *testing.T) {
	src := t.TempDir()
	writeSourceFile(t, src, "already-active.md",
		"---\ntype: Note\ntitle: T\nstatus: active\n---\nBody.\n")

	kbDir := t.TempDir()
	if _, err := kb.Init(kbDir); err != nil {
		t.Fatalf("kb.Init: %v", err)
	}

	withStdout(t, func() {
		if code := cmdImport([]string{"--source", src, "--kb", kbDir, "--default-map", "notes"}); code != 0 {
			t.Fatalf("cmdImport = %d, want 0", code)
		}
	})

	content := mustReadConcept(t, kbDir, "notes/already-active.md")
	if !strings.Contains(content, "status: active") {
		t.Errorf("expected status: active preserved, got:\n%s", content)
	}
}

// TestCmdImport_MapPerDirectory verifies --map routes a specific source
// subdirectory to its own destination map, distinct from the --default-map
// applied to everything else.
func TestCmdImport_MapPerDirectory(t *testing.T) {
	src := t.TempDir()
	writeSourceFile(t, src, "people/alice.md", "About Alice.\n")
	writeSourceFile(t, src, "misc.md", "Misc content.\n")

	kbDir := t.TempDir()
	if _, err := kb.Init(kbDir); err != nil {
		t.Fatalf("kb.Init: %v", err)
	}

	withStdout(t, func() {
		code := cmdImport([]string{
			"--source", src, "--kb", kbDir,
			"--default-map", "notes",
			"--map", "people=entities/people",
		})
		if code != 0 {
			t.Fatalf("cmdImport = %d, want 0", code)
		}
	})

	if _, err := os.Stat(filepath.Join(kbDir, "data", "entities", "people", "alice.md")); err != nil {
		t.Errorf("expected mapped destination entities/people/alice.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(kbDir, "data", "notes", "misc.md")); err != nil {
		t.Errorf("expected default-map destination notes/misc.md: %v", err)
	}
}

// TestCmdImport_UnmappedSourceWithoutMap_Errors verifies that a source
// directory with no --map entry and no --default-map produces an
// explicit error and writes nothing.
func TestCmdImport_UnmappedSourceWithoutMap_Errors(t *testing.T) {
	src := t.TempDir()
	writeSourceFile(t, src, "orphan/note.md", "Content.\n")

	kbDir := t.TempDir()
	if _, err := kb.Init(kbDir); err != nil {
		t.Fatalf("kb.Init: %v", err)
	}

	code := cmdImport([]string{"--source", src, "--kb", kbDir})
	if code != 2 {
		t.Errorf("cmdImport = %d, want 2 (error)", code)
	}

	entries, err := os.ReadDir(filepath.Join(kbDir, "data"))
	if err != nil {
		t.Fatalf("read data dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "index.md" && e.Name() != "log.md" {
			t.Errorf("expected no writes, found: %s", e.Name())
		}
	}
}

// TestCmdImport_LinkRewriting verifies that a relative markdown link between
// two imported files is rewritten to the new layout, while a wiki-link is
// left untouched.
func TestCmdImport_LinkRewriting(t *testing.T) {
	src := t.TempDir()
	writeSourceFile(t, src, "a.md", "See [b](b.md) and [[some-wiki-id]].\n")
	writeSourceFile(t, src, "b.md", "Target content.\n")

	kbDir := t.TempDir()
	if _, err := kb.Init(kbDir); err != nil {
		t.Fatalf("kb.Init: %v", err)
	}

	withStdout(t, func() {
		if code := cmdImport([]string{"--source", src, "--kb", kbDir, "--default-map", "notes"}); code != 0 {
			t.Fatalf("cmdImport = %d, want 0", code)
		}
	})

	content := mustReadConcept(t, kbDir, "notes/a.md")
	if !strings.Contains(content, "[b](b.md)") {
		t.Errorf("expected rewritten link to stay b.md (same directory), got:\n%s", content)
	}
	if !strings.Contains(content, "[[some-wiki-id]]") {
		t.Errorf("expected wiki-link left untouched, got:\n%s", content)
	}
}

// TestSlugify covers the filename -> concept-slug normalization.
func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"My Note":             "my-note",
		"already-kebab":       "already-kebab",
		"Weird!!Chars??":      "weird-chars",
		"":                    "concept",
		"___":                 "concept",
		"Mixed_Case-and.dots": "mixed-case-and-dots",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
