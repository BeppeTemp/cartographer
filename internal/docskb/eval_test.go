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

// maxFallbackOnly is the size of testdata/fallback_only.txt: the questions whose wanted
// page is among the first ten search hits only as a partial hit of the OR fallback (D343),
// exempt from the full-match rule (#2441). The list only shrinks, and the constant with
// it: the test fails unless the two are equal, so a line cannot be added without raising
// this number in review, and a line removed lowers it. A new question, or one that loses
// its full match, is fixed in the docs wording.
const maxFallbackOnly = 23

// loadFallbackOnly reads the allow-list: `<question><TAB><page>` lines, the question as in
// questions.jsonl and the page the wanted id the fallback finds for it. The exemption is
// for that pair: a listed question found through another page fails like an unlisted one.
func loadFallbackOnly(t *testing.T, qs []question) map[string]string {
	t.Helper()
	data, err := os.ReadFile("testdata/fallback_only.txt")
	if err != nil {
		t.Fatal(err)
	}
	wants := map[string][]string{}
	for _, q := range qs {
		wants[q.Q] = q.Want
	}
	list := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		q, page, ok := strings.Cut(line, "\t")
		switch want, known := wants[q]; {
		case !ok || page == "":
			t.Errorf("fallback_only.txt: %q is not a <question><TAB><page> line", line)
		case !known:
			t.Errorf("fallback_only.txt: %q is not a question of questions.jsonl: remove the line and lower maxFallbackOnly", q)
		case !intersects([]string{page}, want):
			t.Errorf("fallback_only.txt: %q is not a page the question %q wants (%v)", page, q, want)
		}
		if _, dup := list[q]; dup {
			t.Errorf("fallback_only.txt: %q is listed twice", q)
		}
		list[q] = page
	}
	if len(list) != maxFallbackOnly {
		t.Errorf("fallback_only.txt has %d questions and maxFallbackOnly is %d: the two are equal and only go down (a question that needs the OR fallback is fixed in the docs wording, a line removed lowers the constant)", len(list), maxFallbackOnly)
	}
	return list
}

func TestRetrievalEval(t *testing.T) {
	qs := loadQuestions(t)
	fallbackOnly := loadFallbackOnly(t, qs)
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
		// A question's target should be a full (AND) match of the query, not a
		// fallback hit: a page that only matches some of the terms passes or fails
		// by how many full matches other pages happen to have. Put the question's
		// words in the target page, not only the ids it is accepted under.
		var sr struct {
			Results []struct {
				ID      string
				Partial bool `json:"partial"`
			} `json:"results"`
		}
		toolJSON(t, r, "search", map[string]any{"query": query}, &sr)
		// A partial hit matched only some of the words (the OR fallback, D343): it ranks by
		// what the other pages say, so a docs edit elsewhere pushes it out of the ten.
		var hits, full, partial []string
		for i, h := range sr.Results {
			if i >= searchK {
				break
			}
			hits = append(hits, h.ID)
			if h.Partial {
				partial = append(partial, h.ID)
			} else {
				full = append(full, h.ID)
			}
		}
		switch {
		case !intersects(hits, q.Want):
			t.Errorf("search %q: want one of %v\n  hits: %v", query, q.Want, hits)
		case intersects(full, q.Want):
			if _, listed := fallbackOnly[q.Q]; listed {
				t.Errorf("search %q: %v is now a full match: remove %q from testdata/fallback_only.txt and lower maxFallbackOnly", query, wanted(full, q.Want), q.Q)
			}
		default:
			// Only through the fallback: allowed for the listed pair of question and page.
			found := wanted(partial, q.Want)
			if page, listed := fallbackOnly[q.Q]; !listed {
				t.Errorf("search %q: %v is found only through the OR fallback (no page holds every word; %d full matches): word the page so that it holds them, the allow-list takes no new question", query, found, len(full))
			} else if found[0] != page {
				t.Errorf("search %q: found only through the OR fallback, now by %s and not by %s, the page testdata/fallback_only.txt lists: word %s so that it holds every word of the question", query, found[0], page, page)
			}
		}
	}
}

// wanted returns the ids of got that the question wants, in rank order.
func wanted(got, want []string) []string {
	var out []string
	for _, g := range got {
		if intersects([]string{g}, want) {
			out = append(out, g)
		}
	}
	return out
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
