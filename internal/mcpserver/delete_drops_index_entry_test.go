package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// #1764: a delete takes the line that lists the concept out of its curated
// index, in the same commit; a generated index is the server's and is left alone.

func deleteIndexKB(t *testing.T) (*kb.KB, *Server) {
	t.Helper()
	k, _ := setupGitKB(t)
	k.AuthName = "docs"
	files := mapFiles("docs", "")
	files["docs/index.md"] = "---\ntype: Index\ntitle: docs\n---\n# docs\n\n- [Gone](gone.md) - wrapped\n  description\n- [[docs/gone2]]\n- [Keep](keep.md)\n- [Narrow](narrow.md)\n- [Both](gone.md) and [Keep](keep.md)\n\n```\n- [Example](fenced.md)\n```\n"
	for _, n := range []string{"gone", "gone2", "keep", "fenced", "narrow"} {
		files["docs/"+n+".md"] = "---\ntype: Note\ntitle: " + n + "\n---\n# " + n + "\n"
	}
	seedIndexMaps(t, k, files)
	if _, err := k.CommitOpAs("test: seed", "", ""); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return k, s
}

func TestConceptDelete_DropsTheCuratedIndexEntry(t *testing.T) {
	t.Parallel()
	k, s := deleteIndexKB(t)
	before := commitCount(t, k)
	res := callTool(t, s, "concept_delete", `{"id":"docs/gone2"}`)
	if res.IsError {
		t.Fatalf("%+v", res.Content)
	}
	if !strings.Contains(res.Content[0].Text, "curated index docs/index.md: removed the entry for docs/gone2") {
		t.Fatalf("result = %s", res.Content[0].Text)
	}
	idx := readIdx(t, k, "docs")
	if strings.Contains(idx, "gone2") || !strings.Contains(idx, "[Keep](keep.md)") {
		t.Fatalf("index:\n%s", idx)
	}
	if commitCount(t, k) != before+1 {
		t.Fatalf("the delete and the index edit are not one commit")
	}
	// A line citing it beside another concept is prose that is not dropped: the
	// backstop refuses the delete, which changes nothing.
	res = callTool(t, s, "concept_delete", `{"id":"docs/gone"}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, `"path":"docs/index.md","check":"broken_link"`) {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(readIdx(t, k, "docs"), "wrapped") {
		t.Fatalf("index:\n%s", readIdx(t, k, "docs"))
	}
	if !strings.Contains(res.Content[0].Text, "edit it with index_patch, then delete") {
		t.Fatalf("the refusal names no fix: %s", res.Content[0].Text)
	}
}

// A fenced example in the index is no entry: the delete leaves it, and the
// backstop does not read it as a link either.
func TestConceptDelete_IgnoresAFencedExampleInTheIndex(t *testing.T) {
	t.Parallel()
	k, s := deleteIndexKB(t)
	res := callTool(t, s, "concept_delete", `{"id":"docs/fenced"}`)
	if res.IsError || strings.Contains(res.Content[0].Text, "curated index") {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(readIdx(t, k, "docs"), "- [Example](fenced.md)") {
		t.Fatalf("index:\n%s", readIdx(t, k, "docs"))
	}
}

func deleteAs(t *testing.T, s *Server, policy auth.Policy, id string) string {
	t.Helper()
	res, err := s.Tools()["concept_delete"].Handler(restrictedContext(policy), json.RawMessage(`{"id":"`+id+`"}`))
	if err != nil || res.IsError {
		t.Fatalf("%v %+v", err, res)
	}
	return res.Content[0].Text
}

// index_patch refuses a token narrowed by type; a delete does not rewrite the
// index on its behalf: the delete stands, the line stays, a note says so.
func TestConceptDelete_IndexNeedsTheMapGrant(t *testing.T) {
	t.Parallel()
	k, s := deleteIndexKB(t)
	typed := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"docs"}, Types: []string{"Note"}}}}
	out := deleteAs(t, s, typed, "docs/narrow")
	if !strings.Contains(out, "still lists docs/narrow, not edited") || !strings.Contains(readIdx(t, k, "docs"), "narrow.md") {
		t.Fatalf("result = %s\nindex:\n%s", out, readIdx(t, k, "docs"))
	}
	if strings.Contains(out, "alongside") {
		t.Fatalf("a line of the index is quoted: %s", out)
	}
	wide := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"docs"}}}}
	out = deleteAs(t, s, wide, "docs/gone2")
	if !strings.Contains(out, "removed the entry for docs/gone2") || strings.Contains(readIdx(t, k, "docs"), "gone2") {
		t.Fatalf("result = %s\nindex:\n%s", out, readIdx(t, k, "docs"))
	}
}

func TestConceptBatchDelete_DropsTheCuratedIndexEntry(t *testing.T) {
	t.Parallel()
	k, s := deleteIndexKB(t)
	tr, out := runBatch(t, s, []map[string]any{
		{"op": "delete", "id": "docs/gone2"},
	})
	if tr.IsError {
		t.Fatalf("%+v", tr.Content)
	}
	if !strings.Contains(strings.Join(anyStrings(out["curated_indexes"]), "\n"), "removed the entry for docs/gone2") {
		t.Fatalf("result = %v", out)
	}
	if strings.Contains(readIdx(t, k, "docs"), "gone2") {
		t.Fatalf("index:\n%s", readIdx(t, k, "docs"))
	}
}

func anyStrings(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, e := range l {
			out = append(out, e.(string))
		}
	}
	return out
}

func TestConceptDelete_LeavesAGeneratedIndexToTheServer(t *testing.T) {
	t.Parallel()
	k, s := generatedKB(t)
	if res := callTool(t, s, "concept_write", `{"id":"ga/one","frontmatter":{"type":"Note","title":"One"},"body":"x\n\n[[anchor]]\n"}`); res.IsError {
		t.Fatalf("%v", res.Content)
	}
	res := callTool(t, s, "concept_delete", `{"id":"ga/one"}`)
	if res.IsError || strings.Contains(res.Content[0].Text, "curated index") {
		t.Fatalf("%+v", res)
	}
	if strings.Contains(readIdx(t, k, "ga"), "ga/one") {
		t.Fatalf("index:\n%s", readIdx(t, k, "ga"))
	}
}
