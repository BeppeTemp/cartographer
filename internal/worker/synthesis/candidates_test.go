package synthesis_test

import (
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/worker/synthesis"
)

var now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

// world scripts the server signals of every source.
func world(existing ...map[string]any) *fake {
	f := &fake{handler: map[string]func(map[string]any) client.ToolOutput{}}
	f.handler["concept_list"] = func(map[string]any) client.ToolOutput {
		return js(map[string]any{"results": append([]map[string]any{
			{"id": "gaps/old", "fields": map[string]any{"timestamp": "2026-01-01", "resolution_status": "resolved"}},
		}, existing...)})
	}
	f.handler["log_tail"] = func(map[string]any) client.ToolOutput { return client.ToolOutput{Text: ""} }
	f.handler["kb_review"] = func(a map[string]any) client.ToolOutput {
		switch a["kind"] {
		case "glossary_gap":
			return js(map[string]any{"items": []any{
				map[string]any{"term": "light", "concepts": []string{"c/1", "c/2"}, "weight": 3},
				map[string]any{"term": "heavy", "concepts": []string{"c/3", "c/4"}, "weight": 9},
			}})
		case "repeated_fact":
			return js(map[string]any{"items": []any{map[string]any{"evidence": "same fact", "concepts": []string{"r/1", "r/2"}, "weight": 5}}})
		}
		return js(map[string]any{"items": []any{}})
	}
	f.handler["kb_status"] = func(map[string]any) client.ToolOutput {
		return js(map[string]any{"search_misses": []any{
			map[string]any{"query": "once", "count": 1},
			map[string]any{"query": "done", "count": 7, "resolved": true},
			map[string]any{"query": "how deploy", "count": 4},
		}})
	}
	f.handler["search"] = func(a map[string]any) client.ToolOutput {
		return js(map[string]any{"results": []any{map[string]any{"id": "m/1"}, map[string]any{"id": "m/2"}}})
	}
	f.handler["atlas_overview"] = func(map[string]any) client.ToolOutput {
		return client.ToolOutput{Text: "## Structure\n\n### Most central\n\n- Alpha (s/alpha)\n- Beta (s/beta)\n\n### Communities\n\n- Alpha — 4 concepts\n"}
	}
	f.handler["link_suggest"] = func(a map[string]any) client.ToolOutput {
		return js(map[string]any{"candidates": []any{map[string]any{"id": "s/other-" + a["id"].(string)[2:]}}})
	}
	return f
}

var rich = synthesis.Budget{Daily: 1_000_000, Remaining: 1_000_000}

func subjects(c []synthesis.Candidate) string {
	var s []string
	for _, x := range c {
		s = append(s, x.Source+":"+x.Subject)
	}
	return strings.Join(s, " | ")
}

func TestCandidates_OrderIsFixedAndStable(t *testing.T) {
	want := "glossary_gap:heavy | glossary_gap:light | search_miss:how deploy | repeated_fact:same fact | link_suggest:Are s/alpha and s/other-alpha related? | link_suggest:Are s/beta and s/other-beta related?"
	cfg := synthesis.Config{MaxCandidates: 10}
	for i := 0; i < 2; i++ {
		got, cfg2, err := synthesis.Candidates(world(), cfg, rich, now)
		must(t, err)
		if subjects(got) != want {
			t.Fatalf("run %d:\n%s\nwant\n%s", i, subjects(got), want)
		}
		if cfg2.Map != "gaps" {
			t.Errorf("map %q: the newest Contradiction's map expected", cfg2.Map)
		}
	}
	got, _, _ := synthesis.Candidates(world(), synthesis.Config{}, rich, now)
	if len(got) != synthesis.DefaultMaxCandidates {
		t.Errorf("default cap: %d", len(got))
	}
}

func TestCandidates_ProbesNeverRecordAMiss(t *testing.T) {
	f := world(map[string]any{"id": "gaps/doc", "fields": map[string]any{"timestamp": "2026-02-01", "resolution_status": "open", "involves": []any{"zz"}}})
	_, _, err := synthesis.Candidates(f, synthesis.Config{MaxCandidates: 10}, rich, now)
	must(t, err)
	n := 0
	for _, c := range f.calls {
		if c.Tool == "search" {
			n++
			if c.Args["record_miss"] != false {
				t.Errorf("search without record_miss false: %v", c.Args)
			}
		}
	}
	if n == 0 {
		t.Error("no search probe was made")
	}
}

func TestCandidates_Skips(t *testing.T) {
	heavy := synthesis.ID("gaps", synthesis.KindMissingContext, []string{"c/3", "c/4"})
	light := synthesis.ID("gaps", synthesis.KindMissingContext, []string{"c/1", "c/2"})
	rep := synthesis.ID("gaps", synthesis.KindOpenQuestion, []string{"r/1", "r/2"})
	f := world(
		// a resolved (possibly rejected) hypothesis of the same id
		map[string]any{"id": heavy, "fields": map[string]any{"timestamp": "2026-03-01", "origin": "synthesis", "resolution_status": "resolved"}},
		// an open synthesis item that covers the set
		map[string]any{"id": "gaps/synthesis-x", "fields": map[string]any{"timestamp": "2026-03-02", "origin": "synthesis", "resolution_status": "open", "involves": []any{"r/1", "r/2", "r/9"}}},
		// a doctor's open gap on the same concepts, found by the search probe
		map[string]any{"id": "m/1", "fields": map[string]any{"timestamp": "2026-03-03", "resolution_status": "open", "involves": []any{"m/2"}}},
	)
	// a key in the last log entry
	f.handler["log_tail"] = func(map[string]any) client.ToolOutput {
		return client.ToolOutput{Text: "## 2026-10-28T10:00:00Z\n\nrun x day=2026-10-28 ... ; " + "\n" + synthesis.ExaminedLine([]string{light}) + "\n\n## 2026-01-01T00:00:00Z\n\n" + synthesis.ExaminedLine([]string{"too-old"}) + "\n"}
	}
	got, _, err := synthesis.Candidates(f, synthesis.Config{Map: "gaps", MaxCandidates: 10}, rich, now)
	must(t, err)
	for _, c := range got {
		if c.ID == heavy || c.ID == light || c.ID == rep {
			t.Errorf("%s should have been skipped", c.Source)
		}
		if c.Source == "search_miss" {
			t.Errorf("a doctor's open gap on the same concepts must skip the miss candidate")
		}
	}
	if len(got) != 2 { // only the two relations remain
		t.Errorf("got %s", subjects(got))
	}
}

func TestExaminedKeys_WindowIsSevenDays(t *testing.T) {
	log := "## 2026-10-25T00:00:00Z\n\n" + synthesis.ExaminedLine([]string{"a/b", "c/d"}) + "\n\n## 2026-10-20T00:00:00Z\n\n" + synthesis.ExaminedLine([]string{"old"}) + "\n"
	got := synthesis.ExaminedKeys(log, now)
	if len(got) != 2 || got[0] != "a/b" || got[1] != "c/d" {
		t.Errorf("got %v", got)
	}
}

func TestCandidates_NoMapEndsTheTask(t *testing.T) {
	f := world()
	f.handler["concept_list"] = func(map[string]any) client.ToolOutput { return js(map[string]any{"results": []any{}}) }
	if _, _, err := synthesis.Candidates(f, synthesis.Config{}, rich, now); err != synthesis.ErrNoMap {
		t.Fatalf("got %v", err)
	}
	// A configured map needs no discovery.
	if _, _, err := synthesis.Candidates(f, synthesis.Config{Map: "gaps"}, rich, now); err != nil {
		t.Fatal(err)
	}
}

func TestCandidates_Budget(t *testing.T) {
	per := synthesis.DefaultContextTokens + synthesis.AnswerCap
	// Reserve is 20% of 1_000_000; room for exactly two candidates above it.
	two := synthesis.Budget{Daily: 1_000_000, Remaining: 200_000 + 2*per + 10}
	got, _, err := synthesis.Candidates(world(), synthesis.Config{MaxCandidates: 10}, two, now)
	must(t, err)
	if len(got) != 2 {
		t.Errorf("a budget for two gave %d", len(got))
	}
	f := world()
	got, _, _ = synthesis.Candidates(f, synthesis.Config{}, synthesis.Budget{Daily: 1_000_000, Remaining: 200_000}, now)
	if len(got) != 0 || len(f.calls) != 0 {
		t.Errorf("the reserve must block the start before any call (%d candidates, %d calls)", len(got), len(f.calls))
	}
}
