package kb

import (
	"sort"

	"github.com/BeppeTemp/cartographer/internal/graphalgo"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// NodeFacets are the facts about a concept the graph tools report beside it.
type NodeFacets struct {
	Title        string
	Type         string
	Status       string
	SupersededBy string
	Collection   string
	// Placeholders are the concept's cited placeholder keys
	// (ConceptFacets.Placeholders, D262). Shared with the cache: read-only.
	Placeholders []string
}

// LinkGraph is the int-indexed projection of the link graph that the
// algorithms in internal/graphalgo run on (D242): only existing concepts a
// caller's include predicate accepts, links to anything else dropped, no
// self-links, no duplicates.
type LinkGraph struct {
	// IDs are the nodes in ascending order; a node's index is its position.
	IDs    []okf.ConceptID
	Index  map[okf.ConceptID]int
	Facets []NodeFacets
	Graph  *graphalgo.Graph
	// Links is the unprojected graph of the same view — every concept, links
	// to missing targets and self-links kept — so a caller needing both reads
	// the files once.
	Links Links
}

// LinkGraph projects the current cached view (D241) onto the concepts include
// accepts (nil accepts all), in O(V+E). include is called at most once per
// concept.
//
// The projection is what makes the graph tools safe for a narrowed principal:
// hidden concepts are removed before anything is computed, so the answer is
// the one the same algorithm gives on a KB in which they do not exist.
// Filtering a result computed on the whole graph instead would leak them
// through scores, distances and paths.
func (kb *KB) LinkGraph(include func(id string) bool) (*LinkGraph, error) {
	view, err := kb.graphView()
	if err != nil {
		return nil, err
	}
	return linkGraphOf(view, include), nil
}

// linkGraphOf is LinkGraph over a view the caller already holds.
func linkGraphOf(view *graphView, include func(id string) bool) *LinkGraph {
	ids := make([]okf.ConceptID, 0, len(view.exists))
	for id := range view.exists {
		if include == nil || include(string(id)) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	lg := &LinkGraph{
		IDs:    ids,
		Index:  make(map[okf.ConceptID]int, len(ids)),
		Facets: make([]NodeFacets, len(ids)),
		Links:  Links{Out: view.adj.out, In: view.adj.in, Exists: view.exists},
	}
	for i, id := range ids {
		lg.Index[id] = i
		f := view.facets[id]
		lg.Facets[i] = NodeFacets{Title: f.Title, Type: f.Type, Status: f.Status, SupersededBy: f.SupersededBy, Collection: conceptCollection(id), Placeholders: f.Placeholders}
	}
	g := &graphalgo.Graph{N: len(ids), Out: make([][]int, len(ids)), In: make([][]int, len(ids))}
	for i, id := range ids {
		for target := range view.adj.out[id] {
			j, ok := lg.Index[target]
			if !ok || j == i {
				continue
			}
			g.Out[i] = append(g.Out[i], j)
			g.In[j] = append(g.In[j], i)
		}
	}
	for i := range ids {
		sort.Ints(g.Out[i])
		sort.Ints(g.In[i])
	}
	lg.Graph = g
	return lg
}

type pageRankPercentiles struct {
	generation uint64
	pct        map[okf.ConceptID]float64
}

// PageRankPercentiles returns, for every concept include accepts (nil accepts
// all), its PageRank percentile on the graph of those concepts (D251): the
// share of the other N−1 concepts with a strictly lower PageRank, so every
// concept at the minimum — any concept nothing links to — gets exactly 0, and
// a single concept gets 0. Percentiles are scale-free: they mean the same on a
// KB of ten concepts and of five thousand.
//
// The whole-KB result (include == nil) is cached per graph view generation
// (D241); any other include is computed fresh, since it belongs to one
// principal.
func (kb *KB) PageRankPercentiles(include func(id string) bool) (map[okf.ConceptID]float64, error) {
	view, err := kb.graphView()
	if err != nil {
		return nil, err
	}
	if include == nil {
		kb.prMu.Lock()
		defer kb.prMu.Unlock()
		if c := kb.prCache; c != nil && c.generation == view.generation {
			return c.pct, nil
		}
	}
	lg := linkGraphOf(view, include)
	pr := graphalgo.PageRank(lg.Graph, 0.85, 1e-9, 100)
	sorted := append([]float64(nil), pr...)
	sort.Float64s(sorted)
	pct := make(map[okf.ConceptID]float64, len(pr))
	for i, id := range lg.IDs {
		if len(pr) > 1 {
			pct[id] = float64(sort.SearchFloat64s(sorted, pr[i])) / float64(len(pr)-1)
		} else {
			pct[id] = 0
		}
	}
	if include == nil {
		kb.prCache = &pageRankPercentiles{generation: view.generation, pct: pct}
	}
	return pct, nil
}
