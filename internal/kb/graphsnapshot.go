package kb

import (
	"path"
	"sort"

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
// Sorting happens before truncation, so the same KB state always produces the
// same response: a graph whose nodes reshuffle between two identical requests
// is unusable as a navigation surface. The traversal computes both degrees as
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

	kept := scoped
	if len(kept) > limit {
		kept = kept[:limit]
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
	sort.Slice(snap.Edges, func(i, j int) bool {
		if snap.Edges[i].Source != snap.Edges[j].Source {
			return snap.Edges[i].Source < snap.Edges[j].Source
		}
		return snap.Edges[i].Target < snap.Edges[j].Target
	})
	if len(snap.Edges) > MaxGraphEdges {
		snap.Edges = snap.Edges[:MaxGraphEdges]
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
