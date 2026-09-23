package graphalgo

import (
	"reflect"
	"sort"
	"testing"
)

func undirected(n int, pairs [][2]int) *Graph { return build(n, pairs) }

func TestWeakComponentsByHand(t *testing.T) {
	g := undirected(7, [][2]int{{0, 1}, {2, 3}, {3, 4}, {6, 5}})
	want := [][]int{{2, 3, 4}, {0, 1}, {5, 6}}
	if got := WeakComponents(g); !reflect.DeepEqual(got, want) {
		t.Fatalf("components = %v, want %v", got, want)
	}
}

func TestArticulationPointsByHand(t *testing.T) {
	for _, tc := range []struct {
		name string
		g    *Graph
		want []Articulation
	}{
		{"path", undirected(5, [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 4}}),
			[]Articulation{{1, 1}, {2, 2}, {3, 1}}},
		{"star", undirected(5, [][2]int{{0, 1}, {0, 2}, {0, 3}, {0, 4}}),
			[]Articulation{{0, 3}}},
		{"two triangles and a bridge node", undirected(7, [][2]int{{0, 1}, {1, 2}, {2, 0}, {2, 3}, {3, 4}, {4, 5}, {5, 6}, {6, 4}}),
			[]Articulation{{2, 2}, {3, 3}, {4, 2}}},
		{"cycle", undirected(5, [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 0}}), nil},
	} {
		if got := ArticulationPoints(tc.g); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// componentsWithout counts, by brute force, the parts a's component splits
// into when a is removed, and returns the size of each.
func componentsWithout(g *Graph, a int) []int {
	adj := g.Undirected()
	seen := make([]bool, g.N)
	seen[a] = true
	var sizes []int
	// Only a's own component matters.
	for _, s := range adj[a] {
		if seen[s] {
			continue
		}
		seen[s] = true
		stack, n := []int{s}, 0
		for len(stack) > 0 {
			u := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			n++
			for _, v := range adj[u] {
				if !seen[v] {
					seen[v] = true
					stack = append(stack, v)
				}
			}
		}
		sizes = append(sizes, n)
	}
	return sizes
}

// Against a brute-force reference and a union-find count on random graphs.
func TestStructureAgainstReferences(t *testing.T) {
	for seed := uint64(1); seed <= 60; seed++ {
		r := rng(seed * 104729)
		n := 2 + r.next(40)
		g := randomGraph(seed, n, n+r.next(n))

		parent := make([]int, n)
		for i := range parent {
			parent[i] = i
		}
		var find func(int) int
		find = func(x int) int {
			for parent[x] != x {
				parent[x] = parent[parent[x]]
				x = parent[x]
			}
			return x
		}
		for u := 0; u < n; u++ {
			for _, v := range g.Out[u] {
				parent[find(u)] = find(v)
			}
		}
		roots := map[int]bool{}
		for u := 0; u < n; u++ {
			roots[find(u)] = true
		}
		comps := WeakComponents(g)
		if len(comps) != len(roots) {
			t.Fatalf("graph %d: %d components, union-find %d", seed, len(comps), len(roots))
		}

		aps := map[int]int{}
		for _, a := range ArticulationPoints(g) {
			aps[a.Node] = a.Separated
		}
		for a := 0; a < n; a++ {
			parts := componentsWithout(g, a)
			if len(parts) < 2 {
				if _, ok := aps[a]; ok {
					t.Fatalf("graph %d: %d reported but removing it splits nothing", seed, a)
				}
				continue
			}
			sort.Sort(sort.Reverse(sort.IntSlice(parts)))
			total := 0
			for _, p := range parts {
				total += p
			}
			if got, ok := aps[a]; !ok || got != total-parts[0] {
				t.Fatalf("graph %d: node %d separated = %d (reported %v), want %d", seed, a, got, ok, total-parts[0])
			}
		}
	}
}

func TestStructureDeterminismAndEdges(t *testing.T) {
	g := randomGraph(5, 30, 45)
	if !reflect.DeepEqual(ArticulationPoints(g), ArticulationPoints(randomGraph(5, 30, 45))) ||
		!reflect.DeepEqual(WeakComponents(g), WeakComponents(randomGraph(5, 30, 45))) {
		t.Fatal("not deterministic")
	}
	if got := WeakComponents(build(0, nil)); len(got) != 0 {
		t.Fatalf("empty: %v", got)
	}
	if got := ArticulationPoints(build(1, nil)); len(got) != 0 {
		t.Fatalf("single node: %v", got)
	}
}
