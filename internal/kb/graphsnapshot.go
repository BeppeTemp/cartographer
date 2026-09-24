package kb

import (
	"math"
	"path"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/graphalgo"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Bounds for a graph snapshot. The default keeps a browser responsive on a
// large KB; the maxima are the point past which a WebGL canvas stops being a
// navigation aid and becomes a hairball, so a request above them is clamped
// rather than refused — an operator asking for too much still gets a usable,
// explicitly truncated answer.
const (
	DefaultGraphNodeLimit = 2000
	MaxGraphNodeLimit     = 5000
	MaxGraphEdges         = 20000
)

// GraphNode is one concept in a snapshot. InDegree and OutDegree count links
// to and from *visible* concepts across the whole KB, before any scope or
// limit is applied: a client showing four of a node's twelve links needs to
// know that eight are missing from the picture.
type GraphNode struct {
	ID         okf.ConceptID `json:"id"`
	Collection string        `json:"collection,omitempty"`
	// Title is the frontmatter title: what a client names a node by, since an
	// id is a path. Empty when the concept has none; the client falls back to
	// the id.
	Title string `json:"title,omitempty"`
	// Type and Status come from the frontmatter and are what a client filters
	// and encodes on: without them a graph can only be filtered by collection,
	// which is the one facet its colours already show.
	Type      string `json:"type,omitempty"`
	Status    string `json:"status,omitempty"`
	Expanded  bool   `json:"expanded,omitempty"`
	SelfLink  bool   `json:"self_link,omitempty"`
	InDegree  int    `json:"in_degree"`
	OutDegree int    `json:"out_degree"`
	// PageRank is the node's global importance on the caller's visible graph
	// (D244), rounded to 6 decimals; Community is its community's rank in
	// GraphSnapshot.Communities. Both are whole-graph values, like the degrees.
	PageRank  float64 `json:"pagerank"`
	Community int     `json:"community"`
}

// SnapshotCommunity is one community of the caller's visible graph (D244):
// its rank (0 = largest), size, anchor (the member a legend names it after)
// and colour slot (1..12, or 0 for singletons and the long tail).
type SnapshotCommunity struct {
	Rank   int           `json:"rank"`
	Size   int           `json:"size"`
	Anchor okf.ConceptID `json:"anchor"`
	Slot   int           `json:"slot"`
}

// GraphEdge is a directed link between two concepts both present in the
// snapshot's node list. Duplicated links between the same pair collapse into
// one edge.
type GraphEdge struct {
	Source okf.ConceptID `json:"source"`
	Target okf.ConceptID `json:"target"`
}

// BrokenTarget is a link whose target is not a concept in this KB. It is
// deliberately not a node: promoting it would invent a concept that does not
// exist and let a graph grow phantom hubs. A target that *does* exist but is
// invisible to the caller is neither an edge nor a broken target — it is
// dropped silently, since reporting it would disclose its id.
type BrokenTarget struct {
	Source okf.ConceptID `json:"source"`
	Target okf.ConceptID `json:"target"`
}

// GraphSnapshotOptions bounds and filters a snapshot.
type GraphSnapshotOptions struct {
	// Scope restricts the node set to one top-level collection ("entities",
	// "incidents"). Empty means the whole KB.
	Scope string
	// Limit caps the returned nodes. <= 0 means DefaultGraphNodeLimit; above
	// MaxGraphNodeLimit it is clamped and the effective value is reported.
	Limit int
	// Include is the caller's visibility predicate. Nil means everything is
	// visible. This package holds no authorization logic of its own: the
	// caller passes the same predicate its other read paths use.
	Include func(id string) bool
}

// GraphSnapshot is a bounded, deterministic view of the KB link graph.
type GraphSnapshot struct {
	Nodes  []GraphNode    `json:"nodes"`
	Edges  []GraphEdge    `json:"edges"`
	Broken []BrokenTarget `json:"broken,omitempty"`
	// Communities covers the whole visible graph, not only the returned
	// nodes, so a scoped view keeps whole-KB colours.
	Communities []SnapshotCommunity `json:"communities"`

	// Truncated is set when any of the three lists was cut by a bound.
	Truncated bool `json:"truncated,omitempty"`
	// TotalNodes and TotalEdges are the untruncated counts for the requested
	// scope, so a caller can say how much it is not showing.
	TotalNodes  int `json:"total_nodes"`
	TotalEdges  int `json:"total_edges"`
	TotalBroken int `json:"total_broken,omitempty"`
	// Limit is the node limit actually applied after clamping.
	Limit int `json:"limit"`
}

// conceptCollection returns the top-level collection of a concept id — the
// first path segment. A concept at the KB root has no collection.
func conceptCollection(id okf.ConceptID) string {
	s := string(id)
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return s[:i]
		}
	}
	return ""
}

// GraphSnapshot reads the link graph from the stat-validated cache (D241) and
// returns a sorted, bounded projection of it.
//
// Selection is by importance and deterministic, and the output is sorted, so
// the same KB state always produces the same response: a graph whose nodes
// reshuffle between two identical requests is unusable as a navigation
// surface. Past the limit the nodes with the highest PageRank are kept, and
// past MaxGraphEdges the edges whose weaker endpoint ranks highest (D244). The traversal computes both degrees as
// it goes — the alternative, one GraphNeighbors call per concept, re-walks the
// whole KB once per node.
//
// Invariant: no returned edge has an endpoint absent from Nodes.
func (kb *KB) GraphSnapshot(opts GraphSnapshotOptions) (GraphSnapshot, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultGraphNodeLimit
	}
	if limit > MaxGraphNodeLimit {
		limit = MaxGraphNodeLimit
	}
	visible := opts.Include
	if visible == nil {
		visible = func(string) bool { return true }
	}

	type conceptMeta struct {
		collection string
		title      string
		typ        string
		status     string
		expanded   bool
		targets    []okf.ConceptID
	}
	// exists holds every concept id in the KB, visible or not: it is what
	// tells a genuinely broken link from a link to a concept this caller may
	// not see. Only the first is reportable.
	exists := map[okf.ConceptID]struct{}{}
	metas := map[okf.ConceptID]*conceptMeta{}
	ids := []okf.ConceptID{}

	view, err := kb.graphView()
	if err != nil {
		return GraphSnapshot{}, err
	}
	// Replays the cached walk (D241) in its order: the later of two files
	// emitting one id wins, as it did when this was a walk.
	for _, e := range view.entries {
		id := e.id
		exists[id] = struct{}{}
		if !visible(string(id)) {
			continue
		}
		// Malformed frontmatter leaves the facets empty rather than failing the
		// walk: one unparseable file must not blank the whole graph.
		meta := &conceptMeta{
			collection: conceptCollection(id),
			expanded:   e.rel == path.Join(string(id), "index.md"),
			targets:    e.links,
			typ:        e.facets.Type,
			title:      e.facets.Title,
			status:     e.facets.Status,
		}
		ids = append(ids, id)
		metas[id] = meta
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	// Pass two resolves every link against the visible concept set. Degrees
	// are whole-KB totals: scope and limit shrink what is drawn, never what a
	// node reports about itself.
	type edgeKey struct{ source, target okf.ConceptID }
	edgeSet := map[edgeKey]struct{}{}
	selfLink := map[okf.ConceptID]bool{}
	inDegree := map[okf.ConceptID]int{}
	outDegree := map[okf.ConceptID]int{}
	var broken []BrokenTarget

	for _, id := range ids {
		seen := map[okf.ConceptID]struct{}{}
		for _, target := range metas[id].targets {
			if _, dup := seen[target]; dup {
				continue
			}
			seen[target] = struct{}{}
			switch {
			case target == id:
				selfLink[id] = true
			case metas[target] != nil:
				edgeSet[edgeKey{id, target}] = struct{}{}
				outDegree[id]++
				inDegree[target]++
			default:
				if _, hidden := exists[target]; hidden {
					// Exists but invisible to this caller: not an edge, and
					// not reportable as broken either.
					continue
				}
				broken = append(broken, BrokenTarget{Source: id, Target: target})
			}
		}
	}

	// Trap: importance and communities are computed on the whole visible
	// graph, before scope and limit (D244). After them, a scoped view would
	// recolour and a truncated one would rank on what survived the cut; and a
	// graph that included hidden concepts would disclose them through the
	// numbers (D226).
	// They are computed on the edges this snapshot reports, not on the cached
	// adjacency: when two files emit one id the two can differ, and the
	// numbers must describe the graph the caller is shown.
	index := make(map[okf.ConceptID]int, len(ids))
	for i, id := range ids {
		index[id] = i
	}
	g := &graphalgo.Graph{N: len(ids), Out: make([][]int, len(ids)), In: make([][]int, len(ids))}
	for key := range edgeSet {
		u, v := index[key.source], index[key.target]
		g.Out[u] = append(g.Out[u], v)
		g.In[v] = append(g.In[v], u)
	}
	degree := make([]int, len(ids))
	for i, id := range ids {
		sort.Ints(g.Out[i])
		sort.Ints(g.In[i])
		degree[i] = inDegree[id] + outDegree[id]
	}
	pagerank := graphalgo.PageRank(g, 0.85, 1e-9, 100)
	prOf := func(id okf.ConceptID) float64 { return pagerank[index[id]] }
	communityOf, communities := graphalgo.Communities(g, 1, degree)

	// Scope selects the full candidate node set; the limit then cuts it.
	scoped := ids
	if opts.Scope != "" {
		scoped = scoped[:0:0]
		for _, id := range ids {
			if metas[id].collection == opts.Scope {
				scoped = append(scoped, id)
			}
		}
	}
	inScope := make(map[okf.ConceptID]struct{}, len(scoped))
	for _, id := range scoped {
		inScope[id] = struct{}{}
	}

	snap := GraphSnapshot{TotalNodes: len(scoped), Limit: limit}
	for key := range edgeSet {
		if _, ok := inScope[key.source]; !ok {
			continue
		}
		if _, ok := inScope[key.target]; !ok {
			continue
		}
		snap.TotalEdges++
	}

	snap.Communities = make([]SnapshotCommunity, len(communities))
	for i, c := range communities {
		snap.Communities[i] = SnapshotCommunity{Rank: c.Rank, Size: c.Size, Anchor: ids[c.Anchor], Slot: c.Slot}
	}

	kept := scoped
	if len(kept) > limit {
		byImportance := append([]okf.ConceptID(nil), scoped...)
		sort.SliceStable(byImportance, func(i, j int) bool {
			pi, pj := prOf(byImportance[i]), prOf(byImportance[j])
			if pi != pj {
				return pi > pj
			}
			return byImportance[i] < byImportance[j]
		})
		kept = byImportance[:limit]
		sort.Slice(kept, func(i, j int) bool { return kept[i] < kept[j] })
		snap.Truncated = true
	}
	inSnapshot := make(map[okf.ConceptID]struct{}, len(kept))
	snap.Nodes = make([]GraphNode, 0, len(kept))
	for _, id := range kept {
		inSnapshot[id] = struct{}{}
		snap.Nodes = append(snap.Nodes, GraphNode{
			ID:         id,
			Collection: metas[id].collection,
			Title:      metas[id].title,
			Type:       metas[id].typ,
			Status:     metas[id].status,
			Expanded:   metas[id].expanded,
			SelfLink:   selfLink[id],
			InDegree:   inDegree[id],
			OutDegree:  outDegree[id],
			PageRank:   math.Round(prOf(id)*1e6) / 1e6,
			Community:  communityOf[index[id]],
		})
	}

	snap.Edges = make([]GraphEdge, 0, len(edgeSet))
	for key := range edgeSet {
		if _, ok := inSnapshot[key.source]; !ok {
			continue
		}
		if _, ok := inSnapshot[key.target]; !ok {
			continue
		}
		snap.Edges = append(snap.Edges, GraphEdge{Source: key.source, Target: key.target})
	}
	bySourceTarget := func(e []GraphEdge) func(i, j int) bool {
		return func(i, j int) bool {
			if e[i].Source != e[j].Source {
				return e[i].Source < e[j].Source
			}
			return e[i].Target < e[j].Target
		}
	}
	sort.Slice(snap.Edges, bySourceTarget(snap.Edges))
	if len(snap.Edges) > MaxGraphEdges {
		weaker := func(e GraphEdge) float64 { return math.Min(prOf(e.Source), prOf(e.Target)) }
		sort.SliceStable(snap.Edges, func(i, j int) bool { return weaker(snap.Edges[i]) > weaker(snap.Edges[j]) })
		snap.Edges = snap.Edges[:MaxGraphEdges]
		sort.Slice(snap.Edges, bySourceTarget(snap.Edges))
		snap.Truncated = true
	}

	kept2 := broken[:0:0]
	for _, b := range broken {
		if _, ok := inSnapshot[b.Source]; ok {
			kept2 = append(kept2, b)
		}
	}
	sort.Slice(kept2, func(i, j int) bool {
		if kept2[i].Source != kept2[j].Source {
			return kept2[i].Source < kept2[j].Source
		}
		return kept2[i].Target < kept2[j].Target
	})
	snap.TotalBroken = len(kept2)
	if len(kept2) > MaxGraphEdges {
		kept2 = kept2[:MaxGraphEdges]
		snap.Truncated = true
	}
	snap.Broken = kept2
	return snap, nil
}
