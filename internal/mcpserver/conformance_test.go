package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/lint"
)

// Every property of a concept-write tool schema must be in lint.ToolParamFields,
// so the lint check and the write-time rejection cannot drift from the tools (D289).
func TestToolParamFieldsCoverWriteSchemas(t *testing.T) {
	k := setupTestKB(t)
	var props func(schema map[string]interface{}) []string
	props = func(schema map[string]interface{}) []string {
		var out []string
		p, _ := schema["properties"].(map[string]interface{})
		for name, v := range p {
			out = append(out, name)
			sub, _ := v.(map[string]interface{})
			if items, ok := sub["items"].(map[string]interface{}); ok {
				out = append(out, props(items)...)
			}
		}
		return out
	}
	for _, tool := range []Tool{toolConceptWrite(k, nil), toolConceptNew(k, nil), toolConceptPatch(k), toolConceptBatch(k)} {
		var schema map[string]interface{}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		for _, name := range props(schema) {
			if !lint.IsToolParamField(name) {
				t.Errorf("%s: schema property %q is missing from lint.ToolParamFields", tool.Name, name)
			}
		}
	}
}

func TestWriteToolsRejectToolParamKeys(t *testing.T) {
	s := graphToolKB(t, map[string]string{"ops/existing.md": "Body.\n"})
	hash := decodeJSON(t, mustText(t, s, "concept_read", `{"id":"ops/existing"}`))["content_hash"].(string)
	wantErr := `frontmatter key "if_match" is a tool parameter, not a field — pass it as a top-level argument`

	cases := map[string]struct{ tool, args string }{
		"write":       {"concept_write", `{"id":"ops/n","frontmatter":{"type":"Note","title":"N","if_match":"x"},"body":"b"}`},
		"patch":       {"concept_patch", `{"id":"ops/existing","if_match":"` + hash + `","frontmatter":{"if_match":"x"}}`},
		"batch-write": {"concept_batch", `{"operations":[{"op":"write","id":"ops/n","frontmatter":{"type":"Note","title":"N","if_match":"x"},"body":"b"}]}`},
		"batch-patch": {"concept_batch", `{"operations":[{"op":"patch","id":"ops/existing","if_match":"` + hash + `","frontmatter":{"if_match":"x"}}]}`},
	}
	for name, c := range cases {
		text, isErr := callJSON(t, s, adminCtx, c.tool, c.args)
		if !isErr || !strings.Contains(text, wantErr) {
			t.Errorf("%s: got %q (error=%v)", name, text, isErr)
		}
	}
	if _, err := s.kbRef.ReadConcept("ops/n"); err == nil {
		t.Error("rejected write persisted a concept")
	}

	// A null in a patch removes the key: that is the repair, not the mistake.
	if text, isErr := callJSON(t, s, adminCtx, "concept_patch", `{"id":"ops/existing","if_match":"`+hash+`","frontmatter":{"if_match":null}}`); isErr {
		t.Errorf("null removal rejected: %s", text)
	}
}

func TestConceptNewRejectsToolParamKeyFromTemplate(t *testing.T) {
	s := graphToolKB(t, nil)
	k := s.kbRef
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "templates", "bad.md"), []byte("---\ntype: Note\ntitle: T\nbody: x\n---\nText\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text, isErr := callJSON(t, s, adminCtx, "concept_new", `{"template":"bad","id":"ops/n"}`)
	if !isErr || !strings.Contains(text, `frontmatter key "body" is a tool parameter`) {
		t.Fatalf("got %q (error=%v)", text, isErr)
	}
}

func TestWriteResponsesCarryFindings(t *testing.T) {
	s := graphToolKB(t, map[string]string{"ops/existing.md": "Body.\n"})
	k := s.kbRef
	mapMD := "---\ntype: Map\nkind: map\ntitle: Ops\nfield_values.status: [open, done]\n---\n# Ops\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "ops", "_map.md"), []byte(mapMD), 0o644); err != nil {
		t.Fatal(err)
	}

	// concept_write: a stale-synonym warning and, on the contract, an error
	// finding; the write itself succeeds.
	out := decodeJSON(t, mustText(t, s, "concept_write",
		`{"id":"ops/a","frontmatter":{"type":"Note","title":"A","status":"nope","aggiornato":"2026-01-01"},"body":"b"}`))
	findings, _ := out["findings"].([]interface{})
	checks := map[string]map[string]interface{}{}
	for _, f := range findings {
		m := f.(map[string]interface{})
		checks[m["check"].(string)] = m
	}
	if checks["invalid_field_value"] == nil || checks["nonstandard_field"] == nil {
		t.Fatalf("findings = %v", out["findings"])
	}
	if fix, _ := checks["nonstandard_field"]["fix"].(map[string]interface{}); fix["kind"] != "rename_field" || fix["to"] != "timestamp" {
		t.Errorf("fix = %v", checks["nonstandard_field"]["fix"])
	}
	if _, err := k.ReadConcept("ops/a"); err != nil {
		t.Fatalf("write did not persist: %v", err)
	}

	// A clean write has no findings key.
	clean := decodeJSON(t, mustText(t, s, "concept_write",
		`{"id":"ops/b","frontmatter":{"type":"Note","title":"B","status":"open"},"body":"b"}`))
	if _, has := clean["findings"]; has {
		t.Errorf("clean write carries findings: %v", clean["findings"])
	}

	// concept_patch and concept_batch report them too.
	hash := out["content_hash"].(string)
	patched := decodeJSON(t, mustText(t, s, "concept_patch",
		`{"id":"ops/a","if_match":"`+hash+`","frontmatter":{"status":"still-wrong"}}`))
	if _, has := patched["findings"]; !has {
		t.Errorf("patch without findings: %v", patched)
	}
	batch := decodeJSON(t, mustText(t, s, "concept_batch",
		`{"operations":[{"op":"write","id":"ops/c","frontmatter":{"type":"Note","title":"C","stato":"x"},"body":"b"},{"op":"write","id":"ops/d","frontmatter":{"type":"Note","title":"D"},"body":"b"}]}`))
	results := batch["results"].([]interface{})
	if _, has := results[0].(map[string]interface{})["findings"]; !has {
		t.Errorf("batch entry 0 without findings: %v", results[0])
	}
	if _, has := results[1].(map[string]interface{})["findings"]; has {
		t.Errorf("clean batch entry carries findings: %v", results[1])
	}
}

func TestLintOutputFixShape(t *testing.T) {
	s := graphToolKB(t, map[string]string{
		"ops/syn.md": "---\ntype: Note\ntitle: S\naggiornato: 2026-01-01\n---\nx\n",
		"ops/nt.md":  "---\ntype: Note\n---\nx\n",
	})
	for _, tool := range []string{"lint", "gate_check"} {
		args := `{}`
		if tool == "gate_check" {
			args = `{"changed_ids":["ops/syn"]}`
		}
		out := decodeJSON(t, mustText(t, s, tool, args))
		key := "findings"
		if tool == "gate_check" {
			key = "lint_findings"
		}
		var withFix, withoutFix bool
		for _, f := range out[key].([]interface{}) {
			m := f.(map[string]interface{})
			_, has := m["fix"]
			switch m["check"] {
			case "nonstandard_field":
				withFix = has
			case "missing_title":
				withoutFix = !has
			}
		}
		if !withFix || !withoutFix {
			t.Errorf("%s: fix shape wrong (withFix=%v withoutFix=%v)", tool, withFix, withoutFix)
		}
	}
}
