package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// archiveFixture lays out a live map "src" (index lines for mover and friend), a
// second live concept that links mover, and an "archive" map.
func archiveFixture(t *testing.T, srcContract, srcIndex string) (*kb.KB, *Server) {
	t.Helper()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	for rel, content := range map[string]string{
		"src/_map.md":      "---\ntype: Map\nkind: map\ntitle: Src\n" + srcContract + "---\n# Src\n",
		"src/index.md":     "---\ntype: Index\ntitle: Src\n---\n" + srcIndex,
		"src/friend.md":    "---\ntype: Note\ntitle: Friend\n---\nSee [mover](mover.md).\n",
		"src/mover.md":     "---\ntype: Note\ntitle: Mover\nstatus: active\n---\nBody [friend](friend.md).\n",
		"archive/_map.md":  "---\ntype: Map\nkind: map\ntitle: Archive\n---\n# Archive\n",
		"archive/index.md": "---\ntype: Index\ntitle: Archive\n---\n# Archive\n",
	} {
		abs := filepath.Join(k.DataRoot(), rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return k, s
}

func hashOf(t *testing.T, k *kb.KB, id string) string {
	t.Helper()
	d, err := k.ReadConcept(okf.ConceptID(id))
	if err != nil {
		t.Fatal(err)
	}
	return d.ContentHash
}

func archiveArgs(t *testing.T, k *kb.KB, extra string) string {
	return `{"id":"src/mover","if_match":"` + hashOf(t, k, "src/mover") + `"` + extra + `}`
}

// The reproduction of the plan issue: a map that never declared
// require_index_entry still loses the entry, and nothing is left behind.
func TestConceptArchive_RemovesTheEntryWithoutRequireIndexEntry(t *testing.T) {
	k, s := archiveFixture(t, "", "- [mover](mover.md)\n- [friend](friend.md)\n")
	res := callTool(t, s, "concept_archive", archiveArgs(t, k, `,"reason":"replaced"`))
	if res.IsError {
		t.Fatalf("concept_archive: %+v", res.Content)
	}
	d, err := k.ReadConcept("archive/mover")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.FrontmatterRaw, "status: deprecated") || !strings.Contains(d.FrontmatterRaw, "title: Mover") {
		t.Errorf("status not written or frontmatter lost:\n%s", d.FrontmatterRaw)
	}
	if _, err := k.ReadConcept("src/mover"); err == nil {
		t.Error("source still readable")
	}
	idx, _ := k.ReadIndex("src")
	if strings.Contains(idx, "mover") || !strings.Contains(idx, "friend.md") {
		t.Errorf("source index wrong:\n%s", idx)
	}
	archIdx, _ := k.ReadIndex("archive")
	if strings.Contains(archIdx, "mover") {
		t.Errorf("destination without require_index_entry must not get an entry:\n%s", archIdx)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if string(out["status"]) != `"deprecated"` {
		t.Errorf("status in result = %s", out["status"])
	}
	friend, _ := k.ReadConcept("src/friend")
	if !strings.Contains(friend.Body, "archive/mover.md") {
		t.Errorf("inbound link not rewritten:\n%s", friend.Body)
	}
	findings, err := lint.Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Check == "broken_link" || f.Check == "index_lists_retired" {
			t.Errorf("lint after archive: %s %s: %s", f.Check, f.Path, f.Message)
		}
	}
	log, _ := os.ReadFile(filepath.Join(k.DataRoot(), "log.md"))
	if !strings.Contains(string(log), "concept_archive: src/mover -> archive/mover (deprecated, replaced)") {
		t.Errorf("log entry missing:\n%s", log)
	}
}

// The trap: concept_move keeps its conservatism on the same fixture.
func TestConceptMove_StillLeavesTheEntryWithoutRequireIndexEntry(t *testing.T) {
	k, s := archiveFixture(t, "", "- [mover](mover.md)\n")
	if res := callTool(t, s, "concept_move", `{"source_id":"src/mover","target_id":"archive/mover"}`); res.IsError {
		t.Fatalf("concept_move: %+v", res.Content)
	}
	idx, _ := k.ReadIndex("src")
	if !strings.Contains(idx, "mover") {
		t.Errorf("concept_move must not edit an index nobody declared curated:\n%s", idx)
	}
}

func TestConceptArchive_KeepsAMultiLinkLineAndReportsIt(t *testing.T) {
	k, s := archiveFixture(t, "", "- [mover](mover.md) and [friend](friend.md)\n")
	res := callTool(t, s, "concept_archive", archiveArgs(t, k, ""))
	if res.IsError {
		t.Fatalf("concept_archive: %+v", res.Content)
	}
	idx, _ := k.ReadIndex("src")
	if !strings.Contains(idx, "friend.md") {
		t.Errorf("the line must be kept:\n%s", idx)
	}
	if !strings.Contains(res.Content[0].Text, "edit it by hand") {
		t.Errorf("result should report the kept line: %s", res.Content[0].Text)
	}
}

func TestConceptArchive_LeavesAGeneratedIndexAlone(t *testing.T) {
	k, s := archiveFixture(t, "index: generated\n", "- [mover](mover.md)\n")
	before, _ := k.ReadIndex("src")
	res := callTool(t, s, "concept_archive", archiveArgs(t, k, ""))
	if res.IsError {
		t.Fatalf("concept_archive: %+v", res.Content)
	}
	if strings.Contains(res.Content[0].Text, "removed the entry") {
		t.Errorf("a generated index is the server's, got: %s", res.Content[0].Text)
	}
	_ = before
}

func TestConceptArchive_AddsTheDestinationEntryWhenOptedIn(t *testing.T) {
	k, s := archiveFixture(t, "", "- [mover](mover.md)\n")
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "archive/_map.md"),
		[]byte("---\ntype: Map\nkind: map\ntitle: Archive\nrequire_index_entry: true\n---\n# Archive\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := archiveArgs(t, k, `,"status":"archived"`)
	if res := callTool(t, s, "concept_archive", args); res.IsError {
		t.Fatalf("concept_archive: %+v", res.Content)
	}
	archIdx, _ := k.ReadIndex("archive")
	if !strings.Contains(archIdx, "mover.md") {
		t.Errorf("destination entry missing:\n%s", archIdx)
	}
	d, _ := k.ReadConcept("archive/mover")
	if !strings.Contains(d.FrontmatterRaw, "status: archived") {
		t.Errorf("status:\n%s", d.FrontmatterRaw)
	}
}

func TestConceptArchive_Refusals(t *testing.T) {
	k, s := archiveFixture(t, "", "- [mover](mover.md)\n")
	good := hashOf(t, k, "src/mover")
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "archive/mover.md"), []byte("---\ntype: Note\ntitle: Old\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, args, want string }{
		{"no id", `{"if_match":"x"}`, "'id' is required"},
		{"no if_match", `{"id":"src/mover"}`, "'if_match' is required"},
		{"unknown to", `{"id":"src/mover","if_match":"` + good + `","to":"nope"}`, "unknown map"},
		{"to with slash", `{"id":"src/mover","if_match":"` + good + `","to":"a/b"}`, "one map name"},
		{"bad status", `{"id":"src/mover","if_match":"` + good + `","status":"gone"}`, "status must be"},
		{"already in to", `{"id":"src/friend","if_match":"x","to":"src"}`, "already in src"},
		{"stale", `{"id":"src/mover","if_match":"deadbeef"}`, "stale_write:"},
		{"target exists", `{"id":"src/mover","if_match":"` + good + `"}`, "conflict: target already exists: archive/mover"},
	}
	for _, c := range cases {
		res := callTool(t, s, "concept_archive", c.args)
		if !res.IsError || !strings.Contains(res.Content[0].Text, c.want) {
			t.Errorf("%s: got %+v, want error containing %q", c.name, res.Content, c.want)
		}
	}
	// Nothing was written by any refusal.
	d, err := k.ReadConcept("src/mover")
	if err != nil || !strings.Contains(d.FrontmatterRaw, "status: active") {
		t.Errorf("a refusal wrote to the source: %v %v", err, d)
	}
}

func TestConceptArchive_IsAdvancedAndNotReadOnly(t *testing.T) {
	_, s := archiveFixture(t, "", "")
	tool, ok := s.Tools()["concept_archive"]
	if !ok {
		t.Fatal("not registered")
	}
	if !advancedToolNames["concept_archive"] || tool.ReadOnly {
		t.Errorf("advanced=%v readonly=%v", advancedToolNames["concept_archive"], tool.ReadOnly)
	}
	if len(tool.Description) > 600 || strings.Contains(tool.Description, "D3") {
		t.Errorf("description over the budget or citing a decision (%d chars)", len(tool.Description))
	}
}

// A late filesystem failure after the status write: explicit error, nothing
// committed (the concept_move contract).
func TestConceptArchive_LateFailureIsAnErrorNamingBothIDs(t *testing.T) {
	k, s := archiveFixture(t, "", "- [mover](mover.md)\n")
	orig := conceptMoveRemove
	t.Cleanup(func() { conceptMoveRemove = orig })
	conceptMoveRemove = func(string) error { return os.ErrPermission }
	res := callTool(t, s, "concept_archive", archiveArgs(t, k, ""))
	if !res.IsError || !strings.Contains(res.Content[0].Text, "src/mover") || !strings.Contains(res.Content[0].Text, "archive/mover") {
		t.Errorf("got %+v", res.Content)
	}
}
