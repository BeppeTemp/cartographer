package kb

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

func TestLinkGraphProjectsTheVisibleConcepts(t *testing.T) {
	f := newGraphFixture(t)
	calls := map[string]int{}
	include := func(id string) bool {
		calls[id]++
		return !strings.HasPrefix(id, "notes/")
	}
	lg, err := f.k.LinkGraph(include)
	if err != nil {
		t.Fatal(err)
	}
	want := []okf.ConceptID{"infra/cluster", "infra/dns", "infra/gateway"}
	if !reflect.DeepEqual(lg.IDs, want) {
		t.Fatalf("ids = %v, want %v", lg.IDs, want)
	}
	for id, n := range calls {
		if n != 1 {
			t.Errorf("include(%s) called %d times", id, n)
		}
	}
	idx := func(id okf.ConceptID) int { return lg.Index[id] }
	// gateway → dns, cluster (missing.md dropped); dns → gateway (the
	// extensionless diagram is no concept); cluster → itself (dropped) and
	// notes/log-review (excluded).
	if got := lg.Graph.Out[idx("infra/gateway")]; !reflect.DeepEqual(got, []int{idx("infra/cluster"), idx("infra/dns")}) {
		t.Errorf("gateway out = %v", got)
	}
	if got := lg.Graph.Out[idx("infra/cluster")]; len(got) != 0 {
		t.Errorf("cluster out = %v, want none (self and excluded targets dropped)", got)
	}
	if got := lg.Graph.In[idx("infra/gateway")]; !reflect.DeepEqual(got, []int{idx("infra/dns")}) {
		t.Errorf("gateway in = %v", got)
	}
	fa := lg.Facets[idx("infra/gateway")]
	if fa.Bytes <= 0 {
		t.Errorf("facets bytes = %d, want the file size", fa.Bytes)
	}
	fa.Bytes = 0
	if !reflect.DeepEqual(fa, NodeFacets{Title: "Gateway", Type: "Service", Status: "active", Collection: "infra"}) {
		t.Errorf("facets = %+v", fa)
	}

	all, err := f.k.LinkGraph(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.IDs) != 5 {
		t.Fatalf("nil include: %v", all.IDs)
	}
}

// The whole-KB percentiles are cached per view generation and follow the
// files (D251); a narrowed include is computed on its own graph.
func TestPageRankPercentiles(t *testing.T) {
	k := snapshotKB(t)
	pct, err := k.PageRankPercentiles(nil)
	if err != nil {
		t.Fatal(err)
	}
	if pct["infra/orphan"] != 0 {
		t.Errorf("a concept nothing links to must be at 0, got %v", pct["infra/orphan"])
	}
	if pct["infra/a"] != 1 {
		t.Errorf("the top concept must be at 1, got %v", pct["infra/a"])
	}
	again, _ := k.PageRankPercentiles(nil)
	if fmt.Sprintf("%p", again) != fmt.Sprintf("%p", pct) {
		t.Error("the whole-KB result is not reused within one generation")
	}
	// Three pages now point at the orphan: it leaves the minimum.
	for _, n := range []string{"x", "y", "z"} {
		if err := os.WriteFile(filepath.Join(k.DataRoot(), "notes", n+".md"), []byte("---\ntype: Note\n---\n[o](../infra/orphan.md)\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := k.PageRankPercentiles(nil)
	if after["infra/orphan"] == 0 {
		t.Error("percentiles did not follow the files")
	}
	narrowed, _ := k.PageRankPercentiles(func(id string) bool { return id != "infra/a" })
	if _, ok := narrowed["infra/a"]; ok {
		t.Error("a hidden concept has a percentile")
	}
}

// A link line six concepts share is a template's boilerplate: its edges weigh
// 1/6, a page's own links weigh 1, and a narrowed include counts only what it
// sees (D317).
func TestLinkGraphEdgeWeights(t *testing.T) {
	f := &graphFixture{t: t, k: mustInitKB(t)}
	f.write("svc/_map.md", "---\ntype: Map\ntitle: Services\n---\n# Services\n")
	f.write("svc/monitoring.md", "---\ntype: Entity\ntitle: Monitoring\n---\nDashboards.\n")
	f.write("svc/peer.md", "---\ntype: Entity\ntitle: Peer\n---\nA peer.\n")
	for i := 0; i < 6; i++ {
		body := "See [monitoring](monitoring.md) for dashboards.\n"
		if i == 0 {
			body += "Talks to [peer](peer.md).\n"
		}
		f.write(fmt.Sprintf("svc/s%d.md", i), "---\ntype: Entity\n---\n"+body)
	}
	weight := func(lg *LinkGraph, from, to okf.ConceptID) float64 {
		t.Helper()
		u, v := lg.Index[from], lg.Index[to]
		for k, j := range lg.Graph.Out[u] {
			if j == v {
				return lg.EdgeWeights[u][k]
			}
		}
		t.Fatalf("no edge %s → %s", from, to)
		return 0
	}
	lg, err := f.k.LinkGraph(nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := weight(lg, "svc/s0", "svc/monitoring"); w != 1.0/6 {
		t.Errorf("boilerplate edge weight = %v, want 1/6", w)
	}
	if w := weight(lg, "svc/s0", "svc/peer"); w != 1 {
		t.Errorf("own edge weight = %v, want 1", w)
	}
	// Two of the six hidden: four carriers stay under the threshold.
	narrowed, err := f.k.LinkGraph(func(id string) bool { return id != "svc/s4" && id != "svc/s5" })
	if err != nil {
		t.Fatal(err)
	}
	if w := weight(narrowed, "svc/s0", "svc/monitoring"); w != 1 {
		t.Errorf("narrowed boilerplate weight = %v, want 1 (hidden carriers not counted)", w)
	}
}
