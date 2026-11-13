package docskb

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

// The retrieval eval (D9): the questions of testdata/questions.jsonl are asked
// to the real read tools over the KB generated from the real docs, exactly as
// `cartographer docs` runs them. Both numbers are what an agent actually reads:
// the skill opens at most five sections per question, and a hit below the tenth
// is not found by an agent that reads snippets. Loosening either is a
// maintainer decision in review, never a per-question exception; a failing
// question is fixed in the docs wording.
const (
	graphResultsK = 5  // first ranked graph_context results, after the seeds
	searchK       = 10 // first search hits
)

type question struct {
	Q    string   `json:"q"`
	Want []string `json:"want"`
}

func loadQuestions(t *testing.T) []question {
	t.Helper()
	f, err := os.Open("testdata/questions.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var qs []question
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var q question
		if err := json.Unmarshal([]byte(line), &q); err != nil || q.Q == "" || len(q.Want) == 0 {
			t.Fatalf("questions.jsonl:%d: want {\"q\":..., \"want\":[...]}: %v", n, err)
		}
		qs = append(qs, q)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return qs
}

// toolJSON calls a read tool and decodes the JSON text of its answer.
func toolJSON(t *testing.T, r *Runner, name string, args map[string]any, out any) {
	t.Helper()
	res := call(t, r, "tools/call", map[string]any{"name": name, "arguments": args})
	var tr struct {
		Content []struct{ Text string } `json:"content"`
	}
	if err := json.Unmarshal(res, &tr); err != nil || len(tr.Content) == 0 {
		t.Fatalf("%s: unreadable answer %.200s: %v", name, res, err)
	}
	if err := json.Unmarshal([]byte(tr.Content[0].Text), out); err != nil {
		t.Fatalf("%s: %v: %.200s", name, err, tr.Content[0].Text)
	}
}

func firstIDs(ids []string, n int) []string {
	if len(ids) > n {
		return ids[:n]
	}
	return ids
}

func intersects(got, want []string) bool {
	for _, g := range got {
		for _, w := range want {
			if g == w {
				return true
			}
		}
	}
	return false
}

func TestRetrievalEval(t *testing.T) {
	qs := loadQuestions(t)
	r := sharedRunner(t) // read-only: the runner of embed_test.go is shared
	for _, q := range qs {
		query := keywords(q.Q)
		var gc struct {
			Seeds   []struct{ ID string } `json:"seeds"`
			Results []struct{ ID string } `json:"results"`
		}
		toolJSON(t, r, "graph_context", map[string]any{"query": query, "limit": graphResultsK}, &gc)
		// What the agent sees: the seeds (the top search hits) then the ranked
		// neighbours, which exclude the seeds (Context 6 of the plan).
		var seen, ranked []string
		for _, s := range gc.Seeds {
			seen = append(seen, s.ID)
		}
		for _, x := range gc.Results {
			ranked = append(ranked, x.ID)
		}
		seen = append(seen, firstIDs(ranked, graphResultsK)...)
		if !intersects(seen, q.Want) {
			t.Errorf("graph_context %q: want one of %v\n  seen: %v", query, q.Want, seen)
		}
		var sr struct {
			Results []struct{ ID string } `json:"results"`
		}
		toolJSON(t, r, "search", map[string]any{"query": query}, &sr)
		var hits []string
		for _, h := range sr.Results {
			hits = append(hits, h.ID)
		}
		if !intersects(firstIDs(hits, searchK), q.Want) {
			t.Errorf("search %q: want one of %v\n  hits: %v", query, q.Want, firstIDs(hits, searchK))
		}
	}
}

// TestEvalCoverage keeps the question set honest: every page of the docs map
// plus the root pages has at least two questions, every expected id exists in
// the generated KB, and no question is asked twice.
func TestEvalCoverage(t *testing.T) {
	qs := loadQuestions(t)
	files, err := Embedded("dev")
	if err != nil {
		t.Fatal(err)
	}
	// Concept ids: an expanded concept's file is <id>/index.md.
	ids := map[string]bool{}
	var pages []string
	for k := range files {
		id, ok := strings.CutPrefix(k, "data/")
		if !ok || !strings.HasSuffix(id, ".md") {
			continue
		}
		id = strings.TrimSuffix(strings.TrimSuffix(id, ".md"), "/index")
		ids[id] = true
		if strings.Count(id, "/") == 1 && !strings.HasPrefix(id, "decisions/") && !strings.HasSuffix(id, "/_map") {
			pages = append(pages, id)
		}
	}
	sort.Strings(pages)
	count := map[string]int{}
	seen := map[string]bool{}
	for _, q := range qs {
		if seen[q.Q] {
			t.Errorf("duplicate question %q", q.Q)
		}
		seen[q.Q] = true
		pgs := map[string]bool{}
		for _, w := range q.Want {
			if !ids[w] {
				t.Errorf("%q: want %q is not a concept of the generated KB", q.Q, w)
			}
			parts := strings.Split(w, "/")
			if len(parts) >= 2 {
				pgs[parts[0]+"/"+parts[1]] = true
			}
		}
		for p := range pgs {
			count[p]++
		}
	}
	var short []string
	for _, p := range pages {
		if count[p] < 2 {
			short = append(short, p)
		}
	}
	if len(short) > 0 {
		t.Errorf("pages with fewer than 2 questions: %v", short)
	}
	decisions := 0
	for _, q := range qs {
		for _, w := range q.Want {
			if strings.HasPrefix(w, "decisions/") {
				decisions++
				break
			}
		}
	}
	if decisions < 12 {
		t.Errorf("%d questions about decisions, want at least 12", decisions)
	}
}

// stopwords are the question words that carry no topic. The skill tells an
// agent to search with the content words of a question, not the whole sentence
// (a keyword index ORs the words of a sentence no page holds in full, and "how"
// and "does" then outweigh the topic); the eval asks the same way, for every
// question alike.
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields("how do does did i what which is are the a an to of in it my for and or can should why when where that not on with so its from be at by there instead one no need go every") {
		stopwords[w] = true
	}
}

func keywords(q string) string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(q)) {
		if w = strings.Trim(w, "?,.!"); w != "" && !stopwords[w] {
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}
