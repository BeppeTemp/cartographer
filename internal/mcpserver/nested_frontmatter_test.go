package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

// D1313: a nested frontmatter value is refused, never written as "%v".

func nestedArgs(t *testing.T, id string, extra map[string]any) string {
	t.Helper()
	fm := map[string]any{"type": "Note", "title": "T"}
	for k, v := range extra {
		fm[k] = v
	}
	b, err := json.Marshal(map[string]any{"id": id, "frontmatter": fm, "body": "# T\n"})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestConceptWrite_RefusesNestedFrontmatter(t *testing.T) {
	t.Parallel()
	cases := map[string]any{
		"map":          map[string]any{"a": "1", "b": map[string]any{"c": 2}},
		"list_of_maps": []any{map[string]any{"x": 1}, map[string]any{"y": 2}},
		"nested_list":  []any{[]any{"a"}, "b"},
	}
	for name, val := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			k := setupTestKB(t)
			s := New("test")
			RegisterKBTools(s, k, Deps{})
			text, isErr := callJSON(t, s, adminCtx, "concept_write", nestedArgs(t, "notes/nested", map[string]any{"cfg": val}))
			if !isErr || !strings.Contains(text, "invalid_frontmatter") || !strings.Contains(text, `"cfg"`) || !strings.Contains(text, "not supported") {
				t.Fatalf("err=%v %s", isErr, text)
			}
			if _, err := k.ReadConcept("notes/nested"); err == nil {
				t.Fatal("concept was written")
			}
		})
	}
}

func TestConceptWrite_ScalarsAndScalarListsUnchanged(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	text, isErr := callJSON(t, s, adminCtx, "concept_write", nestedArgs(t, "notes/flat", map[string]any{"n": 3, "ok": true, "tags": []any{"a", "b"}}))
	if isErr {
		t.Fatal(text)
	}
	raw, err := k.ReadRaw("notes/flat.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"n: 3", "ok: true", "tags: [a, b]"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("missing %q in\n%s", want, raw)
		}
	}
}

func TestConceptBatch_RefusesNestedFrontmatter(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	args := `{"operations":[{"op":"write","id":"notes/nb","frontmatter":{"type":"Note","title":"T","cfg":{"a":"1"}},"body":"# T\n"}]}`
	text, isErr := callJSON(t, s, adminCtx, "concept_batch", args)
	if !isErr || !strings.Contains(text, "invalid_frontmatter") {
		t.Fatalf("err=%v %s", isErr, text)
	}
	if _, err := k.ReadConcept("notes/nb"); err == nil {
		t.Fatal("concept was written")
	}
}
