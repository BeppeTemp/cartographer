package graphalgo

import "sort"

// WeakComponents returns the connected components of the undirected
// projection, each sorted ascending, largest first; components of equal size
// are ordered by their smallest member.
func WeakComponents(g *Graph) [][]int {
	adj := g.Undirected()
	seen := make([]bool, g.N)
	var comps [][]int
	for s := 0; s < g.N; s++ {
		if seen[s] {
			continue
		}
		seen[s] = true
		comp := []int{s}
		for i := 0; i < len(comp); i++ {
			for _, v := range adj[comp[i]] {
				if !seen[v] {
					seen[v] = true
					comp = append(comp, v)
				}
			}
		}
		sort.Ints(comp)
		comps = append(comps, comp)
	}
	sort.SliceStable(comps, func(i, j int) bool {
		if len(comps[i]) != len(comps[j]) {
			return len(comps[i]) > len(comps[j])
		}
		return comps[i][0] < comps[j][0]
	})
	return comps
}

// Articulation is a node whose removal disconnects its component.
type Articulation struct {
	Node int
	// Separated is how many nodes lose their connection to the bulk of the
	// component when Node is removed: the component's size minus one, minus
	// the largest part left.
	Separated int
}

// ArticulationPoints finds the articulation points of the undirected
// projection with Tarjan's lowpoint algorithm, iteratively — a KB's link
// depth is unbounded in principle, and recursion is not. Removing a point a
// splits its component C∖{a} into parts: each DFS child subtree whose lowpoint
// does not reach above a, plus whatever remains on a's ancestor side. The
// result is sorted by node.
func ArticulationPoints(g *Graph) []Articulation {
	adj := g.Undirected()
	n := g.N
	disc := make([]int, n)
	low := make([]int, n)
	size := make([]int, n)
	parent := make([]int, n)
	next := make([]int, n)
	compSize := make([]int, n)
	for i := range disc {
		disc[i], parent[i] = -1, -1
	}
	for _, comp := range WeakComponents(g) {
		for _, u := range comp {
			compSize[u] = len(comp)
		}
	}
	// Per node: how many child subtrees are cut off by it, their total size
	// and the largest one.
	cutCount := make([]int, n)
	cutSum := make([]int, n)
	cutMax := make([]int, n)

	time := 0
	for root := 0; root < n; root++ {
		if disc[root] >= 0 {
			continue
		}
		disc[root], low[root], size[root] = time, time, 1
		time++
		stack := []int{root}
		for len(stack) > 0 {
			u := stack[len(stack)-1]
			if next[u] < len(adj[u]) {
				v := adj[u][next[u]]
				next[u]++
				if v == parent[u] {
					continue // the graph is simple: one tree edge back
				}
				if disc[v] < 0 {
					parent[v] = u
					disc[v], low[v], size[v] = time, time, 1
					time++
					stack = append(stack, v)
				} else if disc[v] < low[u] {
					low[u] = disc[v]
				}
				continue
			}
			stack = stack[:len(stack)-1]
			p := parent[u]
			if p < 0 {
				continue
			}
			if low[u] < low[p] {
				low[p] = low[u]
			}
			size[p] += size[u]
			if low[u] >= disc[p] {
				cutCount[p]++
				cutSum[p] += size[u]
				if size[u] > cutMax[p] {
					cutMax[p] = size[u]
				}
			}
		}
	}

	var out []Articulation
	for u := 0; u < n; u++ {
		isRoot := parent[u] < 0
		if (isRoot && cutCount[u] < 2) || (!isRoot && cutCount[u] < 1) {
			continue
		}
		rest := compSize[u] - 1 - cutSum[u] // the ancestor side; 0 for a root
		largest := max(cutMax[u], rest)
		out = append(out, Articulation{Node: u, Separated: compSize[u] - 1 - largest})
	}
	return out
}
