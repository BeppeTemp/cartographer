package kb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/graphalgo"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// blockSpec describes a synthetic collection: groups of `size` concepts, each
// linked to the next few in its group (a dense community), plus the extra
// links the test plants between groups.
type blockSpec struct {
	collection string
	groups     int
	size       int
}

func clusterConcept(coll string, g, n int) string { return fmt.Sprintf("%s/g%04d-n%02d", coll, g, n) }

// buildClusterKB writes the blocks and the cross links ("a->b" concept ids).
func buildClusterKB(t testing.TB, blocks []blockSpec, cross [][2]string) *KB {
	t.Helper()
	return buildClusterKBDegree(t, blocks, cross, 5)
}

// buildClusterKBDegree is buildClusterKB with each concept linking its next
// `degree` neighbours in its group: a dense KB for a large degree.
func buildClusterKBDegree(t testing.TB, blocks []blockSpec, cross [][2]string, degree int) *KB {
	t.Helper()
	k := newTestKB(t)
	out := map[string][]string{}
	for _, b := range blocks {
		for g := 0; g < b.groups; g++ {
			for n := 0; n < b.size; n++ {
				id := clusterConcept(b.collection, g, n)
				for d := 1; d <= degree && d < b.size; d++ {
					out[id] = append(out[id], clusterConcept(b.collection, g, (n+d)%b.size))
				}
				if b.size == 1 {
					continue
				}
			}
		}
	}
	for _, c := range cross {
		out[c[0]] = append(out[c[0]], c[1], c[1]) // twice: a duplicate link is one edge
	}
	for _, b := range blocks {
		for g := 0; g < b.groups; g++ {
			for n := 0; n < b.size; n++ {
				id := clusterConcept(b.collection, g, n)
				var body strings.Builder
				for _, to := range out[id] {
					fmt.Fprintf(&body, "[[%s]] ", to)
				}
				if g == 0 && n == 5 {
					fmt.Fprintf(&body, "[[%s]] ", id) // a self link is no edge
				}
				path := filepath.Join(k.DataRoot(), id+".md")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("---\ntype: Note\n---\n"+body.String()+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	return k
}

var twoBlocks = []blockSpec{{"alpha", 60, 20}, {"beta", 50, 20}} // 2,200 concepts

func clusterByID(t *testing.T, g ClusterGraph, id string) GraphCluster {
	t.Helper()
	for _, c := range g.Clusters {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("cluster %q missing", id)
	return GraphCluster{}
}

func edgeWeight(g ClusterGraph, a, b string) int {
	for _, e := range g.Edges {
		if e.Source == a && e.Target == b || e.Source == b && e.Target == a {
			return e.Weight
		}
	}
	return 0
}

// describeClusters prints a cluster graph for a failure message: counts, each
// cluster with its size and top member, and each edge weight, so a flake seen
// once in a gate log can be told apart (a lost concept, a moved id).
func describeClusters(g ClusterGraph) string {
	var b strings.Builder
	fmt.Fprintf(&b, "mode %q total_nodes %d total_edges %d nodes %d truncated %v\n", g.Mode, g.TotalNodes, g.TotalEdges, len(g.Nodes), g.Truncated)
	for _, c := range g.Clusters {
		fmt.Fprintf(&b, "  cluster %s size %d internal_edges %d\n", c.ID, c.Size, c.InternalEdges)
	}
	for _, e := range g.Edges {
		fmt.Fprintf(&b, "  edge %s -- %s weight %d\n", e.Source, e.Target, e.Weight)
	}
	return b.String()
}

// clusterIDOf finds the cluster holding a concept by unfolding it with Focus.
func clusterIDOf(t *testing.T, k *KB, concept string) string {
	t.Helper()
	g, err := k.GraphClusters(GraphClusterOptions{Focus: concept})
	if err != nil || len(g.Unfolded) == 0 {
		t.Fatalf("focus %s: %v %+v", concept, err, g.Unfolded)
	}
	return g.Unfolded[0].ID
}

func TestGraphClusters_BelowThresholdIsTheFlatGraph(t *testing.T) {
	t.Parallel()
	k := snapshotKB(t)
	g, err := k.GraphClusters(GraphClusterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := k.GraphSnapshot(GraphSnapshotOptions{})
	if g.Mode != ClusterModeNodes || len(g.Clusters) != 0 || len(g.Nodes) != len(snap.Nodes) || len(g.Edges) != len(snap.Edges) {
		t.Fatalf("mode %q: %d clusters, %d nodes, %d edges; snapshot %d/%d", g.Mode, len(g.Clusters), len(g.Nodes), len(g.Edges), len(snap.Nodes), len(snap.Edges))
	}
}

func TestGraphClusters_EmptyKB(t *testing.T) {
	t.Parallel()
	g, err := newTestKB(t).GraphClusters(GraphClusterOptions{})
	if err != nil || g.Mode != ClusterModeNodes || len(g.Nodes) != 0 || len(g.Clusters) != 0 {
		t.Fatalf("%+v %v", g, err)
	}
}

func TestGraphClusters_ClusterIDIsNeverAConceptID(t *testing.T) {
	t.Parallel()
	if _, err := okf.PathToID(TailClusterID + ".md"); err == nil {
		t.Fatal("the tail id parses as a concept id")
	}
	if _, err := okf.PathToID("c:x.md"); err == nil {
		t.Fatal("a c: id parses as a concept id")
	}
}

func TestGraphClusters_WeightsAndUnfold(t *testing.T) {
	t.Parallel()
	a, b := clusterConcept("alpha", 0, 0), clusterConcept("alpha", 1, 0)
	cross := [][2]string{{a, b}, {b, a}}
	k := buildClusterKB(t, twoBlocks, cross)
	snap, err := k.GraphSnapshot(GraphSnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}

	g, err := k.GraphClusters(GraphClusterOptions{renderBudget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if g.Mode != ClusterModeClusters || len(g.Nodes) != 0 || g.TotalNodes != 2200 {
		t.Fatalf("mode %q, %d nodes, total %d\n%s", g.Mode, len(g.Nodes), g.TotalNodes, describeClusters(g))
	}
	// Every underlying edge is inside a cluster or in exactly one weight.
	sum := 0
	for _, e := range g.Edges {
		sum += e.Weight
	}
	for _, c := range g.Clusters {
		sum += c.InternalEdges
	}
	if g.Truncated || sum != snap.TotalEdges || g.TotalEdges != snap.TotalEdges {
		t.Fatalf("weights+internal = %d, total_edges %d, snapshot %d (%d nodes)\n%s", sum, g.TotalEdges, snap.TotalEdges, snap.TotalNodes, describeClusters(g))
	}

	ca, cb := clusterIDOf(t, k, a), clusterIDOf(t, k, b)
	if w := edgeWeight(g, ca, cb); w != 2 {
		t.Fatalf("cluster edge weight = %d, want 2 (a duplicate link counts once, both directions count); %s -> %s\n%s", w, ca, cb, describeClusters(g))
	}
	if clusterByID(t, g, ca).Size != 20 {
		t.Fatalf("size %d, want 20\n%s", clusterByID(t, g, ca).Size, describeClusters(g))
	}

	u, err := k.GraphClusters(GraphClusterOptions{renderBudget: 1, Unfold: []string{ca}})
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Nodes) != 20 || len(u.Unfolded) != 1 || u.Unfolded[0].Shown != 20 || len(u.Unfolded[0].Members) != 20 || u.Unfolded[0].Total != 20 || u.Truncated {
		t.Fatalf("unfold of %s: %d nodes, %+v\n%s", ca, len(u.Nodes), u.Unfolded, describeClusters(u))
	}
	if folded := clusterByID(t, g, ca); u.Unfolded[0].Slot != folded.Slot || u.Unfolded[0].Collection != folded.Collection {
		t.Fatalf("unfolded %+v lost the colour of %+v", u.Unfolded[0], folded)
	}
	for _, c := range u.Clusters {
		if c.ID == ca {
			t.Fatal("an unfolded cluster is still folded")
		}
	}
	if len(u.Clusters) != len(g.Clusters)-1 {
		t.Fatalf("%d clusters after unfold, %d before", len(u.Clusters), len(g.Clusters))
	}
	towards := 0
	for _, e := range u.Edges {
		if e.Source == cb || e.Target == cb {
			towards += e.Weight
		}
	}
	if towards != 2 {
		t.Fatalf("node-cluster weights toward the neighbour = %d, want 2", towards)
	}
	// A link and its reverse between two drawn nodes are one edge of weight 1.
	for _, e := range u.Edges {
		if !strings.HasPrefix(e.Source, "c:") && !strings.HasPrefix(e.Target, "c:") && e.Weight != 1 {
			t.Fatalf("node-node edge weight %d", e.Weight)
		}
	}
	if u.DroppedUnfolds == nil || len(u.DroppedUnfolds) != 0 {
		t.Fatalf("dropped %v", u.DroppedUnfolds)
	}
}

func TestGraphClusters_FocusUnfoldsItsClusterAndUnknownIsIgnored(t *testing.T) {
	t.Parallel()
	k := buildClusterKB(t, twoBlocks, nil)
	f, _ := k.GraphClusters(GraphClusterOptions{renderBudget: 1, Focus: clusterConcept("beta", 3, 2)})
	found := false
	for _, n := range f.Nodes {
		found = found || string(n.ID) == clusterConcept("beta", 3, 2)
	}
	if !found || len(f.Unfolded) != 1 {
		t.Fatalf("focus not drawn: %+v", f.Unfolded)
	}
	none, _ := k.GraphClusters(GraphClusterOptions{renderBudget: 1})
	bogus, _ := k.GraphClusters(GraphClusterOptions{renderBudget: 1, Focus: "nope/missing"})
	if !reflect.DeepEqual(none, bogus) {
		t.Fatal("an unknown focus changes the answer")
	}
}

func TestGraphClusters_TinyCommunitiesAreGroupedByMapAndBudgetsHold(t *testing.T) {
	t.Parallel()
	k := buildClusterKB(t, []blockSpec{{"alpha", 1100, 2}}, nil) // 1,100 pair communities
	g, err := k.GraphClusters(GraphClusterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// No community reaches MinClusterSize members: they are grouped by Map, and
	// a cluster of two is never drawn.
	if len(g.Clusters) != 1 || len(g.Nodes) != 0 {
		t.Fatalf("%d clusters, %d nodes, want the one Map group", len(g.Clusters), len(g.Nodes))
	}
	group := clusterByID(t, g, "c:~alpha")
	if group.Slot != graphalgo.OtherSlot || group.Label != "alpha" || group.Collection != "alpha" || group.Communities != 1100 || group.Size != 2200 {
		t.Fatalf("map group %+v", group)
	}
	u, _ := k.GraphClusters(GraphClusterOptions{Unfold: []string{group.ID}})
	if len(u.Clusters)+len(u.Nodes) > MaxClusterElements || len(u.Unfolded) != 1 || u.Unfolded[0].Shown >= u.Unfolded[0].Total || !u.Truncated {
		t.Fatalf("group unfold: %d clusters, %d nodes, %+v", len(u.Clusters), len(u.Nodes), u.Unfolded)
	}
}

// The overview is mixed: the largest communities fold into glyphs until the
// render budget holds, and the small ones stay open as concepts with their
// real edges, so the picture says how large the KB is.
func TestGraphClusters_TheOverviewLeavesSmallCommunitiesOpen(t *testing.T) {
	t.Parallel()
	// 12 rings of 200 (each found as two communities of 100) and 50 of 3: 2,550 concepts.
	k := buildClusterKB(t, []blockSpec{{"big", 12, 200}, {"tiny", 50, 3}}, nil)
	g, err := k.GraphClusters(GraphClusterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if g.Mode != ClusterModeClusters || len(g.Clusters) != 24 || len(g.Nodes) != 150 || len(g.Clusters)+len(g.Nodes) > ClusterRenderBudget {
		t.Fatalf("%d clusters, %d nodes\n%s", len(g.Clusters), len(g.Nodes), describeClusters(g))
	}
	loose := 0
	for _, e := range g.Edges {
		if strings.HasPrefix(e.Source, "tiny/") && strings.HasPrefix(e.Target, "tiny/") && e.Weight == 1 {
			loose++
		}
	}
	if loose == 0 {
		t.Fatal("no real edge between the open concepts")
	}
	// Opening one cluster adds its members to the loose ones, within the response cap.
	u, _ := k.GraphClusters(GraphClusterOptions{Unfold: []string{g.Clusters[0].ID}})
	if len(u.Nodes) != 250 || len(u.Clusters) != 23 || len(u.Clusters)+len(u.Nodes) > MaxClusterElements || len(u.DroppedUnfolds) != 0 {
		t.Fatalf("unfold: %d nodes, %d clusters, dropped %v", len(u.Nodes), len(u.Clusters), u.DroppedUnfolds)
	}
}

// A large KB folds whole: communities of MinClusterSize or more are clusters
// of their own, the smaller ones are grouped by Map, and nothing is left as
// loose concepts except a handful too few to make a cluster.
func TestGraphClusters_EveryConceptIsInAClusterOfMinSize(t *testing.T) {
	t.Parallel()
	// 12 communities of 200, 40 of 30, 300 of 3 and 2 of 3 (stray): 4,506 concepts.
	k := buildClusterKB(t, []blockSpec{{"big", 12, 200}, {"mid", 40, 30}, {"tiny", 300, 3}, {"stray", 2, 3}}, nil)
	snap, _ := k.GraphSnapshot(GraphSnapshotOptions{})
	g, err := k.GraphClusters(GraphClusterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if g.Mode != ClusterModeClusters || g.TotalNodes != 4506 {
		t.Fatalf("mode %q, total %d", g.Mode, g.TotalNodes)
	}
	if len(g.Clusters)+len(g.Nodes) > MaxClusterElements {
		t.Fatalf("%d elements\n%s", len(g.Clusters)+len(g.Nodes), describeClusters(g))
	}
	size, big := 0, 0
	for _, c := range g.Clusters {
		if c.Size < MinClusterSize {
			t.Fatalf("cluster %s of %d members", c.ID, c.Size)
		}
		size += c.Size
		if c.Size == 200 {
			big++
		}
	}
	tiny := clusterByID(t, g, "c:~tiny")
	if big != 12 || tiny.Size != 900 || tiny.Communities != 300 || tiny.Label != "tiny" {
		t.Fatalf("%d big clusters, tiny group %+v", big, tiny)
	}
	// The six stray concepts are too few for a cluster: they stay open, with
	// their real edges.
	if len(g.Nodes) != 6 || size+len(g.Nodes) != 4506 {
		t.Fatalf("%d open concepts, clusters hold %d\n%s", len(g.Nodes), size, describeClusters(g))
	}
	sum := 0
	for _, e := range g.Edges {
		sum += e.Weight
	}
	for _, c := range g.Clusters {
		sum += c.InternalEdges
	}
	// A link and its reverse between two open concepts are one drawn edge: the
	// stray triangles' six mutual pairs are the only difference.
	if g.TotalEdges != snap.TotalEdges || sum > snap.TotalEdges || snap.TotalEdges-sum > 6 {
		t.Fatalf("weights+internal = %d, total_edges %d, snapshot %d", sum, g.TotalEdges, snap.TotalEdges)
	}
	// The folded set is stable between requests.
	again, _ := k.GraphClusters(GraphClusterOptions{})
	if !reflect.DeepEqual(g.Clusters, again.Clusters) {
		t.Fatal("the folded set moves between requests")
	}
}

// A dense KB (5,000 concepts, 60,000 links) folds to a response as small as a
// sparse one: the links are aggregated into super-edges, and one open cluster
// brings only its own internal links.
func TestGraphClusters_DenseKBStaysWithinBudgets(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 5,000 concepts")
	}
	t.Parallel()
	k := buildClusterKBDegree(t, []blockSpec{{"dense", 50, 100}}, [][2]string{{clusterConcept("dense", 0, 0), clusterConcept("dense", 1, 0)}}, 12)
	g, err := k.GraphClusters(GraphClusterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// One community of 100 stays open: the overview is under the render budget.
	if g.TotalEdges < 60000 || len(g.Nodes)+len(g.Clusters) > ClusterRenderBudget || len(g.Clusters) != 49 || len(g.Nodes) != 100 {
		t.Fatalf("%d total edges, %d nodes, %d clusters", g.TotalEdges, len(g.Nodes), len(g.Clusters))
	}
	u, err := k.GraphClusters(GraphClusterOptions{Unfold: []string{g.Clusters[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	for name, resp := range map[string]ClusterGraph{"folded": g, "unfolded": u} {
		body, _ := json.Marshal(resp)
		if len(body) > 150<<10 || len(resp.Edges) > MaxClusterEdges || len(resp.Clusters)+len(resp.Nodes) > MaxClusterElements {
			t.Errorf("%s: %d bytes, %d edges, %d elements", name, len(body), len(resp.Edges), len(resp.Clusters)+len(resp.Nodes))
		}
	}
}

func TestGraphClusters_BudgetsAtScale(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 20,000 concepts")
	}
	t.Parallel()
	k := buildClusterKB(t, []blockSpec{{"alpha", 500, 20}, {"beta", 500, 20}}, nil)
	g, err := k.GraphClusters(GraphClusterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(g)
	if len(body) > 150<<10 {
		t.Fatalf("response is %d bytes uncompressed, want under 150 KiB", len(body))
	}
	if g.TotalNodes != 20000 || len(g.Clusters)+len(g.Nodes) > MaxClusterElements || len(g.Edges) > MaxClusterEdges {
		t.Fatalf("%d total, %d clusters, %d edges", g.TotalNodes, len(g.Clusters), len(g.Edges))
	}
}

func TestGraphClusters_IDStability(t *testing.T) {
	t.Parallel()
	k := buildClusterKB(t, twoBlocks, nil)
	before, err := k.GraphClusters(GraphClusterOptions{renderBudget: 1})
	if err != nil {
		t.Fatal(err)
	}
	// A concept that nothing links to ranks low; it links to the group's top member.
	add := filepath.Join(k.DataRoot(), "alpha", "newcomer.md")
	if err := os.WriteFile(add, []byte("---\ntype: Note\n---\n[[alpha/g0003-n00]]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := k.GraphClusters(GraphClusterOptions{renderBudget: 1})
	if err != nil {
		t.Fatal(err)
	}
	ids := func(g ClusterGraph) []string {
		var out []string
		for _, c := range g.Clusters {
			out = append(out, c.ID)
		}
		sort.Strings(out) // the newcomer joins a community: sizes, hence the order, move
		return out
	}
	if !reflect.DeepEqual(ids(before), ids(after)) {
		t.Fatalf("adding a low-rank concept changed cluster ids\nbefore: %v\nafter:  %v\n--- before\n%s--- after\n%s", ids(before), ids(after), describeClusters(before), describeClusters(after))
	}
	// Removing the top member of a cluster renames it; the old id is dropped.
	old := clusterIDOf(t, k, clusterConcept("beta", 2, 0))
	if err := os.Remove(filepath.Join(k.DataRoot(), strings.TrimPrefix(old, "c:")+".md")); err != nil {
		t.Fatal(err)
	}
	g, err := k.GraphClusters(GraphClusterOptions{renderBudget: 1, Unfold: []string{old}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g.DroppedUnfolds, []string{old}) || len(g.Unfolded) != 0 {
		t.Fatalf("removed top member of %s: dropped %v unfolded %v\n%s", old, g.DroppedUnfolds, g.Unfolded, describeClusters(g))
	}
}

// Visibility: a hidden block leaves no trace in a size, label, weight or
// count (D226, D244); the output equals the one of a KB that never had it.
func TestGraphClusters_HiddenConceptsLeaveNoTrace(t *testing.T) {
	t.Parallel()
	cross := [][2]string{{clusterConcept("alpha", 0, 0), clusterConcept("beta", 0, 0)}, {clusterConcept("beta", 1, 1), clusterConcept("alpha", 2, 2)}}
	full := buildClusterKB(t, []blockSpec{{"alpha", 110, 20}, {"beta", 6, 20}}, cross)
	plain := buildClusterKB(t, []blockSpec{{"alpha", 110, 20}}, cross[:0])
	hide := func(id string) bool { return !strings.HasPrefix(id, "beta/") }
	// The plain KB keeps the dangling links to beta: they are broken, not edges.
	for _, opts := range []GraphClusterOptions{
		{Include: hide, renderBudget: 1},
		{Include: hide, renderBudget: 1, Focus: clusterConcept("beta", 0, 0)},
		{Include: hide, renderBudget: 1, Unfold: []string{"c:" + clusterConcept("beta", 0, 0)}},
	} {
		got, err := full.GraphClusters(opts)
		if err != nil {
			t.Fatal(err)
		}
		wantOpts := opts
		wantOpts.Include, wantOpts.Focus = nil, ""
		if opts.Focus == "" && len(opts.Unfold) == 0 {
			wantOpts = GraphClusterOptions{renderBudget: 1}
		}
		want, _ := plain.GraphClusters(wantOpts)
		if got.TotalNodes != 2200 || !reflect.DeepEqual(got, want) {
			t.Fatalf("%+v: output differs from the KB without the hidden block\n got %d/%d\nwant %d/%d", opts, got.TotalNodes, got.TotalEdges, want.TotalNodes, want.TotalEdges)
		}
	}
	// Two principals never share a ranking: each visible set computes once.
	runs := full.GraphRankComputations()
	_, _ = full.GraphClusters(GraphClusterOptions{renderBudget: 1})
	_, _ = full.GraphClusters(GraphClusterOptions{Include: hide, renderBudget: 1})
	_, _ = full.GraphClusters(GraphClusterOptions{renderBudget: 1})
	_, _ = full.GraphClusters(GraphClusterOptions{Include: hide, renderBudget: 1})
	if n := full.GraphRankComputations() - runs; n > 2 {
		t.Fatalf("%d computations for two visible sets", n)
	}
}

func TestGraphClusters_Scope(t *testing.T) {
	t.Parallel()
	k := buildClusterKB(t, []blockSpec{{"alpha", 110, 20}, {"beta", 6, 20}}, [][2]string{{clusterConcept("alpha", 0, 0), clusterConcept("beta", 0, 0)}})
	g, _ := k.GraphClusters(GraphClusterOptions{renderBudget: 1, Scope: "alpha"})
	size := 0
	for _, c := range g.Clusters {
		size += c.Size
		if c.Collection != "alpha" {
			t.Fatalf("cluster %+v outside the scope", c)
		}
	}
	if g.Mode != ClusterModeClusters || g.TotalNodes != 2200 || size != 2200 {
		t.Fatalf("%q total %d, sizes %d", g.Mode, g.TotalNodes, size)
	}
	// A small Map of a big KB falls back to the flat graph.
	s, _ := k.GraphClusters(GraphClusterOptions{renderBudget: 1, Scope: "beta"})
	flat, _ := k.GraphSnapshot(GraphSnapshotOptions{Scope: "beta"})
	if s.Mode != ClusterModeNodes || len(s.Nodes) != 120 || len(s.Communities) != len(flat.Communities) {
		t.Fatalf("%q, %d nodes", s.Mode, len(s.Nodes))
	}
	// The cluster id does not depend on the scope.
	if clusterIDOf(t, k, clusterConcept("alpha", 0, 0)) == "" {
		t.Fatal("no cluster")
	}
}

// At most one cluster is open: opening another folds the previous one.
func TestGraphClusters_OnlyOneClusterIsOpen(t *testing.T) {
	t.Parallel()
	k := buildClusterKB(t, twoBlocks, nil)
	a, b := clusterIDOf(t, k, clusterConcept("alpha", 0, 0)), clusterIDOf(t, k, clusterConcept("alpha", 1, 0))
	g, _ := k.GraphClusters(GraphClusterOptions{renderBudget: 1, Unfold: []string{a, b}})
	if len(g.Unfolded) != 1 || g.Unfolded[0].ID != b || !reflect.DeepEqual(g.DroppedUnfolds, []string{a}) {
		t.Fatalf("unfolded %+v dropped %v: the last requested stays open, the other is dropped", g.Unfolded, g.DroppedUnfolds)
	}
	// A focus in another cluster wins over what the URL asked.
	f, _ := k.GraphClusters(GraphClusterOptions{renderBudget: 1, Focus: clusterConcept("beta", 3, 2), Unfold: []string{a}})
	if len(f.Unfolded) != 1 || f.Unfolded[0].ID == a || !reflect.DeepEqual(f.DroppedUnfolds, []string{a}) {
		t.Fatalf("unfolded %+v dropped %v", f.Unfolded, f.DroppedUnfolds)
	}
}

func TestGraphClusters_DroppedUnfoldsAreDeduplicated(t *testing.T) {
	t.Parallel()
	k := buildClusterKB(t, twoBlocks, nil)
	g, _ := k.GraphClusters(GraphClusterOptions{renderBudget: 1, Unfold: []string{"c:nope", "c:nope", "c:other"}})
	if !reflect.DeepEqual(g.DroppedUnfolds, []string{"c:nope", "c:other"}) {
		t.Fatalf("dropped %v", g.DroppedUnfolds)
	}
	// Focus plus the same cluster in Unfold unfolds it once and drops nothing.
	focus := clusterConcept("alpha", 1, 1)
	id := clusterIDOf(t, k, focus)
	f, _ := k.GraphClusters(GraphClusterOptions{renderBudget: 1, Focus: focus, Unfold: []string{id, id}})
	if len(f.Unfolded) != 1 || len(f.DroppedUnfolds) != 0 {
		t.Fatalf("unfolded %v dropped %v", f.Unfolded, f.DroppedUnfolds)
	}
}
