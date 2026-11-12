package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// writeRings writes `groups` rings of `size` concepts under coll, each linking
// the next five of its ring: dense communities the clustering separates.
// extra adds links ("from" -> "to") on top.
func writeRings(t testing.TB, k *kb.KB, coll string, groups, size int, extra map[string][]string) {
	t.Helper()
	name := func(g, n int) string { return fmt.Sprintf("%s/g%04d-n%02d", coll, g, n) }
	if err := os.MkdirAll(filepath.Join(k.DataRoot(), coll), 0o755); err != nil {
		t.Fatal(err)
	}
	for g := 0; g < groups; g++ {
		for n := 0; n < size; n++ {
			id := name(g, n)
			var body strings.Builder
			fmt.Fprintf(&body, "---\ntype: Note\ntitle: %s %d %d\n---\n", coll, g, n)
			for d := 1; d <= 5; d++ {
				fmt.Fprintf(&body, "[[%s]] ", name(g, (n+d)%size))
			}
			for _, to := range extra[id] {
				fmt.Fprintf(&body, "[[%s]] ", to)
			}
			if err := os.WriteFile(filepath.Join(k.DataRoot(), id+".md"), []byte(body.String()+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// clustersHandler serves a KB above the cluster threshold for a narrow token
// too: 2,200 concepts in "visible", 400 in "hidden" that the visible ones link.
func clustersHandler(t *testing.T) (http.Handler, *kb.KB) {
	t.Helper()
	k := setupTestKB(t)
	k.AuthName = "docs"
	for _, m := range []string{"visible", "hidden"} {
		if err := k.CreateMap(m, m, "map", nil); err != nil {
			t.Fatal(err)
		}
	}
	cross := map[string][]string{}
	for g := 0; g < 20; g++ {
		cross[fmt.Sprintf("visible/g%04d-n00", g)] = []string{fmt.Sprintf("hidden/g%04d-n00", g)}
	}
	writeRings(t, k, "visible", 110, 20, cross)
	writeRings(t, k, "hidden", 20, 20, nil)
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}},
		{Token: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}}},
	})
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	return ts.Middleware(multi.Handler()), k
}

func getClusters(t *testing.T, handler http.Handler, query, token string) (kb.ClusterGraph, string) {
	t.Helper()
	rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/graph/clusters"+query, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("%s %s: %d %s", token, query, rr.Code, rr.Body.String())
	}
	var g kb.ClusterGraph
	if err := json.Unmarshal(rr.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	return g, rr.Body.String()
}

// Two principals with different rules never share a ranking, so a narrowed
// token's clusters, sizes, totals and communities say nothing of the concepts
// it cannot see; a request differing only in scope, unfold or focus reuses
// the entry, and a new revision invalidates it.
func TestUIAPI_GraphClustersNeverShareAnEntryAcrossPrincipals(t *testing.T) {
	t.Parallel()
	handler, k := clustersHandler(t)
	before := k.GraphRankComputations()
	whole, wholeBody := getClusters(t, handler, "", "whole")
	narrow, narrowBody := getClusters(t, handler, "", "narrow")
	getClusters(t, handler, "", "whole")
	getClusters(t, handler, "?scope=visible&unfold="+narrow.Clusters[0].ID, "narrow")
	getClusters(t, handler, "?focus=visible/g0003-n04", "narrow")
	if n := k.GraphRankComputations() - before; n != 2 {
		t.Errorf("%d rankings for two principals, want 2: one entry each, reused", n)
	}

	if whole.Mode != kb.ClusterModeClusters || narrow.Mode != kb.ClusterModeClusters {
		t.Fatalf("modes %q %q, want clusters", whole.Mode, narrow.Mode)
	}
	if !strings.Contains(wholeBody, "hidden/") || whole.TotalNodes <= 2600 {
		t.Errorf("the whole-KB token misses the hidden map: %d nodes", whole.TotalNodes)
	}
	if strings.Contains(narrowBody, "hidden") {
		t.Errorf("the narrow body names the hidden map")
	}
	size := 0
	for _, c := range narrow.Clusters {
		size += c.Size
	}
	if narrow.TotalNodes != 2200 || size != 2200 {
		t.Errorf("narrow: total_nodes %d, cluster sizes %d, want 2,200", narrow.TotalNodes, size)
	}
	for _, c := range narrow.Communities {
		if c.Size > 2200 || !strings.HasPrefix(string(c.Anchor), "visible/") {
			t.Errorf("narrow community %+v reaches past what the token sees", c)
		}
	}

	path := filepath.Join(k.DataRoot(), "visible", "late.md")
	if err := os.WriteFile(path, []byte("---\ntype: Note\n---\n[[visible/g0000-n00]]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mid := k.GraphRankComputations()
	after, _ := getClusters(t, handler, "", "narrow")
	if k.GraphRankComputations() != mid+1 || after.TotalNodes != 2201 {
		t.Errorf("a new revision did not invalidate: %d rankings, %d nodes", k.GraphRankComputations()-mid, after.TotalNodes)
	}
}

func TestUIAPI_GraphClustersParameters(t *testing.T) {
	t.Parallel()
	handler := newUIHandler(t, auth.NewTokenStore(nil), "docs")
	ids := func(n int) string {
		var s []string
		for i := 0; i < n; i++ {
			s = append(s, fmt.Sprintf("c:m/x%d", i))
		}
		return strings.Join(s, ",")
	}
	for query, want := range map[string]int{
		"?unfold=" + ids(50): http.StatusOK,
		"?unfold=" + ids(51): http.StatusBadRequest,
		"?unfold=,,":         http.StatusOK,
		"?scope=a/b":         http.StatusBadRequest,
		"?focus=nope":        http.StatusOK,
	} {
		rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/graph/clusters"+query, "")
		if rr.Code != want {
			t.Errorf("%s: %d, want %d: %s", query, rr.Code, want, rr.Body.String())
		}
	}
	// Below the threshold the route is the flat graph in the cluster envelope.
	g, _ := getClusters(t, handler, "?unfold=c:visible/alpha", "")
	if g.Mode != kb.ClusterModeNodes || len(g.Clusters) != 0 || len(g.Nodes) == 0 || g.Threshold != kb.ClusterThreshold {
		t.Errorf("small KB: %+v", g)
	}
}

// At 20,000 concepts one response stays within the element budgets and under
// 150 KiB uncompressed, folded or with clusters unfolded.
func TestUIAPI_GraphClustersBudgetAtScale(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 20,000 concepts")
	}
	t.Parallel()
	k := setupTestKB(t)
	writeRings(t, k, "alpha", 500, 20, nil)
	writeRings(t, k, "beta", 500, 20, nil)
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	handler := auth.NewTokenStore(nil).Middleware(multi.Handler())

	folded, _ := getClusters(t, handler, "", "")
	query := "?focus=beta/g0042-n07&unfold=" + folded.Clusters[0].ID + "," + folded.Clusters[1].ID
	for _, q := range []string{"", query} {
		g, body := getClusters(t, handler, q, "")
		if len(body) > 150<<10 {
			t.Errorf("%q: %d bytes, want under 150 KiB", q, len(body))
		}
		if g.TotalNodes < 20000 || len(g.Clusters)+len(g.Nodes) > kb.MaxClusterElements || len(g.Edges) > kb.MaxClusterEdges {
			t.Errorf("%q: %d total, %d clusters, %d nodes, %d edges", q, g.TotalNodes, len(g.Clusters), len(g.Nodes), len(g.Edges))
		}
	}
}
