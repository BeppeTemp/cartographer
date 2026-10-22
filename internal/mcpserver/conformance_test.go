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
	t.Parallel()
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
	for _, tool := range []Tool{toolConceptWrite(k, nil, nil), toolConceptNew(k, nil, nil), toolConceptPatch(k, nil), toolConceptBatch(k, nil)} {
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	s := graphToolKB(t, map[string]string{"ops/existing.md": "Body.\n"})
	k := s.kbRef
	mapMD := "---\ntype: Map\nkind: map\ntitle: Ops\nindex: generated\nfield_values.status: [open, done]\n---\n# Ops\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "ops", "_map.md"), []byte(mapMD), 0o644); err != nil {
		t.Fatal(err)
	}

	// An invalid value is a finding the write can avoid: refused, nothing written.
	requireRefusal(t, s, "concept_write",
		`{"id":"ops/a","frontmatter":{"type":"Note","title":"A","status":"nope"},"body":"See [existing](existing.md)."}`, "ops/a.md invalid_field_value")
	if _, err := k.ReadConcept("ops/a"); err == nil {
		t.Fatal("a refused write persisted")
	}

	// A clean write carries an empty findings array (D342).
	out := decodeJSON(t, mustText(t, s, "concept_write",
		`{"id":"ops/a","frontmatter":{"type":"Note","title":"A","status":"open"},"body":"See [existing](existing.md)."}`))
	if f, has := out["findings"].([]interface{}); !has || len(f) != 0 {
		t.Errorf("clean write carries findings: %v", out["findings"])
	}

	// concept_patch reports the array too, and refuses an invalid value.
	hash := out["content_hash"].(string)
	requireRefusal(t, s, "concept_patch",
		`{"id":"ops/a","if_match":"`+hash+`","frontmatter":{"status":"still-wrong"}}`, "ops/a.md invalid_field_value")
	patched := decodeJSON(t, mustText(t, s, "concept_patch",
		`{"id":"ops/a","if_match":"`+hash+`","frontmatter":{"status":"done"}}`))
	if _, has := patched["findings"]; !has {
		t.Errorf("patch without findings: %v", patched)
	}

	// concept_batch is atomic: one invalid entry refuses all of them.
	requireRefusal(t, s, "concept_batch",
		`{"operations":[{"op":"write","id":"ops/c","frontmatter":{"type":"Note","title":"C","status":"x"},"body":"See [a](a.md)."},{"op":"write","id":"ops/d","frontmatter":{"type":"Note","title":"D"},"body":"See [a](a.md)."}]}`,
		"ops/c.md invalid_field_value")
	if _, err := k.ReadConcept("ops/d"); err == nil {
		t.Fatal("a refused batch persisted an entry")
	}
	batch := decodeJSON(t, mustText(t, s, "concept_batch",
		`{"operations":[{"op":"write","id":"ops/c","frontmatter":{"type":"Note","title":"C"},"body":"See [a](a.md)."},{"op":"write","id":"ops/d","frontmatter":{"type":"Note","title":"D"},"body":"See [a](a.md)."}]}`))
	for i, r := range batch["results"].([]interface{}) {
		if _, has := r.(map[string]interface{})["findings"]; has {
			t.Errorf("clean batch entry %d carries findings: %v", i, r)
		}
	}
}

func TestLintOutputFixShape(t *testing.T) {
	t.Parallel()
	s := graphToolKB(t, map[string]string{
		"ops/syn.md": "---\ntype: Note\ntitle: S\naggiornato: 2026-01-01\n---\nx\n",
		"ops/nt.md":  "---\ntype: Note\ntitle: \"🚀\"\n---\n# Deploy 🚀 now\nx\n",
	})
	for _, tool := range []string{"lint", "gate_check"} {
		args := `{}`
		if tool == "gate_check" {
			args = `{"changed_ids":[]}` // the whole-KB gate (D318); changed_ids alone is scoped (D312)
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
			case "title_h1_mismatch": // both sides fail title_quality: no fix (D357)
				withoutFix = !has
			}
		}
		if !withFix || !withoutFix {
			t.Errorf("%s: fix shape wrong (withFix=%v withoutFix=%v)", tool, withFix, withoutFix)
		}
	}
}

// The server's own local state under .cartographer — telemetry rewritten by
// every read and search, the search index — does not invalidate the lint
// cache (D736). usage.json is the exception (D1043), pinned below.
func TestLintInputsStampIgnoresServerState(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	dir := filepath.Join(k.Root, ".cartographer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	before := lintInputsStamp(k)
	for _, name := range []string{"read-access.json", "search-misses.jsonl", "index.db", "auto-repair-log.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := lintInputsStamp(k); got != before {
		t.Fatal("server state under .cartographer changed the lint stamp")
	}
}

// artifact_unused reads the usage reports (D1043): a change to usage.json
// invalidates the whole-KB lint cache, any other .cartographer file does not.
func TestLintInputsStampSeesUsageJSON(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	dir := filepath.Join(k.Root, ".cartographer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	before := lintInputsStamp(k)
	if err := os.WriteFile(filepath.Join(dir, "usage.json"), []byte(`{"entries":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if lintInputsStamp(k) == before {
		t.Fatal("usage.json did not change the lint stamp")
	}
}
