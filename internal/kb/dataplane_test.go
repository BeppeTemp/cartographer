package kb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// writeRaw writes a file under the data root, creating its directory.
func writeRaw(t *testing.T, k *KB, rel, content string) {
	t.Helper()
	abs := filepath.Join(k.DataRoot(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateMapContract_TitleOnMapWithoutIndex(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	if err := k.CreateMap("m", "Old", "map", nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(k.DataRoot(), "m", "index.md")); err != nil {
		t.Fatal(err)
	}
	title := "X"
	if _, err := k.UpdateMapContract("m", MapContractUpdate{Title: &title}); err != nil {
		t.Fatalf("title on a map with no index.md: %v", err)
	}
	if raw, _ := k.ReadRaw("m/_map.md"); !strings.Contains(raw, "title: X") {
		t.Errorf("_map.md not retitled:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(k.DataRoot(), "m", "index.md")); !os.IsNotExist(err) {
		t.Errorf("index.md must not be created: %v", err)
	}
}

func TestUpdateMapContract_FailingIndexStepLeavesMapUnchanged(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	if err := k.CreateMap("m", "Old", "map", nil); err != nil {
		t.Fatal(err)
	}
	// An index.md whose frontmatter does not parse refuses the retitle.
	writeRaw(t, k, "m/index.md", "---\ntype: Index\ntitle: [broken\n---\n# Old\n")
	before, _ := k.ReadRaw("m/_map.md")
	title := "New"
	if _, err := k.UpdateMapContract("m", MapContractUpdate{Title: &title}); err == nil {
		t.Fatal("a broken index.md must refuse the retitle")
	}
	if after, _ := k.ReadRaw("m/_map.md"); after != before {
		t.Errorf("_map.md changed although the index step failed:\n%s", after)
	}
}

func TestWriteConceptBatch_NewExpandedDirRollbackLeavesNothing(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	if err := k.CreateMap("entities", "Entities", "map", nil); err != nil {
		t.Fatal(err)
	}
	fm, _ := okf.ParseFrontmatter("type: Entity\ntitle: Foo")
	ops := []BatchWriteOp{{ID: "entities/den/foo", FM: fm, Body: "# Foo\n"}}
	_, err := k.WriteConceptBatch(ops, "concept_batch: 1 op(s)", func([]BatchWriteResult) error { return errors.New("late failure") })
	if err == nil || strings.Contains(err.Error(), RollbackFailedMarker) {
		t.Fatalf("err = %v, want the late failure without a rollback-failed marker", err)
	}
	if _, statErr := os.Stat(filepath.Join(k.DataRoot(), "entities", "den")); !os.IsNotExist(statErr) {
		t.Errorf("entities/den survived the rollback: %v", statErr)
	}
}

func TestGraphSnapshot_AmbiguousExpandedIsOneNode(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	if err := k.CreateMap("m", "M", "map", nil); err != nil {
		t.Fatal(err)
	}
	writeRaw(t, k, "m/c.md", "---\ntype: Note\ntitle: C\n---\nflat\n")
	writeRaw(t, k, "m/c/index.md", "---\ntype: Note\ntitle: C\n---\nexpanded\n")
	snap, err := k.GraphSnapshot(GraphSnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, node := range snap.Nodes {
		if node.ID == "m/c" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("nodes with id m/c = %d, want 1", n)
	}
	seen := map[okf.ConceptID]bool{}
	for _, node := range snap.Nodes {
		if seen[node.ID] {
			t.Errorf("duplicate node %s", node.ID)
		}
		seen[node.ID] = true
	}
	if snap.TotalNodes != len(snap.Nodes) {
		t.Errorf("TotalNodes = %d, Nodes = %d", snap.TotalNodes, len(snap.Nodes))
	}
}

func TestWalk_SkipsHiddenDirectories(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	if err := k.CreateMap("m", "M", "map", nil); err != nil {
		t.Fatal(err)
	}
	writeRaw(t, k, "m/real.md", "---\ntype: Note\ntitle: R\n---\nx\n")
	writeRaw(t, k, ".trash/old.md", "---\ntype: Note\ntitle: O\n---\nx\n")
	var ids []string
	if err := k.WalkConcepts(func(id okf.ConceptID, _ string) error { ids = append(ids, string(id)); return nil }); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if strings.HasPrefix(id, ".trash") {
			t.Errorf("walk ids include %s", id)
		}
	}
	if _, ok := ConceptIDOfPath("data/.trash/old.md"); ok {
		t.Error("ConceptIDOfPath maps a hidden directory")
	}
	// A hidden FILE is still a concept in the walk, so the mapping agrees.
	writeRaw(t, k, "m/.draft.md", "---\ntype: Note\ntitle: D\n---\nx\n")
	inWalk := false
	_ = k.WalkConcepts(func(id okf.ConceptID, _ string) error { inWalk = inWalk || id == "m/.draft"; return nil })
	if _, ok := ConceptIDOfPath("data/m/.draft.md"); ok != inWalk {
		t.Errorf("ConceptIDOfPath(.draft.md) = %v but the walk has it = %v", ok, inWalk)
	}
	files, err := k.listMDFiles(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasPrefix(f, ".trash") {
			t.Errorf("listMDFiles includes %s", f)
		}
	}
}

func TestWalk_UnreadableDirectoryIsSkipped(t *testing.T) {
	if os.Getuid() == 0 || os.PathSeparator == '\\' {
		t.Skip("permissions are not enforced here")
	}
	t.Parallel()
	k := newTestKB(t)
	if err := k.CreateMap("m", "M", "map", nil); err != nil {
		t.Fatal(err)
	}
	writeRaw(t, k, "m/real.md", "---\ntype: Note\ntitle: R\n---\nx\n")
	bad := filepath.Join(k.DataRoot(), "locked")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bad, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0o755) })
	if _, err := k.GraphSnapshot(GraphSnapshotOptions{}); err != nil {
		t.Errorf("graph with an unreadable directory: %v", err)
	}
	if _, err := k.ConceptSummaries(); err != nil {
		t.Errorf("ConceptSummaries: %v", err)
	}
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: W")
	if _, err := k.WriteConcept("m/w", fm, "x\n", ""); err != nil {
		t.Errorf("write: %v", err)
	}
}

func TestExpandConcept_CRLFRebasesLinks(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	if err := k.CreateMap("m", "M", "map", nil); err != nil {
		t.Fatal(err)
	}
	writeRaw(t, k, "m/a.md", "---\r\ntype: Note\r\ntitle: A\r\n---\r\nSee [b](b.md).\r\n")
	writeRaw(t, k, "m/b.md", "---\ntype: Note\ntitle: B\n---\nb\n")
	if err := k.ExpandConcept("m/a"); err != nil {
		t.Fatal(err)
	}
	got, err := k.ReadRaw("m/a/index.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "(../b.md)") || strings.Contains(got, "(b.md)") {
		t.Errorf("link not rebased:\n%q", got)
	}
	if !strings.Contains(got, "\r\n") {
		t.Errorf("line endings were not kept:\n%q", got)
	}
	if _, err := os.Stat(filepath.Join(k.DataRoot(), "m", "a.md")); !os.IsNotExist(err) {
		t.Errorf("a.md still exists: %v", err)
	}
}

func TestExpandConcept_FailureLeavesNoDirectoryAndRetrySucceeds(t *testing.T) {
	// Not parallel: it swaps package-level hooks.
	k := newTestKB(t)
	if err := k.CreateMap("m", "M", "map", nil); err != nil {
		t.Fatal(err)
	}
	writeRaw(t, k, "m/a.md", "---\ntype: Note\ntitle: A\n---\nSee [b](b.md).\n")
	boom := errors.New("injected")
	origW, origR := expandWriteFile, expandRemoveFile
	t.Cleanup(func() { expandWriteFile, expandRemoveFile = origW, origR })

	for name, inject := range map[string]func(){
		"write":  func() { expandWriteFile = func(string, []byte) error { return boom } },
		"remove": func() { expandRemoveFile = func(string) error { return boom } },
	} {
		expandWriteFile, expandRemoveFile = origW, origR
		inject()
		if err := k.ExpandConcept("m/a"); !errors.Is(err, boom) {
			t.Fatalf("%s: err = %v, want the injected failure", name, err)
		}
		if _, err := os.Stat(filepath.Join(k.DataRoot(), "m", "a")); !os.IsNotExist(err) {
			t.Errorf("%s: the directory survived: %v", name, err)
		}
		if raw, _ := k.ReadRaw("m/a.md"); !strings.Contains(raw, "(b.md)") {
			t.Errorf("%s: m/a.md was modified:\n%s", name, raw)
		}
	}
	expandWriteFile, expandRemoveFile = origW, origR
	if err := k.ExpandConcept("m/a"); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestAppendLog_CRLFKeepsHeaderFirst(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	writeRaw(t, k, "log.md", "# Log\r\n\r\n## 2026-01-01T00:00:00Z\r\n\r\nold\r\n\r\n")
	if err := k.AppendLog("new entry", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	got, _ := k.ReadRaw("log.md")
	if !strings.HasPrefix(got, "# Log\r\n") {
		t.Fatalf("header is not first:\n%q", got)
	}
	if i, j := strings.Index(got, "new entry"), strings.Index(got, "old"); i < 0 || j < 0 || i > j {
		t.Errorf("entry not on top:\n%q", got)
	}
	if strings.Count(got, "# Log") != 1 {
		t.Errorf("header duplicated:\n%q", got)
	}
}

func TestWriteConcept_PortableIDs(t *testing.T) {
	t.Parallel()
	k := newTestKB(t)
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: T")
	if err := k.CreateMap("notes", "Notes", "map", nil); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"Notes/foo":       "notes/foo", // existing parent in another case
		"Bad_Dir/foo":     "bad-dir",   // new parent that is not a slug
		"notes/aux":       "Windows device name",
		"notes/com1":      "Windows device name",
		"newmap/lpt9":     "Windows device name",
		"notes/sub/Upper": "",
	}
	for id, hint := range cases {
		_, err := k.WriteConcept(okf.ConceptID(id), fm, "x\n", "")
		if !errors.Is(err, okf.ErrInvalidPath) || !strings.Contains(err.Error(), hint) {
			t.Errorf("%s: err = %v, want ErrInvalidPath naming %q", id, err, hint)
		}
	}
	entries, _ := os.ReadDir(k.DataRoot())
	for _, e := range entries {
		if e.Name() == "Notes" || e.Name() == "Bad_Dir" {
			t.Errorf("%s was created", e.Name())
		}
	}
	// A legacy parent that exists exactly as spelled still takes writes, and
	// keepName (a move) still wins over the slug rule.
	if _, err := k.WriteConcept("notes/ok", fm, "x\n", ""); err != nil {
		t.Errorf("canonical id refused: %v", err)
	}
	writeRaw(t, k, "Legacy_Map/_map.md", "---\ntype: Map\ntitle: L\n---\n")
	if _, err := k.WriteConcept("Legacy_Map/x", fm, "x\n", ""); err != nil {
		t.Errorf("an existing legacy parent must stay writable: %v", err)
	}
}
