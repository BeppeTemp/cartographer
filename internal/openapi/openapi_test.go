package openapi

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

type leaf struct {
	Name string `json:"name"`
	Opt  string `json:"opt,omitempty"`
	Skip string `json:"-"`
	priv string
}

type apiNode struct {
	When  time.Time        `json:"when"`
	Tags  []string         `json:"tags"`
	Attrs map[string]int   `json:"attrs"`
	Leaf  *leaf            `json:"leaf"`
	Kids  []apiNode        `json:"kids,omitempty"`
	Any   interface{}      `json:"any"`
	Raw   json.RawMessage  `json:"raw"`
	Anon  struct{ X bool } `json:"anon"`
}

func TestReflector_Tags(t *testing.T) {
	r := NewReflector()
	ref := r.Schema(reflect.TypeOf(apiNode{}))
	if ref["$ref"] != "#/components/schemas/Node" {
		t.Fatalf("ref = %v (the api prefix is dropped)", ref)
	}
	node := r.comps["Node"]
	props := node["properties"].(schema)
	if props["when"].(schema)["format"] != "date-time" {
		t.Errorf("time: %v", props["when"])
	}
	if props["tags"].(schema)["items"].(schema)["type"] != "string" {
		t.Errorf("slice: %v", props["tags"])
	}
	if props["attrs"].(schema)["additionalProperties"].(schema)["type"] != "integer" {
		t.Errorf("map: %v", props["attrs"])
	}
	if props["leaf"].(schema)["$ref"] != "#/components/schemas/Leaf" {
		t.Errorf("pointer: %v", props["leaf"])
	}
	if props["kids"].(schema)["items"].(schema)["$ref"] != "#/components/schemas/Node" {
		t.Errorf("recursion: %v", props["kids"])
	}
	if props["anon"].(schema)["type"] != "object" {
		t.Errorf("anonymous struct is inline: %v", props["anon"])
	}
	leafProps := r.comps["Leaf"]["properties"].(schema)
	if _, ok := leafProps["Skip"]; ok {
		t.Error("json:- must be skipped")
	}
	if _, ok := leafProps["priv"]; ok {
		t.Error("unexported field")
	}
	if got := strings.Join(r.comps["Leaf"]["required"].([]string), ","); got != "name" {
		t.Errorf("omitempty is not required: %s", got)
	}
	req := strings.Join(node["required"].([]string), ",")
	if strings.Contains(req, "kids") || !strings.Contains(req, "tags") {
		t.Errorf("required = %s", req)
	}
}

type dup1 struct{ A int }

func TestReflector_NameCollisionFails(t *testing.T) {
	type dup struct{ B int }
	r := NewReflector()
	r.owners["Dup1"] = reflect.TypeOf(dup{})
	r.Schema(reflect.TypeOf(dup1{}))
	if r.err == nil {
		t.Fatal("two types named alike must fail")
	}
}

func TestBuild_Document(t *testing.T) {
	type env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	d := Document{Title: "t", Version: "1", Error: reflect.TypeOf(env{}),
		Ops: []Operation{
			{ID: "a", Path: "/a", Summary: "A", Stability: "stable", Response: reflect.TypeOf(leaf{}), Errors: []int{404},
				Params: []Param{{Name: "id", In: "query", Required: true}}},
			{ID: "b", Path: "/b", Summary: "B", Stability: "preview", Public: true},
		}}
	raw, err := Build(d)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	b := paths["/b"].(map[string]any)["get"].(map[string]any)
	if sec, ok := b["security"].([]any); !ok || len(sec) != 0 {
		t.Errorf("public op security = %v", b["security"])
	}
	ok200 := b["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if ok200["additionalProperties"] != true {
		t.Errorf("free-form response = %v", ok200)
	}
	if _, ok := paths["/a"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)["404"]; !ok {
		t.Error("declared error missing")
	}
	again, _ := Build(d)
	if string(again) != string(raw) {
		t.Error("output is not deterministic")
	}
}

func TestReflector_NullableIsForSlices(t *testing.T) {
	type ok struct {
		L []string `json:"l" openapi:"nullable"`
	}
	r := NewReflector()
	r.Schema(reflect.TypeOf(ok{}))
	if r.err != nil {
		t.Fatalf("slice: %v", r.err)
	}
	if got := r.comps["Ok"]["properties"].(schema)["l"].(schema)["type"]; !reflect.DeepEqual(got, []any{"array", "null"}) {
		t.Errorf("type = %v", got)
	}
	type bad struct {
		P *leaf  `json:"p" openapi:"nullable"`
		S string `json:"s" openapi:"nullable"`
	}
	for _, typ := range []reflect.Type{reflect.TypeOf(bad{})} {
		r := NewReflector()
		r.Schema(typ)
		if r.err == nil || !strings.Contains(r.err.Error(), "slice fields only") {
			t.Errorf("a nullable tag on a non-slice must fail the build, got %v", r.err)
		}
	}
	if _, err := Build(Document{Title: "t", Version: "1", Error: reflect.TypeOf(bad{})}); err == nil {
		t.Error("Build must surface the refusal")
	}
}

func TestBuild_EventStreamOperation(t *testing.T) {
	raw, err := Build(Document{
		Title: "t", Version: "1", Error: reflect.TypeOf(leaf{}),
		Ops: []Operation{{ID: "events", Path: "/events", Errors: []int{429},
			Events: []Event{{Name: "tick", Type: reflect.TypeOf(leaf{})}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	ok := doc["paths"].(map[string]any)["/events"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)
	if _, json := ok["application/json"]; json {
		t.Error("a stream is not application/json")
	}
	sse := ok["text/event-stream"].(map[string]any)
	if sse["x-events"].(map[string]any)["tick"] == nil {
		t.Errorf("event schema missing: %v", sse)
	}
}
