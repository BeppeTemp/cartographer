package mcpserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// A request is one unit of work: the reconcile, the ranking view and the
// archived set are paid once, not once per glossary variant or fallback pass.

func searchCostKB(t *testing.T, variants int) (*Server, *kb.KB) {
	t.Helper()
	files := relatedKB()
	files["ops/old.md"] = "---\ntype: Note\ntitle: Old\nstatus: archived\n---\nOnly archived coelacanth lives here.\n"
	s, k := graphToolKBWithKB(t, files)
	var b strings.Builder
	b.WriteString("terms:\n  - canonical: Zeppelin Hangar\n    aliases: [")
	for i := 0; i < variants; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "zalias%d", i)
	}
	b.WriteString("]\n")
	if err := os.WriteFile(filepath.Join(k.Root, kb.GlossaryFile), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return s, k
}

func TestSearchCost_ValidationsDoNotGrowWithVariantsOrFallback(t *testing.T) {
	t.Parallel()
	cost := func(variants int, tool, args string) int {
		s, k := searchCostKB(t, variants)
		mustText(t, s, tool, args) // warm: the first call builds the index
		before := k.GraphValidations()
		mustText(t, s, tool, args)
		return k.GraphValidations() - before
	}
	for _, tc := range []struct{ name, tool, args string }{
		{"hit", "search", `{"query":"zeppelin"}`},
		{"miss", "search", `{"query":"nonexistentterm"}`},
		{"archived fallback", "search", `{"query":"coelacanth"}`},
		{"glossary", "search", `{"query":"zalias0"}`},
		{"graph_context query", "graph_context", `{"ids":["ops/hub"],"query":"zeppelin"}`},
	} {
		few, many := cost(1, tc.tool, tc.args), cost(6, tc.tool, tc.args)
		if few != many {
			t.Errorf("%s: %d validations with 1 alias, %d with 6", tc.name, few, many)
		}
		limit := 1
		if tc.tool == "graph_context" {
			limit = 2 // its own visibleGraph validates once more
		}
		if many > limit {
			t.Errorf("%s: %d graph validations, want at most %d", tc.name, many, limit)
		}
	}
}

func TestSearchCost_ArchivedFilterAsksNoFacetsPerCandidate(t *testing.T) {
	t.Parallel()
	files := map[string]string{}
	for i := 0; i < 200; i++ {
		files[fmt.Sprintf("ops/page%03d.md", i)] = "Common walrus text.\n"
	}
	s, k := graphToolKBWithKB(t, files)
	args := `{"query":"walrus","limit":5,"related":0}`
	mustText(t, s, "search", args)
	before := k.GraphFacetLookups()
	out := mustText(t, s, "search", args)
	// The 5 returned hits are annotated one by one; the 200 candidates are not.
	if got := k.GraphFacetLookups() - before; got > 5 || !strings.Contains(out, "ops/page") {
		t.Errorf("%d ConceptFacets lookups for a search over 200 candidates (output %.80s)", got, out)
	}
}

// The optimisations must not change an answer: hits and order, with a
// glossary expansion and the archived fallback, for an admin and a narrowed
// token alike.
func TestSearchCost_AnswersAreUnchanged(t *testing.T) {
	t.Parallel()
	s, _ := searchCostKB(t, 2)
	ids := func(ctx requestContext, args string) string {
		text, isErr := callJSON(t, s, ctx, "search", args)
		if isErr {
			t.Fatal(text)
		}
		out := decodeJSON(t, text)
		return fmt.Sprintf("%s|%v|%v", strings.Join(resultIDs(out["results"]), ","), out["expanded_to"], out["archived_fallback"])
	}
	for _, tc := range []struct{ args, want string }{
		{`{"query":"zeppelin","related":0}`, "ops/alpha|<nil>|<nil>"},
		{`{"query":"zalias1","related":0}`, "ops/alpha|[zeppelin hangar zalias0]|<nil>"},
		{`{"query":"coelacanth","related":0}`, "ops/old|<nil>|true"},
		{`{"query":"nonexistentterm","related":0}`, "|<nil>|<nil>"},
	} {
		for _, ctx := range []requestContext{adminCtx, narrowCtx} {
			if got := ids(ctx, tc.args); got != tc.want {
				t.Errorf("%s: %q, want %q", tc.args, got, tc.want)
			}
		}
	}
}
