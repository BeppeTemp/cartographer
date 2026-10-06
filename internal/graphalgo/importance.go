package graphalgo

import (
	"math"
	"sort"
)

// PageRank is the global, directed PageRank of every node (D244): links are
// endorsements, so direction matters here, unlike in retrieval. Power
// iteration with the given damping; the mass of a node with no out-links is
// spread uniformly over every node; it stops when the L1 change of an
// iteration drops below tol, or after maxIter iterations. The result sums
// to 1. Every sum runs in ascending index order, so it is deterministic to the
// last bit.
func PageRank(g *Graph, damping, tol float64, maxIter int) []float64 {
	n := g.N
	pr := make([]float64, n)
	if n == 0 {
		return pr
	}
	for i := range pr {
		pr[i] = 1 / float64(n)
	}
	next := make([]float64, n)
	for iter := 0; iter < maxIter; iter++ {
		var dangling float64
		for u := 0; u < n; u++ {
			if len(neighbours(g.Out, u)) == 0 {
				dangling += pr[u]
			}
		}
		base := (1-damping)/float64(n) + damping*dangling/float64(n)
		for v := 0; v < n; v++ {
			var in float64
			for _, u := range neighbours(g.In, v) {
				in += pr[u] / float64(len(g.Out[u]))
			}
			next[v] = base + damping*in
		}
		var diff float64
		for i := range pr {
			diff += math.Abs(next[i] - pr[i])
		}
		pr, next = next, pr
		if diff < tol {
			break
		}
	}
	return pr
}

// Community is one community of a partition, as the Atlas colours it.
type Community struct {
	// Rank is 0-based: 0 is the largest community; equal sizes are ordered by
	// their smallest member.
	Rank int
	Size int
	// Anchor is the member with the strongest ties inside the community, then
	// the highest degree, then the smallest index (D317): what a legend names
	// the community after.
	Anchor int
	// Slot is the colour slot: 1..CommunitySlots for the largest communities
	// with more than one member, OtherSlot for the rest.
	Slot int
}

// CommunitySlots and OtherSlot mirror web/src/lib/communities.ts: twelve hues,
// and one neutral slot shared by singletons and the long tail.
const (
	CommunitySlots = 12
	OtherSlot      = 0
)

// minModularityGain is the gain a Louvain move must exceed: below it a float
// rounding difference could flip a move, and with it the partition.
const minModularityGain = 1e-12

// Communities partitions the undirected projection of g (D244): Louvain with
// the given resolution, visiting nodes in ascending index order and never
// randomising, then every community split into its connected components — the
// guarantee Leiden adds over Louvain, that no community is disconnected. It
// returns each node's community rank and the communities ordered by rank.
//
// weights, when non-nil, is parallel to g.Out: weights[u][k] is the weight of
// the edge u → g.Out[u][k] (D317: a link written by a template line many
// concepts share weighs less, so boilerplate stops welding unrelated
// concepts into one community). An undirected edge takes the larger of its
// two directed weights — max, not sum, so a mutual link is not counted twice.
// nil weighs every edge 1.
//
// A community's anchor is the member with the strongest ties inside the same
// community — the sum of its edge weights to other members, so the count of
// its neighbours there when weights is nil (D317) — then the highest
// degree[u], then the smallest index: a hub every template links to has the
// highest total degree but is rarely what its community is about.
//
// Ranking and slot rules are the ones web/src/lib/communities.ts applied in
// the browser, so the Atlas colours keep their character.
func Communities(g *Graph, resolution float64, degree []int, weights [][]float64) ([]int, []Community) {
	adj := g.Undirected()
	uw := undirectedWeights(g, adj, weights)
	member := louvain(adj, uw, resolution)
	groups := splitDisconnected(adj, member)

	sort.SliceStable(groups, func(i, j int) bool {
		if len(groups[i]) != len(groups[j]) {
			return len(groups[i]) > len(groups[j])
		}
		return groups[i][0] < groups[j][0]
	})
	rank := make([]int, g.N)
	for r, ids := range groups {
		for _, u := range ids {
			rank[u] = r
		}
	}
	list := make([]Community, len(groups))
	for r, ids := range groups {
		anchor, bestInternal := ids[0], -1.0
		for _, u := range ids {
			internal := 0.0
			for k, v := range adj[u] {
				if rank[v] == r {
					if uw != nil {
						internal += uw[u][k]
					} else {
						internal++
					}
				}
			}
			if internal > bestInternal || internal == bestInternal && degree[u] > degree[anchor] {
				anchor, bestInternal = u, internal
			}
		}
		slot := OtherSlot
		if len(ids) > 1 && r < CommunitySlots {
			slot = r + 1
		}
		list[r] = Community{Rank: r, Size: len(ids), Anchor: anchor, Slot: slot}
	}
	return rank, list
}

// undirectedWeights projects directed edge weights (parallel to g.Out) onto
// the undirected adjacency adj: each undirected edge takes the max of its
// directed weights. nil in, nil out.
func undirectedWeights(g *Graph, adj [][]int, weights [][]float64) [][]float64 {
	if weights == nil {
		return nil
	}
	directed := func(u, v int) (float64, bool) {
		out := neighbours(g.Out, u)
		k := sort.SearchInts(out, v)
		if k < len(out) && out[k] == v && u < len(weights) && k < len(weights[u]) {
			return weights[u][k], true
		}
		return 0, false
	}
	uw := make([][]float64, len(adj))
	for u, vs := range adj {
		uw[u] = make([]float64, len(vs))
		for k, v := range vs {
			w, ok := directed(u, v)
			if back, okBack := directed(v, u); okBack && (!ok || back > w) {
				w, ok = back, true
			}
			if !ok {
				w = 1
			}
			uw[u][k] = w
		}
	}
	return uw
}

// wedge is a weighted edge of an aggregated Louvain level.
type wedge struct {
	to int
	w  float64
}

// louvain returns a community label per node of the undirected adjacency.
// weights, when non-nil, is parallel to adj; nil weighs every edge 1.
func louvain(adj [][]int, weights [][]float64, resolution float64) []int {
	n := len(adj)
	member := make([]int, n)
	for i := range member {
		member[i] = i
	}
	// Level 0: each undirected edge listed at both ends, with the same weight.
	level := make([][]wedge, n)
	self := make([]float64, n)
	for u, vs := range adj {
		for k, v := range vs {
			w := 1.0
			if weights != nil {
				w = weights[u][k]
			}
			level[u] = append(level[u], wedge{v, w})
		}
	}
	for {
		comm, moved := localMoving(level, self, resolution)
		if !moved {
			return member
		}
		// Renumber communities by first appearance in node order, then
		// aggregate: every community becomes one node of the next level.
		renum := map[int]int{}
		for _, c := range comm {
			if _, ok := renum[c]; !ok {
				renum[c] = len(renum)
			}
		}
		for i := range member {
			member[i] = renum[comm[member[i]]]
		}
		k := len(renum)
		weights := make([]map[int]float64, k)
		nextSelf := make([]float64, k)
		for i := range weights {
			weights[i] = map[int]float64{}
		}
		for u, es := range level {
			cu := renum[comm[u]]
			nextSelf[cu] += self[u]
			for _, e := range es {
				cv := renum[comm[e.to]]
				if cu == cv {
					// Each internal edge is seen from both ends: half each
					// keeps the self-loop at the edge's weight.
					nextSelf[cu] += e.w / 2
				} else {
					weights[cu][cv] += e.w
				}
			}
		}
		next := make([][]wedge, k)
		for c, m := range weights {
			keys := make([]int, 0, len(m))
			for v := range m {
				keys = append(keys, v)
			}
			sort.Ints(keys)
			for _, v := range keys {
				next[c] = append(next[c], wedge{v, m[v]})
			}
		}
		level, self = next, nextSelf
	}
}

// localMoving runs Louvain's first phase on one level: nodes in ascending
// order, each moved to the neighbouring community with the best modularity
// gain, until a full pass moves nothing. self[u] is u's self-loop weight
// (counted twice in its strength, as a loop is in modularity). It reports
// whether any node moved.
func localMoving(level [][]wedge, self []float64, resolution float64) ([]int, bool) {
	n := len(level)
	comm := make([]int, n)
	strength := make([]float64, n)
	var total float64 // 2m
	for u, es := range level {
		comm[u] = u
		strength[u] = 2 * self[u]
		for _, e := range es {
			strength[u] += e.w
		}
		total += strength[u]
	}
	if total == 0 {
		return comm, false
	}
	tot := append([]float64(nil), strength...)
	anyMove := false
	toComm := map[int]float64{}
	var cands []int
	for {
		moved := false
		for u := 0; u < n; u++ {
			cu := comm[u]
			clear(toComm)
			cands = cands[:0]
			for _, e := range level[u] {
				c := comm[e.to]
				if _, ok := toComm[c]; !ok {
					cands = append(cands, c)
				}
				toComm[c] += e.w
			}
			tot[cu] -= strength[u]
			gain := func(c int) float64 {
				return toComm[c] - resolution*tot[c]*strength[u]/total
			}
			best, bestGain := cu, gain(cu)
			sort.Ints(cands)
			for _, c := range cands {
				if g := gain(c); g > bestGain+minModularityGain {
					best, bestGain = c, g
				}
			}
			tot[best] += strength[u]
			if best != cu {
				comm[u] = best
				moved, anyMove = true, true
			}
		}
		if !moved {
			return comm, anyMove
		}
	}
}

// splitDisconnected returns every community's connected components (inside
// the community), each sorted ascending.
func splitDisconnected(adj [][]int, member []int) [][]int {
	seen := make([]bool, len(adj))
	var groups [][]int
	for s := range adj {
		if seen[s] {
			continue
		}
		seen[s] = true
		comp := []int{s}
		for i := 0; i < len(comp); i++ {
			for _, v := range adj[comp[i]] {
				if !seen[v] && member[v] == member[s] {
					seen[v] = true
					comp = append(comp, v)
				}
			}
		}
		sort.Ints(comp)
		groups = append(groups, comp)
	}
	return groups
}
