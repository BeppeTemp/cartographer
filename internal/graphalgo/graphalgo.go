// Package graphalgo holds the graph algorithms behind the retrieval tools
// (D242): personalized PageRank, BFS, shortest paths and link prediction over
// an int-indexed adjacency. It is pure — stdlib only, no I/O, no knowledge of
// concepts — and deterministic: every loop runs in ascending node order, so
// the same graph always gives the same answer.
package graphalgo

import (
	"sort"
	"sync"
)

// Graph is a directed graph over nodes 0..N-1. Out[u] and In[u] must be
// sorted ascending and free of duplicates and self-loops; every function here
// relies on that order for its determinism.
type Graph struct {
	N   int
	Out [][]int
	In  [][]int

	undirectedOnce sync.Once
	undirected     [][]int
}

// Undirected returns, for each node, the sorted union of its out- and
// in-neighbours: "A links to B" and "B links to A" are the same evidence of
// relatedness. Computed once.
func (g *Graph) Undirected() [][]int {
	g.undirectedOnce.Do(func() {
		g.undirected = make([][]int, g.N)
		for u := 0; u < g.N; u++ {
			g.undirected[u] = mergeSorted(neighbours(g.Out, u), neighbours(g.In, u))
		}
	})
	return g.undirected
}

func neighbours(adj [][]int, u int) []int {
	if u < len(adj) {
		return adj[u]
	}
	return nil
}

// mergeSorted is the deduplicated union of two ascending lists.
func mergeSorted(a, b []int) []int {
	out := make([]int, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		var next int
		switch {
		case j == len(b) || (i < len(a) && a[i] < b[j]):
			next = a[i]
			i++
		case i == len(a) || b[j] < a[i]:
			next = b[j]
			j++
		default:
			next = a[i]
			i++
			j++
		}
		if len(out) == 0 || out[len(out)-1] != next {
			out = append(out, next)
		}
	}
	return out
}

// Direction selects which edges a traversal follows.
type Direction int

const (
	Out Direction = iota
	In
	Both
)

func (g *Graph) adjacency(d Direction) [][]int {
	switch d {
	case Out:
		return g.Out
	case In:
		return g.In
	default:
		return g.Undirected()
	}
}

// PersonalizedPageRank approximates the personalized PageRank vector of seeds
// on the undirected projection by forward push (Andersen, Chung, Lang 2006).
// alpha is the restart probability; a node is pushed while its residual is at
// least eps times its degree, so the cost is O(1/(alpha·eps)) whatever the
// graph size. Seed weights are normalised to sum 1. An isolated node keeps the
// mass that reaches it, as a node with a self-loop would.
//
// The push queue is FIFO, started with the seeds in ascending order, which is
// what makes the result deterministic.
func PersonalizedPageRank(g *Graph, seeds map[int]float64, alpha, eps float64) []float64 {
	p := make([]float64, g.N)
	if g.N == 0 {
		return p
	}
	adj := g.Undirected()
	r := make([]float64, g.N)
	// Ascending order before any arithmetic: a float sum in map order could
	// differ in its last bit between two runs.
	order := make([]int, 0, len(seeds))
	for u, w := range seeds {
		if u >= 0 && u < g.N && w > 0 {
			order = append(order, u)
		}
	}
	sort.Ints(order)
	var total float64
	for _, u := range order {
		total += seeds[u]
	}
	if total == 0 {
		return p
	}
	for _, u := range order {
		r[u] = seeds[u] / total
	}

	queued := make([]bool, g.N)
	queue := make([]int, 0, len(order))
	for _, u := range order {
		queued[u] = true
		queue = append(queue, u)
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		queued[u] = false
		deg := len(adj[u])
		if deg == 0 {
			p[u] += r[u]
			r[u] = 0
			continue
		}
		if r[u] < eps*float64(deg) {
			continue
		}
		ru := r[u]
		p[u] += alpha * ru
		r[u] = 0
		share := (1 - alpha) * ru / float64(deg)
		for _, v := range adj[u] {
			r[v] += share
			if !queued[v] && r[v] >= eps*float64(max(len(adj[v]), 1)) {
				queued[v] = true
				queue = append(queue, v)
			}
		}
	}
	return p
}

// BFS returns every node's hop distance from the nearest source (-1 when not
// reached within maxHops; maxHops <= 0 means unbounded) and the predecessor it
// was first discovered from (-1 for a source or an unreached node). Sources
// and neighbours are expanded in ascending order.
func BFS(g *Graph, sources []int, d Direction, maxHops int) (dist, pred []int) {
	dist = make([]int, g.N)
	pred = make([]int, g.N)
	for i := range dist {
		dist[i], pred[i] = -1, -1
	}
	srcs := append([]int(nil), sources...)
	sort.Ints(srcs)
	queue := make([]int, 0, len(srcs))
	for _, s := range srcs {
		if s >= 0 && s < g.N && dist[s] < 0 {
			dist[s] = 0
			queue = append(queue, s)
		}
	}
	adj := g.adjacency(d)
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		if maxHops > 0 && dist[u] >= maxHops {
			continue
		}
		for _, v := range neighbours(adj, u) {
			if dist[v] < 0 {
				dist[v] = dist[u] + 1
				pred[v] = u
				queue = append(queue, v)
			}
		}
	}
	return dist, pred
}

// ShortestPath returns a shortest path from s to t, both included, within
// maxHops (<= 0: unbounded), or nil. Among paths of equal length it is the
// lexicographically smallest: BFS with ascending expansion keeps each level's
// queue in lexicographic order of the paths reaching it, so a node's first
// discoverer is the smallest prefix.
func ShortestPath(g *Graph, s, t int, d Direction, maxHops int) []int {
	if s < 0 || s >= g.N || t < 0 || t >= g.N {
		return nil
	}
	if s == t {
		return []int{s}
	}
	dist, pred := BFS(g, []int{s}, d, maxHops)
	if dist[t] < 0 {
		return nil
	}
	path := make([]int, dist[t]+1)
	for i, v := len(path)-1, t; i >= 0; i, v = i-1, pred[v] {
		path[i] = v
	}
	return path
}

// Candidate is a suggested link from ResourceAllocation.
type Candidate struct {
	Node   int
	Score  float64
	Common []int // common undirected neighbours, ascending
}

// ResourceAllocation scores every node at undirected distance exactly 2 from
// u — so not linked to u in either direction — by Σ 1/deg(z) over their common
// undirected neighbours z (Zhou, Lü, Zhang 2009). Evidence through a hub is
// discounted more than Adamic–Adar would. Only candidates with at least
// minCommon common neighbours are kept; the result is sorted by score, highest
// first, then by node.
func ResourceAllocation(g *Graph, u, minCommon int) []Candidate {
	if u < 0 || u >= g.N {
		return nil
	}
	adj := g.Undirected()
	linked := make(map[int]bool, len(adj[u]))
	for _, z := range adj[u] {
		linked[z] = true
	}
	scores := map[int]float64{}
	common := map[int][]int{}
	for _, z := range adj[u] { // ascending: sums and lists come out in order
		w := 1 / float64(len(adj[z]))
		for _, v := range adj[z] {
			if v == u || linked[v] {
				continue
			}
			scores[v] += w
			common[v] = append(common[v], z)
		}
	}
	out := make([]Candidate, 0, len(scores))
	for v, s := range scores {
		if len(common[v]) >= minCommon {
			out = append(out, Candidate{Node: v, Score: s, Common: common[v]})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Node < out[j].Node
	})
	return out
}
