package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// graphToolKB builds a KB from files (data-root-relative path → body) with a
// Note frontmatter unless the body brings its own, mounted under "docs".
func graphToolKB(t *testing.T, files map[string]string) *Server {
	t.Helper()
	k := setupTestKB(t)
	k.AuthName = "docs"
	for _, m := range []string{"ops", "hidden"} {
		if err := k.CreateMap(m, m, "map", nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	for rel, body := range files {
		if !strings.HasPrefix(body, "---\n") {
			title := strings.TrimSuffix(filepath.Base(rel), ".md")
			body = fmt.Sprintf("---\ntype: Note\ntitle: %s\n---\n%s", strings.ToUpper(title[:1])+title[1:], body)
		}
		p := filepath.Join(k.DataRoot(), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return s
}

var (
	adminCtx  = auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "admin", Policy: auth.Policy{Admin: true}})
	narrowCtx = restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"ops", "manutenzione"}}}})
)

// callJSON runs a tool and returns its text, failing on a tool error unless
// wantErr is set.
func callJSON(t *testing.T, s *Server, ctx context.Context, tool, args string) (string, bool) {
	t.Helper()
	res := s.callTool(ctx, tool, json.RawMessage(args))
	if len(res.Content) == 0 {
		t.Fatalf("%s: empty result", tool)
	}
	return res.Content[0].Text, res.IsError
}

func decodeJSON(t *testing.T, text string) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("not JSON: %s", text)
	}
	return out
}

func resultIDs(v interface{}) []string {
	var ids []string
	for _, raw := range v.([]interface{}) {
		ids = append(ids, raw.(map[string]interface{})["id"].(string))
	}
	return ids
}

// hubKB: alpha links beta and a hub; beta links gamma; twenty leaves link to
// the hub. gamma is reached through a specific path, the leaves through a hub.
func hubKB() map[string]string {
	files := map[string]string{
		"ops/alpha.md": "[beta](beta.md) and [hub](hub.md).\n",
		"ops/beta.md":  "[gamma](gamma.md).\n",
		"ops/gamma.md": "End of the chain.\n",
		"ops/hub.md":   "Everything links here.\n",
	}
	for i := 0; i < 20; i++ {
		files[fmt.Sprintf("ops/leaf%02d.md", i)] = "[hub](hub.md).\n"
	}
	return files
}

func TestGraphContextRanksAlongSpecificPathsAboveHubs(t *testing.T) {
	s := graphToolKB(t, hubKB())
	text, isErr := callJSON(t, s, adminCtx, "graph_context", `{"ids":["ops/alpha"],"limit":30}`)
	if isErr {
		t.Fatal(text)
	}
	out := decodeJSON(t, text)
	scores := map[string]float64{}
	var gamma map[string]interface{}
	for _, raw := range out["results"].([]interface{}) {
		r := raw.(map[string]interface{})
		scores[r["id"].(string)] = r["score"].(float64)
		if r["id"] == "ops/alpha" {
			t.Fatal("a seed is listed among its own results")
		}
		if r["id"] == "ops/gamma" {
			gamma = r
		}
	}
	if scores["ops/gamma"] <= scores["ops/leaf00"] {
		t.Fatalf("gamma %v should outrank a hub leaf %v: %s", scores["ops/gamma"], scores["ops/leaf00"], text)
	}
	if gamma["hops"].(float64) != 2 || gamma["via"] != "ops/beta" {
		t.Fatalf("gamma explanation = %v", gamma)
	}
	seeds := out["seeds"].([]interface{})
	if len(seeds) != 1 || seeds[0].(map[string]interface{})["id"] != "ops/alpha" {
		t.Fatalf("seeds = %v", seeds)
	}

	again, _ := callJSON(t, s, adminCtx, "graph_context", `{"ids":["ops/alpha"],"limit":30}`)
	if again != text {
		t.Fatal("graph_context is not deterministic")
	}
	one := decodeJSON(t, mustText(t, s, "graph_context", `{"ids":["ops/alpha"],"limit":1}`))
	if len(one["results"].([]interface{})) != 1 {
		t.Fatalf("limit 1: %v", one["results"])
	}
	def := decodeJSON(t, mustText(t, s, "graph_context", `{"ids":["ops/alpha"]}`))
	if len(def["results"].([]interface{})) != 10 {
		t.Fatalf("default limit: %d results", len(def["results"].([]interface{})))
	}
	big := decodeJSON(t, mustText(t, s, "graph_context", `{"ids":["ops/alpha"],"limit":500}`))
	if n := len(big["results"].([]interface{})); n > 30 {
		t.Fatalf("limit not clamped: %d", n)
	}
}

func mustText(t *testing.T, s *Server, tool, args string) string {
	t.Helper()
	text, isErr := callJSON(t, s, adminCtx, tool, args)
	if isErr {
		t.Fatalf("%s %s: %s", tool, args, text)
	}
	return text
}

func TestGraphContextSeedsFromSearch(t *testing.T) {
	files := hubKB()
	files["ops/gamma.md"] = "The zeppelin hangar.\n"
	s := graphToolKB(t, files)
	out := decodeJSON(t, mustText(t, s, "graph_context", `{"query":"zeppelin"}`))
	if ids := resultIDs(out["seeds"]); len(ids) != 1 || ids[0] != "ops/gamma" {
		t.Fatalf("seeds = %v", out["seeds"])
	}
	if ids := resultIDs(out["results"]); len(ids) == 0 || ids[0] != "ops/beta" {
		t.Fatalf("results = %v", ids)
	}
	none := decodeJSON(t, mustText(t, s, "graph_context", `{"query":"nothingmatchesthis"}`))
	if none["note"] != "no seed matched the query" || len(none["results"].([]interface{})) != 0 {
		t.Fatalf("no hits = %v", none)
	}
	if text, isErr := callJSON(t, s, adminCtx, "graph_context", `{}`); !isErr || !strings.Contains(text, "'query' or 'ids' is required") {
		t.Fatalf("no arguments: %s", text)
	}
}

// A missing id and a hidden one fail with the same text: no existence oracle.
func TestGraphToolsHideAndMissAlike(t *testing.T) {
	files := hubKB()
	files["hidden/secret.md"] = "[alpha](../ops/alpha.md).\n"
	s := graphToolKB(t, files)
	for _, tc := range []struct{ tool, argsFmt string }{
		{"graph_context", `{"ids":["%s"]}`},
		{"link_suggest", `{"id":"%s"}`},
		{"graph_path", `{"source":"%s","target":"ops/alpha"}`},
		{"graph_path", `{"source":"ops/alpha","target":"%s"}`},
		{"graph_neighbors", `{"id":"%s","depth":2,"direction":"in"}`},
	} {
		hidden, hErr := callJSON(t, s, narrowCtx, tc.tool, fmt.Sprintf(tc.argsFmt, "hidden/secret"))
		missing, mErr := callJSON(t, s, narrowCtx, tc.tool, fmt.Sprintf(tc.argsFmt, "hidden/nope"))
		if !hErr || !mErr {
			t.Fatalf("%s: hidden %v %s / missing %v %s", tc.tool, hErr, hidden, mErr, missing)
		}
		if strings.Replace(hidden, "hidden/secret", "X", 1) != strings.Replace(missing, "hidden/nope", "X", 1) {
			t.Fatalf("%s: %q vs %q", tc.tool, hidden, missing)
		}
	}
}

// A narrowed principal gets exactly what an admin gets on the KB without the
// concepts it cannot see: hidden concepts are removed before computing.
func TestGraphToolsNarrowedEquivalence(t *testing.T) {
	visible := map[string]string{
		"ops/s.md":   "[a](a.md) [b](b.md) [t](t.md).\n",
		"ops/a.md":   "[x](x.md) [y](y.md).\n",
		"ops/b.md":   "[x](x.md) [y](y.md) [c](c.md).\n",
		"ops/x.md":   "Shared.\n",
		"ops/y.md":   "Shared too.\n",
		"ops/c.md":   "[t](t.md) [ghost](ghost.md).\n",
		"ops/t.md":   "Target.\n",
		"ops/far.md": "Isolated from ops except through hidden.\n",
	}
	withHidden := map[string]string{
		"hidden/h1.md": "[s](../ops/s.md) [far](../ops/far.md) [x](../ops/x.md).\n",
		"hidden/h2.md": "[a](../ops/a.md) [far](../ops/far.md).\n",
	}
	for k, v := range visible {
		withHidden[k] = v
	}
	full := graphToolKB(t, withHidden)
	stripped := graphToolKB(t, visible)
	for _, tc := range []struct{ tool, args string }{
		{"graph_context", `{"ids":["ops/s"],"limit":30}`},
		{"graph_context", `{"ids":["ops/far"]}`},
		{"link_suggest", `{"id":"ops/s"}`},
		{"link_suggest", `{"id":"ops/far"}`},
		{"graph_path", `{"source":"ops/s","target":"ops/far"}`},
		{"graph_path", `{"source":"ops/s","target":"ops/t","direction":"out"}`},
		// graph_neighbors walks the same visible-induced graph (D249).
		{"graph_neighbors", `{"id":"ops/s","depth":3}`},
		{"graph_neighbors", `{"id":"ops/x","depth":3,"direction":"in"}`},
		{"graph_neighbors", `{"id":"ops/far","depth":3,"direction":"both"}`},
	} {
		got, _ := callJSON(t, full, narrowCtx, tc.tool, tc.args)
		want, _ := callJSON(t, stripped, adminCtx, tc.tool, tc.args)
		if got != want {
			t.Errorf("%s %s:\nnarrowed %s\nstripped %s", tc.tool, tc.args, got, want)
		}
	}
	// And the hidden concepts do change the admin's answer, or the test above
	// would prove nothing.
	adminFull, _ := callJSON(t, full, adminCtx, "graph_path", `{"source":"ops/s","target":"ops/far"}`)
	if !strings.Contains(adminFull, "hidden/h1") {
		t.Fatalf("admin path should cross the hidden map: %s", adminFull)
	}
}

func TestLinkSuggestByHand(t *testing.T) {
	s := graphToolKB(t, map[string]string{
		// u links a and b; x shares both, y only a, z both but is retired,
		// w shares both but is already linked to u (w → u).
		"ops/u.md": "[a](a.md) [b](b.md).\n",
		"ops/a.md": "[x](x.md) [y](y.md) [z](z.md) [w](w.md).\n",
		"ops/b.md": "[x](x.md) [z](z.md) [w](w.md).\n",
		"ops/x.md": "X.\n",
		"ops/y.md": "Y.\n",
		"ops/z.md": "---\ntype: Note\nstatus: deprecated\n---\nZ.\n",
		"ops/w.md": "[u](u.md).\n",
	})
	out := decodeJSON(t, mustText(t, s, "link_suggest", `{"id":"ops/u"}`))
	cands := out["candidates"].([]interface{})
	if len(cands) != 1 {
		t.Fatalf("candidates = %v", cands)
	}
	c := cands[0].(map[string]interface{})
	// deg(a) = |{u,x,y,z,w}| = 5, deg(b) = |{u,x,z,w}| = 4.
	if c["id"] != "ops/x" || c["score"].(float64) != round4(1.0/5+1.0/4) {
		t.Fatalf("candidate = %v", c)
	}
	if common := c["common"].([]interface{}); len(common) != 2 || common[0] != "ops/a" || common[1] != "ops/b" {
		t.Fatalf("common = %v", common)
	}
}

func TestGraphPathByHand(t *testing.T) {
	s := graphToolKB(t, map[string]string{
		// A one-way chain p1 → p2 → p3, and two equal routes s → m1|m2 → t.
		"ops/p1.md": "[p2](p2.md).\n",
		"ops/p2.md": "[p3](p3.md).\n",
		"ops/p3.md": "End.\n",
		"ops/s.md":  "[m2](m2.md) [m1](m1.md).\n",
		"ops/m1.md": "[t](t.md).\n",
		"ops/m2.md": "[t](t.md).\n",
		"ops/t.md":  "T.\n",
	})
	out := decodeJSON(t, mustText(t, s, "graph_path", `{"source":"ops/p3","target":"ops/p1","direction":"out"}`))
	if len(out["path"].([]interface{})) != 0 || out["note"] != "no path within 6 hops" {
		t.Fatalf("against the links: %v", out)
	}
	out = decodeJSON(t, mustText(t, s, "graph_path", `{"source":"ops/p3","target":"ops/p1"}`))
	if got := strings.Join(resultIDs(out["path"]), ","); got != "ops/p3,ops/p2,ops/p1" || out["hops"].(float64) != 2 {
		t.Fatalf("undirected: %v", out)
	}
	out = decodeJSON(t, mustText(t, s, "graph_path", `{"source":"ops/s","target":"ops/t"}`))
	if got := strings.Join(resultIDs(out["path"]), ","); got != "ops/s,ops/m1,ops/t" {
		t.Fatalf("smallest of equal paths: %v", got)
	}
	out = decodeJSON(t, mustText(t, s, "graph_path", `{"source":"ops/s","target":"ops/s"}`))
	if got := strings.Join(resultIDs(out["path"]), ","); got != "ops/s" || out["hops"].(float64) != 0 {
		t.Fatalf("source == target: %v", out)
	}
	if text, isErr := callJSON(t, s, adminCtx, "graph_path", `{"source":"ops/s","target":"ops/t","direction":"in"}`); !isErr || !strings.Contains(text, "expected out or both") {
		t.Fatalf("bad direction: %s", text)
	}
}

// A narrowed token may ask graph_neighbors for any depth (D249): the walk never
// crosses a hidden concept, never reports one — not even as a broken link —
// and still reports a real broken link as missing.
func TestGraphNeighborsNarrowedDepth(t *testing.T) {
	s := graphToolKB(t, map[string]string{
		"ops/a.md":    "[h](../hidden/h.md) [ghost](ghost.md).\n",
		"ops/b.md":    "End.\n",
		"hidden/h.md": "[b](../ops/b.md).\n",
	})
	text, isErr := callJSON(t, s, narrowCtx, "graph_neighbors", `{"id":"ops/a","depth":3}`)
	if isErr {
		t.Fatalf("depth 3 refused to a narrowed token: %s", text)
	}
	if strings.Contains(text, "hidden/") || strings.Contains(text, "ops/b") {
		t.Fatalf("walked through the hidden concept: %s", text)
	}
	out := decodeJSON(t, text)
	list := out["neighbors"].([]interface{})
	if len(list) != 1 {
		t.Fatalf("neighbors = %v", list)
	}
	if n := list[0].(map[string]interface{}); n["id"] != "ops/ghost" || n["missing"] != true {
		t.Fatalf("broken link = %v", n)
	}
	// The admin sees the route the narrowed token must not take.
	admin, _ := callJSON(t, s, adminCtx, "graph_neighbors", `{"id":"ops/a","depth":3}`)
	if !strings.Contains(admin, "ops/b") {
		t.Fatalf("admin should reach ops/b through hidden/h: %s", admin)
	}
}
