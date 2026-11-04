package mcpserver

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// jsonStrings renders a []string as a JSON array.
func jsonStrings(t *testing.T, s []string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func repeated(n int, s string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

// Every argument cap of the read tools is a contract (docs/control-plane.md
// §Defaults and caps): a call at the cap is answered, one past it is refused
// with an error that names the cap, and nothing is truncated.
func TestReadToolArgumentCaps(t *testing.T) {
	t.Parallel()
	s, _ := workServer(t)

	words := func(n int) string { return strings.TrimSpace(strings.Repeat("alpha ", n)) }
	dotted := func(n int) string { return strings.TrimSpace(strings.Repeat("a.b ", n)) } // two terms each, as the index splits it
	dashes := func(n int) string { return strings.TrimSpace(strings.Repeat("--- ", n)) } // a word the index drops, FTS5 keeps
	longWord := func(n int) string { return strings.Repeat("x", n) }
	where := func(n int) string { return jsonStrings(t, repeated(n, "priority=p1")) }
	keys := func(n int) string { return jsonStrings(t, repeated(n, "priority")) }

	cases := []struct {
		name    string
		tool    string
		atCap   string
		pastCap string
		refusal string
	}{
		{"search query bytes", "search",
			fmt.Sprintf(`{"query":%q}`, longWord(maxSearchQueryBytes)),
			fmt.Sprintf(`{"query":%q}`, longWord(maxSearchQueryBytes+1)),
			"query is 1025 bytes: at most 1024 bytes and 64 terms"},
		{"search query bytes, not runes", "search",
			fmt.Sprintf(`{"query":%q}`, strings.Repeat("é", maxSearchQueryBytes/2)),
			fmt.Sprintf(`{"query":%q}`, strings.Repeat("é", maxSearchQueryBytes/2+1)),
			"query is 1026 bytes: at most 1024 bytes and 64 terms"},
		{"search query terms", "search",
			fmt.Sprintf(`{"query":%q}`, words(maxSearchQueryTerms)),
			fmt.Sprintf(`{"query":%q}`, words(maxSearchQueryTerms+1)),
			"query has 65 terms: at most 64 terms and 1024 bytes"},
		{"search query terms split on punctuation", "search",
			fmt.Sprintf(`{"query":%q}`, dotted(maxSearchQueryTerms/2)),
			fmt.Sprintf(`{"query":%q}`, dotted(maxSearchQueryTerms/2+1)),
			"query has 66 terms"},
		{"search query words without letters", "search",
			fmt.Sprintf(`{"query":%q}`, dashes(maxSearchQueryTerms)),
			fmt.Sprintf(`{"query":%q}`, dashes(maxSearchQueryTerms+1)),
			"query has 65 terms"},
		{"graph_context query bytes", "graph_context",
			fmt.Sprintf(`{"query":%q}`, longWord(maxSearchQueryBytes)),
			fmt.Sprintf(`{"query":%q}`, longWord(maxSearchQueryBytes+1)),
			"graph_context: query is 1025 bytes"},
		{"graph_context query terms", "graph_context",
			fmt.Sprintf(`{"query":%q}`, words(maxSearchQueryTerms)),
			fmt.Sprintf(`{"query":%q}`, words(maxSearchQueryTerms+1)),
			"graph_context: query has 65 terms"},
		{"concept_list where", "concept_list",
			`{"where":` + where(maxWhereClauses) + `}`,
			`{"where":` + where(maxWhereClauses+1) + `}`,
			"concept_list: where: 17 clauses: at most 16"},
		{"work_list where", "work_list",
			`{"where":` + where(maxWhereClauses) + `}`,
			`{"where":` + where(maxWhereClauses+1) + `}`,
			"work_list: where: 17 clauses: at most 16"},
		{"work_list order_by", "work_list",
			`{"order_by":` + keys(maxOrderByKeys) + `}`,
			`{"order_by":` + keys(maxOrderByKeys+1) + `}`,
			"work_list: order_by: 9 keys: at most 8"},
		{"work_list fields", "work_list",
			`{"fields":` + keys(maxWorkListFields) + `}`,
			`{"fields":` + keys(maxWorkListFields+1) + `}`,
			"work_list: fields: 9 keys: at most 8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if res := callTool(t, s, tc.tool, tc.atCap); res.IsError {
				t.Fatalf("%s at the cap refused: %s", tc.tool, res.Content[0].Text)
			}
			res := callTool(t, s, tc.tool, tc.pastCap)
			if !res.IsError || !strings.Contains(res.Content[0].Text, tc.refusal) {
				t.Fatalf("%s past the cap: isError=%v, want %q: %+v", tc.tool, res.IsError, tc.refusal, res.Content)
			}
		})
	}
}

// A refusal is a refusal of the whole call: the search is not run, so a
// refused query is not recorded as a search that found nothing.
func TestSearchQueryCapRefusesBeforeSearching(t *testing.T) {
	t.Parallel()
	_, s, l := missFixture(t)
	res := callTool(t, s, "search", fmt.Sprintf(`{"query":%q}`, strings.Repeat("x", maxSearchQueryBytes+1)))
	if !res.IsError {
		t.Fatalf("over-long query answered: %+v", res.Content)
	}
	if got := missLines(t, l); len(got) != 0 {
		t.Fatalf("refused query recorded as a miss: %+v", got)
	}
}

// graph_neighbors clamps depth to 1-12, for every principal, and reports the
// depth it used. The largest int must not keep the walk running.
func TestGraphNeighborsDepthIsClamped(t *testing.T) {
	t.Parallel()
	s := graphToolKB(t, map[string]string{
		"ops/a.md": "[b](b.md)\n",
		"ops/b.md": "[c](c.md)\n",
		"ops/c.md": "End.\n",
	})
	for _, tc := range []struct {
		name      string
		args      string
		wantDepth float64
	}{
		{"largest int", fmt.Sprintf(`{"id":"ops/a","depth":%d}`, math.MaxInt), 12},
		{"one past the maximum", `{"id":"ops/a","depth":13}`, 12},
		{"the maximum", `{"id":"ops/a","depth":12}`, 12},
		{"inside the range", `{"id":"ops/a","depth":2}`, 2},
		{"absent", `{"id":"ops/a"}`, 1},
		{"zero", `{"id":"ops/a","depth":0}`, 1},
		{"negative", `{"id":"ops/a","depth":-7}`, 1},
		{"smallest int", fmt.Sprintf(`{"id":"ops/a","depth":%d}`, math.MinInt), 1},
	} {
		for _, who := range []string{"admin", "narrow"} {
			ctx := adminCtx
			if who == "narrow" {
				ctx = narrowCtx
			}
			t.Run(tc.name+"/"+who, func(t *testing.T) {
				done := make(chan ToolResult, 1)
				go func() { done <- s.callTool(ctx, "graph_neighbors", json.RawMessage(tc.args)) }()
				var res ToolResult
				select {
				case res = <-done:
				case <-time.After(20 * time.Second):
					t.Fatal("graph_neighbors did not return")
				}
				if res.IsError {
					t.Fatalf("refused: %s", res.Content[0].Text)
				}
				out := decodeJSON(t, res.Content[0].Text)
				if out["depth"].(float64) != tc.wantDepth {
					t.Fatalf("reported depth %v, want %v: %s", out["depth"], tc.wantDepth, res.Content[0].Text)
				}
				if tc.wantDepth >= 2 && out["count"].(float64) != 2 {
					t.Fatalf("a walk of depth %v reached %v concepts, want 2: %s", tc.wantDepth, out["count"], res.Content[0].Text)
				}
			})
		}
	}
}

// The Atlas routes relay to the tools, so they refuse above the same caps.
func TestUIAPI_ReadToolCaps(t *testing.T) {
	t.Parallel()
	k := uiFixtureKB(t, "docs")
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}},
	})
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	handler := ts.Middleware(multi.Handler())

	get := func(path string) int { return getUI(t, handler, UIAPIPrefix+"/kbs/docs/"+path, "whole").Code }
	for _, tc := range []struct{ name, atCap, pastCap string }{
		{"search q", "search?q=" + strings.Repeat("x", maxSearchQueryBytes), "search?q=" + strings.Repeat("x", maxSearchQueryBytes+1)},
		{"work fields", "work?fields=" + strings.Repeat("a,", maxWorkListFields-1) + "a", "work?fields=" + strings.Repeat("a,", maxWorkListFields) + "a"},
		{"work order_by", "work?order_by=" + strings.Repeat("a,", maxOrderByKeys-1) + "a", "work?order_by=" + strings.Repeat("a,", maxOrderByKeys) + "a"},
		{"work where", "work?" + strings.Repeat("where=a%3Db&", maxWhereClauses), "work?" + strings.Repeat("where=a%3Db&", maxWhereClauses+1)},
	} {
		if code := get(tc.atCap); code != http.StatusOK {
			t.Errorf("%s at the cap: status %d", tc.name, code)
		}
		if code := get(tc.pastCap); code != http.StatusBadRequest {
			t.Errorf("%s past the cap: status %d, want 400", tc.name, code)
		}
	}
}

// The query of a multi-byte alphabet is measured in bytes: 600 runes of a
// two-byte letter are under 1024 runes and over 1024 bytes.
func TestSearchQueryCapCountsBytesNotRunes(t *testing.T) {
	t.Parallel()
	s, _ := workServer(t)
	query := strings.Repeat("é", 600)
	if n := utf8.RuneCountInString(query); n >= maxSearchQueryBytes {
		t.Fatalf("fixture is %d runes, want fewer than %d", n, maxSearchQueryBytes)
	}
	for _, tool := range []string{"search", "graph_context"} {
		res := callTool(t, s, tool, fmt.Sprintf(`{"query":%q}`, query))
		if !res.IsError || !strings.Contains(res.Content[0].Text, "query is 1200 bytes") {
			t.Fatalf("%s: 600 two-byte runes: isError=%v %+v", tool, res.IsError, res.Content)
		}
	}
}

// A chain longer than the maximum depth: the walk is seen to stop at level 12
// however far the caller asks, and a shorter walk reaches less.
func TestGraphNeighborsDepthStopsAtTheMaximumOnALongChain(t *testing.T) {
	t.Parallel()
	const length = 20
	files := map[string]string{}
	for i := 0; i < length; i++ {
		body := "End.\n"
		if i+1 < length {
			body = fmt.Sprintf("[next](n%02d.md)\n", i+1)
		}
		files[fmt.Sprintf("ops/n%02d.md", i)] = body
	}
	s := graphToolKB(t, files)
	reached := func(depth any) (reportedDepth float64, ids map[string]float64) {
		t.Helper()
		text, isErr := callJSON(t, s, adminCtx, "graph_neighbors", fmt.Sprintf(`{"id":"ops/n00","depth":%v}`, depth))
		if isErr {
			t.Fatalf("depth %v refused: %s", depth, text)
		}
		out := decodeJSON(t, text)
		ids = map[string]float64{}
		for _, raw := range out["neighbors"].([]interface{}) {
			n := raw.(map[string]interface{})
			ids[n["id"].(string)] = n["distance"].(float64)
		}
		return out["depth"].(float64), ids
	}
	for _, depth := range []any{12, 13, 50, math.MaxInt} {
		got, ids := reached(depth)
		if got != 12 || len(ids) != 12 {
			t.Fatalf("depth %v: reported %v and reached %d concepts, want 12 and 12", depth, got, len(ids))
		}
		if ids["ops/n12"] != 12 || ids["ops/n13"] != 0 {
			t.Fatalf("depth %v: the walk must end at level 12: %v", depth, ids)
		}
	}
	if _, ids := reached(5); len(ids) != 5 {
		t.Fatalf("depth 5 reached %d concepts, want 5: %v", len(ids), ids)
	}
}

// The caps are the tools' own, so the routed mount (one endpoint, a kb
// argument naming the KB) answers them the same way.
func TestRoutedMountReadToolCaps(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a", "kb-b")
	call := func(tool string, args map[string]any) ToolResult {
		t.Helper()
		args["kb"] = "kb-a"
		return routedCall(t, handler, tool, args)
	}
	refused := func(res ToolResult, want string) {
		t.Helper()
		if !res.IsError || !strings.Contains(res.Content[0].Text, want) {
			t.Fatalf("want a refusal naming %q: isError=%v %+v", want, res.IsError, res.Content)
		}
	}
	if res := call("search", map[string]any{"query": strings.Repeat("x", maxSearchQueryBytes)}); res.IsError {
		t.Fatalf("search at the cap: %+v", res.Content)
	}
	refused(call("search", map[string]any{"query": strings.Repeat("x", maxSearchQueryBytes+1)}), "at most 1024 bytes and 64 terms")
	refused(call("concept_list", map[string]any{"where": repeated(maxWhereClauses+1, "type=Runbook")}), "concept_list: where: 17 clauses")
	refused(call("work_list", map[string]any{"fields": repeated(maxWorkListFields+1, "priority")}), "work_list: fields: 9 keys")

	res := call("graph_neighbors", map[string]any{"id": "manutenzione/marker", "depth": math.MaxInt})
	if res.IsError {
		t.Fatalf("graph_neighbors: %+v", res.Content)
	}
	if out := decodeJSON(t, res.Content[0].Text); out["depth"].(float64) != 12 {
		t.Fatalf("graph_neighbors reported depth %v, want 12", out["depth"])
	}
}

// boundedQuery is the identity within the caps and, above them, a prefix of
// whole words that is itself within them.
func TestBoundedQuery(t *testing.T) {
	t.Parallel()
	for _, within := range []string{"", "backup restore procedure", "  spaced \t out  ", strings.Repeat("x", maxSearchQueryBytes), strings.TrimSpace(strings.Repeat("w ", maxSearchQueryTerms))} {
		if got := boundedQuery(within); got != within {
			t.Errorf("boundedQuery(%.30q) = %.30q, want it unchanged", within, got)
		}
	}
	words := func(n int) string {
		w := make([]string, n)
		for i := range w {
			w[i] = fmt.Sprintf("term%05d", i)
		}
		return strings.Join(w, " ")
	}
	for _, tc := range []struct {
		name, query string
		wantWords   int // 0: only checked against the caps
	}{
		{"one term past the term cap", words(maxSearchQueryTerms + 1), maxSearchQueryTerms},
		{"far past both caps", words(120_000), 0},
		{"bytes first: a cut inside a word drops the fragment", strings.Repeat("abcdefghij ", 300), 0},
		{"multi-byte words", strings.TrimSpace(strings.Repeat("àèìòù ", 400)), 0},
		{"a single word past the byte cap is cut on a rune boundary", strings.Repeat("é", 2000), 1},
		{"terms the index splits on punctuation", strings.TrimSpace(strings.Repeat("a.b ", 40)), 0},
	} {
		got := boundedQuery(tc.query)
		if err := checkQuerySize("t", got); err != nil {
			t.Errorf("%s: result is over the caps: %v", tc.name, err)
		}
		if !utf8.ValidString(got) || got == "" {
			t.Errorf("%s: result %.40q is empty or not UTF-8", tc.name, got)
		}
		if !strings.HasPrefix(strings.Join(strings.Fields(tc.query), " "), got) {
			t.Errorf("%s: result is not a prefix of the query", tc.name)
		}
		if tc.wantWords > 0 && len(strings.Fields(got)) != tc.wantWords {
			t.Errorf("%s: %d words, want %d", tc.name, len(strings.Fields(got)), tc.wantWords)
		}
		if strings.HasSuffix(tc.name, "inside a word drops the fragment") && strings.HasSuffix(got, "abcdefghi") {
			t.Errorf("%s: kept a cut word: %.40q", tc.name, got[len(got)-20:])
		}
	}
	if got := boundedQuery(strings.Repeat("a.", 600)); got != "" {
		t.Errorf("a first word past the term cap leaves no query, got %.20q", got)
	}
	if got := boundedQuery(words(10) + " " + strings.Repeat("a.b ", 40)); !strings.HasPrefix(got, words(10)) || queryTermCount(got) > maxSearchQueryTerms {
		t.Errorf("mixed query: %.60q", got)
	}
}

// searchProbe records the queries a reconciler's searches run.
type searchProbe struct {
	mu      sync.Mutex
	queries []string
}

func (p *searchProbe) attach(rec *searchReconciler) {
	rec.onSearch = func(q string) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.queries = append(p.queries, q)
	}
}

func (p *searchProbe) take() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.queries
	p.queries = nil
	return out
}

// distinctWords is n words no concept of a fixture contains.
func distinctWords(n int) string {
	w := make([]string, n)
	for i := range w {
		w[i] = fmt.Sprintf("zq%05d", i)
	}
	return strings.Join(w, " ")
}

// A write that adds a concept looks for similar titles with the search, and
// that title is stored text, not a tool argument: an oversized one must not
// make the search oversized. The lookup is advice, so the title is shortened
// to its first terms, and the write is not refused for it.
func TestSimilarTitleLookupStaysWithinTheQueryCaps(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	writeKBFile(t, k, "manutenzione/restore-backup.md", "---\ntype: Runbook\ntitle: Restore backup procedure\n---\n# R\n")
	forwardLinks(t, k, "manutenzione/backup-restore", "manutenzione/huge", "manutenzione/huge-new")
	tpl := filepath.Join(k.Root, "templates", "runbook.md")
	if err := os.MkdirAll(filepath.Dir(tpl), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tpl, []byte("---\ntype: Runbook\ntitle: \"{{title}}\"\n---\n# {{title}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := newSearchReconciler(k, nil)
	probe := &searchProbe{}
	probe.attach(rec)
	sim := &similarFinder{k: k, rec: rec, deps: Deps{}}
	s := New("test")
	s.RegisterTool(gitWrap(k, toolConceptWrite(k, sim, nil)))
	s.RegisterTool(gitWrap(k, toolConceptNew(k, sim, nil)))

	write := func(tool, id, title string) ToolResult {
		t.Helper()
		var args []byte
		if tool == "concept_write" {
			args, _ = json.Marshal(map[string]any{"id": id, "frontmatter": map[string]any{"type": "Runbook", "title": title}, "body": "# " + title + "\n"})
		} else {
			args, _ = json.Marshal(map[string]any{"template": "runbook", "id": id, "vars": map[string]any{"title": title}})
		}
		return callTool(t, s, tool, string(args))
	}

	// A normal title is searched exactly as written, and still finds its twin.
	probe.take()
	out := writeResult(t, write("concept_write", "manutenzione/backup-restore", "Backup restore procedure"))
	if q := probe.take(); len(q) != 1 || q[0] != "Backup restore procedure" {
		t.Fatalf("a normal title was searched as %q", q)
	}
	if len(out.Similar) != 1 || out.Similar[0].ID != "manutenzione/restore-backup" {
		t.Fatalf("similar for a normal title: %+v", out)
	}

	// An oversized one: one search, within the caps, from the title's first
	// terms. The write may be refused by its own gates, but the lookup ran.
	huge := distinctWords(12_000) // about 100 KB
	for _, tool := range []string{"concept_write", "concept_new"} {
		id := map[string]string{"concept_write": "manutenzione/huge", "concept_new": "manutenzione/huge-new"}[tool]
		probe.take()
		write(tool, id, huge)
		queries := probe.take()
		if len(queries) != 1 {
			t.Fatalf("%s: %d searches for an oversized title, want 1", tool, len(queries))
		}
		if err := checkQuerySize(tool, queries[0]); err != nil {
			t.Fatalf("%s: the title was searched whole: %v", tool, err)
		}
		if !strings.HasPrefix(huge, queries[0]) || len(strings.Fields(queries[0])) != maxSearchQueryTerms {
			t.Fatalf("%s: searched %.60q..., want the title's first %d terms", tool, queries[0], maxSearchQueryTerms)
		}
	}
}

// link_suggest's fallback for a concept nothing links to searches its title,
// then each term of it on its own: bounded in the size of the title and in the
// number of those single-term searches.
func TestLinkSuggestTitleFallbackStaysBounded(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	writeKBFile(t, k, "manutenzione/restore-backup.md", "---\ntype: Runbook\ntitle: Restore backup procedure\n---\n# R\n")
	writeKBFile(t, k, "manutenzione/orphan.md", "---\ntype: Note\ntitle: Backup restore notes\n---\n# O\n")
	writeKBFile(t, k, "manutenzione/orphan-huge.md", "---\ntype: Note\ntitle: "+distinctWords(5_000)+"\n---\n# H\n")
	rec := newSearchReconciler(k, nil)
	probe := &searchProbe{}
	probe.attach(rec)
	s := New("test")
	s.RegisterTool(toolLinkSuggest(k, rec, Deps{}))

	probe.take()
	res := callTool(t, s, "link_suggest", `{"id":"manutenzione/orphan"}`)
	if res.IsError || !strings.Contains(res.Content[0].Text, "manutenzione/restore-backup") {
		t.Fatalf("a normal title finds nothing: %+v", res.Content)
	}
	if q := probe.take(); len(q) == 0 || q[0] != "Backup restore notes" {
		t.Fatalf("a normal title was searched as %q", q)
	}

	res = callTool(t, s, "link_suggest", `{"id":"manutenzione/orphan-huge"}`)
	if res.IsError {
		t.Fatalf("oversized title refused: %+v", res.Content)
	}
	queries := probe.take()
	if len(queries) < 2 || len(queries) > 1+8 {
		t.Fatalf("%d searches for an oversized title, want the whole title and at most 8 single terms", len(queries))
	}
	for _, q := range queries {
		if err := checkQuerySize("link_suggest", q); err != nil {
			t.Fatalf("searched %.40q...: %v", q, err)
		}
	}
	if len(strings.Fields(queries[0])) != maxSearchQueryTerms {
		t.Fatalf("the whole-title search has %d terms, want the first %d", len(strings.Fields(queries[0])), maxSearchQueryTerms)
	}
}

// A glossary alias is stored text, not a tool argument, and the variant it
// makes for a short query can be longer than the argument bounds allow: each
// variant is shortened like the query before it reaches the index.
func TestSearchGlossaryVariantStaysWithinTheQueryBounds(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	writeKBFile(t, k, "arch/zephyr.md", "---\ntype: Note\ntitle: Zephyr\n---\n# Zephyr\n\nThe zephyr scheduler.\n")
	// A glossary term is at most 80 bytes, so the variant outgrows the bounds
	// only from a query near them: 63 terms, and an alias of 20 more.
	alias := strings.TrimSpace(strings.Repeat("ab ", 20))
	gloss := "terms:\n  - canonical: Zephyr\n    aliases: [\"" + alias + "\"]\n"
	if err := os.WriteFile(filepath.Join(k.Root, "glossary.yaml"), []byte(gloss), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := newSearchReconciler(k, nil)
	probe := &searchProbe{}
	probe.attach(rec)
	s := New("test")
	s.RegisterTool(toolSearch(k, rec, nil, nil, Deps{}))

	probe.take()
	res := callTool(t, s, "search", `{"query":"zephyr `+distinctWords(62)+`"}`)
	if res.IsError {
		t.Fatalf("search refused: %+v", res.Content)
	}
	queries := probe.take()
	if len(queries) < 2 {
		t.Fatalf("%d searches, want the query and its glossary variant: %q", len(queries), queries)
	}
	for _, q := range queries {
		if err := checkQuerySize("search", q); err != nil {
			t.Fatalf("a glossary variant reached the index unbounded: %.40q...: %v", q, err)
		}
	}
}
