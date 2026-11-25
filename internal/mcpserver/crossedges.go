package mcpserver

import (
	"context"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// crossEdge is one cross-KB edge of a page as the caller may be told of it:
// the target (or source) the page itself holds, a state, and the bind text for
// out_of_scope. Never a title, a size or a count of anything the caller cannot
// read (D2398): an outgoing edge is the id the page's own text
// holds, an incoming one is listed only when the caller reads the linking page.
type crossEdge struct {
	Direction string `json:"direction"`
	KB        string `json:"kb"`
	ID        string `json:"id"`
	State     string `json:"state,omitempty"`
	Bind      string `json:"bind,omitempty"`
}

// crossOwnLink reports a link that names k itself: it is never an edge.
func crossOwnLink(k *kb.KB, l kb.CrossLink) bool {
	m, self := k.Mounts(), k.MountName()
	return m != nil && self != "" && l.Server == "" && m.Canonical(l.KB) == self
}

// crossOutEdges are the outgoing edges of links, per caller (crossStateFor).
// A malformed or self-named link has nothing to name and yields no row.
func crossOutEdges(ctx context.Context, k *kb.KB, links []kb.CrossLink) []crossEdge {
	var out []crossEdge
	seen := map[crossEdge]bool{}
	for _, l := range links {
		if l.Malformed || crossOwnLink(k, l) {
			continue
		}
		e := crossEdge{Direction: "out", KB: l.KB, ID: string(l.ID), State: string(crossStateFor(ctx, k, l))}
		if seen[e] {
			continue
		}
		seen[e] = true
		if crossState(e.State) == crossOutOfScope {
			e.Bind = bindHint(ctx, k.Mounts().Canonical(l.KB))
		}
		out = append(out, e)
	}
	return out
}

// crossInEdge is the row for one page of another KB that links here, or false
// when the caller may not read that page (no grant on its KB, or the page is
// outside its rights): it is then not told it exists, and is not counted.
func crossInEdge(ctx context.Context, k *kb.KB, ref kb.CrossRef) (crossEdge, bool) {
	src, canonical, mounted := crossMounted(k, ref.KB)
	if !mounted || !kbRosterVisible(ctx, canonical) || !Visible(ctx, src, string(ref.ID)) {
		return crossEdge{}, false
	}
	e := crossEdge{Direction: "in", KB: canonical, ID: string(ref.ID)}
	if set := routedSetFrom(ctx); set != nil && !containsString(set, canonical) {
		e.State, e.Bind = string(crossOutOfScope), bindHint(ctx, canonical)
	}
	return e, true
}

// crossEdgesOf lists the cross edges of page id of k for the caller, outgoing
// and/or incoming, sorted. An error reading another KB drops the incoming
// rows: unknown is never reported as empty, and nothing names the KB.
func crossEdgesOf(ctx context.Context, k *kb.KB, id okf.ConceptID, wantOut, wantIn bool) []crossEdge {
	var edges []crossEdge
	if wantOut {
		if links, err := k.CrossLinks(); err == nil {
			edges = append(edges, crossOutEdges(ctx, k, links[id])...)
		}
	}
	if wantIn {
		if refs, err := k.CrossIncoming(id); err == nil {
			for _, ref := range refs {
				if e, ok := crossInEdge(ctx, k, ref); ok {
					edges = append(edges, e)
				}
			}
		}
	}
	sortCrossEdges(edges)
	return edges
}

func sortCrossEdges(edges []crossEdge) {
	sort.SliceStable(edges, func(i, j int) bool { return crossEdgeLess(edges[i], edges[j]) })
}

// crossEdgeLess orders outgoing before incoming, then by KB and id.
func crossEdgeLess(a, b crossEdge) bool {
	if a.Direction != b.Direction {
		return a.Direction > b.Direction
	}
	if a.KB != b.KB {
		return a.KB < b.KB
	}
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	return a.State < b.State
}
