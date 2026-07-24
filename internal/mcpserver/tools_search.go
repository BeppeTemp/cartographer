package mcpserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/embed"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

// --- search ---

// hybridSearchInputSchema is the InputSchema used whenever semantic search may
// be available (deps.Embedder+deps.VecStore and/or deps.SQLIndex).
var hybridSearchInputSchema = json.RawMessage(`{
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
		},
		"mode": {
			"type": "string",
			"enum": ["keyword", "semantic", "hybrid"],
			"description": "Search mode: keyword (default), semantic, or hybrid (requires Ollama)."
		}
	}
}`)

// keywordOnlySearchInputSchema is used when no semantic search dependency is
// configured at all.
var keywordOnlySearchInputSchema = json.RawMessage(`{
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
		},
		"mode": {
			"type": "string",
			"enum": ["keyword", "semantic", "hybrid"],
			"description": "Search mode: keyword (default), semantic, or hybrid. Semantic and hybrid require Ollama."
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

// toolSearch returns the search tool, choosing its behavior according to deps:
//
//   - deps.SQLIndex != nil: keyword search runs against SQLite FTS5, with a
//     fallback to the in-memory index if FTS5 fails; semantic search (when
//     requested) uses the SQLite embedding cache, falling back to the
//     in-memory vector store. Modes reported: keyword_fts5 / hybrid_fts5
//     (or keyword / hybrid on FTS5 fallback).
//   - otherwise, deps.Embedder+deps.VecStore set: hybrid in-memory search.
//     Modes reported: keyword / hybrid.
//   - otherwise: keyword-only search over the in-memory index.
func toolSearch(k *kb.KB, live *liveIndex, deps Deps) Tool {
	hasEmbed := deps.Embedder != nil && deps.VecStore != nil
	hasSQL := deps.SQLIndex != nil

	description := "Keyword search over KB concepts. Returns matching concept IDs ranked by relevance. All query terms are preferred (AND, then OR fallback)."
	schema := keywordOnlySearchInputSchema
	switch {
	case hasEmbed:
		description = "Keyword, semantic, and hybrid search over KB concepts. Set mode=hybrid to combine keyword and vector results."
		schema = hybridSearchInputSchema
	case hasSQL:
		description = "Keyword search over KB concepts (SQLite FTS5 with substring matching). Returns matching concept IDs ranked by relevance."
	}

	return Tool{
		Name:        "search",
		ReadOnly:    true,
		Description: description,
		InputSchema: schema,
		Handler: func(args json.RawMessage) (ToolResult, error) {
			if !hasEmbed && !hasSQL {
				return handleKeywordOnlySearch(live, args)
			}
			return handleHybridSearch(live, deps, args)
		},
	}
}

// handleKeywordOnlySearch implements the plain keyword-only search behavior
// (no Embedder/VecStore/SQLIndex configured).
func handleKeywordOnlySearch(live *liveIndex, args json.RawMessage) (ToolResult, error) {
	var params struct {
		Query string `json:"query"`
		Scope string `json:"scope"`
		Limit int    `json:"limit"`
		Mode  string `json:"mode"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return errorResult("invalid params: " + err.Error()), nil
	}
	if params.Query == "" {
		return errorResult("'query' is required"), nil
	}
	if params.Mode != "" && params.Mode != "keyword" {
		return errorResult(fmt.Sprintf("mode %q not available: semantic/hybrid require a server started with --ollama", params.Mode)), nil
	}

	hits := live.get().Search(params.Query, params.Scope, params.Limit)

	var results []searchHit
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

// handleHybridSearch implements the search behavior when semantic search may be
// available (deps.Embedder+deps.VecStore and/or deps.SQLIndex). It reproduces
// the previous toolSearchWithEmbed / toolSearchWithSQLIndex behavior exactly,
// depending on which deps are set.
func handleHybridSearch(live *liveIndex, deps Deps, args json.RawMessage) (ToolResult, error) {
	var params struct {
		Query       string `json:"query"`
		Scope       string `json:"scope"`
		Limit       int    `json:"limit"`
		Mode        string `json:"mode"`
		UseSemantic bool   `json:"use_semantic"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return errorResult("invalid params: " + err.Error()), nil
	}
	if params.Query == "" {
		return errorResult("'query' is required"), nil
	}
	if params.Mode != "" && params.Mode != "keyword" && params.Mode != "semantic" && params.Mode != "hybrid" {
		return errorResult(fmt.Sprintf("invalid mode %q; expected keyword, semantic, or hybrid", params.Mode)), nil
	}
	useSemantic := params.UseSemantic || params.Mode == "semantic" || params.Mode == "hybrid"
	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}

	// Determine keyword hits: prefer SQLite FTS5 when available, fall back to
	// the in-memory index (either because deps.SQLIndex is nil, or FTS5 failed).
	// ftsSnippets holds the native FTS5 snippet() excerpt per id, when available
	// (D70); ids without an entry fall back to live.snippet.
	var kwHits []searchHit
	ftsSnippets := map[string]string{}
	useSQL := deps.SQLIndex != nil
	if useSQL {
		sqlHits, err := deps.SQLIndex.SearchFTS(params.Query, params.Scope, limit)
		if err != nil {
			useSQL = false
		} else {
			for _, h := range sqlHits {
				if params.Scope == "" || strings.HasPrefix(h.ID, params.Scope) {
					ftsSnippets[h.ID] = h.Snippet
					kwHits = append(kwHits, searchHit{
						ID: h.ID, Score: h.Score,
						Title:   live.title(h.ID),
						Snippet: h.Snippet,
					})
				}
			}
		}
	}
	if !useSQL {
		memHits := live.get().Search(params.Query, params.Scope, limit)
		for _, h := range memHits {
			kwHits = append(kwHits, searchHit{
				ID: h.ID, Score: h.Score,
				Title:   live.title(h.ID),
				Snippet: live.snippet(h.ID, params.Query, snippetMaxChars),
			})
		}
	}

	kwSet := map[string]float64{}
	for _, h := range kwHits {
		kwSet[h.ID] = h.Score
	}

	if !useSemantic {
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

	// Semantic search.
	if deps.Embedder == nil || deps.VecStore == nil {
		return errorResult("semantic search not configured: start the server with --ollama or CARTOGRAPHER_OLLAMA"), nil
	}
	qVec, err := deps.Embedder.Embed(params.Query)
	if err != nil {
		return errorResult(fmt.Sprintf("search: embed query: %v", err)), nil
	}

	var vecScores map[string]float64
	if useSQL {
		ids, vecs, _, err := deps.SQLIndex.AllEmbeddings()
		if err != nil {
			vecHits := deps.VecStore.Search(qVec, limit*2)
			vecScores = make(map[string]float64, len(vecHits))
			for _, h := range vecHits {
				if params.Scope == "" || strings.HasPrefix(h.ID, params.Scope) {
					vecScores[h.ID] = h.Similarity
				}
			}
		} else {
			vecScores = make(map[string]float64, len(ids))
			for i, id := range ids {
				if params.Scope == "" || strings.HasPrefix(id, params.Scope) {
					vecScores[id] = embed.CosineSimilarity(qVec, vecs[i])
				}
			}
		}
	} else {
		vecHits := deps.VecStore.Search(qVec, limit*2)
		vecScores = make(map[string]float64, len(vecHits))
		for _, h := range vecHits {
			if params.Scope == "" || strings.HasPrefix(h.ID, params.Scope) {
				vecScores[h.ID] = h.Similarity
			}
		}
	}

	merged := make(map[string]float64)
	for id, s := range kwSet {
		merged[id] += s
	}
	for id, s := range vecScores {
		merged[id] += s
	}

	results := make([]searchHit, 0, len(merged))
	for id, score := range merged {
		snippet := ftsSnippets[id]
		if snippet == "" {
			snippet = live.snippet(id, params.Query, snippetMaxChars)
		}
		results = append(results, searchHit{
			ID: id, Score: score,
			Title:   live.title(id),
			Snippet: snippet,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})
	if len(results) > limit {
		results = results[:limit]
	}

	mode := "hybrid"
	if useSQL {
		mode = "hybrid_fts5"
	}
	result := map[string]interface{}{
		"query":   params.Query,
		"mode":    mode,
		"count":   len(results),
		"results": results,
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
//   - deps.SQLIndex != nil: rebuilds the in-memory keyword index and, when
//     deps.Embedder is set, the in-memory vector store (backward compat); also
//     populates SQLite FTS5 and the embedding cache (skipping fresh entries by
//     content-hash).
//   - otherwise, deps.Embedder+deps.VecStore set: rebuilds the in-memory
//     keyword index and re-embeds all concepts into the in-memory vector store.
//   - otherwise: rebuilds only the in-memory keyword index.
func toolIndexRebuild(k *kb.KB, live *liveIndex, deps Deps) Tool {
	hasEmbed := deps.Embedder != nil && deps.VecStore != nil
	hasSQL := deps.SQLIndex != nil

	description := "Rebuilds the keyword search index from all KB concepts. The index is derived and disposable; this regenerates it from the .md files."
	switch {
	case hasSQL:
		description = "Rebuilds the keyword search index and vector embedding index from all KB concepts. Uses SQLite persistence when available."
	case hasEmbed:
		description = "Rebuilds both the keyword search index and the vector embedding index from all KB concepts."
	}

	return Tool{
		Name:        "index_rebuild",
		ReadOnly:    true,
		Description: description,
		InputSchema: indexRebuildInputSchema,
		Handler: func(args json.RawMessage) (ToolResult, error) {
			newIdx, newMeta := buildIndex(k)
			live.swap(newIdx, newMeta)

			result := map[string]interface{}{
				"status":           "rebuilt",
				"concepts_indexed": newIdx.Count(),
			}

			// Rebuild in-memory vector store, when an Embedder is configured.
			if hasEmbed {
				embedErrors := 0
				conceptsEmbedded := 0
				k.WalkConcepts(func(id okf.ConceptID, content string) error {
					vec, err := deps.Embedder.Embed(content)
					if err != nil {
						embedErrors++
						return nil
					}
					deps.VecStore.Add(string(id), vec)
					conceptsEmbedded++
					return nil
				})
				result["concepts_embedded"] = conceptsEmbedded
				result["embed_errors"] = embedErrors
			}

			// SQLite: populate FTS5 and embedding cache.
			if hasSQL {
				stats := rebuildSQLIndex(k, deps, true)
				result["sql_upserted"] = stats.upserted
				result["sql_embedded"] = stats.embedded
				result["sql_embed_errors"] = stats.embedErrors
				result["sql_embed_cache_hits"] = stats.embedCacheHits
			}

			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// sqlRebuildStats reports how many concepts were (re)indexed into
// deps.SQLIndex by rebuildSQLIndex, and the embedding-cache outcome when
// embedding was attempted.
type sqlRebuildStats struct {
	upserted       int
	embedded       int
	embedErrors    int
	embedCacheHits int
}

// rebuildSQLIndex walks all KB concepts and upserts them into deps.SQLIndex's
// FTS5 table. When withEmbed is true and deps.Embedder is set, it also
// (re)populates the embedding cache, skipping fresh entries by content-hash —
// this is the same logic used by the index_rebuild tool. Callers that must
// avoid depending on an embedding backend (e.g. best-effort startup recovery,
// where Ollama may be slow or unreachable) pass withEmbed=false to populate
// FTS5 only.
func rebuildSQLIndex(k *kb.KB, deps Deps, withEmbed bool) sqlRebuildStats {
	var stats sqlRebuildStats
	hasEmbed := withEmbed && deps.Embedder != nil

	k.WalkConcepts(func(id okf.ConceptID, content string) error {
		conceptID := string(id)
		contentHash := okf.ContentHash(content)

		if err := deps.SQLIndex.Upsert(conceptID, contentHash, content); err != nil {
			return nil
		}
		stats.upserted++

		if hasEmbed {
			fresh, ferr := deps.SQLIndex.EmbeddingFresh(conceptID, contentHash)
			if ferr != nil {
				fresh = false
			}
			if fresh {
				stats.embedCacheHits++
				return nil
			}

			vec, verr := deps.Embedder.Embed(content)
			if verr != nil {
				stats.embedErrors++
				return nil
			}
			if uerr := deps.SQLIndex.UpsertEmbedding(conceptID, contentHash, deps.Embedder.Model(), vec); uerr != nil {
				return nil
			}
			stats.embedded++
		}
		return nil
	})

	return stats
}

// EnsureSQLIndexFresh populates ix's FTS5 table from k's concepts when ix
// looks empty (Count()==0). This recovers from a fresh git clone: since
// <kb>/.cartographer/index.db is gitignored, a pod restart that re-clones the
// KB starts with an empty SQLite index even though every concept is already
// on disk — until now, only a manual index_rebuild call would fix it.
// Deliberately skips embeddings (Ollama may be absent/slow at boot); the
// embedding cache is populated lazily by index_rebuild or search. Returns the
// number of concepts (re)indexed (0 if ix was already non-empty), and a
// non-nil error only if the emptiness check itself failed — callers should
// treat that as best-effort too and not fail startup on it.
func EnsureSQLIndexFresh(k *kb.KB, ix *sqlindex.Index) (int, error) {
	n, err := ix.Count()
	if err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, nil
	}
	stats := rebuildSQLIndex(k, Deps{SQLIndex: ix}, false)
	return stats.upserted, nil
}
