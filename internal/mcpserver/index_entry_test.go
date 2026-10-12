package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// D714: index_entry on creation (concept_batch and concept_new).

func seedIndexMaps(t *testing.T, k *kb.KB, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		abs := filepath.Join(k.DataRoot(), rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func mapFiles(name, contract string) map[string]string {
	return map[string]string{
		name + "/_map.md":  "---\ntype: Map\nkind: map\ntitle: " + name + "\n" + contract + "---\n# " + name + "\n",
		name + "/index.md": "---\ntype: Index\ntitle: " + name + "\n---\n# " + name + "\n\n- [Old](old.md)\n",
	}
}

func readIdx(t *testing.T, k *kb.KB, name string) string {
	t.Helper()
	s, err := k.ReadIndex(name)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func batchWrite(id, title string, extra map[string]any) map[string]any {
	op := map[string]any{"op": "write", "id": id, "frontmatter": map[string]any{"type": "Note", "title": title}, "body": "# " + title + "\n"}
	for k, v := range extra {
		op[k] = v
	}
	return op
}

func runBatch(t *testing.T, s *Server, ops []map[string]any) (ToolResult, map[string]any) {
	t.Helper()
	tr := decodeToolResult(t, runMCPSequence(t, s, []string{initMsg, batchCallMsg(t, 2, ops)})[1])
	var out map[string]any
	if !tr.IsError {
		if err := json.Unmarshal([]byte(tr.Content[0].Text), &out); err != nil {
			t.Fatal(err)
		}
	}
	return tr, out
}

func TestConceptBatch_IndexEntry_AddsOrderedEntriesInOneCommit(t *testing.T) {
	k, _ := setupGitKB(t)
	k.AutoCommit = true
	seedIndexMaps(t, k, mapFiles("docs", "require_index_entry: true\n"))
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	before := commitCount(t, k)

	tr, out := runBatch(t, s, []map[string]any{
		batchWrite("docs/beta", "Beta", map[string]any{"index_entry": true}),
		batchWrite("docs/skipped", "Skipped", nil),
		batchWrite("docs/alpha", "Alpha", map[string]any{"index_entry": true}),
	})
	if tr.IsError {
		t.Fatalf("concept_batch: %+v", tr.Content)
	}
	idx := readIdx(t, k, "docs")
	want := "## New pages\n\n- [Beta](beta.md)\n- [Alpha](alpha.md)\n"
	if !strings.Contains(idx, want) || strings.Contains(idx, "skipped.md") {
		t.Fatalf("index:\n%s", idx)
	}
	if notes, _ := out["curated_indexes"].([]any); len(notes) != 2 {
		t.Fatalf("curated_indexes = %v", out["curated_indexes"])
	}
	if got := commitCount(t, k) - before; got != 1 {
		t.Fatalf("commits = %d, want 1", got)
	}
	log, _ := k.ReadRaw("log.md")
	if !strings.Contains(log, "- docs/beta (+ index entry)") || strings.Contains(log, "- docs/skipped (+") {
		t.Fatalf("log:\n%s", log)
	}
	// The indexed pages carry no index_incomplete; the skipped one does.
	for _, r := range out["results"].([]any) {
		entry := r.(map[string]any)
		hasIncomplete := strings.Contains(fmt.Sprint(entry["findings"]), "index_incomplete")
		if entry["id"] == "docs/skipped" && !hasIncomplete {
			t.Errorf("skipped page should report index_incomplete: %v", entry)
		}
		if entry["id"] != "docs/skipped" && hasIncomplete {
			t.Errorf("indexed page reports index_incomplete: %v", entry)
		}
	}
}

func TestConceptBatch_IndexEntry_CuratedMapWithoutRequireIndexEntry(t *testing.T) {
	k := setupTestKB(t)
	seedIndexMaps(t, k, mapFiles("plain", ""))
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	if tr, _ := runBatch(t, s, []map[string]any{batchWrite("plain/new", "New", map[string]any{"index_entry": true})}); tr.IsError {
		t.Fatalf("%+v", tr.Content)
	}
	if !strings.Contains(readIdx(t, k, "plain"), "- [New](new.md)") {
		t.Fatal("explicit index_entry must edit a curated index without require_index_entry")
	}
}

func TestConceptBatch_IndexEntry_RefusedShapesChangeNothing(t *testing.T) {
	k := setupTestKB(t)
	seedIndexMaps(t, k, mapFiles("docs", ""))
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	existing := readHash(t, k, "manutenzione/test-runbook")
	cases := []struct {
		name string
		op   map[string]any
		want string
	}{
		{"patch", map[string]any{"op": "patch", "id": "manutenzione/test-runbook", "if_match": existing, "old_string": "Il runbook", "new_string": "Un runbook", "index_entry": true}, "applies only to a creation"},
		{"update write", map[string]any{"op": "write", "id": "manutenzione/test-runbook", "if_match": existing, "frontmatter": map[string]any{"type": "Runbook"}, "body": "x", "index_entry": true}, "applies only to a creation"},
		{"top-level id", batchWrite("toplevel", "Top", map[string]any{"index_entry": true}), "needs a concept inside a map"},
	}
	preLog, _ := k.ReadRaw("log.md")
	preIdx := readIdx(t, k, "docs")
	for _, c := range cases {
		tr, _ := runBatch(t, s, []map[string]any{batchWrite("docs/ok", "Ok", map[string]any{"index_entry": true}), c.op})
		if !tr.IsError || !containsText(tr, c.want) {
			t.Fatalf("%s: %+v", c.name, tr.Content)
		}
	}
	postLog, _ := k.ReadRaw("log.md")
	if postLog != preLog || readIdx(t, k, "docs") != preIdx {
		t.Fatal("log or index changed on a refused batch")
	}
	if _, err := os.Stat(filepath.Join(k.DataRoot(), "docs", "ok.md")); !os.IsNotExist(err) {
		t.Fatal("concept written by a refused batch")
	}
}

func TestConceptBatch_IndexEntry_GeneratedAndAlreadyLinked(t *testing.T) {
	k := setupTestKB(t)
	seedIndexMaps(t, k, mapFiles("gen", "index: generated\n"))
	seedIndexMaps(t, k, mapFiles("docs", ""))
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	// "old" is linked by the seeded index already.
	tr, out := runBatch(t, s, []map[string]any{
		batchWrite("gen/new", "New", map[string]any{"index_entry": true}),
		batchWrite("docs/old", "Old", map[string]any{"index_entry": true}),
	})
	if tr.IsError {
		t.Fatalf("%+v", tr.Content)
	}
	notes := fmt.Sprint(out["curated_indexes"])
	if !strings.Contains(notes, "gen/index.md is generated: it lists gen/new already") || !strings.Contains(notes, "already links docs/old") {
		t.Fatalf("notes = %s", notes)
	}
	// gitWrap regenerates the block; the handler must not add its own heading.
	if strings.Contains(readIdx(t, k, "gen"), "New pages") {
		t.Fatalf("the handler edited a generated index:\n%s", readIdx(t, k, "gen"))
	}
	if strings.Count(readIdx(t, k, "docs"), "old.md") != 1 || strings.Contains(readIdx(t, k, "docs"), "New pages") {
		t.Fatalf("duplicate entry added:\n%s", readIdx(t, k, "docs"))
	}
}

func TestConceptBatch_IndexEntry_FailureRollsBackEverything(t *testing.T) {
	k := setupTestKB(t)
	seedIndexMaps(t, k, mapFiles("one", ""))
	seedIndexMaps(t, k, mapFiles("two", ""))
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	preLog, _ := k.ReadRaw("log.md")
	pre1, pre2 := readIdx(t, k, "one"), readIdx(t, k, "two")

	indexPatchHook = func(mapName string) error {
		if mapName == "two" {
			return errors.New("injected failure")
		}
		return nil
	}
	t.Cleanup(func() { indexPatchHook = nil })

	tr, _ := runBatch(t, s, []map[string]any{
		batchWrite("one/a", "A", map[string]any{"index_entry": true}),
		batchWrite("two/b", "B", map[string]any{"index_entry": true}),
	})
	if !tr.IsError || !containsText(tr, "injected failure") {
		t.Fatalf("want failure, got %+v", tr.Content)
	}
	postLog, _ := k.ReadRaw("log.md")
	if postLog != preLog || readIdx(t, k, "one") != pre1 || readIdx(t, k, "two") != pre2 {
		t.Fatal("log or an index changed after the rollback")
	}
	for _, rel := range []string{"one/a.md", "two/b.md"} {
		if _, err := os.Stat(filepath.Join(k.DataRoot(), rel)); !os.IsNotExist(err) {
			t.Fatalf("%s survived the rollback", rel)
		}
	}
}

func TestConceptNew_IndexEntry(t *testing.T) {
	k, _ := setupGitKB(t)
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "templates", "note.md"), []byte("---\ntype: Note\ntitle: {{title}}\n---\n# Details\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedIndexMaps(t, k, mapFiles("docs", "require_index_entry: true\n"))
	seedIndexMaps(t, k, mapFiles("gen", "index: generated\n"))
	k.AutoCommit = true
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	call := func(id string) ToolResult {
		return decodeToolResult(t, runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "concept_new", map[string]any{
			"template": "note", "id": id, "index_entry": true, "vars": map[string]string{"title": "Rendered Title"}})})[1])
	}

	before := commitCount(t, k)
	tr := call("docs/fresh")
	if tr.IsError {
		t.Fatalf("%+v", tr.Content)
	}
	if !strings.Contains(readIdx(t, k, "docs"), "## New pages\n\n- [Rendered Title](fresh.md)\n") {
		t.Fatalf("index:\n%s", readIdx(t, k, "docs"))
	}
	if !containsText(tr, "curated_indexes") || strings.Contains(tr.Content[0].Text, "index_incomplete") {
		t.Fatalf("result: %s", tr.Content[0].Text)
	}
	if got := commitCount(t, k) - before; got != 1 {
		t.Fatalf("commits = %d, want 1", got)
	}

	if tr = call("gen/fresh"); tr.IsError || !containsText(tr, "is generated: it lists gen/fresh already") {
		t.Fatalf("generated: %+v", tr.Content)
	}

	preLog, _ := k.ReadRaw("log.md")
	if tr = call("loose"); !tr.IsError || !containsText(tr, "needs a concept inside a map") {
		t.Fatalf("top-level: %+v", tr.Content)
	}
	if _, err := os.Stat(filepath.Join(k.DataRoot(), "loose.md")); !os.IsNotExist(err) {
		t.Fatal("top-level concept written")
	}
	if postLog, _ := k.ReadRaw("log.md"); postLog != preLog {
		t.Fatal("log changed on refusal")
	}
}
