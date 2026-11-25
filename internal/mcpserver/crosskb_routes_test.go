package mcpserver

import (
	"encoding/json"
	"github.com/BeppeTemp/cartographer/internal/auth"
	"net/http"
	"net/http/httptest"
	"testing"
)

// routeJSON calls a UI handler directly, as the caller of ctx.
func routeJSON(t *testing.T, s *Server, who, path string, call func(w http.ResponseWriter, r *http.Request)) map[string]any {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil).WithContext(crossCallers()[who])
	w := httptest.NewRecorder()
	call(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("%s as %s: %d %s", path, who, w.Code, w.Body.String())
	}
	return decodeUI(t, w)
}

func rowsOf(body map[string]any, key string) []map[string]any {
	var rows []map[string]any
	list, _ := body[key].([]any)
	for _, r := range list {
		rows = append(rows, r.(map[string]any))
	}
	return rows
}

// AC-cross-kb-links-19
func TestCrossRoutes(t *testing.T) {
	t.Parallel()
	s, r := edgesRig(t)
	concept := func(who string) map[string]any {
		return routeJSON(t, s, who, "/concept?id=p", func(w http.ResponseWriter, q *http.Request) { uiConcept(w, q, r.a, s) })
	}
	external := func(who, query string) map[string]any {
		return routeJSON(t, s, who, "/graph/external"+query, func(w http.ResponseWriter, q *http.Request) { uiGraphExternal(w, q, r.a) })
	}

	// The concept route answers the tool's rows, state for state, and its cross
	// is a list (never null) when empty.
	for _, who := range []string{"admin", "reads-a-b", "b-scoped", "a-only"} {
		got := rowsOf(concept(who), "cross")
		want := crossRows(neighbors(t, s, crossCallers()[who], `{"id":"p","direction":"both"}`))
		if !jsonEqual(t, got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("%s: concept %v, tool %v", who, got, want)
		}
	}
	if c := concept("a-only"); c["cross"] == nil {
		t.Error("cross is null for a caller who reads no other KB")
	}
	q := routeJSON(t, s, "reads-a-b", "/concept?id=q", func(w http.ResponseWriter, x *http.Request) { uiConcept(w, x, r.a, s) })
	if list, ok := q["cross"].([]any); !ok || len(list) != 0 {
		t.Errorf("cross of a page with none = %v", q["cross"])
	}

	// The external edges: the same states, per caller, with the page they belong to.
	ext := rowsOf(external("reads-a-b", ""), "edges")
	var states = map[string]string{}
	for _, e := range ext {
		if e["from"] != "p" {
			t.Errorf("edge from %v", e["from"])
		}
		st, _ := e["state"].(string)
		states[e["direction"].(string)+" "+e["kb"].(string)+"/"+e["id"].(string)] = st
	}
	for k, want := range map[string]string{"out kb-b/ops/x": "resolved", "out kb-b/ops/gone": "missing", "out nope/z": "unresolved", "in kb-b/ops/back": "", "in kb-b/priv/hid": ""} {
		if got, ok := states[k]; !ok || got != want {
			t.Errorf("reads-a-b %s = %q (present %v), want %q", k, got, ok, want)
		}
	}
	if got := rowsOf(external("a-only", ""), "edges"); len(got) != 3 {
		t.Errorf("a-only edges = %v (the three out rows, neutral, and no in row)", got)
	} else {
		for _, e := range got {
			if e["direction"] != "out" || e["state"] != "unresolved" {
				t.Errorf("a-only edge %v", e)
			}
		}
	}
	if got := rowsOf(external("reads-a-b", "?scope=nothing"), "edges"); len(got) != 0 {
		t.Errorf("scope with no page: %v", got)
	}
	if body := external("reads-a-b", "?limit=2"); len(rowsOf(body, "edges")) != 2 || body["truncated"] != true {
		t.Errorf("limit 2: %v", body)
	}
	if body := external("a-only", ""); body["edges"] == nil {
		t.Error("edges is null")
	}

	// A page of kb-b linking a page kb-a does not hold draws no edge from it, in
	// the route or in graph_neighbors.
	putThroughServer(t, r.b, "ops/orphan-link", "[[kb:kb-a/nowhere]]\n")
	for _, e := range rowsOf(external("admin", ""), "edges") {
		if e["from"] == "nowhere" {
			t.Errorf("edge from a page kb-a does not hold: %v", e)
		}
	}
	if text, isErr := callJSON(t, s, crossCallers()["admin"], "graph_neighbors", `{"id":"nowhere","direction":"in"}`); !isErr {
		t.Errorf("graph_neighbors answered for a page only another KB links: %s", text)
	}

	// Never served from the UI body cache: a change of kb-b shows at once, though
	// kb-a's generation did not move.
	multi := NewMultiKBServer("test")
	multi.MountKB("kb-a", func(s *Server) { RegisterKBTools(s, r.a, Deps{}) })
	multi.MountKB("kb-b", func(s *Server) { RegisterKBTools(s, r.b, Deps{}) })
	multi.EnableWeb(nil)
	h := auth.NewTokenStore(nil).Middleware(multi.Handler())
	fetch := func(path string) string {
		t.Helper()
		rr := getUI(t, h, UIAPIPrefix+path, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, rr.Code)
		}
		return rr.Body.String()
	}
	for _, path := range []string{"/kbs/kb-a/concept?id=p", "/kbs/kb-a/graph/external"} {
		before := fetch(path)
		putThroughServer(t, r.b, "ops/x", "")
		if after := fetch(path); after == before {
			t.Errorf("%s: served stale after kb-b changed", path)
		}
		putThroughServer(t, r.b, "ops/x", "x\n")
	}
	var body uiGraphExternalBody
	if err := json.Unmarshal([]byte(fetch("/kbs/kb-a/graph/external")), &body); err != nil || len(body.Edges) == 0 {
		t.Errorf("external through the handler: %v %v", err, body)
	}
}
