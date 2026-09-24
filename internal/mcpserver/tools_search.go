package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

// --- search ---

// searchInputSchema is the single input schema of the search tool. Keyword
// search is the only mode (D135): the removed `mode`/`use_semantic` arguments
// are deliberately absent from it and rejected by the handler.
var searchInputSchema = json.RawMessage(`{
	"type": "object",
	"required": ["query"],
	"properties": {
		"query": {
			"type": "string",
			"description": "Search query (one or more keywords)"
		},
		"scope": {
			"type": "string",
			"description": "Restrict results to concepts under this path prefix (e.g. 'maintenance/')"
		},
		"limit": {
			"type": "integer",
			"description": "Maximum number of results (default 20)"
		}
	}
}`)

// snippetMaxChars bounds the excerpt size returned alongside each search hit
// (D70): with limit 20 and this budget, a search response stays well under
// the 5k char target.
const snippetMaxChars = 200

type searchHit struct {
	ID      string  `json:"id"`
	Score   float64 `json:"score"`
	Title   string  `json:"title,omitempty"`
	Snippet string  `json:"snippet,omitempty"`
}

// toolSearch returns the keyword search tool (D135: keyword is the only mode).
// Two paths, chosen by deps:
//
//   - deps.SQLIndex != nil: SQLite FTS5, falling back to the in-memory index
//     when FTS5 fails. Modes reported: keyword_fts5 / keyword on fallback.
//   - otherwise: the in-memory keyword index. Mode reported: keyword.
func toolSearch(k *kb.KB, rec *searchReconciler, deps Deps) Tool {
	description := "Keyword search over KB concepts. Returns matching concept IDs ranked by relevance. All query terms are preferred (AND, then OR fallback)."
	if deps.SQLIndex != nil {
		description = "Keyword search over KB concepts (SQLite FTS5 with substring matching). Returns matching concept IDs ranked by relevance."
	}

	return Tool{
		Name:        "search",
		ReadOnly:    true,
		Description: description,
		InputSchema: searchInputSchema,
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			return handleSearch(ctx, k, rec, deps, args)
		},
	}
}

// handleSearch is the only search handler. It keeps both keyword paths
// verbatim: the plain in-memory one, and the FTS5 one with its native
// snippet() excerpts (D70) and in-memory fallback.
func handleSearch(ctx requestContext, k *kb.KB, rec *searchReconciler, deps Deps, args json.RawMessage) (ToolResult, error) {
	var params struct {
		Query string `json:"query"`
		Scope string `json:"scope"`
		Limit int    `json:"limit"`
		// Mode and UseSemantic are declared only to reject them: semantic and
		// hybrid search are gone (D135), and a stale caller must fail loudly
		// instead of silently receiving keyword results it did not ask for.
		Mode        string `json:"mode"`
		UseSemantic bool   `json:"use_semantic"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return errorResult("invalid params: " + err.Error()), nil
	}
	if params.Mode != "" || params.UseSemantic {
		return errorResult("semantic and hybrid search have been removed: keyword search is the only mode, so 'mode' and 'use_semantic' are no longer accepted arguments"), nil
	}
	if params.Query == "" {
		return errorResult("'query' is required"), nil
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}

	hits, mode := keywordHits(ctx, k, rec, deps, params.Query, params.Scope, limit)
	result := map[string]interface{}{
		"query":   params.Query,
		"mode":    mode,
		"count":   len(hits),
		"results": hits,
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	return textResult(string(out)), nil
}

// keywordHits is search's ranking, shared with graph_context's seeding (D242)
// so the two cannot disagree about which concepts match a query: the backend
// choice, FTS5 with its in-memory fallback, visibility filtering, merge and
// sort. It returns the hits and the mode search reports.
//
// It reconciles the indexes with the files first (D245), so every reader
// sees every change however it was made. That is the only freshness
// mechanism: no write path updates an index directly any more.
func keywordHits(ctx requestContext, k *kb.KB, rec *searchReconciler, deps Deps, query, scope string, limit int) ([]searchHit, string) {
	if _, err := rec.reconcile(); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: search: %v\n", err)
	}
	live := rec.live
	if deps.SQLIndex == nil {
		hits := live.searchFiltered(query, scope, limit, func(id string) bool {
			return Visible(ctx, k, id)
		})

		results := make([]searchHit, 0, len(hits))
		for _, h := range hits {
			results = append(results, searchHit{
				ID:      h.ID,
				Score:   h.Score,
				Title:   live.title(h.ID),
				Snippet: live.snippet(h.ID, query, snippetMaxChars),
			})
		}
		return results, "keyword"
	}

	// Prefer SQLite FTS5, fall back to the in-memory index when FTS5 fails.
	var kwHits []searchHit
	useSQL := true
	sqlHits, err := deps.SQLIndex.SearchFTSFiltered(query, scope, limit, func(id string) bool {
		return Visible(ctx, k, id)
	})
	if err != nil {
		useSQL = false
	} else {
		for _, h := range sqlHits {
			if scope == "" || strings.HasPrefix(h.ID, scope) {
				snippet := h.Snippet
				if snippet == "" {
					snippet = live.snippet(h.ID, query, snippetMaxChars)
				}
				kwHits = append(kwHits, searchHit{
					ID: h.ID, Score: h.Score,
					Title:   live.title(h.ID),
					Snippet: snippet,
				})
			}
		}
	}
	if !useSQL {
		memHits := live.searchFiltered(query, scope, limit, func(id string) bool {
			return Visible(ctx, k, id)
		})
		for _, h := range memHits {
			kwHits = append(kwHits, searchHit{
				ID: h.ID, Score: h.Score,
				Title:   live.title(h.ID),
				Snippet: live.snippet(h.ID, query, snippetMaxChars),
			})
		}
	}

	sort.Slice(kwHits, func(i, j int) bool {
		if kwHits[i].Score != kwHits[j].Score {
			return kwHits[i].Score > kwHits[j].Score
		}
		return kwHits[i].ID < kwHits[j].ID
	})
	if len(kwHits) > limit {
		kwHits = kwHits[:limit]
	}
	if useSQL {
		return kwHits, "keyword_fts5"
	}
	return kwHits, "keyword"
}

// rebuildSQLIndex walks all KB concepts and upserts them into ix's FTS5
// table: `reindex(full: true)`, run by searchReconciler.rebuild. It returns
// the number of concepts upserted.
func rebuildSQLIndex(k *kb.KB, ix *sqlindex.Index) int {
	upserted := 0

	k.WalkConcepts(func(id okf.ConceptID, content string) error {
		conceptID := string(id)
		contentHash := okf.ContentHash(content)

		if err := ix.Upsert(conceptID, contentHash, content); err != nil {
			return nil
		}
		upserted++
		return nil
	})

	return upserted
}

// EnsureSQLIndexFresh reconciles SQLite FTS5 with the KB files at startup.
func EnsureSQLIndexFresh(k *kb.KB, ix *sqlindex.Index) (ReconcileStats, error) {
	return ReconcileIndex(k, nil, ix)
}
