package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
)

// workServer: open work in "visible" (with KB-own priority/owner fields) and
// in "hidden" (D302).
func workServer(t *testing.T) (*Server, *kb.KB) {
	t.Helper()
	k := kbtest.New(t)
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	if n := utf8.RuneCountInString(toolWorkList(nil, nil).Description); n > 350 {
		t.Fatalf("work_list description is %d chars, cap 350 (D302)", n)
	}
}

// D347: with an open_field, by_status groups by the field's value.
func TestWorkList_OpenFieldState(t *testing.T) {
	t.Parallel()
	k := kbtest.New(t)
	writeKBFile(t, k, "j/_map.md", "---\ntype: Map\ntitle: J\nkind: journal\nopen_field: outcome\nopen_statuses: [open]\n---\n")
	writeKBFile(t, k, "j/a.md", "---\ntype: Incident\ntitle: A\nstatus: active\noutcome: open\n---\n# A\n")
	writeKBFile(t, k, "j/b.md", "---\ntype: Incident\ntitle: B\nstatus: active\noutcome: resolved\n---\n# B\n")
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	r := workCall(t, s, `{}`)
	if r.OpenConcepts != 1 || r.ByStatus["open"] != 1 || len(r.ByStatus) != 1 {
		t.Fatalf("by_status: %+v", r)
	}
}

// D948: a concept that is work by its reason (an open contradiction, an open
// gap, a page past review_after) is counted in open_concepts and by_reason, not
// in by_status, and carries its reason; a retired page past review_after is not
// due; a KB without a reason leaves by_reason out.
func TestWorkList_Reason(t *testing.T) {
	t.Parallel()
	k, err := kb.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeKBFile(t, k, "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	writeKBFile(t, k, "m/contra.md", "---\ntype: Contradiction\ntitle: C\nresolution_status: open\ncontradiction_kind: value_mismatch\ninvolves: [m/a, m/b]\n---\n# C\n")
	writeKBFile(t, k, "m/gap.md", "---\ntype: Contradiction\ntitle: G\ncontradiction_kind: open_question\n---\n# G\n")
	writeKBFile(t, k, "m/gap2.md", "---\ntype: Contradiction\ntitle: G2\ncontradiction_kind: missing_context\n---\n# G2\n")
	writeKBFile(t, k, "m/done.md", "---\ntype: Contradiction\ntitle: D\nresolution_status: resolved\n---\n# D\n")
	writeKBFile(t, k, "m/stale.md", "---\ntype: Topic\ntitle: S\nstatus: active\nreview_after: 2020-01-01\n---\n# S\n")
	writeKBFile(t, k, "m/old.md", "---\ntype: Topic\ntitle: O\nstatus: superseded\nreview_after: 2020-01-01\n---\n# O\n")
	writeKBFile(t, k, "m/later.md", "---\ntype: Topic\ntitle: L\nstatus: active\nreview_after: 2999-01-01\n---\n# L\n")
	writeKBFile(t, k, "m/task.md", "---\ntype: Task\ntitle: T\nstatus: open\n---\n# T\n")
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	r := workCall(t, s, `{"fields":["contradiction_kind"]}`)
	if workIDs(r) != "m/contra,m/gap,m/gap2,m/stale,m/task" || r.Total != 5 || r.OpenConcepts != 5 {
		t.Fatalf("entries: %s %+v", workIDs(r), r)
	}
	if len(r.ByReason) != 3 || r.ByReason["contradiction"] != 1 || r.ByReason["gap"] != 2 || r.ByReason["review"] != 1 {
		t.Fatalf("by_reason: %+v", r.ByReason)
	}
	// by_status counts the concepts open by their status only, so it no longer
	// sums to open_concepts.
	if len(r.ByStatus) != 1 || r.ByStatus["open"] != 1 {
		t.Fatalf("by_status: %+v", r.ByStatus)
	}
	for _, e := range r.Entries {
		want := map[string]string{"m/contra": "contradiction", "m/gap": "gap", "m/gap2": "gap", "m/stale": "review", "m/task": ""}[e.ID]
		if e.Reason != want || !e.OpenPhase {
			t.Errorf("%s: reason %q open_phase %v, want %q open", e.ID, e.Reason, e.OpenPhase, want)
		}
	}
	// The kind of each contradiction, for the caller that asked for it.
	if r.Entries[0].Fields["contradiction_kind"] != "value_mismatch" || r.Entries[1].Fields["contradiction_kind"] != "open_question" {
		t.Fatalf("fields: %+v", r.Entries)
	}
	// A reason is work in the concepts view, and filters like any field.
	if got := workIDs(workCall(t, s, `{"include":"concepts"}`)); got != "m/contra,m/gap,m/gap2,m/stale,m/task" {
		t.Fatalf("include concepts: %s", got)
	}
	if got := workIDs(workCall(t, s, `{"where":["contradiction_kind=open_question"]}`)); got != "m/gap" {
		t.Fatalf("where: %s", got)
	}
	// kb_status.work counts the same concepts as open_concepts.
	st, err := s.Tools()["kb_status"].Handler(authLocalContext(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Work map[string]int `json:"work"`
	}
	_ = json.Unmarshal([]byte(st.Content[0].Text), &status)
	if status.Work["open_concepts"] != 5 {
		t.Fatalf("kb_status.work: %+v", status.Work)
	}

	// No reason in the KB: by_reason and every entry's reason are left out.
	plain, _ := workServer(t)
	text := callTool(t, plain, "work_list", `{}`).Content[0].Text
	if strings.Contains(text, "by_reason") || strings.Contains(text, `"reason"`) {
		t.Fatalf("a KB without a reason reports one: %s", text)
	}
}
