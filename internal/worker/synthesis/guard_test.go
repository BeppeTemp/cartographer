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
			return client.ToolOutput{IsError: true, Text: "concept_read: concept not found"}
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
	pages := map[string]map[string]any{"arch/a": active, "arch/b": active, "gaps/mine": synthOpen}
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
	pages := map[string]map[string]any{"arch/a": synthOpen, "arch/b": synthOpen, "gaps/mine": synthOpen}
	f := store(pages)
	g := &synthesis.Guard{Inner: f, Target: h.ID()}

	w := good(h)
	w["reason"] = "the model's own reason"
	if out, _ := g.CallTool("concept_write", w); out.IsError || g.Written != h.ID() {
		t.Fatalf("create refused: %+v", out)
	}
	// Every write carries the guard's reason, never the model's.
	for _, c := range f.calls {
		if c.Tool == "concept_write" && c.Args["reason"] != "synthesis: "+h.ID() {
			t.Errorf("write reason = %v", c.Args["reason"])
		}
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

func openHyp() map[string]any {
	return map[string]any{"frontmatter": map[string]any{"type": "Contradiction", "origin": "synthesis", "resolution_status": "open", "involves": []any{"a", "b", "c", "d", "e"}}, "body": "T\n\n## Evidence\n\n- [[arch/a]]: a calls b\n\n## How to confirm or reject\n\nx\n", "content_hash": "h2"}
}

func TestGuard_Hardening(t *testing.T) {
	h := hyp()
	pages := map[string]map[string]any{"gaps/mine": openHyp()}
	f := store(pages)
	g := &synthesis.Guard{Inner: f, Target: h.ID()}
	w := good(h)
	w["kb"] = "other-kb"
	patch := func(extra map[string]any) map[string]any {
		m := map[string]any{"id": "gaps/mine", "if_match": "h2"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	refused := map[string]struct {
		tool string
		args any
	}{
		"kb on a write":        {"concept_write", w},
		"target_kb on a read":  {"concept_read", map[string]any{"id": "x", "target_kb": "other"}},
		"kb on a search":       {"search", map[string]any{"query": "x", "kb": "other"}},
		"kb on a patch":        {"concept_patch", patch(map[string]any{"kb": "o", "frontmatter": map[string]any{"confidence": "low"}})},
		"edit replace_all":     {"concept_patch", patch(map[string]any{"edits": []any{map[string]any{"old_string": "a calls b", "new_string": "x", "replace_all": true}}})},
		"edit heading CR":      {"concept_patch", patch(map[string]any{"edits": []any{map[string]any{"old_string": "a calls b", "new_string": "x\r# H"}}})},
		"edit heading LF":      {"concept_patch", patch(map[string]any{"old_string": "a calls b", "new_string": "x\n## H"})},
		"edit outside":         {"concept_patch", patch(map[string]any{"edits": []any{map[string]any{"old_string": "T\n", "new_string": "x"}}})},
		"confidence null":      {"concept_patch", patch(map[string]any{"frontmatter": map[string]any{"confidence": nil}})},
		"confidence number":    {"concept_patch", patch(map[string]any{"frontmatter": map[string]any{"confidence": 3}})},
		"involves over cap":    {"concept_patch", patch(map[string]any{"frontmatter_append": map[string]any{"involves": []string{"f", "g"}}})},
		"provenance not id":    {"concept_patch", patch(map[string]any{"frontmatter_append": map[string]any{"provenance": []string{"../etc/passwd"}}})},
		"provenance extension": {"concept_patch", patch(map[string]any{"frontmatter_append": map[string]any{"provenance": []string{"a/b.md"}}})},
		"append other key":     {"concept_patch", patch(map[string]any{"frontmatter_append": map[string]any{"tags": []string{"x"}}})},
	}
	for name, c := range refused {
		if out, _ := g.CallTool(c.tool, c.args); !out.IsError || !strings.Contains(out.Text, "synthesis creates new gaps only") {
			t.Errorf("%s: not refused: %+v", name, out)
		}
	}
	if f.count("concept_write")+f.count("concept_patch")+f.count("search") != 0 {
		t.Error("a refused call reached the server")
	}
	// An allowed append within the cap still passes.
	if out, _ := g.CallTool("concept_patch", patch(map[string]any{"frontmatter_append": map[string]any{"involves": []string{"f"}}})); out.IsError {
		t.Errorf("append within the cap refused: %+v", out)
	}
}

// Only a recognised not-found means the concept is absent: any other read error
// (a routed connection, a permission) must not turn into a write.
func TestGuard_ReadErrorFailsClosed(t *testing.T) {
	h := hyp()
	f := store(map[string]map[string]any{})
	f.handler["concept_read"] = func(map[string]any) client.ToolOutput {
		return client.ToolOutput{IsError: true, Text: "kb is required on a routed connection"}
	}
	g := &synthesis.Guard{Inner: f, Target: h.ID()}
	if out, _ := g.CallTool("concept_write", good(h)); !out.IsError || f.count("concept_write") != 0 {
		t.Fatalf("written after an unreadable read: %+v", out)
	}
	f.handler["concept_read"] = func(a map[string]any) client.ToolOutput {
		if strings.HasPrefix(a["id"].(string), "arch/") { // the cited pages exist, the target does not
			return js(map[string]any{"frontmatter": map[string]any{"type": "Note"}, "body": "x"})
		}
		return client.ToolOutput{IsError: true, Text: `concept_read "x": concept not found: x.md`}
	}
	if out, _ := g.CallTool("concept_write", good(h)); out.IsError {
		t.Fatalf("a real not-found must allow the create: %+v", out)
	}
}

// The server applies edits in order: edit 1 may make a text unique elsewhere
// for edit 2. The guard replays them on the evolving body.
func TestGuard_EditsAreReplayedInOrder(t *testing.T) {
	bodies := map[string]string{
		"proof":   "T\n\n## Statement\n\nfoo bar\n\n## Evidence\n\n- foo bar\n",
		"inline":  "T\n\n## Statement\n\nsee ## Evidence below: foo bar\n\n## Evidence\n\n- foo bar\n",
		"fake H2": "T\n\n## Statement\n\nfoo bar\n\n## Evidence\n\n- foo bar\n\n## Evidence\n\n- other\n",
	}
	for name, body := range bodies {
		page := openHyp()
		page["body"] = body
		f := store(map[string]map[string]any{"gaps/mine": page})
		g := &synthesis.Guard{Inner: f}
		args := map[string]any{"id": "gaps/mine", "if_match": "h2", "edits": []any{
			map[string]any{"old_string": "- foo bar", "new_string": "x"},
			map[string]any{"old_string": "foo bar", "new_string": "EVIL"},
		}}
		if out, _ := g.CallTool("concept_patch", args); !out.IsError || f.count("concept_patch") != 0 {
			t.Errorf("%s: refused expected: %+v", name, out)
		}
	}
	// One edit inside Evidence, and two in sequence inside it, pass.
	page := openHyp()
	page["body"] = "T\n\n## Statement\n\nfoo\n\n## Evidence\n\n- a\n- b\n\n## How to confirm or reject\n\nx\n"
	f := store(map[string]map[string]any{"gaps/mine": page})
	g := &synthesis.Guard{Inner: f}
	ok := map[string]any{"id": "gaps/mine", "if_match": "h2", "edits": []any{
		map[string]any{"old_string": "- a", "new_string": "- a2"},
		map[string]any{"old_string": "- b", "new_string": "- b2"},
	}}
	if out, _ := g.CallTool("concept_patch", ok); out.IsError {
		t.Errorf("legitimate edits refused: %+v", out)
	}
}

// The provenance is checked against the KB at write time, and every Evidence
// line must link a provenance entry (WP1, "checked with concept_read").
func TestGuard_ProvenanceExistsAndEvidenceLinksIt(t *testing.T) {
	h := hyp()
	page := map[string]any{"frontmatter": map[string]any{"type": "Note"}, "body": "x", "content_hash": "h"}
	pages := map[string]map[string]any{"arch/a": page} // arch/b vanished
	f := store(pages)
	g := &synthesis.Guard{Inner: f, Target: h.ID()}
	if out, _ := g.CallTool("concept_write", good(h)); !out.IsError || !strings.Contains(out.Text, "arch/b") {
		t.Fatalf("a missing provenance entry was accepted: %+v", out)
	}
	pages["arch/b"] = page
	w := good(h)
	w["body"] = strings.Replace(h.Body(), "[[arch/b]]", "arch/b", 1)
	if out, _ := g.CallTool("concept_write", w); !out.IsError || !strings.Contains(out.Text, "links no provenance") {
		t.Fatalf("an evidence line without a link was accepted: %+v", out)
	}
	w["body"] = "T\n"
	if out, _ := g.CallTool("concept_write", w); !out.IsError {
		t.Fatalf("a body without Evidence was accepted: %+v", out)
	}
	if f.count("concept_write") != 0 {
		t.Error("a refused write reached the server")
	}
	if out, _ := g.CallTool("concept_write", good(h)); out.IsError {
		t.Fatalf("a valid hypothesis refused: %+v", out)
	}
}
