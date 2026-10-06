package kb

import (
	"sort"
	"strings"

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
	// Bytes is the concept file's size (D301), from its stat signature.
	Bytes int64
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
	// EdgeWeights is parallel to Graph.Out: EdgeWeights[u][k] weighs the edge
	// u → Graph.Out[u][k] (D317). An edge written by a line that at least
	// boilerplateThreshold of the projected concepts carry — a template's
	// boilerplate — weighs 1/N; every other edge weighs 1.
	EdgeWeights [][]float64
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
		Links:  Links{Out: view.adj.out, In: view.adj.in, Exists: view.exists, Bytes: view.bytes},
	}
	for i, id := range ids {
		lg.Index[id] = i
		f := view.facets[id]
		lg.Facets[i] = NodeFacets{Title: f.Title, Type: f.Type, Status: f.Status, SupersededBy: f.SupersededBy, Collection: conceptCollection(id), Placeholders: f.Placeholders, Bytes: view.bytes[id]}
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
	// Counted over the projected concepts only: a count that included hidden
	// ones would disclose them through the communities (D226).
	counts := linkLineCounts(ids, func(id okf.ConceptID) map[okf.ConceptID][]string { return view.linkLines[id] })
	lg.EdgeWeights = make([][]float64, len(ids))
	for i, id := range ids {
		lg.EdgeWeights[i] = make([]float64, len(g.Out[i]))
		for k, j := range g.Out[i] {
			lg.EdgeWeights[i][k] = linkEdgeWeight(view.linkLines[id][ids[j]], counts)
		}
	}
	return lg
}

// boilerplateThreshold is how many concepts must carry the same link line
// before the edges it writes are discounted (D317): five pages saying the
// same thing is a template, not five authors agreeing.
const boilerplateThreshold = 5

// linkLinesOf returns, per concept ID body links to, the distinct normalised
// lines that link it: code masked as ExtractLinks masks it, whitespace runs
// collapsed, as lint's factLines normalises. A link whose text spans lines
// has no line and so is never discounted.
func linkLinesOf(body, basePath string, isAsset func(string) bool) map[okf.ConceptID][]string {
	var out map[okf.ConceptID][]string
	for _, line := range strings.Split(MaskCodeSpans(body), "\n") {
		if !strings.Contains(line, "](") && !strings.Contains(line, "[[") {
			continue
		}
		norm := strings.Join(strings.Fields(line), " ")
		for _, target := range ExtractLinks(line, basePath, isAsset) {
			if out == nil {
				out = map[okf.ConceptID][]string{}
			}
			lines := out[target]
			if len(lines) == 0 || lines[len(lines)-1] != norm {
				out[target] = append(lines, norm)
			}
		}
	}
	return out
}

// linkLineCounts counts, per normalised link line, how many of ids carry it.
func linkLineCounts(ids []okf.ConceptID, linesOf func(okf.ConceptID) map[okf.ConceptID][]string) map[string]int {
	counts := map[string]int{}
	for _, id := range ids {
		seen := map[string]bool{}
		for _, lines := range linesOf(id) {
			for _, l := range lines {
				if !seen[l] {
					seen[l] = true
					counts[l]++
				}
			}
		}
	}
	return counts
}

// linkEdgeWeight weighs an edge written by lines: by its most specific line
// (the one the fewest concepts carry), 1/N once N reaches
// boilerplateThreshold, otherwise 1.
func linkEdgeWeight(lines []string, counts map[string]int) float64 {
	n := 0
	for _, l := range lines {
		if c := counts[l]; n == 0 || c < n {
			n = c
		}
	}
	if n >= boilerplateThreshold {
		return 1 / float64(n)
	}
	return 1
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
