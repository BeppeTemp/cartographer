package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// A hit carries its file's size, so an agent knows what reading it whole costs
// before it pays it (D301).
func TestSearchHitsCarryBytes(t *testing.T) {
	body := "---\ntype: Note\ntitle: Gate\n---\nThe gateway runs here.\n"
	s := graphToolKB(t, map[string]string{"ops/gate.md": body})
	text, isErr := callJSON(t, s, adminCtx, "search", `{"query":"gateway"}`)
	if isErr {
		t.Fatal(text)
	}
	var out struct {
		Results []struct {
			ID    string `json:"id"`
			Bytes int64  `json:"bytes"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].ID != "ops/gate" || out.Results[0].Bytes != int64(len(body)) {
		t.Fatalf("results = %+v, want ops/gate with bytes %d", out.Results, len(body))
	}
}

// A scope no visible concept starts with is a wrong argument: refused with the
// prefixes that do exist, and never recorded as a knowledge gap.
func TestSearchRefusesAScopeThatMatchesNothing(t *testing.T) {
	_, s, misses := missFixture(t)
	for _, scope := range []string{"docs", "docs/", "nothing-here/"} {
		args, _ := json.Marshal(map[string]string{"query": "runbook", "scope": scope})
		tr := s.callTool(authLocalContext(), "search", args)
		text := tr.Content[0].Text
		if !tr.IsError {
			t.Fatalf("scope %q: want an error, got %s", scope, text)
		}
		for _, want := range []string{`scope "` + scope + `" matches no concept`, "not a KB name", "here: manutenzione."} {
			if !strings.Contains(text, want) {
				t.Fatalf("scope %q: error lacks %q: %s", scope, want, text)
			}
		}
	}
	if got := missLines(t, misses); len(got) != 0 {
		t.Fatalf("an unmatched scope was recorded as a miss: %+v", got)
	}
	// A scope that matches concepts keeps today's answer, miss included.
	out := decodeJSON(t, callOK(t, s, "search", `{"query":"zebrafish","scope":"manutenzione/"}`))
	if out["count"].(float64) != 0 {
		t.Fatalf("scoped empty search = %v", out)
	}
	if got := missLines(t, misses); len(got) != 1 {
		t.Fatalf("a matched scope with no hit is a miss as before: %+v", got)
	}
	// A partial prefix is a prefix: it matches.
	out = decodeJSON(t, callOK(t, s, "search", `{"query":"runbook","scope":"manut"}`))
	if out["count"].(float64) != 1 {
		t.Fatalf("partial-prefix scope = %v", out)
	}
}

// A narrowed caller is answered from what it can see. A scope over a map it is
// not granted is refused earlier, by the policy; a granted map with nothing in
// it reaches this check, which lists only the maps the caller can see.
func TestSearchUnmatchedScopeListsOnlyVisiblePrefixes(t *testing.T) {
	s := graphToolKB(t, map[string]string{
		"ops/gate.md":      "The gateway runs here.\n",
		"hidden/secret.md": "The gateway is hidden here.\n",
	})
	narrow := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"ops", "empty"}}}})
	text, isErr := callJSON(t, s, narrow, "search", `{"query":"gateway","scope":"empty"}`)
	if !isErr {
		t.Fatalf("an unmatched scope must be refused: %s", text)
	}
	if !strings.Contains(text, "Maps and journals here: ops. ") {
		t.Fatalf("a narrowed caller must be shown only its visible map: %s", text)
	}
	text, isErr = callJSON(t, s, adminCtx, "search", `{"query":"gateway","scope":"empty"}`)
	if !isErr || !strings.Contains(text, "here: hidden, manutenzione, ops. ") {
		t.Fatalf("the admin is shown every map: %s", text)
	}
}
