package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// doctorKB is a scripted KB: kb_status answers from st, kb_review from items, and
// log_append and concept_patch record what reached the server.
type doctorKB struct {
	st      doctorStatus
	items   []doctorItem
	log     string // the log_tail answer
	entries []map[string]any
	patches []map[string]any
	calls   []string
	failing bool // every write answers a refusal
	repairs []map[string]any
	maps    []map[string]any // map_update and index_patch arguments
	pages   map[string]string
	perPage int // kb_review items per page, 0 for one page
	links   map[string][]string
}

func (k *doctorKB) ListTools() ([]client.ToolInfo, error) {
	var out []client.ToolInfo
	for _, n := range []string{"kb_status", "kb_repair", "kb_review", "index_get", "index_patch", "log_tail", "log_append", "search", "concept_read", "concept_patch", "concept_write", "concept_batch", "map_update", "concept_delete", "artifact_write"} {
		out = append(out, client.ToolInfo{Name: n, InputSchema: json.RawMessage(`{"type":"object"}`)})
	}
	return out, nil
}

func (k *doctorKB) CallTool(name string, args any) (client.ToolOutput, error) {
	k.calls = append(k.calls, name)
	var m map[string]any
	_ = json.Unmarshal(rawJSON(args), &m)
	switch name {
	case "kb_status":
		b, _ := json.Marshal(k.st)
		return client.ToolOutput{Text: string(b)}, nil
	case "kb_review":
		var items []doctorItem
		for _, it := range k.items {
			if it.Kind == m["kind"] {
				items = append(items, it)
			}
		}
		page := map[string]any{"items": items}
		if offset, _ := m["offset"].(float64); k.perPage > 0 {
			from := min(int(offset), len(items))
			to := min(from+k.perPage, len(items))
			page["items"] = items[from:to]
			if to < len(items) {
				page["next_offset"] = to
			}
		}
		b, _ := json.Marshal(page)
		return client.ToolOutput{Text: string(b)}, nil
	case "log_tail":
		return client.ToolOutput{Text: k.log}, nil
	case "concept_read":
		if text, ok := k.pages[fmt.Sprint(m["id"])]; ok {
			return client.ToolOutput{Text: text}, nil
		}
	case "graph_neighbors":
		var out []map[string]any
		for _, id := range k.links[fmt.Sprint(m["id"])] {
			out = append(out, map[string]any{"id": id})
		}
		b, _ := json.Marshal(map[string]any{"neighbors": append(out, map[string]any{"id": "broken/link", "missing": true})})
		return client.ToolOutput{Text: string(b)}, nil
	case "kb_repair":
		k.repairs = append(k.repairs, m)
		return client.ToolOutput{Text: `{"applied_total":3,"checks":[],"dry_run":false}`}, nil
	case "map_update", "index_patch":
		k.maps = append(k.maps, m)
	case "concept_patch":
		if k.failing {
			return client.ToolOutput{IsError: true, Text: "forbidden"}, nil
		}
		k.patches = append(k.patches, m)
	case "log_append":
		k.entries = append(k.entries, m)
	}
	return client.ToolOutput{Text: "ok"}, nil
}

type doctorScript struct {
	turns []Response
	reqs  []Request
}

func (m *doctorScript) Complete(_ context.Context, r Request) (Response, error) {
	m.reqs = append(m.reqs, r)
	if len(m.reqs) > len(m.turns) {
		return Response{}, fmt.Errorf("script exhausted")
	}
	return m.turns[len(m.reqs)-1], nil
}

func toolCall(id, name, args string) ToolCall {
	return ToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
}

func item(kind string, pages ...string) doctorItem {
	return doctorItem{Kind: kind, Concepts: pages, Evidence: "evidence of " + pages[0], SuggestedAction: "set status to done"}
}

func dueKB(items ...doctorItem) (*doctorKB, *doctorInstance) {
	k := &doctorKB{items: items}
	now := time.Date(2026, 10, 30, 3, 0, 0, 0, time.UTC)
	return k, &doctorInstance{now: func() time.Time { return now }, perimeter: "docs-night"}
}

func doctorRun(d *doctorInstance, k *doctorKB, m Model, grant int) (Result, error) {
	return d.Run(context.Background(), &WorkItem{Key: "doctor"}, Runtime{KB: "kb-a", Perimeter: "docs-night", Model: m, Grant: grant, Tools: k})
}

func patchTurn(id, args string) Response {
	return Response{Stop: StopToolUse, ToolCalls: []ToolCall{toolCall(id, "concept_patch", args)}, Usage: Usage{InputTokens: 100}}
}

func TestDoctorNextEligibility(t *testing.T) {
	for name, tc := range map[string]struct {
		mod  func(*doctorKB)
		want bool
	}{
		"an item":            {func(*doctorKB) {}, true},
		"degraded":           {func(k *doctorKB) { k.st.PushState = "degraded" }, false},
		"nothing to examine": {func(k *doctorKB) { k.items = nil }, false},
		"only an operator's kind": {func(k *doctorKB) {
			k.items = []doctorItem{item("template_proposal", "a/_map"), {Kind: "lint_judgement", Check: "stray_file", Concepts: []string{"a"}}}
		}, false},
	} {
		k, d := dueKB(item("status_reclassify", "a"))
		tc.mod(k)
		it, err := d.Next(context.Background(), "kb-a", k)
		if err != nil || (it != nil) != tc.want {
			t.Errorf("%s: item %v err %v", name, it, err)
		}
	}
}

func TestDoctorEachItemIsAFreshSmallLoop(t *testing.T) {
	k, d := dueKB(item("status_reclassify", "a"), item("zombie_work", "b"))
	m := &doctorScript{turns: []Response{
		patchTurn("1", `{"id":"a","if_match":"h","frontmatter":{"status":"done"}}`),
		{Stop: StopEnd},
		{Stop: StopEnd, Text: "left as it is"},
	}}
	res, err := doctorRun(d, k, m, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	if res.Units != 2 || res.Commits != 1 {
		t.Errorf("result %+v", res)
	}
	if strings.Contains(fmt.Sprint(m.reqs[2].Messages), "status_reclassify") {
		t.Error("the second item's loop carries the first item's context")
	}
	for i, r := range m.reqs {
		if got := len(r.Tools); got != 3 {
			t.Errorf("request %d offers %d tools", i, got)
		}
	}
	if len(m.reqs[0].System) > 2<<10 {
		t.Errorf("instructions of one item: %d bytes", len(m.reqs[0].System))
	}
	if !strings.Contains(m.reqs[0].System, "journal") || !strings.Contains(m.reqs[2].System, "origin is retired") {
		t.Error("each item gets the rule of its kind")
	}
	if !strings.Contains(m.reqs[0].Messages[0].Text, "BEGIN DATA item") {
		t.Errorf("the item is not quoted as data: %q", m.reqs[0].Messages[0].Text)
	}
	if reason := k.patches[0]["reason"]; reason != "doctor: "+item("status_reclassify", "a").key() {
		t.Errorf("commit reason %v", reason)
	}
}

func TestDoctorRecordSkipsExaminedItemsUntilTheyExpire(t *testing.T) {
	seen, old, fresh := item("status_reclassify", "a"), item("status_reclassify", "b"), item("status_reclassify", "c")
	k, d := dueKB(seen, old, fresh)
	path := doctorLogPath("docs-night")
	k.log = fmt.Sprintf("## 2026-10-29T03:00:00Z\n\n[%s] day=2026-10-29 examined=%s writes=1\n\n## 2026-09-01T03:00:00Z\n\n[%s] day=2026-09-01 examined=%s writes=0\n", path, seen.key(), path, old.key())
	m := &doctorScript{turns: []Response{{Stop: StopEnd}, {Stop: StopEnd}}}
	if _, err := doctorRun(d, k, m, 100_000); err != nil {
		t.Fatal(err)
	}
	if len(m.reqs) != 2 || strings.Contains(m.reqs[0].Messages[0].Text, "evidence of a") {
		t.Fatalf("a recent item was examined again: %d requests", len(m.reqs))
	}
	if len(k.entries) != 1 {
		t.Fatalf("entries %v", k.entries)
	}
	e := k.entries[0]
	if e["path"] != path {
		t.Errorf("path %v", e["path"])
	}
	line := e["entry"].(string)
	if !strings.Contains(line, "day=2026-10-30") || !strings.Contains(line, old.key()) || !strings.Contains(line, fresh.key()) {
		t.Errorf("entry %q", line)
	}
	// What this run recorded is what the next run reads back.
	k.log = "## 2026-10-30T03:00:00Z\n\n[" + path + "] " + line + "\n"
	k.items = []doctorItem{old, fresh}
	it, err := d.Next(context.Background(), "kb-a", k)
	if err != nil || it != nil {
		t.Errorf("next run: item %v err %v", it, err)
	}
}

func TestDoctorStopsAtTheGrantAndAtTheCaps(t *testing.T) {
	items := []doctorItem{item("status_reclassify", "a"), item("status_reclassify", "b"), item("status_reclassify", "c")}
	k, d := dueKB(items...)
	m := &doctorScript{turns: []Response{{Stop: StopEnd, Usage: Usage{InputTokens: 15_000}}, {Stop: StopEnd}}}
	res, _ := doctorRun(d, k, m, doctorMinGrant+10_000)
	if res.Units != 1 || len(m.reqs) != 1 {
		t.Errorf("grant: units %d, requests %d", res.Units, len(m.reqs))
	}

	k, d = dueKB(items...)
	d.maxWrites = 1
	m = &doctorScript{turns: []Response{patchTurn("1", `{"id":"a","if_match":"h"}`), {Stop: StopEnd}}}
	res, _ = doctorRun(d, k, m, 100_000)
	if res.Commits != 1 || res.Units != 1 {
		t.Errorf("write cap: %+v", res)
	}

	// The cap met inside an item: its other writes come with the next run.
	k, d = dueKB(items[0])
	d.maxWrites = 1
	m = &doctorScript{turns: []Response{patchTurn("1", `{"id":"a","if_match":"h"}`), patchTurn("2", `{"id":"a","if_match":"h"}`), {Stop: StopEnd}}}
	if res, _ = doctorRun(d, k, m, 100_000); res.Commits != 1 || res.Units != 0 {
		t.Errorf("an item cut by the cap counted as examined: %+v", res)
	}

	var many []doctorItem
	for i := range doctorReviewItems + 3 {
		many = append(many, item("status_reclassify", fmt.Sprint("p", i)))
	}
	k, d = dueKB(many...)
	got, err := d.queue(k)
	if err != nil || len(got) != doctorReviewItems {
		t.Errorf("queue %d err %v", len(got), err)
	}

	// The items a rule can use may sit past the first page of their kind.
	mixed := make([]doctorItem, 0, 8)
	for i := range 6 {
		mixed = append(mixed, doctorItem{Kind: "lint_judgement", Check: "broken_link", Concepts: []string{fmt.Sprint("x", i)}})
	}
	mixed = append(mixed, doctorItem{Kind: "lint_judgement", Check: "stale_open", Concepts: []string{"y"}})
	k, d = dueKB(mixed...)
	k.perPage = 2
	if got, err = d.queue(k); err != nil || len(got) != 1 || got[0].Concepts[0] != "y" {
		t.Errorf("paging: %v err %v", got, err)
	}
}

func TestDoctorGuardFencesTheWrite(t *testing.T) {
	for name, args := range map[string]string{
		"another page":   `{"id":"other","if_match":"h","frontmatter":{"status":"done"}}`,
		"a denied field": `{"id":"a","if_match":"h","frontmatter":{"type":"Topic"}}`,
		"the clock":      `{"id":"a","if_match":"h","frontmatter":{"timestamp":"2026-10-30"}}`,
		"null field":     `{"id":"a","if_match":"h","frontmatter":{"status":null}}`,
		"replace all":    `{"id":"a","if_match":"h","old_string":"x","new_string":"y","replace_all":true}`,
		"the marker":     `{"id":"a","if_match":"h","new_string":"kb\u002ddoctor"}`,
		"a body edit":    `{"id":"a","if_match":"h","old_string":"x","new_string":"y"}`,
		"a list of them": `{"id":"a","if_match":"h","edits":[{"old_string":"x","new_string":"y"}]}`,
	} {
		k, d := dueKB(item("status_reclassify", "a"))
		m := &doctorScript{turns: []Response{patchTurn("1", args), {Stop: StopEnd}}}
		res, err := doctorRun(d, k, m, 100_000)
		if err != nil || len(k.patches) != 0 || res.Commits != 0 {
			t.Errorf("%s: err %v, patches %v, %+v", name, err, k.patches, res)
		}
	}

	// A rule with a word may edit the body, one literal edit at a time.
	k, d := dueKB(item("repeated_fact", "a", "b"))
	d.allow = []string{doctorAllowPages}
	m := &doctorScript{turns: []Response{
		patchTurn("1", `{"id":"a","if_match":"h","edits":[{"old_string":"x","new_string":"y","replace_all":true}]}`),
		patchTurn("2", `{"id":"a","if_match":"h","edits":[{"old_string":"x","new_string":"y"}]}`),
		{Stop: StopEnd},
	}}
	if _, err := doctorRun(d, k, m, 100_000); err != nil || len(k.patches) != 1 {
		t.Errorf("edits: err %v, patches %v", err, k.patches)
	}
}

func TestDoctorOffersOnlyTheFencedTools(t *testing.T) {
	k, d := dueKB(item("status_reclassify", "a"))
	m := &doctorScript{turns: []Response{
		{Stop: StopToolUse, ToolCalls: []ToolCall{
			toolCall("1", "kb_repair", `{"check":"*","dry_run":false}`),
			toolCall("2", "map_update", `{"name":"a"}`),
			toolCall("3", "concept_write", `{"id":"gaps/x"}`),
			toolCall("4", "concept_delete", `{"id":"a"}`),
			toolCall("5", "artifact_write", `{"path":"instructions.md"}`),
			toolCall("6", "log_append", `{"entry":"kb-doctor: done"}`),
		}},
		{Stop: StopEnd},
	}}
	if _, err := doctorRun(d, k, m, 100_000); err != nil {
		t.Fatal(err)
	}
	for _, c := range k.calls {
		if c == "kb_repair" || c == "map_update" || c == "concept_write" || c == "concept_delete" || c == "artifact_write" {
			t.Errorf("%s reached the KB", c)
		}
	}
	for _, e := range k.entries {
		if strings.Contains(fmt.Sprint(e), doctorMarker) {
			t.Errorf("marker entry reached the KB: %v", e)
		}
	}
}

func TestDoctorBelowTheReserveStartsNoItem(t *testing.T) {
	k, d := dueKB(item("status_reclassify", "a"))
	m := &doctorScript{}
	res, err := doctorRun(d, k, m, doctorMinGrant-1)
	if err != nil || len(m.reqs) != 0 || len(k.entries) != 0 || !strings.Contains(res.Summary, "below the reserve") {
		t.Errorf("res %+v err %v reqs %d entries %v", res, err, len(m.reqs), k.entries)
	}
}

func TestDoctorDegradedBetweenNextAndRunIsASkip(t *testing.T) {
	k, d := dueKB(item("status_reclassify", "a"))
	k.st.PushState = "degraded"
	m := &doctorScript{}
	res, _ := doctorRun(d, k, m, 100_000)
	if len(m.reqs) != 0 || !strings.Contains(res.Summary, "skipped") {
		t.Errorf("res %+v reqs %d", res, len(m.reqs))
	}
}

// The words of doctor_allow turn rules on; a kind no rule names stays out at any level.
func TestDoctorAllowTurnsRulesOn(t *testing.T) {
	section := func(check string) doctorItem {
		return doctorItem{Kind: "lint_judgement", Check: check, Concepts: []string{"m/" + check}}
	}
	sprawl := doctorItem{Kind: "observation", Check: "facet_sprawl", Concepts: []string{"m/_map"}, Proposal: json.RawMessage(`{"field":"tags","values":["a"]}`)}
	all := []doctorItem{item("status_reclassify", "a"), item("repeated_fact", "b", "c"), section("template_extra_section"), section("template_section_missing"),
		{Kind: "observation", Check: "island", Concepts: []string{"d"}}, sprawl, item("index_unclassified", "m/e"), item("harvest_candidate", "j/f"), item("lesson_unpromoted", "j/g"),
		item("template_proposal", "m/_map"), {Kind: "observation", Check: "map_misfit", Concepts: []string{"h"}}, item("glossary_gap", "i")}
	for name, tc := range map[string]struct {
		allow []string
		want  int
	}{
		"nothing":   {nil, 1},
		"pages":     {[]string{doctorAllowPages}, 5},
		"structure": {[]string{doctorAllowStructure}, 3},
		"archive":   {[]string{doctorAllowArchive}, 3},
		"all":       {doctorAllowWords, 9},
	} {
		k, d := dueKB(all...)
		d.allow = tc.allow
		items, err := d.queue(k)
		if err != nil || len(items) != tc.want {
			t.Errorf("%s: %d items, want %d: %v", name, len(items), tc.want, err)
		}
	}
}

func TestDoctorRepairsWithNoModel(t *testing.T) {
	findings := map[string]int{"broken_link": 2, "invalid_field_value": 4, "unmapped_folder": 1, "junk_file": 1, "reciprocal_link_item": 300, "a_future_check": 1}
	for name, tc := range map[string]struct {
		allow []string
		want  string
	}{
		"default":        {[]string{doctorAllowRepair}, "[broken_link]"},
		"with structure": {[]string{doctorAllowRepair, doctorAllowStructure}, "[broken_link invalid_field_value]"},
	} {
		k, d := dueKB()
		d.allow, k.st.Conformance.Repairable = tc.allow, findings
		if it, err := d.Next(context.Background(), "kb-a", k); err != nil || it == nil {
			t.Fatalf("%s: repairable KB not offered: %v %v", name, it, err)
		}
		m := &doctorScript{}
		res, err := doctorRun(d, k, m, 100_000)
		if err != nil || len(m.reqs) != 0 || len(k.repairs) != 1 || res.Commits != 1 || res.Tokens != 0 {
			t.Fatalf("%s: res %+v err %v reqs %d repairs %v", name, res, err, len(m.reqs), k.repairs)
		}
		if got := fmt.Sprint(k.repairs[0]["check"]); got != tc.want || k.repairs[0]["dry_run"] != false {
			t.Errorf("%s: repair %v, want %s", name, k.repairs[0], tc.want)
		}
	}
	k, d := dueKB()
	k.st.Conformance.Repairable = findings
	if it, _ := d.Next(context.Background(), "kb-a", k); it != nil {
		t.Error("a task without the word repair was offered a repair")
	}
}

// With nothing to do the queue is not read at every poll, and a repair that fixes
// nothing does not loop.
func TestDoctorRestsAKBThatDoesNotMove(t *testing.T) {
	k, d := dueKB()
	now := time.Date(2026, 10, 30, 3, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	d.Next(context.Background(), "kb-a", k)
	calls := len(k.calls)
	if it, _ := d.Next(context.Background(), "kb-a", k); it != nil || len(k.calls) != calls {
		t.Errorf("a resting KB was read: %v", k.calls[calls:])
	}
	now = now.Add(doctorRest + time.Minute)
	if d.Next(context.Background(), "kb-a", k); len(k.calls) == calls {
		t.Error("the rest never ended")
	}

	k, d = dueKB()
	d.allow, k.st.Conformance.Repairable = []string{doctorAllowRepair}, map[string]int{"broken_link": 1}
	d.repair(k, []string{"broken_link"})
	d.rest("kb-a")
	if it, _ := d.Next(context.Background(), "kb-a", k); it != nil {
		t.Error("a resting KB was offered")
	}
}

func TestDoctorGuardFencesAMap(t *testing.T) {
	turn := func(name, args string) Response {
		return Response{Stop: StopToolUse, ToolCalls: []ToolCall{toolCall("1", name, args)}, Usage: Usage{InputTokens: 100}}
	}
	sprawl := doctorItem{Kind: "observation", Check: "facet_sprawl", Concepts: []string{"m/_map"},
		Proposal: json.RawMessage(`{"field":"tags","values":["a","b"],"drop":["c"]}`)}
	page := `{"frontmatter":{"title":"M","field_values.*.tags":["old"],"field_values.Entity.kind":["x","y"]}}`

	// One field given: the map keeps the rest.
	k, d := dueKB(sprawl)
	d.allow, k.pages = []string{doctorAllowStructure}, map[string]string{"m/_map": page}
	m := &doctorScript{turns: []Response{turn("map_update", `{"map":"m","field_values":{"*":{"tags":["b","a"]}}}`), {Stop: StopEnd}}}
	res, err := doctorRun(d, k, m, 100_000)
	if err != nil || len(k.maps) != 1 || res.Commits != 1 {
		t.Fatalf("res %+v err %v maps %v", res, err, k.maps)
	}
	if got := fmt.Sprint(k.maps[0]["field_values"]); got != "map[*:map[tags:[b a]] Entity:map[kind:[x y]]]" {
		t.Errorf("field_values = %s", got)
	}
	if !strings.HasPrefix(fmt.Sprint(k.maps[0]["reason"]), "doctor: facet_sprawl-") {
		t.Errorf("reason = %v", k.maps[0]["reason"])
	}

	for name, args := range map[string]string{
		"another map":   `{"map":"other","field_values":{"*":{"tags":["a","b"]}}}`,
		"another key":   `{"map":"m","field_values":{"*":{"tags":["a","b"]}},"concept_types":["Topic"]}`,
		"no vocabulary": `{"map":"m"}`,
		"a new value":   `{"map":"m","field_values":{"*":{"tags":["a","z"]}}}`,
		"part of it":    `{"map":"m","field_values":{"*":{"tags":["a"]}}}`,
		"a value twice": `{"map":"m","field_values":{"*":{"tags":["a","a"]}}}`,
		"no value":      `{"map":"m","field_values":{"*":{"tags":[]}}}`,
		"another field": `{"map":"m","field_values":{"*":{"kind":["a","b"]}}}`,
		"another type":  `{"map":"m","field_values":{"Entity":{"tags":["a","b"]}}}`,
		"two fields":    `{"map":"m","field_values":{"*":{"tags":["a","b"],"kind":["b"]}}}`,
	} {
		k, d := dueKB(sprawl)
		d.allow, k.pages = []string{doctorAllowStructure}, map[string]string{"m/_map": page}
		m := &doctorScript{turns: []Response{turn("map_update", args), {Stop: StopEnd}}}
		if _, err := doctorRun(d, k, m, 100_000); err != nil || len(k.maps) != 0 {
			t.Errorf("%s: err %v, maps %v", name, err, k.maps)
		}
	}

	for name, args := range map[string]string{
		"another index": `{"path":"other","if_match":"h","old_string":"x","new_string":"y"}`,
		"the root":      `{"if_match":"h","old_string":"x","new_string":"y"}`,
	} {
		k, d := dueKB(item("index_unclassified", "m/a"))
		d.allow = []string{doctorAllowStructure}
		m := &doctorScript{turns: []Response{turn("index_patch", args), {Stop: StopEnd}}}
		if _, err := doctorRun(d, k, m, 100_000); err != nil || len(k.maps) != 0 {
			t.Errorf("%s: err %v, maps %v", name, err, k.maps)
		}
	}
	// An item with no proposal has nothing to apply: it costs no model call.
	bare := sprawl
	bare.Proposal = nil
	k, d = dueKB(bare)
	d.allow = []string{doctorAllowStructure}
	if items, err := d.queue(k); err != nil || len(items) != 0 {
		t.Errorf("an item with no proposal was queued: %v %v", items, err)
	}

	// A structure rule has no body to edit, not even the map page's.
	k, d = dueKB(sprawl)
	d.allow, k.pages = []string{doctorAllowStructure}, map[string]string{"m/_map": page}
	m = &doctorScript{turns: []Response{patchTurn("1", `{"id":"m/_map","if_match":"h","old_string":"x","new_string":"y"}`), {Stop: StopEnd}}}
	if _, err := doctorRun(d, k, m, 100_000); err != nil || len(k.patches) != 0 {
		t.Errorf("a structure rule patched a page: err %v, patches %v", err, k.patches)
	}

	k, d = dueKB(item("index_unclassified", "m/a"))
	d.allow = []string{doctorAllowStructure}
	m = &doctorScript{turns: []Response{turn("index_patch", `{"path":"m","if_match":"h","old_string":"x","new_string":"y"}`), {Stop: StopEnd}}}
	if _, err := doctorRun(d, k, m, 100_000); err != nil || len(k.maps) != 1 {
		t.Errorf("the item's index: err %v, maps %v", err, k.maps)
	}

	// A rule without the tool does not get it, at any level.
	k, d = dueKB(item("repeated_fact", "a", "b"))
	d.allow = doctorAllowWords
	m = &doctorScript{turns: []Response{turn("map_update", `{"map":"a","field_values":{"*":{"tags":["a"]}}}`), {Stop: StopEnd}}}
	if _, err := doctorRun(d, k, m, 100_000); err != nil || len(k.maps) != 0 {
		t.Errorf("a page rule reached a map: err %v, maps %v", err, k.maps)
	}
}

// Facts go to the live pages the entry links; only the entry changes status.
func TestDoctorCarryingRuleEditsBodiesElsewhere(t *testing.T) {
	k, d := dueKB(item("harvest_candidate", "j/entry"))
	d.allow, k.links = []string{doctorAllowArchive}, map[string][]string{"j/entry": {"live/page"}}
	m := &doctorScript{turns: []Response{
		patchTurn("1", `{"id":"live/page","if_match":"h","old_string":"x","new_string":"x\n- a fact"}`),
		patchTurn("2", `{"id":"live/page","if_match":"h","frontmatter":{"status":"done"}}`),
		patchTurn("3", `{"id":"elsewhere/page","if_match":"h","old_string":"x","new_string":"x\n- a fact"}`),
		patchTurn("4", `{"id":"broken/link","if_match":"h","old_string":"x","new_string":"x\n- a fact"}`),
		patchTurn("5", `{"id":"j/entry","if_match":"h","frontmatter":{"status":"archived"}}`),
		{Stop: StopEnd},
	}}
	if _, err := doctorRun(d, k, m, 100_000); err != nil || len(k.patches) != 2 || k.patches[0]["id"] != "live/page" || k.patches[1]["id"] != "j/entry" {
		t.Errorf("err %v, patches %v", err, k.patches)
	}
}

// A link the model writes does not open the page it names: the targets are read
// before its first write, and they do not carry over to the next item.
func TestDoctorCarryingRuleTargetsAreReadFirst(t *testing.T) {
	k, d := dueKB(item("harvest_candidate", "j/entry"), item("lesson_unpromoted", "j/other"))
	d.allow, k.links = []string{doctorAllowArchive}, map[string][]string{"j/entry": {"live/page"}}
	m := &doctorScript{turns: []Response{
		patchTurn("1", `{"id":"j/entry","if_match":"h","old_string":"x","new_string":"x [p](secret/page)"}`),
		patchTurn("2", `{"id":"secret/page","if_match":"h","old_string":"x","new_string":"y"}`),
		{Stop: StopEnd},
		patchTurn("3", `{"id":"live/page","if_match":"h","old_string":"x","new_string":"y"}`),
		{Stop: StopEnd},
	}}
	if _, err := doctorRun(d, k, m, 100_000); err != nil || len(k.patches) != 1 || k.patches[0]["id"] != "j/entry" {
		t.Errorf("err %v, patches %v", err, k.patches)
	}
}

func TestDoctorKeyNamesOneItem(t *testing.T) {
	fact := func(line string) doctorItem {
		return doctorItem{Kind: "repeated_fact", Concepts: []string{"a", "b"}, Evidence: "the same line in 2 concepts: " + line}
	}
	if fact("one").key() == fact("two").key() {
		t.Error("two facts on the same pages share a key: the second would never be examined")
	}
	if fact("one").key() != fact("one").key() {
		t.Error("the key of a fact is not stable")
	}
	stale := func(days string) doctorItem {
		return doctorItem{Kind: "lint_judgement", Check: "stale_open", Concepts: []string{"a"}, Evidence: "open for " + days}
	}
	if stale("90 days").key() != stale("91 days").key() {
		t.Error("an age in the evidence made the same item new")
	}
}

func TestDoctorAllowIsValidated(t *testing.T) {
	v := DoctorKind().Validate
	if err := v(map[string]any{"max_writes": 1, "doctor_allow": []string{"repair", "archive"}}); err != nil {
		t.Error(err)
	}
	if err := v(map[string]any{"max_writes": 1, "doctor_allow": []string{"everything"}}); err == nil || !strings.Contains(err.Error(), "everything") {
		t.Errorf("an unknown word: %v", err)
	}
	opts := map[string]any{"doctor_allow": []string{doctorAllowPages}, doctorKeyPerimeter: "p"}
	inst, err := DoctorKind().Build(opts)
	if err != nil || !inst.(*doctorInstance).allows(doctorAllowPages) || inst.(*doctorInstance).allows(doctorAllowRepair) {
		t.Errorf("built %+v, %v", inst, err)
	}
}

func TestDoctorKindTakesThePerimeterFromTheLoader(t *testing.T) {
	k := DoctorKind()
	if _, err := k.Build(map[string]any{"max_writes": 5}); err == nil {
		t.Error("a doctor task built without the loader has no log path")
	}
	opts := map[string]any{"max_writes": 5}
	if err := k.Load(LoadEnv{Perimeter: "docs-night"}, opts); err != nil {
		t.Fatal(err)
	}
	inst, err := k.Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	if d := inst.(*doctorInstance); d.perimeter != "docs-night" || d.maxWrites != 5 {
		t.Errorf("instance %+v", d)
	}
}

func TestDoctorKindIsRegisteredAndNoMarkerStrips(t *testing.T) {
	if _, ok := DefaultKinds().Lookup("doctor"); !ok {
		t.Fatal("the doctor kind is not registered")
	}
	if got := noMarker("x kb-doctor y"); strings.Contains(got, doctorMarker) {
		t.Errorf("noMarker left %q", got)
	}
}

func TestDoctorReadOnlyTokenAbortsTheRun(t *testing.T) {
	k, d := dueKB(item("status_reclassify", "a"), item("status_reclassify", "b"))
	k.failing = true
	var turns []Response
	for i := 0; i < 10; i++ {
		turns = append(turns, patchTurn(fmt.Sprint(i), `{"id":"a","if_match":"h"}`))
	}
	m := &doctorScript{turns: turns}
	_, err := doctorRun(d, k, m, 100_000)
	if !errors.Is(err, errDoctorRefused) || len(m.reqs) != maxRefusedWrites {
		t.Errorf("err %v, model calls %d", err, len(m.reqs))
	}
	if len(k.entries) != 0 {
		t.Errorf("an aborted item was recorded as examined: %v", k.entries)
	}
}

func TestDoctorModelErrorsEndTheRunAndAreNotRecorded(t *testing.T) {
	k, d := dueKB(item("status_reclassify", "a"), item("status_reclassify", "b"), item("status_reclassify", "c"))
	m := &doctorScript{} // every call fails
	res, err := doctorRun(d, k, m, 100_000)
	if err == nil || len(m.reqs) != doctorMaxModelErrors || res.Units != 0 || len(k.entries) != 0 {
		t.Errorf("err %v reqs %d %+v entries %v", err, len(m.reqs), res, k.entries)
	}
}

func TestDoctorRulesMatchTheirInstructions(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range doctorRules {
		if seen[r.kind+r.check] || r.how == "" {
			t.Errorf("rule %s %s", r.kind, r.check)
		}
		seen[r.kind+r.check] = true
	}
	for _, f := range doctorFields {
		if !strings.Contains(doctorInstructions, "`"+f+"`") {
			t.Errorf("the instructions do not name %s", f)
		}
	}
}

func TestNoMarkerCannotRebuildItself(t *testing.T) {
	for _, in := range []string{"kb-kb-doctor-doctor", "kb-kb-kb-doctordoctor-doctor", "kb-doctor"} {
		if got := noMarker(in); strings.Contains(got, doctorMarker) {
			t.Errorf("noMarker(%q) = %q", in, got)
		}
	}
}

func TestBudgetEntryCarriesNoMarker(t *testing.T) {
	b := newTestBudget(map[string]*ledgerCaller{"kb-a": {}})
	if err := b.Begin([]string{"kb-a"}, time.Date(2026, 10, 30, 3, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	b.Touch("kb-a")
	b.Summarize("kb-a", "boom kb-doctor")
	e := b.entry("kb-a", "error", "stopped: kb-kb-doctor-doctor")
	if strings.Contains(e, doctorMarker) {
		t.Errorf("entry %q", e)
	}
}
