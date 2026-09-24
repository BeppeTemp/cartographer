package mcpserver

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/graphalgo"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// The graph retrieval tools (D242). Each runs on kb.LinkGraph(visible): the
// subgraph induced by the concepts this caller may see. Hidden concepts are
// removed *before* anything is computed, never filtered out of a result
// afterwards — a score, a distance or a path computed on the whole graph would
// carry them — so a narrowed principal gets exactly the answer an admin gets
// on a KB in which the hidden concepts do not exist.

const (
	// graphContextAlpha is the restart probability of graph_context's
	// personalized PageRank. With the mean undirected degree of real KBs at
	// 6–9 it lets 2–3-hop context score while staying local.
	graphContextAlpha = 0.25
	// graphContextEps is the forward-push residual threshold per unit of
	// degree: the approximation's cost is O(1/(alpha·eps)).
	graphContextEps = 1e-4
	// graphContextSeedHits is how many search hits seed graph_context.
	graphContextSeedHits = 5
	// linkSuggestMinCommon: a single shared neighbour is too weak to act on.
	linkSuggestMinCommon = 2
)

// visibleGraph is the one way these tools obtain a graph.
func visibleGraph(ctx requestContext, k *kb.KB) (*kb.LinkGraph, error) {
	return k.LinkGraph(func(id string) bool { return Visible(ctx, k, id) })
}

// notFound is the one error for a missing and for a hidden concept: two texts
// would make the tool an existence oracle for a narrowed token.
func notFound(id string) ToolResult {
	return errorResult("not found: " + id)
}

func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }

func clampInt(v, def, lo, hi int) int {
	if v == 0 {
		v = def
	}
	return max(lo, min(hi, v))
}

// --- graph_context ---

func toolGraphContext(k *kb.KB, rec *searchReconciler, deps Deps) Tool {
	return Tool{
		Name:     "graph_context",
		ReadOnly: true,
		Description: "Ranked context around a question or a set of concepts: the concepts most related to them, " +
			"following links in both directions, with the hop count and the concept each was reached through.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "A question or topic; its top search hits seed the ranking"},
				"ids": {"type": "array", "items": {"type": "string"}, "maxItems": 10, "description": "ConceptIDs to start from (at most 10)"},
				"limit": {"type": "integer", "description": "Results to return (default 10, 1–30)"}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Query string   `json:"query"`
				IDs   []string `json:"ids"`
				Limit int      `json:"limit"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.Query == "" && len(params.IDs) == 0 {
				return errorResult("'query' or 'ids' is required"), nil
			}
			if len(params.IDs) > 10 {
				return errorResult("'ids' takes at most 10 concepts"), nil
			}
			limit := clampInt(params.Limit, 10, 1, 30)

			// Hidden concepts removed before computing: see the file comment.
			lg, err := visibleGraph(ctx, k)
			if err != nil {
				return errorResult(fmt.Sprintf("graph_context: %v", err)), nil
			}
			seeds := map[int]float64{}
			for _, id := range params.IDs {
				i, ok := lg.Index[okf.ConceptID(id)]
				if !ok {
					return notFound(id), nil
				}
				seeds[i] += 1
			}
			if params.Query != "" {
				// Reciprocal rank, not the raw score: FTS5 and the in-memory
				// index score on different scales.
				hits, _ := keywordHits(ctx, k, rec, deps, params.Query, "", graphContextSeedHits)
				for r, h := range hits {
					if i, ok := lg.Index[okf.ConceptID(h.ID)]; ok {
						seeds[i] += 1 / float64(1+r)
					}
				}
			}

			type seedOut struct {
				ID     string  `json:"id"`
				Title  string  `json:"title,omitempty"`
				Weight float64 `json:"weight"`
			}
			type resultOut struct {
				ID      string  `json:"id"`
				Title   string  `json:"title,omitempty"`
				Score   float64 `json:"score"`
				Hops    int     `json:"hops"`
				Via     string  `json:"via,omitempty"`
				Snippet string  `json:"snippet,omitempty"`
			}
			result := map[string]interface{}{}
			if params.Query != "" {
				result["query"] = params.Query
			}
			if len(params.IDs) > 0 {
				result["ids"] = params.IDs
			}
			seedList := make([]seedOut, 0, len(seeds))
			sources := make([]int, 0, len(seeds))
			for i, w := range seeds {
				sources = append(sources, i)
				seedList = append(seedList, seedOut{ID: string(lg.IDs[i]), Title: lg.Facets[i].Title, Weight: round4(w)})
			}
			sort.Slice(seedList, func(a, b int) bool {
				if seedList[a].Weight != seedList[b].Weight {
					return seedList[a].Weight > seedList[b].Weight
				}
				return seedList[a].ID < seedList[b].ID
			})
			result["seeds"] = seedList
			if len(seeds) == 0 {
				result["results"] = []resultOut{}
				result["count"] = 0
				result["note"] = "no seed matched the query"
				out, _ := json.MarshalIndent(result, "", "  ")
				return textResult(string(out)), nil
			}

			scores := graphalgo.PersonalizedPageRank(lg.Graph, seeds, graphContextAlpha, graphContextEps)
			dist, pred := graphalgo.BFS(lg.Graph, sources, graphalgo.Both, 0)
			results := []resultOut{}
			for i, p := range scores {
				if _, seed := seeds[i]; seed || round4(p) <= 0 {
					continue
				}
				r := resultOut{ID: string(lg.IDs[i]), Title: lg.Facets[i].Title, Score: round4(p), Hops: dist[i]}
				if pred[i] >= 0 {
					r.Via = string(lg.IDs[pred[i]])
				}
				results = append(results, r)
			}
			sort.Slice(results, func(a, b int) bool {
				if results[a].Score != results[b].Score {
					return results[a].Score > results[b].Score
				}
				if results[a].Hops != results[b].Hops {
					return results[a].Hops < results[b].Hops
				}
				return results[a].ID < results[b].ID
			})
			if len(results) > limit {
				results = results[:limit]
			}
			if params.Query != "" {
				for i := range results {
					results[i].Snippet = rec.live.snippet(results[i].ID, params.Query, 160)
				}
			}
			result["results"] = results
			result["count"] = len(results)
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- link_suggest ---

func toolLinkSuggest(k *kb.KB) Tool {
	return Tool{
		Name:     "link_suggest",
		ReadOnly: true,
		Description: "Suggests existing concepts a concept should probably link to: those sharing at least two " +
			"neighbours with it and not yet linked either way, ranked so that evidence through hubs counts less.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["id"],
			"properties": {
				"id": {"type": "string", "description": "ConceptID to suggest links for"},
				"limit": {"type": "integer", "description": "Candidates to return (default 5, 1–20)"}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				ID    string `json:"id"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.ID == "" {
				return errorResult("'id' is required"), nil
			}
			limit := clampInt(params.Limit, 5, 1, 20)

			// Hidden concepts removed before computing: see the file comment.
			lg, err := visibleGraph(ctx, k)
			if err != nil {
				return errorResult(fmt.Sprintf("link_suggest: %v", err)), nil
			}
			u, ok := lg.Index[okf.ConceptID(params.ID)]
			if !ok {
				return notFound(params.ID), nil
			}
			type candidateOut struct {
				ID     string   `json:"id"`
				Title  string   `json:"title,omitempty"`
				Score  float64  `json:"score"`
				Common []string `json:"common"`
			}
			candidates := []candidateOut{}
			for _, c := range graphalgo.ResourceAllocation(lg.Graph, u, linkSuggestMinCommon) {
				if status := lg.Facets[c.Node].Status; status == "deprecated" || status == "superseded" {
					continue
				}
				common := make([]string, 0, 5)
				for _, z := range c.Common {
					if len(common) == 5 {
						break
					}
					common = append(common, string(lg.IDs[z]))
				}
				candidates = append(candidates, candidateOut{
					ID: string(lg.IDs[c.Node]), Title: lg.Facets[c.Node].Title, Score: round4(c.Score), Common: common,
				})
				if len(candidates) == limit {
					break
				}
			}
			out, _ := json.MarshalIndent(map[string]interface{}{
				"id":         params.ID,
				"candidates": candidates,
				"count":      len(candidates),
			}, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- graph_path ---

func toolGraphPath(k *kb.KB) Tool {
	return Tool{
		Name:        "graph_path",
		ReadOnly:    true,
		Description: "Shortest chain of links from one concept to another, following links in both directions unless direction is out.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["source", "target"],
			"properties": {
				"source": {"type": "string", "description": "ConceptID to start from"},
				"target": {"type": "string", "description": "ConceptID to reach"},
				"direction": {"type": "string", "enum": ["out", "both"], "description": "out follows links as written; both (default) either way"},
				"max_hops": {"type": "integer", "description": "Longest path considered (default 6, 1–12)"}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Source    string `json:"source"`
				Target    string `json:"target"`
				Direction string `json:"direction"`
				MaxHops   int    `json:"max_hops"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.Source == "" || params.Target == "" {
				return errorResult("'source' and 'target' are required"), nil
			}
			direction := graphalgo.Both
			switch params.Direction {
			case "", "both":
			case "out":
				direction = graphalgo.Out
			default:
				return errorResult(fmt.Sprintf("invalid direction %q: expected out or both", params.Direction)), nil
			}
			maxHops := clampInt(params.MaxHops, 6, 1, 12)

			// Hidden concepts removed before computing: see the file comment.
			lg, err := visibleGraph(ctx, k)
			if err != nil {
				return errorResult(fmt.Sprintf("graph_path: %v", err)), nil
			}
			s, ok := lg.Index[okf.ConceptID(params.Source)]
			if !ok {
				return notFound(params.Source), nil
			}
			t, ok := lg.Index[okf.ConceptID(params.Target)]
			if !ok {
				return notFound(params.Target), nil
			}
			type stepOut struct {
				ID    string `json:"id"`
				Title string `json:"title,omitempty"`
			}
			path := []stepOut{}
			for _, i := range graphalgo.ShortestPath(lg.Graph, s, t, direction, maxHops) {
				path = append(path, stepOut{ID: string(lg.IDs[i]), Title: lg.Facets[i].Title})
			}
			result := map[string]interface{}{
				"source": params.Source,
				"target": params.Target,
				"path":   path,
				"hops":   max(len(path)-1, 0),
			}
			if len(path) == 0 {
				result["note"] = fmt.Sprintf("no path within %d hops", maxHops)
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}
