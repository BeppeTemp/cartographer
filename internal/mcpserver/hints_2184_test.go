package mcpserver

import (
	"strings"
	"testing"
)

// The messages of #2184 each say what to do; a read of a file-shaped argument
// never shows the host's filesystem.
func TestErrorMessagesSayWhatToDo(t *testing.T) {
	t.Parallel()
	s := graphToolKB(t, map[string]string{"ops/alpha.md": "---\ntype: Note\ntitle: A\n---\nx\n"})
	cases := []struct {
		name, tool, args string
		want             []string
		never            []string
	}{
		{"index_get with a file", "index_get", `{"path":"ops/index.md"}`, []string{"folder or map name", `"ops"`, "concept_read"}, []string{"not a directory", "ReadRaw"}},
		{"index_get through a page", "index_get", `{"path":"ops/alpha.md/x"}`, []string{"no such folder"}, []string{"not a directory", "ReadRaw", "lstat"}},
		{"concept_read with .md", "concept_read", `{"id":"ops/alpha.md"}`, []string{"no .md extension", `"ops/alpha"`}, []string{"not a directory", "lstat"}},
		{"if_match", "concept_patch", `{"id":"ops/alpha","old_string":"x","new_string":"y"}`, []string{"content_hash", "concept_read"}, nil},
		{"artifact_read of a page", "artifact_read", `{"path":"ops/alpha.md"}`, []string{"concept_read", "index_get"}, nil},
		{"lint unknown check", "lint", `{"check":"nonsense"}`, []string{`unknown check "nonsense"`, "valid checks are", "nonstandard_field"}, nil},
	}
	for _, c := range cases {
		text, isErr := callJSON(t, s, adminCtx, c.tool, c.args)
		if !isErr {
			t.Errorf("%s: no error: %s", c.name, text)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s: %q missing from %q", c.name, w, text)
			}
		}
		for _, n := range c.never {
			if strings.Contains(text, n) {
				t.Errorf("%s: %q leaked in %q", c.name, n, text)
			}
		}
	}
}

// A repair names the field and the mapping, and a status outside the
// vocabulary that the rename produced is a finding.
func TestRepairedNamesFieldAndFlagsStatusOutsideVocabulary(t *testing.T) {
	t.Parallel()
	_, s := rowKB(t)
	out := decodeJSON(t, mustText(t, s, "concept_write",
		`{"id":"ops/n","frontmatter":{"type":"Note","title":"N","stato":"ascoltato"},"body":"# N\n\nSee [A](a.md).\n"}`))
	list, _ := out["repaired"].([]interface{})
	if len(list) != 1 {
		t.Fatalf("repaired = %v", out["repaired"])
	}
	r := list[0].(map[string]interface{})
	fields, _ := r["fields"].([]interface{})
	if len(fields) != 1 || fields[0] != "stato -> status" {
		t.Fatalf("fields = %v", r)
	}
	if !findingChecks(out)["ops/n.md invalid_field_value"] {
		t.Fatalf("no finding for the status outside the vocabulary: %v", out["findings"])
	}
	// a status the vocabulary knows is no finding
	out = decodeJSON(t, mustText(t, s, "concept_write",
		`{"id":"ops/m","frontmatter":{"type":"Note","title":"M","stato":"active"},"body":"# M\n\nSee [A](a.md).\n"}`))
	if findingChecks(out)["ops/m.md invalid_field_value"] {
		t.Fatalf("a known status was flagged: %v", out["findings"])
	}
}

// #2239: lint, validate and gate_check refuse an unknown scope the same way,
// naming map_list and no server path.
func TestUnknownScopeIsRefusedByLintValidateGate(t *testing.T) {
	t.Parallel()
	s := graphToolKB(t, map[string]string{"ops/alpha.md": "---\ntype: Note\ntitle: A\n---\nx\n"})
	for _, tool := range []string{"lint", "validate", "gate_check"} {
		for _, scope := range []string{"nosuchmap", `\"\"`} {
			text, isErr := callJSON(t, s, adminCtx, tool, `{"scope":"`+scope+`"}`)
			if !isErr || !strings.Contains(text, "unknown scope") || !strings.Contains(text, "map_list") {
				t.Errorf("%s %s: %v %q", tool, scope, isErr, text)
			}
			if strings.Contains(text, "lstat") || strings.Contains(text, "/data/") {
				t.Errorf("%s %s: path leaked: %q", tool, scope, text)
			}
		}
		if text, isErr := callJSON(t, s, adminCtx, tool, `{"scope":"ops"}`); isErr {
			t.Errorf("%s: known scope refused: %q", tool, text)
		}
	}
}
