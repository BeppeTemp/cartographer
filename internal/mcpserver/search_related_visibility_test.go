package mcpserver

import (
	"strings"
	"sync"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// countingVisibility is a visibility memo whose check runs the real policy,
// except that it hides `hide` and counts what it was asked. Installed in a
// request's context it stands in for the memo search would create, so a test
// sees who asks it.
func countingVisibility(ctx requestContext, k *kb.KB, hide string) (requestContext, func() map[string]int) {
	var mu sync.Mutex
	asked := map[string]int{}
	m := newVisibilityMemo(k, func(id string) bool {
		mu.Lock()
		asked[id]++
		mu.Unlock()
		return id != hide && Visible(ctx, k, id)
	})
	return contextWithMemo(ctx, m), func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]int{}
		for id, n := range asked {
			out[id] = n
		}
		return out
	}
}

var (
	wholeKBPolicy  = auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}
	narrowedPolicy = auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"ops"}}}}
)

// A narrowed principal's block is built on the visibility the ranking already
// computed: the memo is the one source of it. A concept that memo hides is
// absent from the block, which a second, independent visibility pass would
// not know.
func TestSearchRelated_NarrowedBlockReusesTheRankingsVisibility(t *testing.T) {
	t.Parallel()
	s, k := graphToolKBWithKB(t, relatedKB())
	args := `{"query":"zeppelin","related":10}`

	base := restrictedContext(narrowedPolicy)
	text, _ := callJSON(t, s, base, "search", args)
	if !contains(relatedIDs(decodeJSON(t, text)), "ops/gamma") {
		t.Fatalf("fixture: gamma should be in the narrowed block: %s", text)
	}

	ctx, asked := countingVisibility(base, k, "ops/gamma")
	text, _ = callJSON(t, s, ctx, "search", args)
	ids := relatedIDs(decodeJSON(t, text))
	if contains(ids, "ops/gamma") {
		t.Errorf("the block asked visibility again instead of reusing the ranking's: %v", ids)
	}
	if !contains(ids, "ops/beta") {
		t.Errorf("fixture: beta should still be offered: %v", ids)
	}
	for id, n := range asked() {
		if n != 1 {
			t.Errorf("visibility of %s was decided %d times in one search", id, n)
		}
	}
	all, err := k.LinkGraph(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(asked()) != len(all.IDs) {
		t.Errorf("visibility decided for %d concepts, want the %d of the graph: the memo is not the ranking's", len(asked()), len(all.IDs))
	}
}

// A token that sees the whole KB has nothing to filter: the block asks for no
// visibility at all, as the ranking's centrality prior does not.
func TestSearchRelated_WholeKBTokenAsksNoVisibility(t *testing.T) {
	t.Parallel()
	s, k := graphToolKBWithKB(t, relatedKB())
	ctx, asked := countingVisibility(restrictedContext(wholeKBPolicy), k, "ops/gamma")
	text, _ := callJSON(t, s, ctx, "search", `{"query":"zeppelin","related":10}`)
	ids := relatedIDs(decodeJSON(t, text))
	if !contains(ids, "ops/gamma") {
		t.Errorf("the block filtered a whole-KB principal's graph: %v", ids)
	}
	// The ranking decides visibility of the candidates it fetches: one page
	// mentions the term.
	if n := len(asked()); n > 1 {
		t.Errorf("visibility decided for %d concepts, want only the one candidate: %v", n, asked())
	}
	admin, _ := callJSON(t, s, adminCtx, "search", `{"query":"zeppelin","related":10}`)
	if strings.TrimSpace(admin) != strings.TrimSpace(text) {
		t.Errorf("a whole-KB token and an admin got different answers:\n%s\n%s", text, admin)
	}
}
