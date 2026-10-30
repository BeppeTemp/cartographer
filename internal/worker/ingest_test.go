package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// ikb is a small in-memory KB behind the MCP tools the ingest task uses: the
// queue, reads, a compare-and-swap patch, writes, the gate and the unit listing.
type ikb struct {
	mu      sync.Mutex
	pages   map[string]*ipage
	gate    string // gate_check answer
	patches []string
	// beforePatch runs before a concept_patch is checked (a rival replica's write).
	beforePatch func(k *ikb, id string)
	onGate      func(k *ikb)
}

type ipage struct {
	fm   map[string]any
	body string
	ver  int
}

func newIKB() *ikb { return &ikb{pages: map[string]*ipage{}, gate: `{"pass":true}`} }

func (k *ikb) put(id string, fm map[string]any, body string) {
	k.pages[id] = &ipage{fm: fm, body: body, ver: 1}
}

func (k *ikb) unit(id, source, status, executor string, extra map[string]any) {
	fm := map[string]any{"type": "IngestUnit", "source": source, "status": status, "executor": executor}
	for a, b := range extra {
		fm[a] = b
	}
	k.put(id, fm, "# "+id+"\n")
}

func (k *ikb) src(id string, fm map[string]any) {
	f := map[string]any{"type": "Source", "ingest_status": "pending"}
	for a, b := range fm {
		f[a] = b
	}
	k.put(id, f, "# "+id+"\n")
}

func (k *ikb) status(id string) string { s, _ := k.pages[id].fm["status"].(string); return s }

func jtext(v any) client.ToolOutput {
	b, _ := json.Marshal(v)
	return client.ToolOutput{Text: string(b)}
}
func terr(s string) client.ToolOutput { return client.ToolOutput{Text: s, IsError: true} }

func (k *ikb) ListTools() ([]client.ToolInfo, error) {
	var out []client.ToolInfo
	for _, n := range DefaultTools() {
		out = append(out, client.ToolInfo{Name: n, Description: n, InputSchema: json.RawMessage(`{"type":"object"}`)})
	}
	return out, nil
}

func (k *ikb) CallTool(name string, args any) (client.ToolOutput, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	var a map[string]any
	switch v := args.(type) {
	case map[string]any:
		a = v
	default:
		b, _ := json.Marshal(args)
		_ = json.Unmarshal(b, &a)
	}
	id, _ := a["id"].(string)
	switch name {
	case "work_list":
		var ids []string
		for pid, p := range k.pages {
			if p.fm["type"] == "IngestUnit" && (p.fm["status"] == "open" || p.fm["status"] == "in-progress") {
				ids = append(ids, pid)
			}
		}
		sort.Strings(ids)
		var es []map[string]any
		for _, pid := range ids {
			f := map[string]string{}
			for _, key := range []string{"status", "executor", "review_after", "claimed_by", "source"} {
				if s, ok := k.pages[pid].fm[key].(string); ok {
					f[key] = s
				}
			}
			es = append(es, map[string]any{"id": pid, "fields": f})
		}
		return jtext(map[string]any{"entries": es}), nil
	case "concept_read":
		p, ok := k.pages[id]
		if !ok {
			return terr("not found"), nil
		}
		return jtext(map[string]any{"id": id, "content_hash": fmt.Sprint(p.ver), "frontmatter": p.fm, "body": p.body}), nil
	case "concept_patch":
		if k.beforePatch != nil {
			k.beforePatch(k, id)
		}
		p := k.pages[id]
		if fmt.Sprint(p.ver) != a["if_match"] {
			return terr("stale_write: the concept changed"), nil
		}
		for key, v := range a["frontmatter"].(map[string]any) {
			p.fm[key] = v
		}
		if u, ok := a["unset"].([]string); ok {
			for _, key := range u {
				delete(p.fm, key)
			}
		}
		if old, ok := a["old_string"].(string); ok {
			p.body = strings.Replace(p.body, old, a["new_string"].(string), 1)
		}
		p.ver++
		k.patches = append(k.patches, id)
		return jtext(map[string]any{"ok": true}), nil
	case "concept_write":
		if strings.Contains(fmt.Sprint(a["body"]), "BROKEN") {
			return terr("write gate: broken link"), nil
		}
		k.put(id, map[string]any{"type": "Topic"}, fmt.Sprint(a["body"]))
		return jtext(map[string]any{"ok": true}), nil
	case "gate_check":
		if k.onGate != nil {
			k.onGate(k)
		}
		return client.ToolOutput{Text: k.gate}, nil
	case "concept_list":
		var rs []map[string]any
		for pid, p := range k.pages {
			if p.fm["type"] == "IngestUnit" && p.fm["source"] == strings.TrimPrefix(fmt.Sprint(a["where"].([]string)[1]), "source=") {
				rs = append(rs, map[string]any{"id": pid, "status": p.fm["status"]})
			}
		}
		return jtext(map[string]any{"count": len(rs), "results": rs}), nil
	}
	return terr("unexpected tool " + name), nil
}

// smodel returns its turns in order (the workertest double cannot be imported
// from package worker without a cycle).
type smodel struct {
	Turns []Response
	n     int
}

func (m *smodel) Complete(context.Context, Request) (Response, error) {
	if m.n >= len(m.Turns) {
		return Response{}, fmt.Errorf("the script has no turn left")
	}
	m.n++
	return m.Turns[m.n-1], nil
}

func writeTurns(page string) []Response {
	args, _ := json.Marshal(map[string]any{"id": page, "body": "text", "reason": "unit"})
	return []Response{
		{Stop: StopToolUse, ToolCalls: []ToolCall{{ID: "1", Name: "concept_write", Arguments: args}}, Usage: Usage{InputTokens: 100, OutputTokens: 10}},
		{Stop: StopEnd, Text: "done", Usage: Usage{InputTokens: 100, OutputTokens: 10}},
	}
}

func newInst(t *testing.T, tier string) *ingestInstance {
	t.Helper()
	i, err := IngestKind().Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	in := i.(*ingestInstance)
	in.Bind(&Config{}, Profile{Tier: tier})
	in.now = func() time.Time { return time.Date(2031, 2, 3, 12, 0, 0, 0, time.UTC) }
	return in
}

func rtFor(k *ikb, m Model, replica string) Runtime {
	return Runtime{KB: "kb-a", Perimeter: "docs", Replica: replica, Model: m, Grant: 100000, Tools: k}
}

func next(t *testing.T, in *ingestInstance, k *ikb) *WorkItem {
	t.Helper()
	it, err := in.Next(context.Background(), "kb-a", k)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func TestIngestSelectionTierAndConnector(t *testing.T) {
	k := newIKB()
	k.src("s/http", map[string]any{"locator": "https://example.com/a"})
	k.src("s/git", map[string]any{"locator": "ssh://git@example.com/r.git"})
	k.src("s/conn", map[string]any{"connector": "team-docs"})
	k.unit("u/1-human", "s/http", "open", "human", nil)
	k.unit("u/2-large", "s/http", "open", "large", nil)
	k.unit("u/3-git", "s/git", "open", "any", nil)
	k.unit("u/4-conn", "s/conn", "open", "any", nil)
	k.unit("u/5-leased", "s/http", "in-progress", "any", map[string]any{"review_after": "2031-02-03"})
	k.unit("u/6-any", "s/http", "open", "any", nil)

	small := newInst(t, TierSmall)
	if it := next(t, small, k); it == nil || it.Key != "u/6-any" {
		t.Fatalf("small worker took %+v, want u/6-any", it)
	}
	large := newInst(t, TierLarge)
	if it := next(t, large, k); it == nil || it.Key != "u/2-large" {
		t.Fatalf("large worker took %+v, want u/2-large", it)
	}
	if len(k.patches) != 0 {
		t.Fatalf("selection wrote: %v", k.patches)
	}
	// An expired lease is claimable again.
	k.pages["u/5-leased"].fm["review_after"] = "2031-02-02"
	k.pages["u/6-any"].fm["status"] = "done"
	if it := next(t, small, k); it == nil || it.Key != "u/5-leased" {
		t.Fatalf("expired lease: took %+v", it)
	}
	// The skips are counted for the run's summary, and the empty queue is nil.
	k.pages["u/5-leased"].fm["status"] = "done"
	if it := next(t, small, k); it != nil {
		t.Fatalf("queue should be empty, got %+v", it)
	}
	if s := small.takeSkips(); !strings.Contains(s, "connector=2") || !strings.Contains(s, "executor=") {
		t.Fatalf("skips = %q", s)
	}
}

func TestIngestRunDoneAndSourceClosedAfterLastUnit(t *testing.T) {
	k := newIKB()
	k.src("s/a", map[string]any{"locator": "https://example.com/a"})
	k.unit("u/1", "s/a", "open", "any", nil)
	k.unit("u/2", "s/a", "open", "any", nil)
	in := newInst(t, TierSmall)

	for i, want := range []string{"pending", "ingested"} {
		it := next(t, in, k)
		m := &smodel{Turns: writeTurns(fmt.Sprintf("pages/p%d", i))}
		res, err := in.Run(context.Background(), it, rtFor(k, m, "docs-aa"))
		if err != nil || res.Units != 1 {
			t.Fatalf("run %d: %+v %v", i, res, err)
		}
		if got := k.pages["s/a"].fm["ingest_status"]; got != want {
			t.Fatalf("after unit %d the Source is %v, want %s", i+1, got, want)
		}
	}
	u := k.pages["u/1"]
	for _, key := range []string{"claimed_by", "claimed_at", "review_after"} {
		if _, ok := u.fm[key]; ok {
			t.Errorf("claim key %s left on a done unit", key)
		}
	}
	if u.fm["status"] != "done" || !strings.Contains(u.body, "pages/p0") {
		t.Errorf("unit = %v / %q", u.fm, u.body)
	}
}

func TestIngestClaimRaceOneWins(t *testing.T) {
	k := newIKB()
	k.src("s/a", map[string]any{"locator": "https://example.com/a"})
	k.unit("u/1", "s/a", "open", "any", nil)
	k.unit("u/2", "s/a", "open", "any", nil)
	a, b := newInst(t, TierSmall), newInst(t, TierSmall)
	ia, ib := next(t, a, k), next(t, b, k)
	if ia.Key != "u/1" || ib.Key != "u/1" {
		t.Fatalf("replicas converge on the head, got %s %s", ia.Key, ib.Key)
	}
	ma, mb := &smodel{Turns: writeTurns("pages/a")}, &smodel{Turns: writeTurns("pages/b")}
	// Replica B's claim reaches the server first: A's if_match is stale.
	k.beforePatch = func(k *ikb, id string) {
		if k.pages[id].fm["claimed_by"] == nil && id == "u/1" {
			k.pages[id].ver++
			k.pages[id].fm["status"] = "in-progress"
			k.pages[id].fm["claimed_by"] = "docs-bb"
			k.pages[id].fm["review_after"] = "2031-02-05"
		}
	}
	res, err := a.Run(context.Background(), ia, rtFor(k, ma, "docs-aa"))
	if err != nil || res.Units != 0 || ma.n != 0 {
		t.Fatalf("the loser ran: %+v %v requests=%d", res, err, ma.n)
	}
	k.beforePatch = nil
	if it := next(t, a, k); it == nil || it.Key != "u/2" {
		t.Fatalf("after losing, next = %+v, want u/2", it)
	}
	if res, err := b.Run(context.Background(), ib, rtFor(k, mb, "docs-bb")); err == nil && res.Units != 0 {
		t.Fatalf("B ran a unit already claimed by itself: %+v", res)
	}
}

func TestIngestReleaseCases(t *testing.T) {
	cases := []struct {
		name  string
		turns []Response
		gate  string
		grant int
		ctx   func() context.Context
		note  string
		pages []string
	}{
		{"broken write is refused by the gate", []Response{
			{Stop: StopToolUse, ToolCalls: []ToolCall{{ID: "1", Name: "concept_write", Arguments: json.RawMessage(`{"id":"pages/x","body":"BROKEN","reason":"unit"}`)}}},
			{Stop: StopEnd, Text: "x"}}, "", 0, nil, "nothing was written", nil},
		{"gate_check fails after the loop", writeTurns("pages/y"), `{"pass":false,"lint_findings":[{"check":"broken_link"}],"gate_blockers":[]}`, 0, nil, "1 lint findings", []string{"pages/y"}},
		{"foreign blocker", writeTurns("pages/y"), `{"pass":false,"gate_blockers":[{"path":"c/other.md"}]}`, 0, nil, "1 other blockers", []string{"pages/y"}},
		{"cut by budget", []Response{{Stop: StopToolUse, ToolCalls: []ToolCall{{ID: "1", Name: "concept_list", Arguments: json.RawMessage(`{}`)}}, Usage: Usage{InputTokens: 30000}}}, "", 25000, nil, "cut by budget", nil},
		{"interrupted", writeTurns("pages/z"), "", 0, func() context.Context { c, f := context.WithCancel(context.Background()); f(); return c }, "interrupted", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k := newIKB()
			if c.gate != "" {
				k.gate = c.gate
			}
			k.src("s/a", map[string]any{"locator": "https://example.com/a"})
			k.unit("u/1", "s/a", "open", "any", nil)
			in := newInst(t, TierSmall)
			rt := rtFor(k, &smodel{Turns: c.turns}, "docs-aa")
			if c.grant > 0 {
				rt.Grant = c.grant
			}
			ctx := context.Background()
			if c.ctx != nil {
				ctx = c.ctx()
			}
			res, err := in.Run(ctx, next(t, in, k), rt)
			if err != nil || res.Units != 0 {
				t.Fatalf("res=%+v err=%v", res, err)
			}
			u := k.pages["u/1"]
			if u.fm["status"] != "open" || u.fm["claimed_by"] != nil || u.fm["review_after"] != nil || u.fm["claimed_at"] != nil {
				t.Fatalf("unit not released: %v", u.fm)
			}
			if !strings.Contains(u.body, c.note) {
				t.Fatalf("note %q missing in %q", c.note, u.body)
			}
			for _, p := range c.pages {
				if !strings.Contains(u.body, p) {
					t.Errorf("note does not list %s: %q", p, u.body)
				}
			}
			if k.pages["s/a"].fm["ingest_status"] != "pending" {
				t.Error("a released unit closed its Source")
			}
		})
	}
}

func TestIngestOwnContradictionIsTheUnitsOutput(t *testing.T) {
	k := newIKB()
	k.gate = `{"pass":false,"gate_blockers":[{"path":"pages/y.md"}]}`
	k.src("s/a", map[string]any{"locator": "https://example.com/a"})
	k.unit("u/1", "s/a", "open", "any", nil)
	in := newInst(t, TierSmall)
	res, err := in.Run(context.Background(), next(t, in, k), rtFor(k, &smodel{Turns: writeTurns("pages/y")}, "docs-aa"))
	if err != nil || res.Units != 1 || k.status("u/1") != "done" {
		t.Fatalf("res=%+v err=%v status=%s", res, err, k.status("u/1"))
	}
}

func TestIngestBelowTheFloorClaimsNothing(t *testing.T) {
	k := newIKB()
	k.src("s/a", map[string]any{"locator": "https://example.com/a"})
	k.unit("u/1", "s/a", "open", "any", nil)
	in := newInst(t, TierSmall)
	rt := rtFor(k, &smodel{}, "docs-aa")
	rt.Grant = ingestFloorDefault - 1
	if _, err := in.Run(context.Background(), next(t, in, k), rt); err != nil || len(k.patches) != 0 {
		t.Fatalf("err=%v patches=%v", err, k.patches)
	}
	in.maxSpent = 50000 // the dearest completed unit raises the floor
	rt.Grant = 40000
	if _, err := in.Run(context.Background(), &WorkItem{Key: "u/1", Data: ingestCand{unit: "u/1", source: "s/a"}}, rt); err != nil || len(k.patches) != 0 {
		t.Fatalf("raised floor: err=%v patches=%v", err, k.patches)
	}
}

func TestIngestRefusedReleaseLeavesTheUnitAlone(t *testing.T) {
	k := newIKB()
	k.gate = `{"pass":false,"lint_findings":[{}]}`
	k.src("s/a", map[string]any{"locator": "https://example.com/a"})
	k.unit("u/1", "s/a", "open", "any", nil)
	in := newInst(t, TierSmall)
	// While the loop ends, the lease passed and another replica re-claimed the unit.
	k.onGate = func(k *ikb) {
		k.pages["u/1"].ver++
		k.pages["u/1"].fm["claimed_by"] = "docs-other"
	}
	res, err := in.Run(context.Background(), next(t, in, k), rtFor(k, &smodel{Turns: writeTurns("pages/y")}, "docs-aa"))
	if err != nil || res.Units != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	u := k.pages["u/1"]
	if u.fm["claimed_by"] != "docs-other" || u.fm["status"] != "in-progress" || strings.Contains(u.body, "Worker note") {
		t.Fatalf("the unit was touched: %v %q", u.fm, u.body)
	}
}

func TestIngestInstructionsAreTheBundledUnitMode(t *testing.T) {
	s, err := ingestInstructions()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"headless worker", "## Unit mode", "IngestUnit", "source_fetch"} {
		if !strings.Contains(s, want) {
			t.Errorf("instructions lack %q", want)
		}
	}
	if strings.Contains(s, "## Procedure") || strings.Contains(s, "### 1. Identify") {
		t.Error("the interactive procedure leaked into the unit instructions")
	}
	if _, err := unitSection("# KB Ingest\n\n## Procedure\nx\n"); err == nil {
		t.Error("a bundle without the Unit mode section must fail the start")
	}
}

func TestIngestKindIsInTheRegistryAndTheTable(t *testing.T) {
	if _, ok := DefaultKinds().Lookup("ingest"); !ok {
		t.Fatal("ingest is not registered")
	}
}
