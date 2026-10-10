package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

const listsID = "notes/lists"

func listsKB(t *testing.T, fmExtra string) (*Server, *kb.KB) {
	t.Helper()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	raw := "---\ntype: Note\ntitle: Lists\n" + fmExtra + "---\n# Lists\n\nalpha line\nbeta line\n"
	if _, err := k.WriteConcept(okf.ConceptID(listsID), mustFM(t, raw), "# Lists\n\nalpha line\nbeta line\n", ""); err != nil {
		t.Fatal(err)
	}
	return s, k
}

func mustFM(t *testing.T, raw string) *okf.Frontmatter {
	t.Helper()
	inner := strings.TrimSuffix(strings.TrimPrefix(raw, "---\n"), "---\n# Lists\n\nalpha line\nbeta line\n")
	fm, err := okf.ParseFrontmatter(inner)
	if err != nil {
		t.Fatal(err)
	}
	return fm
}

// patchCall runs concept_patch with the current hash and returns the decoded
// answer (or the error text).
func patchCall(t *testing.T, s *Server, k *kb.KB, args map[string]any) (map[string]any, string) {
	t.Helper()
	args["id"] = listsID
	if _, ok := args["if_match"]; !ok {
		args["if_match"] = readHash(t, k, listsID)
	}
	raw, _ := json.Marshal(args)
	res := callTool(t, s, "concept_patch", string(raw))
	if res.IsError {
		return nil, res.Content[0].Text
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	return out, ""
}

func fmValue(t *testing.T, k *kb.KB, key string) any {
	t.Helper()
	d, err := k.ReadConcept(listsID)
	if err != nil {
		t.Fatal(err)
	}
	fm, _ := okf.ParseFrontmatter(d.FrontmatterRaw)
	v, _ := fm.Get(key)
	return v
}

func droppedMsgs(out map[string]any) []string {
	var msgs []string
	for _, f := range out["findings"].([]any) {
		m := f.(map[string]any)
		if m["check"] == "list_items_dropped" {
			msgs = append(msgs, m["message"].(string))
		}
	}
	return msgs
}

func TestPatch_AppendRemove(t *testing.T) {
	s, k := listsKB(t, "provenance: [a, b]\nscalar: only\nblock:\n  x: 1\n")

	if _, e := patchCall(t, s, k, map[string]any{"frontmatter_append": map[string]any{"provenance": []string{"c", "a", "c"}}}); e != "" {
		t.Fatal(e)
	}
	if got := fmt.Sprint(fmValue(t, k, "provenance")); got != "[a b c]" {
		t.Errorf("append keeps order, dedupes: %s", got)
	}
	patchCall(t, s, k, map[string]any{"frontmatter_append": map[string]any{"fresh": "one"}})
	if got := fmt.Sprint(fmValue(t, k, "fresh")); got != "[one]" {
		t.Errorf("append to missing key: %s", got)
	}
	patchCall(t, s, k, map[string]any{"frontmatter_append": map[string]any{"scalar": []string{"two"}}})
	if got := fmt.Sprint(fmValue(t, k, "scalar")); got != "[only two]" {
		t.Errorf("append to scalar: %s", got)
	}
	patchCall(t, s, k, map[string]any{"frontmatter_remove": map[string]any{"provenance": []string{"a", "zzz"}, "nokey": "x"}})
	if got := fmt.Sprint(fmValue(t, k, "provenance")); got != "[b c]" {
		t.Errorf("remove present/absent: %s", got)
	}
	patchCall(t, s, k, map[string]any{"frontmatter_remove": map[string]any{"fresh": "one"}})
	if v := fmValue(t, k, "fresh"); v != nil {
		t.Errorf("removing the last item must delete the key, got %v", v)
	}

	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"append type", map[string]any{"frontmatter_append": map[string]any{"type": "x"}}, `cannot change "type"`},
		{"remove type", map[string]any{"frontmatter_remove": map[string]any{"type": "x"}}, `cannot change "type"`},
		{"append block", map[string]any{"frontmatter_append": map[string]any{"block": "x"}}, "block is not a list"},
		{"remove block", map[string]any{"frontmatter_remove": map[string]any{"block": "x"}}, "block is not a list"},
		{"append tool param", map[string]any{"frontmatter_append": map[string]any{"if_match": "x"}}, "tool parameter"},
		{"bad value", map[string]any{"frontmatter_append": map[string]any{"k": 3}}, "invalid params"},
		{"empty item", map[string]any{"frontmatter_append": map[string]any{"k": []string{""}}}, "invalid params"},
	} {
		before := readHash(t, k, listsID)
		if _, e := patchCall(t, s, k, tc.args); !strings.Contains(e, tc.want) {
			t.Errorf("%s: error %q, want %q", tc.name, e, tc.want)
		}
		if readHash(t, k, listsID) != before {
			t.Errorf("%s: a refused patch wrote", tc.name)
		}
	}

	// unset wins over append; merge then append apply in order.
	patchCall(t, s, k, map[string]any{
		"frontmatter":        map[string]any{"order": []string{"m"}},
		"frontmatter_append": map[string]any{"order": []string{"n"}, "gone": []string{"g"}},
		"unset":              []string{"gone"},
	})
	if got := fmt.Sprint(fmValue(t, k, "order")); got != "[m n]" {
		t.Errorf("merge then append: %s", got)
	}
	if fmValue(t, k, "gone") != nil {
		t.Errorf("unset must win")
	}
	// Removing a tool-parameter key is the repair and is allowed.
	if _, e := patchCall(t, s, k, map[string]any{"frontmatter_remove": map[string]any{"if_match": "x"}}); e != "" {
		t.Errorf("remove of a tool-param key refused: %s", e)
	}
}

// The incident: a second session sends a stale full list; the hash is current.
func TestPatch_IncidentStaleListAppendVsReplace(t *testing.T) {
	s, k := listsKB(t, "provenance: [a]\n")
	stale := []string{"a"}
	// Another session appends b.
	patchCall(t, s, k, map[string]any{"frontmatter_append": map[string]any{"provenance": "b"}})
	// This session appends c from its stale copy: b survives.
	patchCall(t, s, k, map[string]any{"frontmatter_append": map[string]any{"provenance": "c"}})
	if got := fmt.Sprint(fmValue(t, k, "provenance")); got != "[a b c]" {
		t.Fatalf("append lost an item: %s", got)
	}
	// The old form (stale list resent whole) loses b and c, and says so.
	out, e := patchCall(t, s, k, map[string]any{"frontmatter": map[string]any{"provenance": append(stale, "d")}})
	if e != "" {
		t.Fatal(e)
	}
	msgs := droppedMsgs(out)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "provenance (2)") || !strings.Contains(msgs[0], `"b", "c"`) {
		t.Errorf("want list_items_dropped provenance (2), got %v", msgs)
	}
}

func TestPatch_ListItemsDropped(t *testing.T) {
	cases := []struct {
		name    string
		extra   string
		args    map[string]any
		wantMsg string // substring; "" = no finding
	}{
		{"3 to 1", "p: [a, b, c]\n", map[string]any{"frontmatter": map[string]any{"p": []string{"a"}}}, "p (2)"},
		{"superset", "p: [a, b]\n", map[string]any{"frontmatter": map[string]any{"p": []string{"a", "b", "c"}}}, ""},
		{"reorder", "p: [a, b]\n", map[string]any{"frontmatter": map[string]any{"p": []string{"b", "a"}}}, ""},
		{"null", "p: [a, b]\n", map[string]any{"frontmatter": map[string]any{"p": nil}}, ""},
		{"unset", "p: [a, b]\n", map[string]any{"frontmatter": map[string]any{"p": []string{"a"}}, "unset": []string{"p"}}, ""},
		{"remove", "p: [a, b]\n", map[string]any{"frontmatter": map[string]any{"p": []string{"a"}}, "frontmatter_remove": map[string]any{"p": "a"}}, ""},
		{"scalar to list", "p: a\n", map[string]any{"frontmatter": map[string]any{"p": []string{"b"}}}, `p (1): "a"`},
		{"list to scalar", "p: [a, b]\n", map[string]any{"frontmatter": map[string]any{"p": "a"}}, `p (1): "b"`},
		{"list to scalar kept", "p: [a]\n", map[string]any{"frontmatter": map[string]any{"p": "a"}}, ""},
		{"truncated", "p: [a1, a2, a3, a4, a5, a6, a7]\n", map[string]any{"frontmatter": map[string]any{"p": []string{"z"}}}, "p (7): \"a1\", \"a2\", \"a3\", \"a4\", \"a5\", +2 more"},
		{"scalar to scalar", "p: a\n", map[string]any{"frontmatter": map[string]any{"p": "b"}}, ""},
		{"body only", "p: [a, b]\n", map[string]any{"old_string": "alpha", "new_string": "ALPHA"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, k := listsKB(t, tc.extra)
			out, e := patchCall(t, s, k, tc.args)
			if e != "" {
				t.Fatal(e)
			}
			msgs := droppedMsgs(out)
			if tc.wantMsg == "" {
				if len(msgs) != 0 {
					t.Errorf("unexpected finding %v", msgs)
				}
				return
			}
			if len(msgs) != 1 || !strings.Contains(msgs[0], tc.wantMsg) || !strings.Contains(msgs[0], "frontmatter_append/frontmatter_remove") {
				t.Errorf("got %v, want %q", msgs, tc.wantMsg)
			}
		})
	}
}

func TestPatch_ListItemsDropped_ItemsCut(t *testing.T) {
	long := strings.Repeat("x", 200)
	s, k := listsKB(t, "p: ["+long+", b]\n")
	out, _ := patchCall(t, s, k, map[string]any{"frontmatter": map[string]any{"p": []string{"b"}}})
	msgs := droppedMsgs(out)
	if len(msgs) != 1 || strings.Contains(msgs[0], strings.Repeat("x", 81)) {
		t.Errorf("item not cut to 80 bytes: %v", msgs)
	}
}

func TestBatch_PatchAppendAndDropped(t *testing.T) {
	s, k := listsKB(t, "p: [a, b]\n")
	ops := []map[string]any{{"op": "patch", "id": listsID, "if_match": readHash(t, k, listsID),
		"frontmatter_append": map[string]any{"q": "x"}, "frontmatter": map[string]any{"p": []string{"a"}}}}
	raw, _ := json.Marshal(map[string]any{"operations": ops})
	res := callTool(t, s, "concept_batch", string(raw))
	if res.IsError {
		t.Fatal(res.Content[0].Text)
	}
	if got := fmt.Sprint(fmValue(t, k, "q")); got != "[x]" {
		t.Errorf("batch append: %s", got)
	}
	if !strings.Contains(res.Content[0].Text, "list_items_dropped") {
		t.Errorf("batch must report the drop: %s", res.Content[0].Text)
	}
	// Append-only batch patch is valid; an error names the operation.
	ops[0]["if_match"] = readHash(t, k, listsID)
	ops[0] = map[string]any{"op": "patch", "id": listsID, "if_match": readHash(t, k, listsID), "frontmatter_append": map[string]any{"type": "x"}}
	raw, _ = json.Marshal(map[string]any{"operations": ops})
	if res := callTool(t, s, "concept_batch", string(raw)); !res.IsError || !strings.Contains(res.Content[0].Text, `cannot change "type"`) {
		t.Errorf("batch type refusal: %+v", res)
	}
}

func TestPatch_FindingsAlwaysPresentAndEditMatches(t *testing.T) {
	s, k := listsKB(t, "")
	out, e := patchCall(t, s, k, map[string]any{"old_string": "alpha", "new_string": "ALPHA"})
	if e != "" {
		t.Fatal(e)
	}
	if f, ok := out["findings"].([]any); !ok || len(f) != 0 {
		// Structural findings of the fixture page may exist; the key must be an array.
		if !ok {
			t.Errorf("findings must be an array: %v", out["findings"])
		}
	}
	if _, ok := out["edit_matches"]; ok {
		t.Errorf("single form has no edit_matches")
	}
	out, e = patchCall(t, s, k, map[string]any{"edits": []map[string]any{
		{"old_string": "ALPHA", "new_string": "a"},
		{"old_string": "line", "new_string": "ln", "replace_all": true},
		{"old_string": "beta", "new_string": "b"},
	}})
	if e != "" {
		t.Fatal(e)
	}
	if got := fmt.Sprint(out["edit_matches"]); got != "[1 2 1]" || out["replacements"].(float64) != 4 {
		t.Errorf("edit_matches = %s replacements = %v", got, out["replacements"])
	}
}

func TestApplyPatchEdit_Hints(t *testing.T) {
	body := "# Title\n\nIl cliente è già “pronto” qui\nsecond line\nsecond line\n"
	_, _, err := applyPatchEdit(body, "Il cliente e gia \"pronto\" qui", "x", false)
	if err == nil || !strings.Contains(err.Error(), `closest line 3: "Il cliente è già “pronto” qui"`) {
		t.Errorf("accent/quote hint: %v", err)
	}
	_, _, err = applyPatchEdit(body, "\n\nIl cliente e gia pronto qui\nanother", "x", false)
	if err == nil || !strings.Contains(err.Error(), "closest line 3") {
		t.Errorf("first non-empty line: %v", err)
	}
	_, _, err = applyPatchEdit(body, "completely unrelated sentence", "x", false)
	if err == nil || strings.Contains(err.Error(), "closest") || err.Error() != "old_string_not_found: no match for old_string" {
		t.Errorf("unrelated text must carry no hint: %v", err)
	}
	_, _, err = applyPatchEdit(body, "second line", "x", false)
	if err == nil || !strings.Contains(err.Error(), "matches 2 times (lines 4, 5)") {
		t.Errorf("ambiguous lines: %v", err)
	}
}

func TestPatch_BatchErrorNamesEditAndHint(t *testing.T) {
	s, k := listsKB(t, "")
	_, e := patchCall(t, s, k, map[string]any{"edits": []map[string]any{
		{"old_string": "alpha", "new_string": "a"},
		{"old_string": "bta line", "new_string": "b"},
	}})
	if !strings.Contains(e, "edit 2 of 2") || !strings.Contains(e, "closest line") {
		t.Errorf("error = %q", e)
	}
}
