package synthesis_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/worker/synthesis"
)

// store is a tiny in-memory KB behind the fake: the D11 test compares it
// byte for byte before and after a hostile run.
func store(pages map[string]map[string]any) *fake {
	f := &fake{handler: map[string]func(map[string]any) client.ToolOutput{}}
	f.handler["concept_read"] = func(a map[string]any) client.ToolOutput {
		p, ok := pages[a["id"].(string)]
		if !ok {
			return client.ToolOutput{IsError: true, Text: "not found"}
		}
		return js(p)
	}
	for _, w := range []string{"concept_write", "concept_patch", "concept_delete", "concept_merge", "artifact_write"} {
		w := w
		f.handler[w] = func(a map[string]any) client.ToolOutput {
			id, _ := a["id"].(string)
			if w != "concept_patch" { // a patch is a no-op: the page keeps its shape for the next call
				pages[id] = map[string]any{"written_by": w}
			}
			return client.ToolOutput{Text: "ok"}
		}
	}
	f.handler["search"] = func(map[string]any) client.ToolOutput { return js(map[string]any{"results": []any{}}) }
	return f
}

func good(h synthesis.Hypothesis) map[string]any {
	return map[string]any{"id": h.ID(), "frontmatter": h.Frontmatter(), "body": h.Body()}
}

func TestGuard_RefusesEverythingOutsideTheAllowlist(t *testing.T) {
	h := hyp()
	active := map[string]any{"frontmatter": map[string]any{"type": "Note", "status": "active"}, "body": "Claim.", "content_hash": "h1"}
	synthOpen := map[string]any{"frontmatter": map[string]any{"type": "Contradiction", "origin": "synthesis", "resolution_status": "open"}, "body": "T\n\n## Evidence\n\n- [[arch/a]]: a calls b\n\n## How to confirm or reject\n\nx\n", "content_hash": "h2"}
	pages := map[string]map[string]any{"arch/a": active, "gaps/mine": synthOpen}
	before, _ := json.Marshal(pages)
	f := store(pages)
	g := &synthesis.Guard{Inner: f, Target: h.ID()}

	badKind := good(h)
	badKind["frontmatter"].(map[string]any)["contradiction_kind"] = "factual"
	closing := good(h)
	closing["frontmatter"].(map[string]any)["resolution"] = "done"
	refused := []struct {
		name string
		tool string
		args any
	}{
		{"overwrite an active page", "concept_write", map[string]any{"id": "arch/a", "frontmatter": map[string]any{"type": "Note"}, "body": "x"}},
		{"another id", "concept_write", map[string]any{"id": "gaps/other", "frontmatter": h.Frontmatter(), "body": "x"}},
		{"blocking kind", "concept_write", badKind},
		{"closing field", "concept_write", closing},
		{"patch an active page", "concept_patch", map[string]any{"id": "arch/a", "if_match": "h1", "old_string": "Claim.", "new_string": "Other."}},
		{"close a hypothesis", "concept_patch", map[string]any{"id": "gaps/mine", "if_match": "h2", "frontmatter": map[string]any{"resolution_status": "resolved"}}},
		{"change the kind", "concept_patch", map[string]any{"id": "gaps/mine", "if_match": "h2", "frontmatter": map[string]any{"contradiction_kind": "factual"}}},
		{"patch without if_match", "concept_patch", map[string]any{"id": "gaps/mine", "frontmatter": map[string]any{"confidence": "low"}}},
		{"edit outside Evidence", "concept_patch", map[string]any{"id": "gaps/mine", "if_match": "h2", "old_string": "T\n", "new_string": "U\n"}},
		{"replace_all", "concept_patch", map[string]any{"id": "gaps/mine", "if_match": "h2", "old_string": "a calls b", "new_string": "x", "replace_all": true}},
		{"delete", "concept_delete", map[string]any{"id": "arch/a"}},
		{"merge", "concept_merge", map[string]any{"id": "arch/a"}},
		{"artifact", "artifact_write", map[string]any{"id": "arch/a"}},
		{"unknown tool", "repair_revert", map[string]any{}},
		{"search recording a miss", "search", map[string]any{"query": "x", "record_miss": true}},
	}
	for _, c := range refused {
		out, err := g.CallTool(c.tool, c.args)
		if err != nil || !out.IsError || !strings.Contains(out.Text, "synthesis creates new gaps only") {
			t.Errorf("%s: not refused: %+v %v", c.name, out, err)
		}
	}
	if g.Refused != len(refused) {
		t.Errorf("refused %d of %d", g.Refused, len(refused))
	}
	after, _ := json.Marshal(pages)
	if string(before) != string(after) {
		t.Fatalf("D11: the store changed\nbefore %s\nafter  %s", before, after)
	}
	for _, w := range []string{"concept_write", "concept_patch", "concept_delete", "concept_merge", "artifact_write"} {
		if f.count(w) != 0 {
			t.Errorf("%s reached the server", w)
		}
	}
}

func TestGuard_AllowsTheTwoWrites(t *testing.T) {
	h := hyp()
	synthOpen := map[string]any{"frontmatter": map[string]any{"type": "Contradiction", "origin": "synthesis", "resolution_status": "open"}, "body": "T\n\n## Evidence\n\n- [[arch/a]]: a calls b\n\n## How to confirm or reject\n\nx\n", "content_hash": "h2"}
	pages := map[string]map[string]any{"gaps/mine": synthOpen}
	f := store(pages)
	g := &synthesis.Guard{Inner: f, Target: h.ID()}

	if out, _ := g.CallTool("concept_write", good(h)); out.IsError || g.Written != h.ID() {
		t.Fatalf("create refused: %+v", out)
	}
	// The same id again (a lost race) is an existing concept now.
	if out, _ := g.CallTool("concept_write", good(h)); !out.IsError {
		t.Error("second create accepted")
	}
	for _, args := range []map[string]any{
		{"id": "gaps/mine", "if_match": "h2", "frontmatter": map[string]any{"confidence": "medium"}},
		{"id": "gaps/mine", "if_match": "h2", "frontmatter_append": map[string]any{"provenance": []string{"arch/b"}}},
		{"id": "gaps/mine", "if_match": "h2", "old_string": "a calls b", "new_string": "a calls b and c"},
	} {
		if out, err := g.CallTool("concept_patch", args); err != nil || out.IsError {
			t.Errorf("%v refused: %+v", args, out)
		}
	}
}

func TestGuard_SearchProbesNeverRecordAMiss(t *testing.T) {
	f := store(map[string]map[string]any{})
	g := &synthesis.Guard{Inner: f}
	if _, err := g.CallTool("search", map[string]any{"query": "x"}); err != nil {
		t.Fatal(err)
	}
	if got := f.calls[0].Args["record_miss"]; got != false {
		t.Errorf("record_miss = %v", got)
	}
}

func TestGuard_ListsTheAllowlistOnly(t *testing.T) {
	g := &synthesis.Guard{Inner: store(nil)}
	listed, _ := g.ListTools()
	var names []string
	for _, l := range listed {
		names = append(names, l.Name)
	}
	if want := []string{"search", "concept_read", "concept_write", "concept_patch", "kb_review"}; !reflect.DeepEqual(names, want) {
		t.Errorf("got %v", names)
	}
}
