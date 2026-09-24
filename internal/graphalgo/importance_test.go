package graphalgo

import (
	"math"
	"reflect"
	"testing"
)

func TestPageRankByHand(t *testing.T) {
	// A 3-cycle: every node has the same rank.
	pr := PageRank(build(3, [][2]int{{0, 1}, {1, 2}, {2, 0}}), 0.85, 1e-12, 100)
	for u, p := range pr {
		if math.Abs(p-1.0/3) > 1e-9 {
			t.Fatalf("cycle pr[%d] = %v", u, p)
		}
	}
	// 0 → 1 with 1 dangling: a = 0.075 + 0.425·b, a + b = 1 → a = 0.5/1.425.
	pr = PageRank(build(2, [][2]int{{0, 1}}), 0.85, 1e-12, 100)
	if want := 0.5 / 1.425; math.Abs(pr[0]-want) > 1e-9 || math.Abs(pr[1]-(1-want)) > 1e-9 {
		t.Fatalf("dangling = %v", pr)
	}
	// A star pointing at its centre, plus an isolated node: sums to 1.
	pr = PageRank(build(5, [][2]int{{1, 0}, {2, 0}, {3, 0}}), 0.85, 1e-9, 100)
	var sum float64
	for _, p := range pr {
		sum += p
	}
	if math.Abs(sum-1) > 1e-9 || pr[0] <= pr[1] || pr[1] != pr[4] {
		t.Fatalf("star = %v (sum %v)", pr, sum)
	}
	if got := PageRank(&Graph{}, 0.85, 1e-9, 100); len(got) != 0 {
		t.Fatalf("empty = %v", got)
	}
}

func degrees(g *Graph) []int {
	d := make([]int, g.N)
	for u := range d {
		d[u] = len(g.Out[u]) + len(g.In[u])
	}
	return d
}

// cliques builds k cliques of size s, clique i joined to clique i+1 by one
// edge when ring is set.
func cliques(k, s int, ring bool) *Graph {
	var edges [][2]int
	for c := 0; c < k; c++ {
		for i := 0; i < s; i++ {
			for j := i + 1; j < s; j++ {
				edges = append(edges, [2]int{c*s + i, c*s + j})
			}
		}
		if ring && k > 1 {
			edges = append(edges, [2]int{c * s, ((c + 1) % k) * s})
		}
	}
	return build(k*s, edges)
}

// The parity case of web/src/test/communities.test.ts: two 5-cliques joined by
// one bridge, plus an isolated node.
func TestCommunitiesTwoCliques(t *testing.T) {
	var edges [][2]int
	for c := 0; c < 2; c++ {
		for i := 0; i < 5; i++ {
			for j := i + 1; j < 5; j++ {
				edges = append(edges, [2]int{c*5 + i, c*5 + j})
			}
		}
	}
	edges = append(edges, [2]int{0, 5})
	g := build(11, edges)
	// The snapshot fixture reports degree 8 for every clique member.
	deg := make([]int, 11)
	for u := 0; u < 10; u++ {
		deg[u] = 8
	}
	rank, list := Communities(g, 1, deg)
	want := []int{0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 2}
	if !reflect.DeepEqual(rank, want) {
		t.Fatalf("rank = %v", rank)
	}
	wantList := []Community{{0, 5, 0, 1}, {1, 5, 5, 2}, {2, 1, 10, OtherSlot}}
	if !reflect.DeepEqual(list, wantList) {
		t.Fatalf("list = %+v", list)
	}
}

func TestCommunitiesRingOfCliques(t *testing.T) {
	g := cliques(14, 5, true)
	rank, list := Communities(g, 1, degrees(g))
	if len(list) != 14 {
		t.Fatalf("%d communities: %+v", len(list), list)
	}
	for c := 0; c < 14; c++ {
		for i := 1; i < 5; i++ {
			if rank[c*5+i] != rank[c*5] {
				t.Fatalf("clique %d split: %v", c, rank)
			}
		}
	}
	// Equal sizes rank by smallest member; only twelve get a hue.
	for r, c := range list {
		if c.Rank != r || c.Size != 5 || c.Anchor != r*5 {
			t.Fatalf("community %d = %+v", r, c)
		}
		if wantSlot := r + 1; r >= CommunitySlots && c.Slot != OtherSlot || r < CommunitySlots && c.Slot != wantSlot {
			t.Fatalf("slot of %d = %d", r, c.Slot)
		}
	}
}

// A community Louvain returns disconnected is split into its components.
func TestSplitDisconnected(t *testing.T) {
	adj := build(5, [][2]int{{0, 1}, {2, 3}, {1, 4}}).Undirected()
	got := splitDisconnected(adj, []int{7, 7, 7, 7, 9})
	want := [][]int{{0, 1}, {2, 3}, {4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("groups = %v", got)
	}
}

// No community is ever disconnected, on any graph.
func TestCommunitiesAreConnected(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		g := randomGraph(seed, 80, 160)
		rank, list := Communities(g, 1, degrees(g))
		adj := g.Undirected()
		for _, c := range list {
			var ids []int
			for u, r := range rank {
				if r == c.Rank {
					ids = append(ids, u)
				}
			}
			if len(ids) != c.Size {
				t.Fatalf("seed %d: size %d vs %d members", seed, c.Size, len(ids))
			}
			m := make([]int, g.N)
			for i := range m {
				m[i] = -1
			}
			for _, u := range ids {
				m[u] = c.Rank
			}
			parts := 0
			for _, p := range splitDisconnected(adj, m) {
				if m[p[0]] == c.Rank {
					parts++
				}
			}
			if parts != 1 {
				t.Fatalf("seed %d: community %d is in %d pieces", seed, c.Rank, parts)
			}
		}
	}
}

func TestCommunitiesDeterministicAndRelabelInvariant(t *testing.T) {
	g := randomGraph(5, 200, 500)
	r1, l1 := Communities(g, 1, degrees(g))
	r2, l2 := Communities(g, 1, degrees(g))
	if !reflect.DeepEqual(r1, r2) || !reflect.DeepEqual(l1, l2) {
		t.Fatal("not deterministic")
	}
	// Relabelling: on a graph whose partition is unambiguous, the same
	// communities come back under the new labels.
	g = cliques(6, 6, true)
	n := g.N
	perm := make([]int, n)
	r := rng(3)
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
	rg, _ := Communities(g, 1, degrees(g))
	rh, _ := Communities(h, 1, degrees(h))
	for u := 0; u < n; u++ {
		for v := 0; v < n; v++ {
			if (rg[u] == rg[v]) != (rh[perm[u]] == rh[perm[v]]) {
				t.Fatalf("partition differs under relabelling at %d,%d", u, v)
			}
		}
	}
	pg := PageRank(g, 0.85, 1e-12, 100)
	ph := PageRank(h, 0.85, 1e-12, 100)
	for u := 0; u < n; u++ {
		if math.Abs(pg[u]-ph[perm[u]]) > 1e-9 {
			t.Fatalf("pagerank of %d: %v vs %v", u, pg[u], ph[perm[u]])
		}
	}
}
