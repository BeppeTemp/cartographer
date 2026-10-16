package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// relatedKB: alpha is the only page about the zeppelin; it links beta and a
// hub, beta links gamma, five leaves link the hub.
func relatedKB() map[string]string {
	files := map[string]string{
		"ops/alpha.md": "Zeppelin hangar. [beta](beta.md) [hub](hub.md).\n",
		"ops/beta.md":  "Second page. [gamma](gamma.md).\n",
		"ops/gamma.md": "Third page.\n",
		"ops/hub.md":   "Everything links here.\n",
	}
	for _, n := range []string{"leaf1", "leaf2", "leaf3", "leaf4", "leaf5"} {
		files["ops/"+n+".md"] = "[hub](hub.md).\n"
	}
	return files
}

func relatedSearch(t *testing.T, s *Server, args string) (map[string]interface{}, string) {
	t.Helper()
	text := mustText(t, s, "search", args)
	return decodeJSON(t, text), text
}

func relatedIDs(out map[string]interface{}) []string {
	v, ok := out["related"]
	if !ok {
		return nil
	}
	return resultIDs(v)
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// A graph read that fails leaves the cache on an older snapshot, which can name
// a concept deleted since: the block is left out rather than built on it.
func TestSearchRelated_AFailedGraphReadOmitsTheBlock(t *testing.T) {
	t.Parallel()
	s, k := graphToolKBWithKB(t, relatedKB())
	out, _ := relatedSearch(t, s, `{"query":"zeppelin","related":3}`)
	if len(relatedIDs(out)) == 0 {
		t.Fatalf("control: the block is empty: %v", out)
	}

	// The concept files cannot be listed, so the validation fails.
	moved := k.DataRoot() + ".moved"
	if err := os.Rename(k.DataRoot(), moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(moved, k.DataRoot()) })
	out, text := relatedSearch(t, s, `{"query":"zeppelin","related":3}`)
	if _, ok := out["related"]; ok {
		t.Errorf("a block was built on a snapshot of a failed graph read: %s", text)
	}
}

// The block is an addition: the hits, their order and their scores are the
// ones search returns without it.
func TestSearchRelated_AddsLinkedPagesWithoutTouchingTheHits(t *testing.T) {
	t.Parallel()
	s := graphToolKB(t, relatedKB())
	with, _ := relatedSearch(t, s, `{"query":"zeppelin"}`)
	without, _ := relatedSearch(t, s, `{"query":"zeppelin","related":0}`)

	a, _ := json.Marshal(with["results"])
	b, _ := json.Marshal(without["results"])
	if string(a) != string(b) {
		t.Fatalf("related changed the hits:\n%s\n%s", a, b)
	}
	if _, ok := without["related"]; ok {
		t.Fatalf("related: 0 still returned a block: %v", without["related"])
	}

	ids := relatedIDs(with)
	if len(ids) != searchRelatedDefault {
		t.Fatalf("default block has %d entries, want %d: %v", len(ids), searchRelatedDefault, ids)
	}
	if contains(ids, "ops/alpha") {
		t.Fatalf("the hit is repeated in related: %v", ids)
	}
	for _, raw := range with["related"].([]interface{}) {
		e := raw.(map[string]interface{})
		if _, ok := e["snippet"]; ok {
			t.Fatalf("an entry carries a snippet: %v", e)
		}
		if _, ok := e["score"]; ok {
			t.Fatalf("an entry carries a score: %v", e)
		}
		if e["bytes"].(float64) <= 0 || e["title"] == "" {
			t.Fatalf("an entry misses what it costs to read: %v", e)
		}
	}
	for _, raw := range with["related"].([]interface{}) {
		if e := raw.(map[string]interface{}); e["id"] == "ops/beta" && e["via"] != "ops/alpha" {
			t.Fatalf("beta reached through %v, want alpha", e["via"])
		}
	}
}

func TestSearchRelated_LeavesOutEveryReturnedHitAndRetiredConcepts(t *testing.T) {
	t.Parallel()
	files := relatedKB()
	files["ops/beta.md"] = "Zeppelin second page. [gamma](gamma.md) [delta](delta.md).\n"
	files["ops/delta.md"] = "---\ntype: Note\nstatus: deprecated\n---\nOld page.\n"
	files["ops/alpha.md"] = "Zeppelin hangar. [beta](beta.md) [hub](hub.md) [delta](delta.md).\n"
	s := graphToolKB(t, files)
	out, text := relatedSearch(t, s, `{"query":"zeppelin","related":10}`)
	hits := resultIDs(out["results"])
	if len(hits) != 2 {
		t.Fatalf("hits = %v", hits)
	}
	ids := relatedIDs(out)
	for _, h := range hits {
		if contains(ids, h) {
			t.Fatalf("hit %s is repeated in related: %s", h, text)
		}
	}
	if contains(ids, "ops/delta") {
		t.Fatalf("a deprecated concept is offered: %v", ids)
	}
	if !contains(ids, "ops/gamma") {
		t.Fatalf("gamma, linked from a hit, is missing: %v", ids)
	}
}

func TestSearchRelated_SizeAndKnob(t *testing.T) {
	t.Parallel()
	files := map[string]string{"ops/hub.md": "Zeppelin everywhere.\n"}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n"} {
		files["ops/"+n+".md"] = "[hub](hub.md).\n"
	}
	s := graphToolKB(t, files)
	count := func(args string) int {
		out, _ := relatedSearch(t, s, args)
		return len(relatedIDs(out))
	}
	for _, tc := range []struct {
		args string
		want int
	}{
		{`{"query":"zeppelin"}`, searchRelatedDefault},
		{`{"query":"zeppelin","related":2}`, 2},
		{`{"query":"zeppelin","related":0}`, 0},
		{`{"query":"zeppelin","related":-3}`, 0},
		{`{"query":"zeppelin","related":500}`, searchRelatedMax},
	} {
		if got := count(tc.args); got != tc.want {
			t.Errorf("%s: %d entries, want %d", tc.args, got, tc.want)
		}
	}

	out, text := relatedSearch(t, s, `{"query":"zeppelin"}`)
	block, _ := json.MarshalIndent(out["related"], "  ", "  ")
	if len(block) > 1024 {
		t.Errorf("the default block is %d bytes, over 1 KiB:\n%s", len(block), block)
	}
	if again, _ := callJSON(t, s, adminCtx, "search", `{"query":"zeppelin"}`); again != text {
		t.Error("search with a related block is not deterministic")
	}
}

// The block says nothing when the graph has nothing to add, or when the hits
// are not a match worth following.
func TestSearchRelated_SilentWhenThereIsNothingToFollow(t *testing.T) {
	t.Parallel()
	files := relatedKB()
	files["ops/lonely.md"] = "A xylophone page with no links.\n"
	files["ops/closed.md"] = "---\ntype: Note\nstatus: archived\n---\nA quokka entry. [alpha](alpha.md)\n"
	s := graphToolKB(t, files)

	if out, _ := relatedSearch(t, s, `{"query":"xylophone"}`); out["count"].(float64) != 1 || relatedIDs(out) != nil {
		t.Errorf("an unlinked hit got a block: %v", out)
	}
	// A hit that matches only some of the terms (the OR fallback) is not a
	// seed: a weak match must not pull its neighbours in.
	out, _ := relatedSearch(t, s, `{"query":"zeppelin nonexistentterm"}`)
	if out["or_fallback"] != true || out["count"].(float64) == 0 {
		t.Fatalf("fixture: expected an OR fallback hit, got %v", out)
	}
	if relatedIDs(out) != nil {
		t.Errorf("a partial-only match seeded a block: %v", out["related"])
	}
	// Archived entries come back only as the fallback: closed pages do not
	// offer their neighbours.
	out, _ = relatedSearch(t, s, `{"query":"quokka"}`)
	if out["archived_fallback"] != true {
		t.Fatalf("fixture: expected the archived fallback, got %v", out)
	}
	if relatedIDs(out) != nil {
		t.Errorf("an archived fallback seeded a block: %v", out["related"])
	}
	if out, _ := relatedSearch(t, s, `{"query":"quokka","include_archived":true}`); relatedIDs(out) != nil {
		t.Errorf("an archived hit seeded a block: %v", out["related"])
	}
}

// Neighbours that are already hits are not repeated: when every one is, there
// is nothing to add and the block is left out, not offered empty.
func TestSearchRelated_OmittedWhenEveryNeighbourIsAlreadyAHit(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"ops/den-a.md": "A marmot burrow. [b](den-b.md)\n",
		"ops/den-b.md": "Another marmot burrow. [a](den-a.md)\n",
	}
	s := graphToolKB(t, files)
	out, text := relatedSearch(t, s, `{"query":"marmot"}`)
	if out["count"].(float64) != 2 {
		t.Fatalf("fixture: both pages should be hits: %s", text)
	}
	if _, ok := out["related"]; ok {
		t.Errorf("a block with nothing new was returned: %s", text)
	}

	// The same pages with one more neighbour: the block is back, so the
	// omission above is the graph's doing, not the fixture's.
	files["ops/other.md"] = "[a](den-a.md)\n"
	out, _ = relatedSearch(t, graphToolKB(t, files), `{"query":"marmot"}`)
	if ids := relatedIDs(out); len(ids) != 1 || ids[0] != "ops/other" {
		t.Errorf("control: related = %v, want [ops/other]", ids)
	}
}

// A narrowed principal gets what an admin gets on a KB in which the hidden
// concepts do not exist: a hidden hub neither appears in the block nor decides
// what the block shows.
func TestSearchRelated_NarrowedEquivalence(t *testing.T) {
	t.Parallel()
	visible := map[string]string{
		"ops/s.md":   "Needle. [a](a.md) [b](b.md).\n",
		"ops/a.md":   "[x](x.md) [y](y.md).\n",
		"ops/b.md":   "[x](x.md) [y](y.md).\n",
		"ops/x.md":   "Shared.\n",
		"ops/y.md":   "Shared too.\n",
		"ops/far.md": "Reachable from s only through hidden pages.\n",
	}
	withHidden := map[string]string{
		"hidden/h1.md": "[s](../ops/s.md) [far](../ops/far.md).\n",
		"hidden/h2.md": "[s](../ops/s.md) [far](../ops/far.md).\n",
		"hidden/h3.md": "[s](../ops/s.md) [far](../ops/far.md).\n",
	}
	for k, v := range visible {
		withHidden[k] = v
	}
	full := graphToolKB(t, withHidden)
	stripped := graphToolKB(t, visible)
	args := `{"query":"needle","related":10}`

	got, _ := callJSON(t, full, narrowCtx, "search", args)
	want, _ := callJSON(t, stripped, adminCtx, "search", args)
	if got != want {
		t.Errorf("narrowed %s\nstripped %s", got, want)
	}
	if strings.Contains(got, "hidden/") {
		t.Errorf("a hidden concept leaked into the answer: %s", got)
	}
	// Without this the test proves nothing: the hidden pages do reach the
	// admin's block and change what it shows.
	admin, _ := callJSON(t, full, adminCtx, "search", args)
	if !strings.Contains(admin, "hidden/h1") || admin == want {
		t.Errorf("the hidden pages should shape the admin's block: %s", admin)
	}
}

// Validating the graph walks and stats every concept file, the dominant cost
// of a graph read: the block must ride on the validation the ranking already
// paid for, not add a second one.
func TestSearchRelated_DoesNotValidateTheGraphAgain(t *testing.T) {
	t.Parallel()
	s, k := graphToolKBWithKB(t, relatedKB())
	validations := func(args string) int {
		mustText(t, s, "search", args) // warm: the first call builds the index
		before := k.GraphValidations()
		mustText(t, s, "search", args)
		return k.GraphValidations() - before
	}
	off := validations(`{"query":"zeppelin","related":0}`)
	on := validations(`{"query":"zeppelin"}`)
	if on != off {
		t.Errorf("a search with the block validated the graph %d times, %d without it", on, off)
	}
}

func TestSearchRelated_SchemaAcceptsTheKnob(t *testing.T) {
	t.Parallel()
	var schema struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(searchInputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Properties["related"].Type != "integer" {
		t.Errorf("search schema does not offer an integer related: %s", searchInputSchema)
	}
}

// BenchmarkSearchRelated puts a number on what the block costs per search on a
// KB of 3000 linked concepts, for the three kinds of caller that pay it
// differently (an admin and a whole-KB token skip the per-concept visibility
// check, a narrowed one does not): `go test ./internal/mcpserver -run '^$'
// -bench SearchRelated -benchtime=10x`. Each iteration runs the search without
// the block and with it, back to back, and overhead-ms/op is the median of
// their differences: on a loaded machine only a difference taken inside one
// run means anything, and a median keeps a single stall out of it.
func BenchmarkSearchRelated(b *testing.B) {
	files := map[string]string{}
	for i := 0; i < 3000; i++ {
		marker := ""
		if i%150 == 0 {
			marker = " Marker page."
		}
		files[fmt.Sprintf("ops/c%04d.md", i)] = fmt.Sprintf("Page %d about zeppelins.%s [a](c%04d.md) [b](c%04d.md) [c](c%04d.md)\n", i, marker, (i+1)%3000, (i*7+3)%3000, (i*31+11)%3000)
	}
	s := graphToolKB(b, files)
	// A selective query: twenty pages carry the marker, as a real question
	// matches a few pages of a KB, not all of them.
	const off, on = `{"query":"marker","limit":10,"related":0}`, `{"query":"marker","limit":10}`
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"admin", adminCtx},
		{"whole-kb", restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}})},
		{"narrowed", restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"ops"}}}})},
	} {
		b.Run(tc.name, func(b *testing.B) {
			search := func(args string) time.Duration {
				start := time.Now()
				if res := s.callTool(tc.ctx, "search", json.RawMessage(args)); res.IsError {
					b.Fatal(res.Content[0].Text)
				}
				return time.Since(start)
			}
			search(on) // builds the index and the graph cache
			var without, with time.Duration
			diffs := make([]time.Duration, 0, b.N)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				o, w := search(off), search(on)
				without, with = without+o, with+w
				diffs = append(diffs, w-o)
			}
			sort.Slice(diffs, func(i, j int) bool { return diffs[i] < diffs[j] })
			ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
			b.ReportMetric(ms(without)/float64(b.N), "without-ms/op")
			b.ReportMetric(ms(with)/float64(b.N), "with-ms/op")
			b.ReportMetric(ms(diffs[len(diffs)/2]), "overhead-ms/op")
		})
	}
}
