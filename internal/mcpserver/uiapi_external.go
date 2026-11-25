package mcpserver

import (
	"net/http"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// uiExternalLimit is the default and the ceiling of GET /graph/external rows.
const uiExternalLimit = 2000

// uiExternalEdge is one cross-KB edge of a page, with the page it belongs to.
type uiExternalEdge struct {
	crossEdge
	From string `json:"from"`
}

// uiGraphExternalBody is the body of GET /kbs/{kb}/graph/external.
type uiGraphExternalBody struct {
	Edges     []uiExternalEdge `json:"edges"`
	Limit     int              `json:"limit"`
	Truncated bool             `json:"truncated"`
}

// uiGraphExternal serves the cross-KB edges of the pages the caller sees.
//
// Trap: it is deliberately not part of /graph, whose body is cached by this
// KB's generation and the caller's view of it: a cross state depends on the
// caller's rights on other KBs and on their pages, which that key does not
// hold (D2398). The route is never served from the UI cache.
func uiGraphExternal(w http.ResponseWriter, r *http.Request, k *kb.KB) {
	if uiBadLimit(w, r) {
		return
	}
	ctx := r.Context()
	scope, ok := uiGraphScope(w, r)
	if !ok {
		return
	}
	limit := uiLimit(r, uiExternalLimit, uiExternalLimit)
	inScope := func(id string) bool {
		return (scope == "" || conceptCollection(id) == scope) && Visible(ctx, k, id)
	}

	body := uiGraphExternalBody{Edges: []uiExternalEdge{}, Limit: limit}
	var edges []uiExternalEdge
	if links, err := k.CrossLinks(); err == nil {
		for id, ls := range links {
			if !inScope(string(id)) {
				continue
			}
			for _, e := range crossOutEdges(ctx, k, ls) {
				edges = append(edges, uiExternalEdge{crossEdge: e, From: string(id)})
			}
		}
	}
	// A backlink to a page this KB does not hold has no node to start from.
	graph, gerr := k.Links()
	if refs, err := k.CrossIncomingAll(); err == nil && gerr == nil {
		for _, ref := range refs {
			if _, there := graph.Exists[ref.Target]; !there || !inScope(string(ref.Target)) {
				continue
			}
			if e, ok := crossInEdge(ctx, k, kb.CrossRef{KB: ref.KB, ID: ref.ID}); ok {
				edges = append(edges, uiExternalEdge{crossEdge: e, From: string(ref.Target)})
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		return crossEdgeLess(a.crossEdge, b.crossEdge)
	})
	if len(edges) > limit {
		edges, body.Truncated = edges[:limit], true
	}
	if edges != nil {
		body.Edges = edges
	}
	writeUIJSON(w, http.StatusOK, body)
}
