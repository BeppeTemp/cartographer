package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/worker/synthesis"
)

// skb is an in-memory KB behind the MCP tools the synthesis task uses: the
// server signals, reads and the two writes. Package worker cannot import the
// server (imports_test.go), so the real handler is exercised by the guard and
// candidate tests of internal/worker/synthesis against the same payloads.
type skb struct {
	mu      sync.Mutex
	pages   map[string]map[string]any // id -> concept_read payload
	log     string
	writes  []map[string]any
	probes  []map[string]any
	gloss   []map[string]any
	counter int
}

func newSKB() *skb {
	k := &skb{pages: map[string]map[string]any{}, gloss: []map[string]any{{"term": "light", "concepts": []string{"c/1", "c/2"}, "weight": 3}}}
	for _, id := range []string{"c/1", "c/2"} {
		k.pages[id] = map[string]any{"id": id, "frontmatter": map[string]any{"type": "Note", "status": "active"}, "body": "Claim of " + id + ".", "content_hash": "h-" + id}
	}
	return k
}

func (k *skb) ListTools() ([]client.ToolInfo, error) {
	var out []client.ToolInfo
	for _, n := range append(DefaultTools(), "concept_delete", "concept_merge") {
		out = append(out, client.ToolInfo{Name: n, Description: n, InputSchema: json.RawMessage(`{"type":"object"}`)})
	}
	return out, nil
}

func (k *skb) snapshot() string {
	b, _ := json.Marshal(k.pages)
	return string(b)
}

func (k *skb) CallTool(name string, args any) (client.ToolOutput, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	var a map[string]any
	b, _ := json.Marshal(args)
	_ = json.Unmarshal(b, &a)
	id, _ := a["id"].(string)
	switch name {
	case "concept_list":
		var rs []map[string]any
		for pid, p := range k.pages {
			if fm := p["frontmatter"].(map[string]any); fm["type"] == "Contradiction" {
				rs = append(rs, map[string]any{"id": pid, "fields": map[string]any{
					"origin": fm["origin"], "resolution_status": fm["resolution_status"], "involves": fm["involves"], "timestamp": fm["timestamp"]}})
			}
		}
		return jtext(map[string]any{"results": rs}), nil
	case "map_list":
		return jtext([]any{map[string]any{"name": "gaps"}}), nil
	case "log_tail":
		return client.ToolOutput{Text: k.log}, nil
	case "kb_review":
		if a["kind"] == "glossary_gap" {
			return jtext(map[string]any{"items": k.gloss}), nil
		}
		return jtext(map[string]any{"items": []any{}}), nil
	case "kb_status":
		return jtext(map[string]any{"search_misses": []any{}}), nil
	case "atlas_overview":
		return client.ToolOutput{Text: ""}, nil
	case "graph_context":
		return client.ToolOutput{Text: "context of " + fmt.Sprint(a["ids"])}, nil
	case "search":
		k.probes = append(k.probes, a)
		return jtext(map[string]any{"results": []any{}}), nil
	case "concept_read":
		p, ok := k.pages[id]
		if !ok {
			return terr("concept not found"), nil
		}
		return jtext(p), nil
	case "concept_write":
		k.writes = append(k.writes, a)
		fm, _ := a["frontmatter"].(map[string]any)
		k.pages[id] = map[string]any{"id": id, "frontmatter": fm, "body": a["body"], "content_hash": "h-new"}
		return jtext(map[string]any{"ok": true}), nil
	}
	return terr("unexpected tool " + name), nil
}

func synthesisHyp() synthesis.Hypothesis {
	return synthesis.Hypothesis{
		Map: "gaps", Kind: synthesis.KindMissingContext, Title: "Is there a page about light?",
		Involves: []string{"c/1", "c/2"}, Provenance: []string{"c/1", "c/2"}, Confidence: "low",
		Date:     time.Date(2031, 2, 3, 0, 0, 0, 0, time.UTC),
		Evidence: []synthesis.Evidence{{ID: "c/1", Says: "uses the term"}, {ID: "c/2", Says: "uses the term too"}},
		Confirm:  "Check that no page defines it; if so, write one.",
	}
}

func callTurn(id, tool string, args any) Response {
	b, _ := json.Marshal(args)
	return Response{Stop: StopToolUse, ToolCalls: []ToolCall{{ID: id, Name: tool, Arguments: b}}, Usage: Usage{InputTokens: 100, OutputTokens: 10}}
}

func endTurn(text string) Response {
	return Response{Stop: StopEnd, Text: text, Usage: Usage{InputTokens: 100, OutputTokens: 10}}
}

func newSynth(t *testing.T) *synthesisInstance {
	t.Helper()
	i, err := SynthesisKind().Build(map[string]any{
		"synthesis_map": "gaps", "synthesis_max_candidates": 5, "synthesis_max_new": 3,
		"synthesis_context_tokens": 6000, "synthesis_reserve": "0.2",
	})
	if err != nil {
		t.Fatal(err)
	}
	in := i.(*synthesisInstance)
	in.now = func() time.Time { return time.Date(2031, 2, 3, 12, 0, 0, 0, time.UTC) }
	return in
}

func runSynth(t *testing.T, in *synthesisInstance, k *skb, m Model, grant int) Result {
	t.Helper()
	it, err := in.Next(context.Background(), "kb-a", k)
	if err != nil || it == nil {
		t.Fatalf("Next = %v, %v", it, err)
	}
	res, err := in.Run(context.Background(), it, Runtime{KB: "kb-a", Perimeter: "docs", Replica: "docs-aa", Model: m,
		Profile: Profile{Name: "cloud", DailyTokens: 1_000_000}, Grant: grant, Tools: k})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func TestSynthesisWritesOneGapThenDedups(t *testing.T) {
	k := newSKB()
	h := synthesisHyp()
	w := map[string]any{"id": h.ID(), "frontmatter": h.Frontmatter(), "body": h.Body(), "reason": "mine"}
	m := &smodel{Turns: []Response{callTurn("1", "concept_write", w), endTurn("done")}}
	res := runSynth(t, newSynth(t), k, m, 500_000)

	fm := k.pages[h.ID()]["frontmatter"].(map[string]any)
	if fm["origin"] != "synthesis" || fm["contradiction_kind"] != "missing_context" || fm["resolution_status"] != "open" {
		t.Fatalf("frontmatter = %v", fm)
	}
	if len(k.writes) != 1 || k.writes[0]["reason"] != "synthesis: "+h.ID() {
		t.Fatalf("writes = %v: the commit reason must be the guard's", k.writes)
	}
	if !strings.Contains(fmt.Sprint(k.writes[0]["body"]), "[[c/1]]") {
		t.Errorf("body lacks the evidence links: %v", k.writes[0]["body"])
	}
	if res.Units != 1 || res.Commits != 1 || res.Tokens == 0 || !strings.Contains(res.Summary, synthesis.ExaminedLine([]string{h.ID()})) || !strings.Contains(res.Summary, h.ID()) {
		t.Fatalf("result = %+v", res)
	}
	for _, p := range k.probes {
		if p["record_miss"] != false {
			t.Errorf("a probe fed the miss log: %v", p)
		}
	}

	// A second run proposes nothing: the id exists, the model is never called.
	again := &smodel{}
	if res := runSynth(t, newSynth(t), k, again, 500_000); res.Units != 0 || again.n != 0 || len(k.writes) != 1 {
		t.Fatalf("second run: %+v, model calls %d, writes %d", res, again.n, len(k.writes))
	}

	// With the page gone, the key in the last run record still skips the candidate.
	delete(k.pages, h.ID())
	k.log = "## 2031-02-02 10:00\n- worker run — " + res.Summary + "\n"
	if res := runSynth(t, newSynth(t), k, again, 500_000); res.Units != 0 || again.n != 0 || len(k.writes) != 1 {
		t.Fatalf("after the log: %+v", res)
	}
}

// D11 through the whole loop: a model told to overwrite, patch and delete an
// active page changes nothing, and the refusals are the model's to read.
func TestSynthesisNeverTouchesAnActivePage(t *testing.T) {
	k := newSKB()
	before := k.snapshot()
	m := &smodel{Turns: []Response{
		callTurn("1", "concept_write", map[string]any{"id": "c/1", "body": "overwritten", "frontmatter": map[string]any{"type": "Note"}}),
		callTurn("2", "concept_patch", map[string]any{"id": "c/1", "if_match": "h-c/1", "old_string": "Claim", "new_string": "Lie"}),
		callTurn("3", "concept_delete", map[string]any{"id": "c/1"}),
		endTurn("nothing"),
	}}
	res := runSynth(t, newSynth(t), k, m, 500_000)
	if k.snapshot() != before || len(k.writes) != 0 {
		t.Fatalf("the KB changed: writes %v", k.writes)
	}
	if res.Units != 0 || res.Commits != 0 {
		t.Fatalf("result = %+v", res)
	}
}

func TestSynthesisBudgetAndSetup(t *testing.T) {
	k := newSKB()
	m := &smodel{}
	// Below the reserve of the daily budget the task does not start a candidate.
	res := runSynth(t, newSynth(t), k, m, 100_000)
	if m.n != 0 || res.Tokens != 0 || res.Units != 0 {
		t.Fatalf("the reserve did not block: %+v", res)
	}
	// No map known and none configured: logged, no write.
	i, _ := SynthesisKind().Build(map[string]any{"synthesis_map": "", "synthesis_max_candidates": 5, "synthesis_max_new": 3, "synthesis_context_tokens": 6000, "synthesis_reserve": "0.2"})
	res = runSynth(t, i.(*synthesisInstance), k, m, 500_000)
	if !strings.Contains(res.Summary, "no contradiction map") || m.n != 0 {
		t.Fatalf("summary = %q", res.Summary)
	}
	// Two consecutive model errors end the task with an error.
	k.gloss = []map[string]any{{"term": "a", "concepts": []string{"c/1"}, "weight": 3}, {"term": "b", "concepts": []string{"c/2"}, "weight": 2}, {"term": "c", "concepts": []string{"c/1", "c/2"}, "weight": 1}}
	in := newSynth(t)
	it, _ := in.Next(context.Background(), "kb-a", k)
	_, err := in.Run(context.Background(), it, Runtime{KB: "kb-a", Model: &smodel{}, Profile: Profile{DailyTokens: 1_000_000}, Grant: 500_000, Tools: k})
	if err == nil || !strings.Contains(err.Error(), "2 consecutive model errors") {
		t.Fatalf("err = %v", err)
	}
}

func TestSynthesisKindOptions(t *testing.T) {
	kind := SynthesisKind()
	good := map[string]any{"synthesis_map": "gaps", "synthesis_max_candidates": 5, "synthesis_max_new": 3, "synthesis_context_tokens": 6000, "synthesis_reserve": "0.2"}
	if err := kind.Validate(good); err != nil {
		t.Fatal(err)
	}
	for key, bad := range map[string]any{"synthesis_map": "a/b", "synthesis_max_new": 0, "synthesis_reserve": "1.5", "synthesis_context_tokens": 10} {
		o := map[string]any{}
		for k, v := range good {
			o[k] = v
		}
		o[key] = bad
		if err := kind.Validate(o); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s=%v: err = %v", key, bad, err)
		}
	}
	if !reflect.DeepEqual(DefaultKinds().Names(), []string{"ingest", "synthesis"}) {
		t.Errorf("kinds = %v", DefaultKinds().Names())
	}
}
