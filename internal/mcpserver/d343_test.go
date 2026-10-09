package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// D343 WP1: every denial names a code, the "forbidden" prefix stays, and the
// exact-concept sites keep answering "not found".
func TestPolicyDenialsCarryReasonCodes(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "templates", "untyped.md"), []byte("---\ntitle: x\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "templates", "runbook.md"), []byte("---\ntype: Runbook\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ro := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"manutenzione"}}}}
	rw := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"manutenzione"}}}}
	cases := []struct {
		name   string
		policy auth.Policy
		tool   string
		args   string
		code   string
	}{
		{"unclassified", rw, "no_such_tool", `{}`, "unclassified_tool"},
		{"read-only write", ro, "concept_new", `{"id":"manutenzione/n","template":"runbook"}`, "read_only_token"},
		{"read-only whole-kb write", ro, "map_create", `{"name":"x"}`, "read_only_token"},
		{"scoped whole-kb", ro, "kb_status", `{}`, "needs_whole_kb"},
		{"service secrets", rw, "service_get", `{"service_id":"s","resolve_secrets":true}`, "needs_whole_kb"},
		{"move rewrite", rw, "concept_move", `{"source_id":"manutenzione/test-runbook","target_id":"manutenzione/m"}`, "needs_whole_kb"},
		{"archive scoped", rw, "concept_archive", `{"id":"manutenzione/test-runbook","if_match":"x"}`, "needs_whole_kb"},
		{"archive bad args", rw, "concept_archive", `{}`, "bad_arguments"},
		{"root index", rw, "index_patch", `{"path":"","old_string":"a","new_string":"b"}`, "needs_whole_kb"},
		{"bad args", rw, "concept_write", `[1]`, "bad_arguments"},
		{"bad batch op", rw, "concept_batch", `{"operations":[1]}`, "bad_arguments"},
		{"template missing", rw, "concept_new", `{"id":"manutenzione/n","template":"missing"}`, "template_unusable"},
		{"template untyped", rw, "concept_new", `{"id":"manutenzione/n","template":"untyped"}`, "template_unusable"},
		{"template outside scope", rw, "concept_new", `{"id":"other/n","template":"runbook"}`, "outside_scope"},
		{"move outside", rw, "concept_move", `{"source_id":"manutenzione/test-runbook","target_id":"other/m","rewrite_links":false}`, "outside_scope"},
		{"batch outside", rw, "concept_batch", `{"operations":[{"op":"write","id":"other/n","body":"x"}]}`, "outside_scope"},
		{"index outside", rw, "index_patch", `{"path":"other","old_string":"a","new_string":"b"}`, "outside_scope"},
		{"supersede outside", rw, "supersede", `{"source_id":"other/a","target_id":"other/b"}`, "outside_scope"},
		{"missing id", rw, "concept_write", `{"body":"x"}`, "missing_id"},
		{"scope outside", ro, "concept_list", `{"scope":"other"}`, "outside_scope"},
	}
	for _, c := range cases {
		err := authorizeTool(c.policy, k, "docs", c.tool, json.RawMessage(c.args))
		if err == nil || !strings.HasPrefix(err.Error(), "forbidden: "+c.code+": ") {
			t.Errorf("%s: got %v, want forbidden: %s", c.name, err, c.code)
		}
	}

	// The two template failures read identically (no probing of templates/).
	e1 := authorizeTool(rw, k, "docs", "concept_new", json.RawMessage(`{"id":"manutenzione/n","template":"missing"}`))
	e2 := authorizeTool(rw, k, "docs", "concept_new", json.RawMessage(`{"id":"manutenzione/n","template":"untyped"}`))
	if e1.Error() != e2.Error() {
		t.Errorf("template denials differ: %v / %v", e1, e2)
	}
	// A batch denial never names the operation or its id.
	be := authorizeTool(rw, k, "docs", "concept_batch", json.RawMessage(`{"operations":[{"op":"write","id":"other/secret-n","body":"x"}]}`))
	if be == nil || strings.Contains(be.Error(), "other/secret-n") {
		t.Errorf("batch denial discloses the operation: %v", be)
	}
	// Exact-concept sites stay generic.
	for _, tool := range []string{"concept_read", "concept_write"} {
		err := authorizeTool(rw, k, "docs", tool, json.RawMessage(`{"id":"other/x","body":"x"}`))
		if err == nil || err.Error() != genericNotFound {
			t.Errorf("%s outside scope: got %v, want %q", tool, err, genericNotFound)
		}
	}
}

func TestMapListExposesFieldValues(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	callOK(t, s, "map_create", `{"name":"fv-map","title":"FV","field_values":{"tags":["a","b"]},"field_values_by_type":{"Incident":{"tags":["c"]}}}`)
	var infos []struct {
		Name              string                         `json:"name"`
		FieldValues       map[string][]string            `json:"field_values"`
		FieldValuesByType map[string]map[string][]string `json:"field_values_by_type"`
	}
	if err := json.Unmarshal([]byte(callOK(t, s, "map_list", `{}`)), &infos); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, i := range infos {
		switch i.Name {
		case "fv-map":
			seen++
			if strings.Join(i.FieldValues["tags"], ",") != "a,b" || strings.Join(i.FieldValuesByType["Incident"]["tags"], ",") != "c" {
				t.Errorf("fv-map vocabularies: %+v", i)
			}
		case "manutenzione":
			seen++
			if i.FieldValues != nil || i.FieldValuesByType != nil {
				t.Errorf("map without contract carries vocabularies: %+v", i)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("maps seen = %d in %+v", seen, infos)
	}
	// A scoped token sees only its visible maps.
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"manutenzione"}}}})
	tr := s.callTool(ctx, "map_list", json.RawMessage(`{}`))
	if tr.IsError || strings.Contains(tr.Content[0].Text, "fv-map") {
		t.Errorf("scoped map_list leaks: %+v", tr)
	}
}

func TestConceptListStatusAndFields(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	dir := filepath.Join(k.DataRoot(), "inv")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"a.md":   "---\ntype: Note\ntitle: A\nstatus: open\npriority: P1\ntags: [x, y]\nnested:\n  k: v\nempty: \"\"\n---\nbody\n",
		"bad.md": "---\ntype: Note\ntitle: [unclosed\n---\nbody\n",
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out struct {
		Results []struct {
			ID     string         `json:"id"`
			Status string         `json:"status"`
			Fields map[string]any `json:"fields"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(callOK(t, s, "concept_list", `{"scope":"inv","fields":["priority","tags","nested","missing","empty"]}`)), &out); err != nil {
		t.Fatal(err)
	}
	for _, r := range out.Results {
		switch r.ID {
		case "inv/a":
			if r.Status != "open" || r.Fields["priority"] != "P1" || fmt.Sprint(r.Fields["tags"]) != "[x y]" {
				t.Errorf("inv/a: %+v", r)
			}
			if _, ok := r.Fields["nested"]; ok {
				t.Errorf("nested value returned: %+v", r.Fields)
			}
			if _, ok := r.Fields["missing"]; ok {
				t.Errorf("absent key returned: %+v", r.Fields)
			}
			if v, ok := r.Fields["empty"]; !ok || v != "" {
				t.Errorf("empty string field = %v, %v", v, ok)
			}
		case "inv/bad":
			if len(r.Fields) != 0 {
				t.Errorf("malformed frontmatter returned fields: %+v", r)
			}
		}
	}
	if len(out.Results) != 2 {
		t.Fatalf("results = %+v", out.Results)
	}
	tr := s.callTool(authLocalContext(), "concept_list", json.RawMessage(`{"fields":["a","b","c","d","e","f","g","h","i"]}`))
	if !tr.IsError || !strings.Contains(tr.Content[0].Text, "at most 8") {
		t.Errorf("9 fields accepted: %+v", tr)
	}
	tr = s.callTool(authLocalContext(), "concept_list", json.RawMessage(`{"fields":[""]}`))
	if !tr.IsError {
		t.Errorf("empty field key accepted")
	}
}

type orHit struct {
	ID      string `json:"id"`
	Partial bool   `json:"partial"`
}

type orResp struct {
	Results    []orHit `json:"results"`
	OrFallback bool    `json:"or_fallback"`
	Note       string  `json:"note"`
}

func writeORFixture(t *testing.T, k *kb.KB) {
	t.Helper()
	dir := filepath.Join(k.DataRoot(), "orf")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The AND page repeats nothing; the OR pages repeat one term a lot, so by
	// raw score they would outrank it.
	files := map[string]string{
		"full":  "gitea token orfano esposto once",
		"only1": "gitea gitea gitea gitea gitea gitea gitea gitea",
		"only2": "token token token token token token token token",
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(dir, n+".md"), []byte("---\ntype: Note\ntitle: "+n+"\n---\n"+c+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchOrFallbackBelowFloorIsMarked(t *testing.T) {
	freshnessBackends(t, func(t *testing.T, k *kb.KB, s *Server) {
		writeORFixture(t, k)
		var r orResp
		if err := json.Unmarshal([]byte(searchText(t, s, "gitea token orfano esposto")), &r); err != nil {
			t.Fatal(err)
		}
		if len(r.Results) != 3 || r.Results[0].ID != "orf/full" || r.Results[0].Partial {
			t.Fatalf("results = %+v", r.Results)
		}
		for _, h := range r.Results[1:] {
			if !h.Partial {
				t.Errorf("OR-only hit not partial: %+v", h)
			}
		}
		if !r.OrFallback || !strings.Contains(r.Note, "partially") {
			t.Errorf("response not marked: %+v", r)
		}
		// A one-term query is never partial.
		var one orResp
		if err := json.Unmarshal([]byte(searchText(t, s, "gitea")), &one); err != nil {
			t.Fatal(err)
		}
		if one.OrFallback {
			t.Errorf("one-term query marked: %+v", one)
		}
		// Zero AND hits: still marked.
		var zero orResp
		if err := json.Unmarshal([]byte(searchText(t, s, "gitea token")), &zero); err != nil {
			t.Fatal(err)
		}
		if len(zero.Results) == 0 {
			t.Fatal("no hits")
		}
		for _, h := range zero.Results {
			if h.ID == "orf/full" && h.Partial {
				t.Errorf("full match flagged partial: %+v", h)
			}
		}
	})
}

func TestSearchOrFallbackHiddenHitsDoNotCountTowardFloor(t *testing.T) {
	freshnessBackends(t, func(t *testing.T, k *kb.KB, s *Server) {
		writeORFixture(t, k)
		hidden := filepath.Join(k.DataRoot(), "secret")
		if err := os.MkdirAll(hidden, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 4; i++ {
			c := "---\ntype: Note\ntitle: h\n---\ngitea token orfano esposto\n"
			if err := os.WriteFile(filepath.Join(hidden, fmt.Sprintf("h%d.md", i)), []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		k.AuthName = "docs"
		ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"orf"}}}})
		tr := s.callTool(ctx, "search", json.RawMessage(`{"query":"gitea token orfano esposto"}`))
		if tr.IsError {
			t.Fatalf("%+v", tr.Content)
		}
		var r orResp
		if err := json.Unmarshal([]byte(tr.Content[0].Text), &r); err != nil {
			t.Fatal(err)
		}
		if !r.OrFallback || len(r.Results) != 3 {
			t.Fatalf("hidden AND hits suppressed the OR pass: %+v", r)
		}
	})
}
