package mcpserver

import (
	"encoding/json"
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
func toolSearch(k *kb.KB, live *liveIndex, deps Deps) Tool {
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
			return handleSearch(ctx, k, live, deps, args)
		},
	}
}

// handleSearch is the only search handler. It keeps both keyword paths
// verbatim: the plain in-memory one, and the FTS5 one with its native
// snippet() excerpts (D70) and in-memory fallback.
func handleSearch(ctx requestContext, k *kb.KB, live *liveIndex, deps Deps, args json.RawMessage) (ToolResult, error) {
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

	if deps.SQLIndex == nil {
		hits := live.get().SearchFiltered(params.Query, params.Scope, limit, func(id string) bool {
			return Visible(ctx, k, id)
		})

		results := make([]searchHit, 0, len(hits))
		for _, h := range hits {
			results = append(results, searchHit{
				ID:      h.ID,
				Score:   h.Score,
				Title:   live.title(h.ID),
				Snippet: live.snippet(h.ID, params.Query, snippetMaxChars),
			})
		}

		result := map[string]interface{}{
			"query":   params.Query,
			"mode":    "keyword",
			"count":   len(results),
			"results": results,
		}
		out, _ := json.MarshalIndent(result, "", "  ")
		return textResult(string(out)), nil
	}

	// Prefer SQLite FTS5, fall back to the in-memory index when FTS5 fails.
	var kwHits []searchHit
	useSQL := true
	sqlHits, err := deps.SQLIndex.SearchFTSFiltered(params.Query, params.Scope, limit, func(id string) bool {
		return Visible(ctx, k, id)
	})
	if err != nil {
		useSQL = false
	} else {
		for _, h := range sqlHits {
			if params.Scope == "" || strings.HasPrefix(h.ID, params.Scope) {
				kwHits = append(kwHits, searchHit{
					ID: h.ID, Score: h.Score,
					Title:   live.title(h.ID),
					Snippet: h.Snippet,
				})
			}
		}
	}
	if !useSQL {
		memHits := live.get().SearchFiltered(params.Query, params.Scope, limit, func(id string) bool {
			return Visible(ctx, k, id)
		})
		for _, h := range memHits {
			kwHits = append(kwHits, searchHit{
				ID: h.ID, Score: h.Score,
				Title:   live.title(h.ID),
				Snippet: live.snippet(h.ID, params.Query, snippetMaxChars),
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
	mode := "keyword"
	if useSQL {
		mode = "keyword_fts5"
	}
	result := map[string]interface{}{
		"query":   params.Query,
		"mode":    mode,
		"count":   len(kwHits),
		"results": kwHits,
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	return textResult(string(out)), nil
}

// --- index_rebuild ---

// indexRebuildInputSchema is shared by all index_rebuild variants.
var indexRebuildInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"scope": {
			"type": "string",
			"description": "Currently unused; always rebuilds the entire index."
		}
	}
}`)

// toolIndexRebuild returns the index_rebuild tool, whose behavior depends on deps:
//
//   - deps.SQLIndex != nil: rebuilds the in-memory keyword index and
//     repopulates SQLite FTS5.
//   - otherwise: rebuilds only the in-memory keyword index.
func toolIndexRebuild(k *kb.KB, live *liveIndex, deps Deps) Tool {
	hasSQL := deps.SQLIndex != nil

	description := "Rebuilds the keyword search index from all KB concepts. The index is derived and disposable; this regenerates it from the .md files."
	if hasSQL {
		description = "Rebuilds the keyword search index from all KB concepts. Uses SQLite persistence when available."
	}

	return Tool{
		Name:        "index_rebuild",
		ReadOnly:    true,
		Description: description,
		InputSchema: indexRebuildInputSchema,
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			newIdx, newMeta := buildIndex(k)
			live.swap(newIdx, newMeta)

			result := map[string]interface{}{
				"status":           "rebuilt",
				"concepts_indexed": newIdx.Count(),
			}

			// SQLite: populate FTS5.
			if hasSQL {
				stats := rebuildSQLIndex(k, deps)
				result["sql_upserted"] = stats.upserted
			}

			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// sqlRebuildStats reports how many concepts were (re)indexed into
// deps.SQLIndex by rebuildSQLIndex.
type sqlRebuildStats struct {
	upserted int
}

// rebuildSQLIndex walks all KB concepts and upserts them into deps.SQLIndex's
// FTS5 table — the same logic used by the index_rebuild tool.
func rebuildSQLIndex(k *kb.KB, deps Deps) sqlRebuildStats {
	var stats sqlRebuildStats

	k.WalkConcepts(func(id okf.ConceptID, content string) error {
		conceptID := string(id)
		contentHash := okf.ContentHash(content)

		if err := deps.SQLIndex.Upsert(conceptID, contentHash, content); err != nil {
			return nil
		}
		stats.upserted++
		return nil
	})

	return stats
}

// EnsureSQLIndexFresh reconciles SQLite FTS5 with the KB files at startup.
func EnsureSQLIndexFresh(k *kb.KB, ix *sqlindex.Index) (ReconcileStats, error) {
	return ReconcileIndex(k, nil, ix)
}
