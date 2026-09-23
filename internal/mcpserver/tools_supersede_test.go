package mcpserver

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// supersede validates its successor and updates the search indexes (D243).
func TestSupersedeValidatesAndIndexes(t *testing.T) {
	s := graphToolKB(t, map[string]string{
		"ops/old.md":       "The old way.\n",
		"ops/new.md":       "The new way.\n",
		"hidden/secret.md": "Hidden.\n",
		"ops/bystander.md": "Unrelated.\n",
	})
	k := s.kbRef

	if text, isErr := callJSON(t, s, adminCtx, "supersede", `{"source_id":"ops/old","target_id":"ops/old"}`); !isErr || !strings.Contains(text, "a concept cannot supersede itself") {
		t.Fatalf("self: %s", text)
	}
	// A missing successor is refused by the handler; one outside what the
	// caller may write is refused earlier by the policy, with the same text
	// whether it exists or not.
	if text, isErr := callJSON(t, s, adminCtx, "supersede", `{"source_id":"ops/old","target_id":"ops/nope"}`); !isErr || text != "supersede: target not found: ops/nope" {
		t.Fatalf("missing target: %q", text)
	}
	writer := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"ops"}, Write: true}}})
	missing, mErr := callJSON(t, s, writer, "supersede", `{"source_id":"ops/old","target_id":"hidden/nope"}`)
	hidden, hErr := callJSON(t, s, writer, "supersede", `{"source_id":"ops/old","target_id":"hidden/secret"}`)
	if !mErr || !hErr || missing != hidden {
		t.Fatalf("missing %q / hidden %q", missing, hidden)
	}

	logBefore, _ := k.ReadRaw("log.md")
	if text, isErr := callJSON(t, s, adminCtx, "supersede", `{"source_id":"ops/old","target_id":"ops/new","reason":"zanzibarquux"}`); isErr {
		t.Fatal(text)
	}
	// Search sees the rewritten frontmatter at once, with no reconcile.
	out := decodeJSON(t, mustText(t, s, "search", `{"query":"zanzibarquux"}`))
	if ids := resultIDs(out["results"]); len(ids) != 1 || ids[0] != "ops/old" {
		t.Fatalf("search after supersede = %v", out["results"])
	}
	logAfter, _ := k.ReadRaw("log.md")
	added := strings.TrimPrefix(logAfter, logBefore)
	if strings.Count(added, "supersede:") != 1 || !strings.Contains(added, "supersede: ops/old → ops/new") {
		t.Fatalf("log gained %q", added)
	}
}

// concept_move carries superseded_by along with the links (D243).
func TestConceptMoveRewritesSupersededBy(t *testing.T) {
	files := map[string]string{
		"ops/old.md":  "---\ntype: Note\nstatus: superseded\nsuperseded_by: ops/new\n---\nOld.\n",
		"ops/new.md":  "New.\n",
		"ops/a.md":    "---\ntype: Note\nstatus: superseded\nsuperseded_by: ops/b\n---\nA.\n",
		"ops/b.md":    "B.\n",
		"ops/kept.md": "---\ntype: Note\nsuperseded_by: ops/stay\n---\nKept.\n",
		"ops/stay.md": "Stay.\n",
	}

	s := graphToolKB(t, files)
	mustText(t, s, "concept_move", `{"source_id":"ops/new","target_id":"ops/newer"}`)
	if got := supersededBy(t, s, "ops/old"); got != "ops/newer" {
		t.Fatalf("predecessor points at %q after its successor moved", got)
	}

	// Both ends in one batch: the moved predecessor is walked under its new id.
	mustText(t, s, "concept_move", `{"moves":[{"source_id":"ops/a","target_id":"ops/a2"},{"source_id":"ops/b","target_id":"ops/b2"}]}`)
	if got := supersededBy(t, s, "ops/a2"); got != "ops/b2" {
		t.Fatalf("batch: %q", got)
	}

	// rewrite_links: false leaves relations alone too, and lint says so.
	mustText(t, s, "concept_move", `{"source_id":"ops/stay","target_id":"ops/gone","rewrite_links":false}`)
	if got := supersededBy(t, s, "ops/kept"); got != "ops/stay" {
		t.Fatalf("rewrite_links false changed it to %q", got)
	}
	findings, err := lint.Run(s.kbRef, "", false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range findings {
		if f.Path == "ops/kept.md" && f.Check == "broken_relation" {
			found = true
		}
	}
	if !found {
		t.Fatal("the dangling superseded_by was not reported")
	}
}

func supersededBy(t *testing.T, s *Server, id string) string {
	t.Helper()
	data, err := s.kbRef.ReadConcept(okf.ConceptID(id))
	if err != nil {
		t.Fatal(err)
	}
	fm, err := okf.ParseFrontmatter(data.FrontmatterRaw)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := fm.Get("superseded_by")
	str, _ := v.(string)
	return str
}
