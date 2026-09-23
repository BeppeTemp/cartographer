package graphalgo

import (
	"math"
	"reflect"
	"sort"
	"testing"
)

// build makes a Graph from directed edges, with the sorted, deduplicated,
// self-loop-free adjacency the package requires.
func build(n int, edges [][2]int) *Graph {
	out := make([]map[int]bool, n)
	in := make([]map[int]bool, n)
	for i := range out {
		out[i], in[i] = map[int]bool{}, map[int]bool{}
	}
	for _, e := range edges {
		if e[0] != e[1] {
			out[e[0]][e[1]] = true
			in[e[1]][e[0]] = true
		}
	}
	g := &Graph{N: n, Out: make([][]int, n), In: make([][]int, n)}
	for u := 0; u < n; u++ {
		g.Out[u] = keys(out[u])
		g.In[u] = keys(in[u])
	}
	return g
}

func keys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// rng is a small deterministic generator: the tests must not depend on
// math/rand's sequence across Go versions.
type rng uint64

func (r *rng) next(n int) int {
	*r = *r*6364136223846793005 + 1442695040888963407
	return int(uint64(*r>>33) % uint64(n))
}

func randomGraph(seed uint64, n, m int) *Graph {
	r := rng(seed)
	edges := make([][2]int, 0, m)
	for i := 0; i < m; i++ {
		edges = append(edges, [2]int{r.next(n), r.next(n)})
	}
	return build(n, edges)
}

func TestUndirectedIsTheSortedUnion(t *testing.T) {
	g := build(4, [][2]int{{0, 1}, {1, 0}, {2, 0}, {0, 3}})
	want := [][]int{{1, 2, 3}, {0}, {0}, {0}}
	if got := g.Undirected(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Undirected = %v, want %v", got, want)
	}
}

func TestPersonalizedPageRankByHand(t *testing.T) {
	// A path 0-1-2 seeded at 0, alpha 0.5, with an exact threshold: the push
	// sequence can be followed on paper.
	g := build(3, [][2]int{{0, 1}, {1, 2}})
	p := PersonalizedPageRank(g, map[int]float64{0: 1}, 0.5, 1e-12)
	// Stationary solution of p = a·s + (1-a)·p·W on the undirected path.
	want := referencePPR(g, map[int]float64{0: 1}, 0.5)
	for i := range p {
		if math.Abs(p[i]-want[i]) > 1e-9 {
			t.Fatalf("p = %v, want %v", p, want)
		}
	}
	if !(p[0] > p[1] && p[1] > p[2]) {
		t.Fatalf("mass must decay with distance from the seed: %v", p)
	}
}

func TestPersonalizedPageRankIsolatedSeedKeepsItsMass(t *testing.T) {
	g := build(3, [][2]int{{1, 2}})
	p := PersonalizedPageRank(g, map[int]float64{0: 2, 1: 2}, 0.25, 1e-6)
	if math.Abs(p[0]-0.5) > 1e-12 {
		t.Fatalf("isolated seed mass = %v, want 0.5", p[0])
	}
}

// referencePPR is a power iteration on the same model the push approximates:
// the undirected random walk with restart, an isolated node looping on itself.
func referencePPR(g *Graph, seeds map[int]float64, alpha float64) []float64 {
	adj := g.Undirected()
	s := make([]float64, g.N)
	var total float64
	for _, w := range seeds {
		total += w
	}
	for u, w := range seeds {
		s[u] = w / total
	}
	x := append([]float64(nil), s...)
	for iter := 0; iter < 5000; iter++ {
		next := make([]float64, g.N)
		for u := 0; u < g.N; u++ {
			next[u] += alpha * s[u]
			if len(adj[u]) == 0 {
				next[u] += (1 - alpha) * x[u]
				continue
			}
			share := (1 - alpha) * x[u] / float64(len(adj[u]))
			for _, v := range adj[u] {
				next[v] += share
			}
		}
		x = next
	}
	return x
}

func TestPersonalizedPageRankMatchesPowerIteration(t *testing.T) {
	for seed := uint64(1); seed <= 50; seed++ {
		r := rng(seed * 7919)
		n := 5 + r.next(60)
		g := randomGraph(seed, n, n*(1+r.next(4)))
		seeds := map[int]float64{r.next(n): 1, r.next(n): 0.5}
		want := referencePPR(g, seeds, 0.25)
		l1 := func(eps float64) float64 {
			got := PersonalizedPageRank(g, seeds, 0.25, eps)
			var sum float64
			for i := range got {
				sum += math.Abs(got[i] - want[i])
			}
			return sum
		}
		// Forward push stops with every residual below eps·deg(u), so the
		// L1 error is at most eps·Σdeg; and it shrinks with eps.
		var sumDeg float64
		for _, nb := range g.Undirected() {
			sumDeg += float64(len(nb))
		}
		if got := l1(1e-5); got > 1e-5*sumDeg {
			t.Fatalf("graph %d (n=%d): L1 error %v above the bound %v", seed, n, got, 1e-5*sumDeg)
		}
		if got := l1(1e-6); got > 1e-3 {
			t.Fatalf("graph %d (n=%d): L1 error %v at eps 1e-6", seed, n, got)
		}
	}
}

func TestDeterminism(t *testing.T) {
	g := randomGraph(42, 80, 300)
	seeds := map[int]float64{3: 1, 17: 0.3, 60: 0.7}
	a := PersonalizedPageRank(g, seeds, 0.25, 1e-4)
	b := PersonalizedPageRank(randomGraph(42, 80, 300), seeds, 0.25, 1e-4)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("PPR differs between two runs")
	}
	d1, p1 := BFS(g, []int{5, 1}, Both, 0)
	d2, p2 := BFS(g, []int{1, 5}, Both, 0)
	if !reflect.DeepEqual(d1, d2) || !reflect.DeepEqual(p1, p2) {
		t.Fatal("BFS depends on the order sources are given in")
	}
	if !reflect.DeepEqual(ResourceAllocation(g, 7, 1), ResourceAllocation(g, 7, 1)) {
		t.Fatal("ResourceAllocation differs between two runs")
	}
}

// Relabelling the nodes must not change what the algorithms say, only the
// labels: PPR within its approximation, distances and scores exactly.
func TestInvarianceUnderRelabelling(t *testing.T) {
	const n = 40
	g := randomGraph(9, n, 120)
	perm := make([]int, n) // old → new
	r := rng(99)
	for i := range perm {
		perm[i] = i
	}
	for i := n - 1; i > 0; i-- {
		j := r.next(i + 1)
		perm[i], perm[j] = perm[j], perm[i]
	}
	var edges [][2]int
	for u := 0; u < n; u++ {
		for _, v := range g.Out[u] {
			edges = append(edges, [2]int{perm[u], perm[v]})
		}
	}
	h := build(n, edges)

	pg := PersonalizedPageRank(g, map[int]float64{0: 1}, 0.25, 1e-6)
	ph := PersonalizedPageRank(h, map[int]float64{perm[0]: 1}, 0.25, 1e-6)
	var l1 float64
	for u := 0; u < n; u++ {
		l1 += math.Abs(pg[u] - ph[perm[u]])
	}
	if l1 > 1e-3 {
		t.Fatalf("PPR under relabelling: L1 %v", l1)
	}

	dg, _ := BFS(g, []int{0}, Out, 0)
	dh, _ := BFS(h, []int{perm[0]}, Out, 0)
	for u := 0; u < n; u++ {
		if dg[u] != dh[perm[u]] {
			t.Fatalf("BFS distance of %d: %d vs %d", u, dg[u], dh[perm[u]])
		}
	}
	for t2 := 0; t2 < n; t2++ {
		a, b := ShortestPath(g, 0, t2, Both, 0), ShortestPath(h, perm[0], perm[t2], Both, 0)
		if len(a) != len(b) {
			t.Fatalf("path length to %d: %d vs %d", t2, len(a), len(b))
		}
	}
	ra := map[int]float64{}
	for _, c := range ResourceAllocation(g, 0, 1) {
		ra[perm[c.Node]] = c.Score
	}
	for _, c := range ResourceAllocation(h, perm[0], 1) {
		if math.Abs(ra[c.Node]-c.Score) > 1e-12 {
			t.Fatalf("RA score of %d: %v vs %v", c.Node, ra[c.Node], c.Score)
		}
		delete(ra, c.Node)
	}
	if len(ra) != 0 {
		t.Fatalf("RA candidates differ: %v", ra)
	}
}

func TestBFSByHand(t *testing.T) {
	// 0→1→2, 3→1: out from 0 reaches 2 in two hops; in from 1 reaches 0 and 3.
	g := build(4, [][2]int{{0, 1}, {1, 2}, {3, 1}})
	dist, pred := BFS(g, []int{0}, Out, 0)
	if !reflect.DeepEqual(dist, []int{0, 1, 2, -1}) || !reflect.DeepEqual(pred, []int{-1, 0, 1, -1}) {
		t.Fatalf("out: dist %v pred %v", dist, pred)
	}
	dist, _ = BFS(g, []int{1}, In, 0)
	if !reflect.DeepEqual(dist, []int{1, 0, -1, 1}) {
		t.Fatalf("in: dist %v", dist)
	}
	dist, _ = BFS(g, []int{0}, Both, 1)
	if !reflect.DeepEqual(dist, []int{0, 1, -1, -1}) {
		t.Fatalf("both, one hop: dist %v", dist)
	}
}

func TestShortestPathByHand(t *testing.T) {
	// Two equal-length routes 0→1→3 and 0→2→3: the smaller wins. And a
	// one-way chain has no path against its direction unless both is asked.
	g := build(5, [][2]int{{0, 2}, {0, 1}, {2, 3}, {1, 3}, {4, 0}})
	if got := ShortestPath(g, 0, 3, Out, 0); !reflect.DeepEqual(got, []int{0, 1, 3}) {
		t.Fatalf("path = %v", got)
	}
	if got := ShortestPath(g, 0, 4, Out, 0); got != nil {
		t.Fatalf("against the links: %v", got)
	}
	if got := ShortestPath(g, 0, 4, Both, 0); !reflect.DeepEqual(got, []int{0, 4}) {
		t.Fatalf("undirected: %v", got)
	}
	if got := ShortestPath(g, 4, 3, Out, 2); got != nil {
		t.Fatalf("beyond max hops: %v", got)
	}
	if got := ShortestPath(g, 2, 2, Out, 0); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("s == t: %v", got)
	}
}

func TestResourceAllocationByHand(t *testing.T) {
	// u=0 links 1 and 2; 3 is reached through both (deg 2 and deg 3), 4 only
	// through 2; 1 is already linked to 0 and is never a candidate.
	g := build(5, [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}, {2, 4}, {1, 2}})
	got := ResourceAllocation(g, 0, 1)
	// deg(1) = |{0,2,3}| = 3, deg(2) = |{0,1,3,4}| = 4.
	want := []Candidate{
		{Node: 3, Score: 1.0/3 + 1.0/4, Common: []int{1, 2}},
		{Node: 4, Score: 1.0 / 4, Common: []int{2}},
	}
	if len(got) != len(want) {
		t.Fatalf("RA = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Node != want[i].Node || math.Abs(got[i].Score-want[i].Score) > 1e-12 || !reflect.DeepEqual(got[i].Common, want[i].Common) {
			t.Fatalf("RA = %+v, want %+v", got, want)
		}
	}
	if got := ResourceAllocation(g, 0, 2); len(got) != 1 || got[0].Node != 3 {
		t.Fatalf("minCommon 2 = %+v", got)
	}
}

func TestEmptyAndSingleNode(t *testing.T) {
	for _, g := range []*Graph{build(0, nil), build(1, nil)} {
		p := PersonalizedPageRank(g, map[int]float64{0: 1}, 0.25, 1e-4)
		if g.N == 1 && p[0] != 1 {
			t.Fatalf("single node PPR = %v", p)
		}
		if g.N == 0 && len(p) != 0 {
			t.Fatalf("empty PPR = %v", p)
		}
		dist, _ := BFS(g, []int{0}, Both, 0)
		if len(dist) != g.N {
			t.Fatalf("BFS on %d nodes: %v", g.N, dist)
		}
		if g.N == 1 && !reflect.DeepEqual(ShortestPath(g, 0, 0, Both, 0), []int{0}) {
			t.Fatal("single node path")
		}
		if ShortestPath(g, 0, 1, Both, 0) != nil {
			t.Fatal("path to a node that does not exist")
		}
		if got := ResourceAllocation(g, 0, 1); len(got) != 0 {
			t.Fatalf("RA = %v", got)
		}
	}
}
