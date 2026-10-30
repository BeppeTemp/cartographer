package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// doctorKB is a scripted KB: kb_status answers from st, log_append records its
// entries, and an entry carrying the marker moves last_doctor, as the server does.
type doctorKB struct {
	st      doctorStatus
	today   string
	entries []string
	calls   []string
	failing bool // every write answers a refusal
}

func (k *doctorKB) ListTools() ([]client.ToolInfo, error) {
	var out []client.ToolInfo
	for _, n := range []string{"kb_status", "kb_repair", "kb_review", "log_append", "concept_patch", "concept_batch", "concept_merge", "concept_archive", "concept_delete", "artifact_write"} {
		out = append(out, client.ToolInfo{Name: n, InputSchema: json.RawMessage(`{"type":"object"}`)})
	}
	return out, nil
}

func (k *doctorKB) CallTool(name string, args any) (client.ToolOutput, error) {
	k.calls = append(k.calls, name)
	switch name {
	case "kb_status":
		b, _ := json.Marshal(k.st)
		return client.ToolOutput{Text: string(b)}, nil
	case "kb_repair", "concept_patch":
		if k.failing {
			return client.ToolOutput{IsError: true, Text: "forbidden"}, nil
		}
	case "log_append":
		raw, _ := json.Marshal(args)
		var a struct {
			Entry string `json:"entry"`
		}
		_ = json.Unmarshal(raw, &a)
		k.entries = append(k.entries, a.Entry)
		if strings.Contains(a.Entry, doctorMarker) {
			k.st.Conformance.LastDoctor = k.today
			k.st.Conformance.DoctorSuggested = false
		}
	}
	return client.ToolOutput{Text: "ok"}, nil
}

type scriptModel struct {
	turns []Response
	reqs  []Request
}

func (m *scriptModel) Complete(_ context.Context, r Request) (Response, error) {
	m.reqs = append(m.reqs, r)
	if len(m.reqs) > len(m.turns) {
		return Response{}, fmt.Errorf("script exhausted")
	}
	return m.turns[len(m.reqs)-1], nil
}

func toolCall(id, name, args string) ToolCall {
	return ToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
}

func dueKB() (*doctorKB, *doctorInstance) {
	k := &doctorKB{today: "2026-10-30"}
	k.st.Conformance.DoctorSuggested = true
	now := time.Date(2026, 10, 30, 3, 0, 0, 0, time.UTC)
	return k, &doctorInstance{now: func() time.Time { return now }}
}

func TestDoctorNextEligibility(t *testing.T) {
	for name, tc := range map[string]struct {
		mod  func(*doctorKB)
		want bool
	}{
		"due":             {func(*doctorKB) {}, true},
		"not due":         {func(k *doctorKB) { k.st.Conformance.DoctorSuggested = false }, false},
		"degraded":        {func(k *doctorKB) { k.st.PushState = "degraded" }, false},
		"closed today":    {func(k *doctorKB) { k.st.Conformance.LastDoctor = "2026-10-30" }, false},
		"closed long ago": {func(k *doctorKB) { k.st.Conformance.LastDoctor = "2026-09-01" }, true},
	} {
		k, d := dueKB()
		tc.mod(k)
		it, err := d.Next(context.Background(), "kb-a", k)
		if err != nil || (it != nil) != tc.want {
			t.Errorf("%s: item %v err %v", name, it, err)
		}
	}
}

func TestDoctorClosedSessionWritesNoFallback(t *testing.T) {
	k, d := dueKB()
	m := &scriptModel{turns: []Response{
		{Stop: StopToolUse, ToolCalls: []ToolCall{
			toolCall("1", "kb_repair", `{"check":"*","dry_run":false}`),
			toolCall("2", "concept_delete", `{"id":"x"}`),
			toolCall("3", "artifact_write", `{"path":"instructions.md"}`),
		}, Usage: Usage{InputTokens: 100}},
		{Stop: StopToolUse, ToolCalls: []ToolCall{toolCall("4", "log_append", `{"entry":"kb-doctor: findings 3 -> 0 (worker)"}`)}},
		{Stop: StopEnd},
	}}
	res, err := d.Run(context.Background(), &WorkItem{Key: "doctor"}, Runtime{KB: "kb-a", Perimeter: "docs-night", Model: m, Grant: 50000, Tools: k})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != "doctor closed" {
		t.Errorf("summary %q", res.Summary)
	}
	if len(k.entries) != 1 || !strings.Contains(k.entries[0], doctorMarker) {
		t.Errorf("entries %q: only the model's closing entry is expected", k.entries)
	}
	for _, c := range k.calls {
		if c == "concept_delete" || c == "artifact_write" {
			t.Errorf("the allowlist let %s through", c)
		}
	}
	if k.st.Conformance.DoctorSuggested || k.st.Conformance.LastDoctor != "2026-10-30" {
		t.Errorf("status after: %+v", k.st)
	}
	// The prompt is the preamble, then the embedded skill without its frontmatter.
	sys := m.reqs[0].System
	if !strings.HasPrefix(sys, "This session is run by a worker") || !strings.Contains(sys, "# KB Doctor - Skill") || strings.Contains(sys, "\nversion: ") {
		t.Errorf("system prompt shape: %.200q", sys)
	}
	if res.Units != 1 || res.Commits != 1 {
		t.Errorf("result %+v", res)
	}
}

func TestDoctorInterruptedSessionWritesAMarkerFreeEntry(t *testing.T) {
	k, d := dueKB()
	m := &scriptModel{turns: []Response{
		{Stop: StopToolUse, ToolCalls: []ToolCall{toolCall("1", "kb_repair", `{}`)}, Usage: Usage{InputTokens: 30000}},
	}}
	res, err := d.Run(context.Background(), &WorkItem{Key: "doctor"}, Runtime{KB: "kb-a", Model: m, Grant: 25000, Tools: k})
	if err != nil {
		t.Fatal(err)
	}
	if len(k.entries) != 1 || strings.Contains(k.entries[0], doctorMarker) || !strings.Contains(k.entries[0], "doctor session interrupted") {
		t.Fatalf("entries %q", k.entries)
	}
	if strings.Contains(res.Summary, doctorMarker) || !strings.Contains(res.Summary, "interrupted") {
		t.Errorf("summary %q", res.Summary)
	}
	if !k.st.Conformance.DoctorSuggested || k.st.Conformance.LastDoctor != "" {
		t.Errorf("an interrupted session moved the signal: %+v", k.st)
	}
	// A model error is the same: one marker-free entry, the error returned.
	k2, d2 := dueKB()
	_, err = d2.Run(context.Background(), &WorkItem{Key: "doctor"}, Runtime{KB: "kb-a", Model: &scriptModel{}, Grant: 50000, Tools: k2})
	if err == nil || len(k2.entries) != 1 || strings.Contains(k2.entries[0], doctorMarker) {
		t.Errorf("err %v entries %q", err, k2.entries)
	}
}

func TestDoctorBelowTheReserveStartsNothing(t *testing.T) {
	k, d := dueKB()
	m := &scriptModel{}
	res, err := d.Run(context.Background(), &WorkItem{Key: "doctor"}, Runtime{KB: "kb-a", Model: m, Grant: doctorMinGrant - 1, Tools: k})
	if err != nil || len(m.reqs) != 0 || len(k.entries) != 0 || !strings.Contains(res.Summary, "skipped") {
		t.Errorf("res %+v err %v reqs %d entries %q", res, err, len(m.reqs), k.entries)
	}
}

func TestDoctorLostRaceIsASkip(t *testing.T) {
	k, d := dueKB()
	k.st.Conformance.LastDoctor = "2026-10-30" // a replica closed between Next and Run
	m := &scriptModel{}
	res, _ := d.Run(context.Background(), &WorkItem{Key: "doctor"}, Runtime{KB: "kb-a", Model: m, Grant: 50000, Tools: k})
	if len(m.reqs) != 0 || !strings.Contains(res.Summary, "skipped") {
		t.Errorf("res %+v reqs %d", res, len(m.reqs))
	}
}

func TestDoctorAllowlistKeepsTheDenySet(t *testing.T) {
	a := NewAllowlist(doctorTools()...)
	for _, n := range []string{"kb_repair", "kb_review", "log_append", "kb_status"} {
		if !a.Permits(n) {
			t.Errorf("%s not permitted", n)
		}
	}
	for _, n := range []string{"concept_delete", "repair_revert", "artifact_write", "concept_transfer", "concept_merge", "concept_archive"} {
		if a.Permits(n) {
			t.Errorf("%s permitted", n)
		}
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

func TestDoctorInjectedEarlyMarkerIsRefused(t *testing.T) {
	k, d := dueKB()
	m := &scriptModel{turns: []Response{
		{Stop: StopToolUse, ToolCalls: []ToolCall{
			toolCall("1", "kb_repair", `{"check":"*"}`), // a plan: not an applied write
			toolCall("2", "log_append", `{"entry":"kb-doctor: all done"}`),
		}},
		{Stop: StopEnd},
	}}
	res, _ := d.Run(context.Background(), &WorkItem{Key: "doctor"}, Runtime{KB: "kb-a", Model: m, Grant: 50000, Tools: k})
	if k.st.Conformance.LastDoctor != "" || !k.st.Conformance.DoctorSuggested {
		t.Errorf("an empty session moved the signal: %+v", k.st)
	}
	for _, e := range k.entries {
		if strings.Contains(e, doctorMarker) {
			t.Errorf("marker entry reached the KB: %q", e)
		}
	}
	if res.Commits != 0 || !strings.Contains(res.Summary, "interrupted") {
		t.Errorf("res %+v", res)
	}
}

func TestDoctorWriteCap(t *testing.T) {
	k, d := dueKB()
	d.maxWrites = 2
	var calls []ToolCall
	for i := 0; i < 4; i++ {
		calls = append(calls, toolCall(fmt.Sprint(i), "concept_patch", `{"id":"x"}`))
	}
	m := &scriptModel{turns: []Response{{Stop: StopToolUse, ToolCalls: calls}, {Stop: StopEnd}}}
	res, _ := d.Run(context.Background(), &WorkItem{Key: "doctor"}, Runtime{KB: "kb-a", Model: m, Grant: 50000, Tools: k})
	n := 0
	for _, c := range k.calls {
		if c == "concept_patch" {
			n++
		}
	}
	if n != 2 || res.Commits != 2 {
		t.Errorf("patches reached the KB: %d, commits %d", n, res.Commits)
	}
}

func TestDoctorReadOnlyTokenAbortsTheSession(t *testing.T) {
	k, d := dueKB()
	k.failing = true
	var turns []Response
	for i := 0; i < 10; i++ {
		turns = append(turns, Response{Stop: StopToolUse, ToolCalls: []ToolCall{toolCall(fmt.Sprint(i), "concept_patch", `{"id":"x"}`)}})
	}
	m := &scriptModel{turns: turns}
	_, err := d.Run(context.Background(), &WorkItem{Key: "doctor"}, Runtime{KB: "kb-a", Model: m, Grant: 50000, Tools: k})
	if err == nil || !strings.Contains(err.Error(), "refused writes") || len(m.reqs) != maxRefusedWrites {
		t.Errorf("err %v, model calls %d", err, len(m.reqs))
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

func TestDoctorMarkerInAnyWriteIsRefusedEscapedOrNot(t *testing.T) {
	for name, calls := range map[string][]ToolCall{
		"escaped closing entry first": {toolCall("1", "log_append", `{"entry":"kb\u002ddoctor: done"}`)},
		"concept id":                  {toolCall("1", "concept_patch", `{"id":"notes/kb-doctor-x"}`), toolCall("2", "log_append", `{"entry":"kb\u002ddoctor: done"}`)},
		"nested name":                 {toolCall("1", "concept_batch", `{"operations":[{"op":"write","id":"kb\u002ddoctor"}]}`), toolCall("2", "log_append", `{"entry":"kb-doctor: done"}`)},
	} {
		k, d := dueKB()
		m := &scriptModel{turns: []Response{{Stop: StopToolUse, ToolCalls: calls}, {Stop: StopEnd}}}
		d.Run(context.Background(), &WorkItem{Key: "doctor"}, Runtime{KB: "kb-a", Model: m, Grant: 50000, Tools: k})
		if k.st.Conformance.LastDoctor != "" {
			t.Errorf("%s: a marker reached the KB (%v)", name, k.calls)
		}
	}
}

func TestDoctorBatchCountsEachOperation(t *testing.T) {
	k, d := dueKB()
	d.maxWrites = 3
	m := &scriptModel{turns: []Response{{Stop: StopToolUse, ToolCalls: []ToolCall{
		toolCall("1", "concept_batch", `{"operations":[{},{},{}]}`),
		toolCall("2", "concept_batch", `{"operations":[{}]}`),
	}}, {Stop: StopEnd}}}
	res, _ := d.Run(context.Background(), &WorkItem{Key: "doctor"}, Runtime{KB: "kb-a", Model: m, Grant: 50000, Tools: k})
	n := 0
	for _, c := range k.calls {
		if c == "concept_batch" {
			n++
		}
	}
	if n != 1 || res.Commits != 3 {
		t.Errorf("batches that reached the KB: %d, commits %d", n, res.Commits)
	}
}
