package mcpserver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// D1099: concept_write takes index_entry with the concept_new semantics, and
// the first page of an archive is accepted by the write gate.

// The map is not called "docs": the repodocs check fails the build on a cited
// docs/*.md path that does not exist.
func cwArgs(id, title, body string, extra map[string]any) string {
	m := map[string]any{"id": id, "frontmatter": map[string]any{"type": "Note", "title": title}, "body": body}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func TestConceptWrite_IndexEntry_AddsEntryInOneCommit(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	seedIndexMaps(t, k, mapFiles("pages", ""))
	forwardLinks(t, k, "pages/fresh", "pages/plain")
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	before := commitCount(t, k)

	text, isErr := callJSON(t, s, adminCtx, "concept_write", cwArgs("pages/fresh", "Fresh", "# Fresh\n", map[string]any{"index_entry": true}))
	if isErr {
		t.Fatalf("%s", text)
	}
	if !strings.Contains(readIdx(t, k, "pages"), "## New pages\n\n- [Fresh](fresh.md)\n") {
		t.Fatalf("index:\n%s", readIdx(t, k, "pages"))
	}
	if !strings.Contains(text, "curated_indexes") || strings.Contains(text, "index_incomplete") {
		t.Fatalf("result: %s", text)
	}
	if got := commitCount(t, k) - before; got != 1 {
		t.Fatalf("commits = %d, want 1", got)
	}
	if _, err := k.ReadConcept("pages/fresh"); err != nil {
		t.Fatal(err)
	}

	// Without index_entry the response has no curated_indexes key and the
	// gate asks for the entry (D907).
	preIdx := readIdx(t, k, "pages")
	text, isErr = callJSON(t, s, adminCtx, "concept_write", cwArgs("pages/plain", "Plain", "# Plain\n", nil))
	if !isErr || !strings.Contains(text, "index_incomplete") || readIdx(t, k, "pages") != preIdx {
		t.Fatalf("plain creating write: err=%v %s", isErr, text)
	}
}

func TestConceptWrite_IndexEntry_GeneratedAndAlreadyLinked(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	seedIndexMaps(t, k, mapFiles("gen", "index: generated\n"))
	seedIndexMaps(t, k, mapFiles("pages", ""))
	forwardLinks(t, k, "gen/new", "pages/old")
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	text, isErr := callJSON(t, s, adminCtx, "concept_write", cwArgs("gen/new", "New", "# New\n", map[string]any{"index_entry": true}))
	if isErr || !strings.Contains(text, "gen/index.md is generated: it lists gen/new already") {
		t.Fatalf("generated: %v %s", isErr, text)
	}
	if strings.Contains(readIdx(t, k, "gen"), "New pages") {
		t.Fatalf("generated index edited:\n%s", readIdx(t, k, "gen"))
	}
	// "old" is linked by the seeded curated index already.
	text, isErr = callJSON(t, s, adminCtx, "concept_write", cwArgs("pages/old", "Old", "# Old\n", map[string]any{"index_entry": true}))
	if isErr || !strings.Contains(text, "already links pages/old") {
		t.Fatalf("linked: %v %s", isErr, text)
	}
	if idx := readIdx(t, k, "pages"); strings.Count(idx, "old.md") != 1 || strings.Contains(idx, "New pages") {
		t.Fatalf("duplicate entry:\n%s", idx)
	}
}

func TestConceptWrite_IndexEntry_RefusedShapesChangeNothing(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	seedIndexMaps(t, k, mapFiles("pages", ""))
	// A map whose curated index cannot be read.
	seedIndexMaps(t, k, map[string]string{"broken/_map.md": "---\ntype: Map\nkind: map\ntitle: broken\n---\n"})
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	existing := readHash(t, k, "manutenzione/test-runbook")
	cases := []struct {
		name, args, want string
	}{
		{"existing id", cwArgs("manutenzione/test-runbook", "T", "x", map[string]any{"index_entry": true}), "applies only to a creation"},
		{"existing id with if_match", cwArgs("manutenzione/test-runbook", "T", "x", map[string]any{"index_entry": true, "if_match": existing}), "applies only to a creation"},
		{"missing id with if_match", cwArgs("pages/ghost", "G", "x", map[string]any{"index_entry": true, "if_match": "abc"}), "applies only to a creation"},
		{"top-level id", cwArgs("toplevel", "Top", "x", map[string]any{"index_entry": true}), "needs a concept inside a map"},
		{"unreadable index", cwArgs("broken/page", "P", "x", map[string]any{"index_entry": true}), "index.md unreadable"},
	}
	preLog, _ := k.ReadRaw("log.md")
	preIdx := readIdx(t, k, "pages")
	preBody, _ := os.ReadFile(filepath.Join(k.DataRoot(), "manutenzione", "test-runbook.md"))
	for _, c := range cases {
		text, isErr := callJSON(t, s, adminCtx, "concept_write", c.args)
		if !isErr || !strings.Contains(text, c.want) {
			t.Fatalf("%s: err=%v %s", c.name, isErr, text)
		}
	}
	postLog, _ := k.ReadRaw("log.md")
	postBody, _ := os.ReadFile(filepath.Join(k.DataRoot(), "manutenzione", "test-runbook.md"))
	if postLog != preLog || readIdx(t, k, "pages") != preIdx || string(postBody) != string(preBody) {
		t.Fatal("log, index or page changed on a refusal")
	}
	for _, rel := range []string{"pages/ghost.md", "toplevel.md", "broken/page.md"} {
		if _, err := os.Stat(filepath.Join(k.DataRoot(), rel)); !os.IsNotExist(err) {
			t.Fatalf("%s written by a refused call", rel)
		}
	}
}

// The index edit failing after the write is a note, not an error; the KB is
// not a git repository, so no gate rolls the write back.
func TestConceptWrite_IndexEntry_EditFailureIsANote(t *testing.T) {
	k := setupTestKB(t)
	if err := os.RemoveAll(filepath.Join(k.Root, ".git")); err != nil {
		t.Fatal(err)
	}
	seedIndexMaps(t, k, mapFiles("pages", ""))
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	indexPatchHook = func(string) error { return errors.New("injected failure") }
	t.Cleanup(func() { indexPatchHook = nil })
	text, isErr := callJSON(t, s, adminCtx, "concept_write", cwArgs("pages/fresh", "Fresh", "# Fresh\n", map[string]any{"index_entry": true}))
	if isErr || !strings.Contains(text, "injected failure") || !strings.Contains(text, "curated_indexes") {
		t.Fatalf("want a note, got err=%v %s", isErr, text)
	}
	if _, err := k.ReadConcept("pages/fresh"); err != nil {
		t.Fatalf("the concept must stay written: %v", err)
	}
}

func TestConceptWrite_SchemaAdvertisesIndexEntry(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	var tool *Tool
	for _, tl := range s.tools {
		if tl.Name == "concept_write" {
			tool = tl
		}
	}
	if tool == nil {
		t.Fatal("concept_write not registered")
	}
	var schema struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Properties["index_entry"].Type != "boolean" {
		t.Fatalf("index_entry not advertised as boolean: %+v", schema.Properties)
	}
}

// The first-write path end to end (D1099): a fresh git KB, a new map, then the
// first page with index_entry succeeds; a second page that links nothing is
// refused as orphan; the same call linking the first succeeds.
func TestWriteGate_FirstPageOfNewMapPasses_SecondMustLink(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	if text, isErr := callJSON(t, s, adminCtx, "map_create", `{"name":"hr","title":"HR"}`); isErr {
		t.Fatalf("map_create: %s", text)
	}
	before := commitCount(t, k)
	text, isErr := callJSON(t, s, adminCtx, "concept_write", cwArgs("hr/first", "First", "# First\n", map[string]any{"index_entry": true}))
	if isErr {
		t.Fatalf("the first page was refused: %s", text)
	}
	if got := commitCount(t, k) - before; got != 1 {
		t.Fatalf("commits = %d, want 1 (page and index entry)", got)
	}
	gateClean(t, k)
	assertNoFinding := func(check string) {
		t.Helper()
		out, _ := callJSON(t, s, adminCtx, "lint", `{}`)
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, `"`+check+`"`) && strings.Contains(line, "hr/") {
				t.Fatalf("lint reports %s: %s", check, out)
			}
		}
	}
	assertNoFinding("orphan")
	assertNoFinding("index_incomplete")

	head := gitOut(t, k, "rev-parse", "HEAD")
	text, isErr = callJSON(t, s, adminCtx, "concept_write", cwArgs("hr/second", "Second", "# Second\n", map[string]any{"index_entry": true}))
	refusedCleanly(t, k, head, text, isErr, "orphan")

	text, isErr = callJSON(t, s, adminCtx, "concept_write", cwArgs("hr/second", "Second", "# Second\n\nSee [First](first.md).\n", map[string]any{"index_entry": true}))
	if isErr {
		t.Fatalf("the linking second page was refused: %s", text)
	}
	assertNoFinding("orphan")
}

// #1273: a page that is both an orphan and missing from its map's curated index
// is one finding naming both, with the one fix; a page that is only missing
// from the index keeps its index_incomplete finding.
func TestConceptWrite_OrphanAndMissingIndexEntryIsOneFinding(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	if text, isErr := callJSON(t, s, adminCtx, "map_create", `{"name":"hr","title":"HR"}`); isErr {
		t.Fatalf("map_create: %s", text)
	}
	if text, isErr := callJSON(t, s, adminCtx, "concept_write", cwArgs("hr/first", "First", "# First\n", map[string]any{"index_entry": true})); isErr {
		t.Fatalf("first page: %s", text)
	}

	text, isErr := callJSON(t, s, adminCtx, "concept_write", cwArgs("hr/lonely", "Lonely", "# Lonely\n", nil))
	if !isErr || !strings.Contains(text, "orphan and index_incomplete") {
		t.Fatalf("lonely page: err=%v %s", isErr, text)
	}
	if strings.Count(text, `"path":"hr/lonely.md"`) != 1 || strings.Contains(text, "missing curated index entry") {
		t.Errorf("the page must be one finding, not two: %s", text)
	}

	text, isErr = callJSON(t, s, adminCtx, "concept_write", cwArgs("hr/linked", "Linked", "# Linked\n\nSee [First](first.md).\n", nil))
	if !isErr || !strings.Contains(text, "missing curated index entry") || strings.Contains(text, "orphan and index_incomplete") {
		t.Errorf("a linked page keeps its plain index_incomplete: err=%v %s", isErr, text)
	}
}
