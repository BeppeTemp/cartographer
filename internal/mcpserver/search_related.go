package mcpserver

import (
	"fmt"
	"os"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// search's related block (D781): the few concepts the links around the hits
// reach, offered with the hits so an agent sees the pages beside its answer
// without having to know graph_context exists. It never touches the hits.
const (
	// searchRelatedDefault is the block's size when the caller does not say.
	// Flipping it to 0 turns the block off by default and keeps the parameter.
	searchRelatedDefault = 5
	// searchRelatedMax bounds what a caller can ask for: ~150 bytes an entry.
	searchRelatedMax = 10
)

// relatedHit is one entry of the block: where it is, what it costs to read,
// and the concept it was reached through. No snippet and no score — the block
// says "look here next", the ranking is search's.
type relatedHit struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Via   string `json:"via,omitempty"`
	Bytes int64  `json:"bytes"`
}

// relatedLimit is the block size a `related` argument asks for: absent is the
// default, 0 turns the block off, anything above the maximum is the maximum.
func relatedLimit(arg *int) int {
	if arg == nil {
		return searchRelatedDefault
	}
	return max(0, min(*arg, searchRelatedMax))
}

// relatedBlock returns up to limit concepts around hits, nil when there is
// nothing to add.
//
// Seeds are the leading full-match hits, weighted 1/(1+rank) as in
// graph_context so the two agree. A partial (OR-fallback) hit and an archived
// one never seed: a weak or closed match must not pull its neighbours into the
// answer. Every returned hit is left out of the block, not only the seeds, and
// so is every retired concept.
//
// Trap: the graph is projected through visibleGraph, which removes the
// concepts the caller cannot see before the ranking runs. Filtering a block
// computed on the whole graph would still let a hidden hub decide what is shown.
func relatedBlock(ctx requestContext, k *kb.KB, hits []searchHit, limit int) []relatedHit {
	if limit <= 0 || len(hits) == 0 {
		return nil
	}
	// The ranking above has just validated the graph: project that snapshot
	// instead of walking every file a second time.
	lg, ok := k.LinkGraphOfLastView(func(id string) bool { return Visible(ctx, k, id) })
	if !ok {
		var err error
		if lg, err = visibleGraph(ctx, k); err != nil {
			fmt.Fprintf(os.Stderr, "cartographer: search related: %v\n", err)
			return nil
		}
	}
	shown := make(map[int]bool, len(hits))
	seeds := map[int]float64{}
	for r, h := range hits {
		i, ok := lg.Index[okf.ConceptID(h.ID)]
		if !ok {
			continue
		}
		shown[i] = true
		if r < graphContextSeedHits && !h.Partial && !h.Archived {
			seeds[i] += 1 / float64(1+r)
		}
	}
	if len(seeds) == 0 {
		return nil
	}
	skip := func(i int) bool { return shown[i] || kb.IsRetired(lg.Facets[i].Status) }
	var block []relatedHit
	for _, n := range rankNeighbours(lg, seeds, skip) {
		e := relatedHit{ID: string(lg.IDs[n.node]), Title: lg.Facets[n.node].Title, Bytes: lg.Facets[n.node].Bytes}
		if n.via >= 0 {
			e.Via = string(lg.IDs[n.via])
		}
		block = append(block, e)
		if len(block) == limit {
			break
		}
	}
	return block
}
