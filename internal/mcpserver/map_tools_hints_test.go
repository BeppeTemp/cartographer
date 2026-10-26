package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func mapToolsServer(t *testing.T) (*Server, func(name, args string) (map[string]any, ToolResult)) {
	t.Helper()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return s, func(name, args string) (map[string]any, ToolResult) {
		t.Helper()
		res := callTool(t, s, name, args)
		var out map[string]any
		if !res.IsError {
			if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
				t.Fatalf("%s: not JSON: %v: %s", name, err, res.Content[0].Text)
			}
		}
		return out, res
	}
}

// concept_types makes a map strict on map_create too (#836 WP1): nothing is refused.
func TestMapCreate_ConceptTypesMakeTheMapStrict(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	if res := callTool(t, s, "map_create", `{"name":"strict-map","title":"Strict","concept_types":["Note"]}`); res.IsError {
		t.Fatalf("map_create refused concept_types: %+v", res.Content)
	}
	if c, err := k.ReadMapContract("strict-map"); err != nil || c.OntologyMode != "strict" || len(c.ConceptTypes) != 1 {
		t.Fatalf("contract = %+v, %v", c, err)
	}
	if res := callTool(t, s, "map_create", `{"name":"flex-map","title":"Flex"}`); res.IsError {
		t.Fatal(res.Content)
	}
	if c, err := k.ReadMapContract("flex-map"); err != nil || c.OntologyMode != "flexible" {
		t.Fatalf("contract = %+v, %v", c, err)
	}
}

func TestMapToolsSharedEnvelopeKeys(t *testing.T) {
	t.Parallel()
	_, call := mapToolsServer(t)
	cases := []struct{ args, kind, mode string }{
		{`{"name":"plain","title":"Plain"}`, "map", "flexible"},
		{`{"name":"jrnl","title":"Journal","kind":"journal"}`, "journal", "flexible"},
		{`{"name":"strict","title":"Strict","concept_types":["Note"]}`, "map", "strict"},
	}
	for _, c := range cases {
		out, res := call("map_create", c.args)
		if res.IsError {
			t.Fatalf("map_create %s: %+v", c.args, res.Content)
		}
		if out["status"] != "created" || out["map"] == nil || out["title"] == nil || out["kind"] != c.kind || out["ontology_mode"] != c.mode {
			t.Errorf("map_create envelope = %v, want kind %s mode %s", out, c.kind, c.mode)
		}
	}

	out, res := call("map_update", `{"map":"plain","title":"Plain 2"}`)
	if res.IsError {
		t.Fatal(res.Content)
	}
	if out["kind"] != "map" || out["ontology_mode"] != "flexible" || out["status"] != "updated" || out["title"] != "Plain 2" || out["contract"] == nil {
		t.Errorf("map_update envelope = %v", out)
	}

	out, res = call("map_delete", `{"map":"plain"}`)
	if res.IsError {
		t.Fatal(res.Content)
	}
	if out["status"] != "deleted" || out["map"] != "plain" || out["title"] != "Plain 2" || out["kind"] != "map" {
		t.Errorf("map_delete envelope = %v", out)
	}
}

func TestMapListEntriesCarryMapKey(t *testing.T) {
	t.Parallel()
	s, call := mapToolsServer(t)
	call("map_create", `{"name":"m1","title":"M One"}`)
	res := callTool(t, s, "map_list", `{}`)
	var entries []map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &entries); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e["map"] != e["name"] || e["name"] == nil {
			t.Errorf("entry map != name: %v", e)
		}
		found = found || e["name"] == "m1"
	}
	if !found {
		t.Fatalf("m1 missing: %v", entries)
	}
}

func TestMapCreate_MapAlias(t *testing.T) {
	t.Parallel()
	s, call := mapToolsServer(t)
	if out, res := call("map_create", `{"map":"only-map","title":"T"}`); res.IsError || out["map"] != "only-map" {
		t.Fatalf("map-only: %v %+v", out, res.Content)
	}
	if out, res := call("map_create", `{"name":"same","map":"same","title":"T"}`); res.IsError || out["map"] != "same" {
		t.Fatalf("equal: %v %+v", out, res.Content)
	}
	if out, res := call("map_create", `{"name":"only-name","title":"T"}`); res.IsError || out["map"] != "only-name" {
		t.Fatalf("name-only: %v %+v", out, res.Content)
	}
	_, res := call("map_create", `{"name":"a","map":"b","title":"T"}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, `"a"`) || !strings.Contains(res.Content[0].Text, `"b"`) {
		t.Fatalf("differing: %+v", res.Content)
	}
	_, res = call("map_create", `{"title":"T"}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "'name' is required") {
		t.Fatalf("neither: %+v", res.Content)
	}
	// The alias stays hidden: it would grow tools/list (D285).
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(s.Tools()["map_create"].InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if _, ok := schema.Properties["map"]; ok {
		t.Fatal("map_create must not advertise the 'map' alias")
	}
}

func TestMapToolsActionableErrors(t *testing.T) {
	t.Parallel()
	_, call := mapToolsServer(t)
	_, res := call("map_create", `{"name":"bad","title":"B","templates":["a"],"default_template":"b"}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "same call") || !strings.Contains(res.Content[0].Text, "default_template") {
		t.Fatalf("map_create: %+v", res.Content)
	}
	call("map_create", `{"name":"tm","title":"T","templates":["a"]}`)
	_, res = call("map_update", `{"map":"tm","templates":["a"],"default_template":"b"}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "same call") {
		t.Fatalf("map_update handler: %+v", res.Content)
	}
	_, res = call("map_update", `{"map":"tm","default_template":"b"}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "same call") || !strings.Contains(res.Content[0].Text, "default_template") {
		t.Fatalf("map_update kb: %+v", res.Content)
	}
}

func TestConceptBatchMoveHint(t *testing.T) {
	t.Parallel()
	_, call := mapToolsServer(t)
	_, res := call("concept_batch", `{"operations":[{"op":"move","id":"a/b"}]}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "concept_move") {
		t.Fatalf("move: %+v", res.Content)
	}
	_, res = call("concept_batch", `{"operations":[{"op":"delete","id":"a/b"}]}`)
	if !res.IsError || strings.Contains(res.Content[0].Text, "concept_move") {
		t.Fatalf("delete: %+v", res.Content)
	}
}
