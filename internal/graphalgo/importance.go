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
	// Anchor is the member with the highest degree (then the smallest index):
	// what a legend names the community after.
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

// Communities partitions the undirected, unweighted projection of g (D244):
// Louvain with the given resolution, visiting nodes in ascending index order
// and never randomising, then every community split into its connected
// components — the guarantee Leiden adds over Louvain, that no community is
// disconnected. degree[u] is the degree an anchor is chosen by. It returns
// each node's community rank and the communities ordered by rank.
//
// Ranking, anchor and slot rules are the ones web/src/lib/communities.ts
// applied in the browser, so the Atlas colours keep their character.
func Communities(g *Graph, resolution float64, degree []int) ([]int, []Community) {
	adj := g.Undirected()
	member := louvain(adj, resolution)
	groups := splitDisconnected(adj, member)

	sort.SliceStable(groups, func(i, j int) bool {
		if len(groups[i]) != len(groups[j]) {
			return len(groups[i]) > len(groups[j])
		}
		return groups[i][0] < groups[j][0]
	})
	rank := make([]int, g.N)
	list := make([]Community, len(groups))
	for r, ids := range groups {
		anchor := ids[0]
		for _, u := range ids {
			rank[u] = r
			if degree[u] > degree[anchor] {
				anchor = u
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

// wedge is a weighted edge of an aggregated Louvain level.
type wedge struct {
	to int
	w  float64
}

// louvain returns a community label per node of the undirected adjacency.
func louvain(adj [][]int, resolution float64) []int {
	n := len(adj)
	member := make([]int, n)
	for i := range member {
		member[i] = i
	}
	// Level 0: every edge weighs 1, each undirected edge listed at both ends.
	level := make([][]wedge, n)
	self := make([]float64, n)
	for u, vs := range adj {
		for _, v := range vs {
			level[u] = append(level[u], wedge{v, 1})
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
