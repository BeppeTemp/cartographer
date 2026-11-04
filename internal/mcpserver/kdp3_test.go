package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// A title with a line break or a concept type that reads back as several
// types is refused before anything is written; a title with a colon
// round-trips.
func TestMapCreate_RefusesWhatTheDescriptorCannotHold(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	for _, args := range []string{
		`{"name":"bad1","title":"Ops\nindex: generated"}`,
		`{"name":"bad2","title":"Ops","concept_types":["A, B"]}`,
		`{"name":"bad3","title":"Ops","concept_types":["C]"]}`,
		`{"name":"bad4","title":"Ops","concept_types":[""]}`,
	} {
		if res := callTool(t, s, "map_create", args); !res.IsError {
			t.Errorf("map_create %s: want a refusal", args)
		}
	}
	for _, n := range []string{"bad1", "bad2", "bad3", "bad4"} {
		if _, err := os.Stat(filepath.Join(k.DataRoot(), n)); !os.IsNotExist(err) {
			t.Errorf("%s left behind: %v", n, err)
		}
	}

	if res := callTool(t, s, "map_create", `{"name":"good","title":"Ops: the --- plan, [draft]","concept_types":["Note"],"required_fields":{"*":["title"]}}`); res.IsError {
		t.Fatalf("map_create: %+v", res.Content)
	}
	meta, err := k.ReadArchiveMeta("good")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := meta.Get("title"); v != "Ops: the --- plan, [draft]" {
		t.Errorf("title = %q", v)
	}
	c, err := k.ReadMapContract("good")
	if err != nil || len(c.ConceptTypes) != 1 || c.ConceptTypes[0] != "Note" || len(c.RequiredFields) != 1 {
		t.Errorf("contract = %+v, %v", c, err)
	}
}

// A descriptor that exists and cannot be read refuses the writes it would
// judge; no descriptor at all is no constraint.
func TestUnreadableDescriptorRefusesWrites(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	files := satMapFiles("ok", "other")
	files["broken/_map.md"] = "---\ntype: Map\nkind: map\ntitle: broken\nconcept_types: [Note, \"Runbook\n---\n# broken\n"
	files["broken/index.md"] = "---\ntype: Index\ntitle: broken\n---\n# broken\n"
	files["ok/p.md"] = satPage("p\n")
	files["bare/q.md"] = satPage("q\n")
	satWriteFiles(t, k, files)

	if _, err := k.ReadArchiveMeta("broken"); err == nil {
		t.Fatal("fixture descriptor reads fine")
	}
	w := callTool(t, s, "concept_write", `{"id":"broken/new","frontmatter":{"type":"Note","title":"N"},"body":"x\n"}`)
	if !w.IsError || !strings.Contains(w.Content[0].Text, "broken") {
		t.Errorf("concept_write into a broken map: %+v", w)
	}
	b := callTool(t, s, "concept_batch", `{"operations":[{"op":"write","id":"broken/new2","frontmatter":{"type":"Note","title":"N"},"body":"x\n"}]}`)
	if !b.IsError {
		t.Errorf("concept_batch into a broken map accepted: %+v", b)
	}
	m := callTool(t, s, "concept_move", `{"source_id":"ok/p","target_id":"broken/p"}`)
	if !m.IsError {
		t.Errorf("concept_move into a broken map accepted: %+v", m)
	}
	if _, err := os.Stat(filepath.Join(k.DataRoot(), "ok", "p.md")); err != nil {
		t.Errorf("source moved: %v", err)
	}
	fm, _ := okf.ParseFrontmatter("type: Note")
	if err := transferTypeViolation(k, conceptMoveEntry{SourceID: "ok/p", TargetID: "broken/p"}, fm); err == nil {
		t.Error("transfer into a broken map accepted")
	}

	// No descriptor: no constraint.
	if err := introducedTypeViolation(k, "bare/new", fm); err != nil {
		t.Errorf("a map with no descriptor refused: %v", err)
	}
	if err := mapContractViolation(k, "bare/new", fm); err != nil {
		t.Errorf("a map with no descriptor refused: %v", err)
	}
}

// A scalar concept_types neither panics nor admits anything on the refusal text.
func TestAdmittedTypes_ShapesNeverPanic(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int{"concept_types: Note": 0, "concept_types: [A, B]": 2, "title: x": 0} {
		fm, err := okf.ParseFrontmatter(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := admittedTypes(fm); len(got) != want {
			t.Errorf("%q: admittedTypes = %v", in, got)
		}
	}
}

// A page citing a Source keeps citing it after the Source moves.
func TestConceptMove_RewritesProvenanceCitations(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	files := satMapFiles("m", "sources")
	files["sources/s1.md"] = "---\ntype: Source\ntitle: S1\n---\nsource\n"
	files["m/page.md"] = "---\ntype: Note\ntitle: Page\nprovenance: [sources/s1, other]\n---\nbody\n"
	satWriteFiles(t, k, files)

	if res := callTool(t, s, "concept_move", `{"source_id":"sources/s1","target_id":"sources/s2"}`); res.IsError {
		t.Fatalf("concept_move: %+v", res.Content)
	}
	d, err := k.ReadConcept("m/page")
	if err != nil {
		t.Fatal(err)
	}
	fm, _ := okf.ParseFrontmatter(d.FrontmatterRaw)
	got, _ := fm.Get("provenance")
	list, _ := got.([]string)
	if len(list) != 2 || list[0] != "sources/s2" || list[1] != "other" {
		t.Errorf("provenance = %v", got)
	}
}

// A resolved Contradiction is not resolved again without being reopened.
func TestConflictResolve_RefusesAResolvedContradiction(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	files := satMapFiles("gaps")
	files["gaps/c1.md"] = "---\ntype: Contradiction\ntitle: C1\nresolution_status: open\n---\nbody\n"
	satWriteFiles(t, k, files)

	if res := callTool(t, s, "conflict_resolve", `{"contradiction_id":"gaps/c1","resolution":"first"}`); res.IsError {
		t.Fatalf("first resolve: %+v", res.Content)
	}
	path := filepath.Join(k.DataRoot(), "gaps", "c1.md")
	before, _ := os.ReadFile(path)
	if res := callTool(t, s, "conflict_resolve", `{"contradiction_id":"gaps/c1","resolution":"second"}`); !res.IsError {
		t.Fatalf("second resolve accepted: %+v", res.Content)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Errorf("page changed:\n%s\n---\n%s", before, after)
	}
}
