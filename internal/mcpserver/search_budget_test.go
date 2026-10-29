package mcpserver

import (
	"fmt"
	"strings"
	"testing"
)

// searchBudgetBytes is the average size a default `search` answer may reach.
// D1354 restored the v0.26 default (limit 20, a related block of 5) after the
// lighter one of D1185 (limit 8, no block) cost answer quality: this fixture
// measures 4588 bytes on average with it, and the budget is that plus about
// 10%. The rule of D1185, "do not raise the number", is superseded: the number
// is the measured average plus a margin, and moves only when the default does,
// in a decision that says why. What the test still catches is the answer
// growing by itself (a longer snippet, a new field on every hit).
const searchBudgetBytes = 5000

// searchBudgetKB generates about 40 concepts with realistic titles, ~200-char
// snippets and links, so that a default query matches more pages than the
// default limit and the limit bites (the e2e fixture has only 10 concepts).
func searchBudgetKB() map[string]string {
	subjects := []string{"backup", "monitoring", "certificate", "storage", "network", "database", "deployment", "firewall", "cluster", "ingress"}
	kinds := []string{"runbook", "incident", "overview", "checklist"}
	files := map[string]string{}
	for i, subj := range subjects {
		for j, kind := range kinds {
			id := fmt.Sprintf("ops/%s-%s", subj, kind)
			next := fmt.Sprintf("%s-%s", subjects[(i+1)%len(subjects)], kinds[j])
			title := strings.ToUpper(subj[:1]) + subj[1:] + " " + kind
			body := fmt.Sprintf("# %s\n\nThis %s covers the %s service of the platform: how it is operated, who owns it, "+
				"which alerts fire and what to check first when the nightly job fails. See [%s](%s.md) for the neighbouring service "+
				"and the maintenance window agreed with the platform team.\n", title, kind, subj, next, next)
			files[id+".md"] = fmt.Sprintf("---\ntype: Note\ntitle: %s\n---\n%s", title, body)
		}
	}
	files["ops/glossary-ingress.md"] = "---\ntype: Glossary\ntitle: Ingress\naliases: [edge-proxy]\n---\n# Ingress\n\nThe ingress is the edge proxy that terminates TLS for every public service.\n"
	return files
}

func TestSearchDefaultAnswerBudget(t *testing.T) {
	t.Parallel()
	s, _ := graphToolKBWithKB(t, searchBudgetKB())
	queries := []string{
		`{"query":"runbook"}`,
		`{"query":"incident"}`,
		`{"query":"backup"}`,
		`{"query":"service platform"}`,
		`{"query":"nightly job fails"}`,
		`{"query":"alerts owner"}`,
		`{"query":"maintenance window"}`,
		`{"query":"certificate checklist"}`,
		`{"query":"edge-proxy"}`,
		`{"query":"ingress","scope":"ops"}`,
		`{"query":"storage overview","scope":"ops"}`,
		`{"query":"firewall network","scope":"ops"}`,
		`{"query":"neighbouring service"}`,
		`{"query":"zzzzabsent","record_miss":false}`,
	}
	total, withRelated := 0, 0
	for _, q := range queries {
		res := callTool(t, s, "search", q)
		if res.IsError {
			t.Fatalf("%s: %s", q, res.Content[0].Text)
		}
		out := decodeJSON(t, res.Content[0].Text)
		if rs, ok := out["results"].([]interface{}); ok && len(rs) > searchDefaultLimit {
			t.Errorf("%s: %d hits, over the default limit %d", q, len(rs), searchDefaultLimit)
		}
		if _, ok := out["related"]; ok {
			withRelated++
		}
		total += len(res.Content[0].Text)
	}
	// The budget measures the default as it is restored (D1354): a fixture whose
	// answers carry no related block would measure a lighter answer than agents get.
	if withRelated == 0 {
		t.Error("no default search answer carries a related block: the budget is not measuring the default")
	}
	if avg := total / len(queries); avg > searchBudgetBytes {
		t.Errorf("a default search answer averages %d bytes over %d queries, budget %d", avg, len(queries), searchBudgetBytes)
	} else {
		t.Logf("default search answer: %d bytes on average (budget %d)", avg, searchBudgetBytes)
	}
}

// The limit is a default, not a clamp: absent gives 20 hits of a larger
// answer set, an explicit limit gives what it says, above or below the default.
func TestSearchDefaultLimit(t *testing.T) {
	t.Parallel()
	s, _ := graphToolKBWithKB(t, searchBudgetKB())
	for _, tc := range []struct {
		args string
		want int
	}{
		{`{"query":"runbook"}`, 11},
		{`{"query":"service"}`, 20},
		{`{"query":"service","limit":8}`, 8},
		{`{"query":"service","limit":3}`, 3},
		{`{"query":"service","limit":30}`, 30},
	} {
		out := decodeJSON(t, mustText(t, s, "search", tc.args))
		if got := len(resultIDs(out["results"])); got != tc.want {
			t.Errorf("%s: %d hits, want %d", tc.args, got, tc.want)
		}
	}
}
