package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// workServer: open work in "visible" (with KB-own priority/owner fields) and
// in "hidden" (D302).
func workServer(t *testing.T) (*Server, *kb.KB) {
	t.Helper()
	k, err := kb.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	k.AuthName = "docs"
	for _, m := range []string{"visible", "hidden"} {
		writeKBFile(t, k, m+"/_map.md", "---\ntype: Map\ntitle: "+m+"\n---\n")
	}
	writeKBFile(t, k, "visible/a.md", "---\ntype: Task\ntitle: A\nstatus: open\npriority: p2\nowner: user\ntags: [x, y]\n---\n# A\n")
	writeKBFile(t, k, "visible/b.md", "---\ntype: Topic\ntitle: B\nstatus: in-progress\npriority: p1\n---\n# B\n")
	writeKBFile(t, k, "visible/c.md", "---\ntype: Note\ntitle: C\nstatus: done\n---\n# C\n\n## Todo\n\n- [ ] one\n- [ ] two\n")
	writeKBFile(t, k, "visible/d.md", "---\ntype: Note\ntitle: D\n---\n# D\n\n- [x] closed\n")
	writeKBFile(t, k, "hidden/h.md", "---\ntype: Task\ntitle: H\nstatus: open\n---\n# H\n\n- [ ] secret item\n")
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return s, k
}

func workCall(t *testing.T, s *Server, args string) workResponse {
	t.Helper()
	res := callTool(t, s, "work_list", args)
	if res.IsError {
		t.Fatalf("work_list %s: %s", args, res.Content[0].Text)
	}
	var out workResponse
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func workIDs(r workResponse) string {
	var ids []string
	for _, e := range r.Entries {
		ids = append(ids, e.ID)
	}
	return strings.Join(ids, ",")
}

func TestWorkList(t *testing.T) {
	s, _ := workServer(t)
	all := workCall(t, s, `{"scope":"visible"}`)
	if workIDs(all) != "visible/a,visible/b,visible/c" || all.Total != 3 || all.OpenConcepts != 2 || all.OpenItems != 2 || len(all.ByStatus) != 2 || all.ByStatus["done"] != 0 || all.ByMap["visible"] != 3 {
		t.Fatalf("all: %s %+v", workIDs(all), all)
	}
	if c := all.Entries[2]; c.OpenPhase || len(c.Items) != 2 || c.Items[0].Section != "Todo" {
		t.Fatalf("done concept with items: %+v", c)
	}
	if got := workIDs(workCall(t, s, `{"scope":"visible","include":"concepts"}`)); got != "visible/a,visible/b" {
		t.Fatalf("include concepts: %s", got)
	}
	if got := workIDs(workCall(t, s, `{"scope":"visible","include":"items"}`)); got != "visible/c" {
		t.Fatalf("include items: %s", got)
	}
	// where: =, != (a missing key matches != only), list fields match any element.
	for args, want := range map[string]string{
		`{"scope":"visible","where":["priority=p1"]}`:  "visible/b",
		`{"scope":"visible","where":["priority!=p1"]}`: "visible/a,visible/c",
		`{"scope":"visible","where":["tags=y"]}`:       "visible/a",
	} {
		if got := workIDs(workCall(t, s, args)); got != want {
			t.Fatalf("%s: %s, want %s", args, got, want)
		}
	}
	// order_by: strings, missing last; fields: scalars only.
	r := workCall(t, s, `{"scope":"visible","order_by":["priority"],"fields":["owner","tags"]}`)
	if workIDs(r) != "visible/b,visible/a,visible/c" {
		t.Fatalf("order_by: %s", workIDs(r))
	}
	if f := r.Entries[1].Fields; f["owner"] != "user" || len(f) != 1 || r.Entries[0].Fields["owner"] != "" {
		t.Fatalf("fields: %+v", r.Entries)
	}
	// Pagination is stable and counts are over the filtered set.
	p1 := workCall(t, s, `{"scope":"visible","limit":2}`)
	p2 := workCall(t, s, `{"scope":"visible","limit":2,"offset":2}`)
	if workIDs(p1)+","+workIDs(p2) != workIDs(all) || p1.NextOffset == nil || *p1.NextOffset != 2 || p2.NextOffset != nil || p1.Total != 3 {
		t.Fatalf("pages: %s | %s", workIDs(p1), workIDs(p2))
	}
	if res := callTool(t, s, "work_list", `{"include":"tasks"}`); !res.IsError {
		t.Fatal("bad include accepted")
	}
}

// TestWorkListRestrictedCaller is the D302 trap: a hidden concept never
// contributes to work_list's entries or counts, nor to kb_status.work.
func TestWorkListRestrictedCaller(t *testing.T) {
	s, _ := workServer(t)
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}})
	res := s.callTool(ctx, "work_list", json.RawMessage(`{}`))
	if res.IsError {
		t.Fatalf("map-scoped token refused: %s", res.Content[0].Text)
	}
	text := res.Content[0].Text
	var out workResponse
	_ = json.Unmarshal([]byte(text), &out)
	if strings.Contains(text, "hidden") || out.Total != 3 || out.OpenItems != 2 {
		t.Fatalf("hidden work leaked: %s", text)
	}
	st, err := s.Tools()["kb_status"].Handler(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Work map[string]int `json:"work"`
	}
	_ = json.Unmarshal([]byte(st.Content[0].Text), &status)
	if status.Work["open_concepts"] != 2 || status.Work["open_items"] != 2 {
		t.Fatalf("kb_status.work counts a hidden concept: %+v", status.Work)
	}
}

// TestWorkListDescriptionBudget pins the D302 cap on work_list's own text.
func TestWorkListDescriptionBudget(t *testing.T) {
	if n := utf8.RuneCountInString(toolWorkList(nil, nil).Description); n > 350 {
		t.Fatalf("work_list description is %d chars, cap 350 (D302)", n)
	}
}
