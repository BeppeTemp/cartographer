package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// D312: a write answers with the structural findings it caused, not only the
// frontmatter ones.

func findingChecks(out map[string]interface{}) map[string]bool {
	got := map[string]bool{}
	list, _ := out["findings"].([]interface{})
	for _, f := range list {
		m := f.(map[string]interface{})
		got[m["path"].(string)+" "+m["check"].(string)] = true
	}
	return got
}

func requireIndexEntry(t *testing.T, s *Server, index string) {
	t.Helper()
	k := s.kbRef
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "ops", "_map.md"), []byte("---\ntype: Map\nkind: map\ntitle: Ops\n---\n# Ops\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "ops", "index.md"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
}

// requireRefusal calls a gated tool and wants the write gate's refusal naming
// each "<path> <check>" (D907): what a write used to answer with as a finding
// it now answers with as a refusal, and nothing is written.
func requireRefusal(t *testing.T, s *Server, tool, args string, wants ...string) {
	t.Helper()
	text, isErr := callJSON(t, s, adminCtx, tool, args)
	if !isErr || !strings.Contains(text, "write_gate: refused") {
		t.Fatalf("%s was not refused: %s", tool, text)
	}
	for _, w := range wants {
		path, check, _ := strings.Cut(w, " ")
		if !strings.Contains(text, `"path":"`+path+`","check":"`+check+`"`) {
			t.Errorf("refusal lacks %s: %s", w, text)
		}
	}
}

func TestWriteFeedback_ConceptWriteBrokenLink(t *testing.T) {
	t.Parallel()
	s := graphToolKB(t, map[string]string{"ops/a.md": "See [b](b.md).\n", "ops/b.md": "See [a](a.md).\n"})
	requireRefusal(t, s, "concept_write",
		`{"id":"ops/c","frontmatter":{"type":"Note","title":"C"},"body":"See [a](a.md) and [gone](gone.md)."}`, "ops/c.md broken_link")
	if _, err := s.kbRef.ReadConcept("ops/c"); err == nil {
		t.Fatal("the refused page was written")
	}
}

func TestWriteFeedback_ConceptNewIndexIncomplete(t *testing.T) {
	t.Parallel()
	s := graphToolKB(t, map[string]string{"ops/a.md": "A.\n"})
	requireIndexEntry(t, s, "# Ops\n\n- [A](a.md)\n")
	if err := os.MkdirAll(filepath.Join(s.kbRef.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.kbRef.Root, "templates", "plain.md"), []byte("---\ntype: Note\ntitle: P\n---\nSee [a](a.md).\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	requireRefusal(t, s, "concept_new", `{"template":"plain","id":"ops/p"}`, "ops/index.md index_incomplete")
	out := decodeJSON(t, mustText(t, s, "concept_new", `{"template":"plain","id":"ops/p","index_entry":true}`))
	if findingChecks(out)["ops/index.md index_incomplete"] {
		t.Fatalf("index_entry left the index incomplete: %v", out["findings"])
	}
}

func TestWriteFeedback_ConceptMoveLinkToRetired(t *testing.T) {
	t.Parallel()
	s := graphToolKB(t, map[string]string{
		"ops/x.md": "---\ntype: Note\ntitle: X\nstatus: deprecated\n---\nX.\n",
		"ops/y.md": "See [x](x.md).\n",
	})
	out := decodeJSON(t, mustText(t, s, "concept_move", `{"source_id":"ops/x","target_id":"ops/x2"}`))
	if !findingChecks(out)["ops/x2.md link_to_retired"] {
		t.Fatalf("findings = %v", out["findings"])
	}
}

// Without the backlink rewrite the pages still linking the old ID are broken
// by the move: a write that introduces them is refused (D907).
func TestWriteFeedback_ConceptMoveLeavesBrokenLinkers(t *testing.T) {
	t.Parallel()
	s := graphToolKB(t, map[string]string{"ops/x.md": "X.\n", "ops/y.md": "See [x](x.md).\n"})
	requireRefusal(t, s, "concept_move", `{"source_id":"ops/x","target_id":"ops/x2","rewrite_links":false}`, "ops/y.md broken_link")
	if _, err := s.kbRef.ReadConcept("ops/x"); err != nil {
		t.Fatalf("the refused move was applied: %v", err)
	}
}

// A supersede that leaves a live page linking the retired one is refused
// (D907); once the linker is retargeted it goes through.
func TestWriteFeedback_SupersedeReportsLinkers(t *testing.T) {
	t.Parallel()
	s := graphToolKB(t, map[string]string{"ops/old.md": "Old.\n", "ops/new.md": "New.\n", "ops/y.md": "See [old](old.md).\n"})
	requireRefusal(t, s, "supersede", `{"source_id":"ops/old","target_id":"ops/new"}`, "ops/old.md link_to_retired")
	hash := conceptHash(t, callTool(t, s, "concept_read", `{"id":"ops/y"}`))
	mustText(t, s, "concept_patch", `{"id":"ops/y","if_match":"`+hash+`","old_string":"[old](old.md)","new_string":"[new](new.md)"}`)
	text := mustText(t, s, "supersede", `{"source_id":"ops/old","target_id":"ops/new"}`)
	if !strings.HasPrefix(text, "superseded ops/old → ops/new") {
		t.Fatalf("supersede = %q", text)
	}
}

func TestWriteFeedback_IndexPatchDroppedEntry(t *testing.T) {
	t.Parallel()
	s := graphToolKB(t, map[string]string{"ops/a.md": "A.\n", "ops/b.md": "B.\n"})
	requireIndexEntry(t, s, "# Ops\n\n- [A](a.md)\n- [B](b.md)\n")
	_, hash, err := s.kbRef.IndexHash("ops")
	if err != nil {
		t.Fatal(err)
	}
	out := decodeJSON(t, mustText(t, s, "index_patch", `{"path":"ops","old_string":"- [B](b.md)\n","new_string":"","if_match":"`+hash+`"}`))
	if !findingChecks(out)["ops/index.md index_incomplete"] {
		t.Fatalf("findings = %v", out["findings"])
	}
}

func TestWriteFeedback_GateCheckChangedIDs(t *testing.T) {
	t.Parallel()
	s := graphToolKB(t, map[string]string{
		"ops/bad.md":   "See [gone](gone.md).\n",
		"ops/other.md": "See [gone](gone2.md).\n",
	})
	mustText(t, s, "map_update", `{"map":"ops","index":"generated"}`)
	if text, isErr := callJSON(t, s, adminCtx, "concept_write", `{"id":"ops/ok","frontmatter":{"type":"Note","title":"Ok"},"body":"See [other](other.md)."}`); isErr {
		t.Fatalf("concept_write: %s", text)
	}
	out := decodeJSON(t, mustText(t, s, "gate_check", `{"changed_ids":["ops/ok"]}`))
	if out["lint_scope"] != "changed_ids" || out["pass"] != true {
		t.Fatalf("clean changed id: %v", out)
	}
	// The unrelated neighbour's own broken link is not this session's.
	if list, _ := out["lint_findings"].([]interface{}); len(list) != 0 {
		t.Fatalf("scoped gate carried a neighbour's findings: %v", list)
	}
	// An error finding on a changed id fails the gate.
	if err := os.WriteFile(filepath.Join(s.kbRef.DataRoot(), "ops", "_map.md"), []byte("---\ntype: Map\nkind: map\ntitle: Ops\nrequired_fields: [owner]\n---\n# Ops\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = decodeJSON(t, mustText(t, s, "gate_check", `{"changed_ids":["ops/ok"]}`))
	if out["pass"] != false {
		t.Fatalf("missing required field must fail the scoped gate: %v", out)
	}
	// With a scope, the scope's lint runs as before.
	out = decodeJSON(t, mustText(t, s, "gate_check", `{"changed_ids":["ops/ok"],"scope":"ops"}`))
	if out["lint_scope"] != "scope" {
		t.Fatalf("scope + changed_ids: %v", out["lint_scope"])
	}
	// No changed_ids: the whole-KB gate.
	out = decodeJSON(t, mustText(t, s, "gate_check", `{"changed_ids":[]}`))
	if out["lint_scope"] != "kb" {
		t.Fatalf("empty changed_ids: %v", out["lint_scope"])
	}
}

func TestWriteFeedback_ToolDescriptionsSayFindings(t *testing.T) {
	t.Parallel()
	s := graphToolKB(t, nil)
	for _, name := range []string{"concept_write", "concept_new", "concept_patch", "concept_batch", "concept_move", "supersede", "index_patch", "gate_check"} {
		tool, ok := s.tools[name]
		if !ok {
			t.Fatalf("tool %s not registered", name)
		}
		want := "findings"
		if name == "gate_check" {
			want = "session end"
		}
		if !strings.Contains(tool.Description, want) {
			t.Errorf("%s description lacks %q: %q", name, want, tool.Description)
		}
	}
	if d := s.tools["concept_write"].Description; !strings.Contains(d, "structural") {
		t.Errorf("concept_write must say the findings are structural: %q", d)
	}
}
