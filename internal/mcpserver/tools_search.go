package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/search"
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
		"query": {"type": "string"},
		"scope": {"type": "string"},
		"limit": {"type": "integer"},
		"record_miss": {"type": "boolean"},
		"include_archived": {"type": "boolean"},
		"related": {"type": "integer"}
	}
}`)

// snippetMaxChars bounds the excerpt size returned alongside each search hit
// (D70): with the default limit and this budget, a search response stays well
// under the 2.5 KB average TestSearchDefaultAnswerBudget enforces.
const snippetMaxChars = 200

// searchDefaultLimit is the number of hits when the caller passes no limit
// (D1185, was 20 in D70). An explicit limit is honoured as it is.
const searchDefaultLimit = 8

// searchCentralityBeta caps the centrality prior at +20% (D251): enough to
// reorder near-ties, too little to lift a weak match over a strong one.
// maxPriorWindow caps the candidates the prior re-ranks.
const (
	searchCentralityBeta = 0.2
	maxPriorWindow       = 100
)

type searchHit struct {
	ID      string  `json:"id"`
	Score   float64 `json:"score"`
	Title   string  `json:"title,omitempty"`
	Snippet string  `json:"snippet,omitempty"`
	// Archived marks a hit that is an archived concept (D322): only the
	// archived fallback and include_archived return those.
	Archived bool `json:"archived,omitempty"`
	// Partial marks a hit that matched only some query terms (D343): it comes
	// from the OR pass and ranks after every full match.
	Partial bool `json:"partial,omitempty"`
	// Section is the "##" heading that matched best, exactly as concept_read's
	// section argument accepts it; the snippet is then that section's excerpt
	// (D1207). Omitted when naming a section saves nothing (sectionFor).
	Section string `json:"section,omitempty"`
	// Read is "whole" for a page that must be read whole: a journal entry, or a
	// type its map lists in read_whole (D1207). Such a hit never has a Section.
	Read string `json:"read,omitempty"`
	// Bytes is the concept file's size (D301, D779): what reading it whole
	// costs, so an agent can choose outline or section before paying it.
	Bytes int64 `json:"bytes"`
}

// searchHitLess orders hits: full matches before partial ones whatever the
// score (D343), then score, then id.
func searchHitLess(a, b searchHit) bool {
	if a.Partial != b.Partial {
		return !a.Partial
	}
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.ID < b.ID
}

// toolSearch returns the keyword search tool (D135: keyword is the only mode).
// Two paths, chosen by deps:
//
//   - deps.SQLIndex != nil: SQLite FTS5, falling back to the in-memory index
//     when FTS5 fails. Modes reported: keyword_fts5 / keyword on fallback.
//   - otherwise: the in-memory keyword index. Mode reported: keyword.
func toolSearch(k *kb.KB, rec *searchReconciler, misses *searchMissLog, reads *readAccessLog, deps Deps) Tool {
	description := "Keyword search over concepts; returns IDs, best first (all terms preferred, then OR fallback)."
	if deps.SQLIndex != nil {
		description = "Keyword search over concepts (SQLite FTS5, substring matching); returns IDs, best first."
	}
	description += " Default 8 hits; related: N adds the neighbourhood of the hits. A hit's section (named only on pages over 2048 bytes) goes to concept_read section; read: \"whole\" means read it whole. Glossary aliases match too (expanded_to). scope is a map name or id prefix, never a KB name."

	return Tool{
		Name:        "search",
		ReadOnly:    true,
		Description: description,
		InputSchema: searchInputSchema,
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			return handleSearch(ctx, k, rec, misses, reads, deps, args)
		},
	}
}

// handleSearch is the only search handler. It keeps both keyword paths
// verbatim: the plain in-memory one, and the FTS5 one with its native
// snippet() excerpts (D70) and in-memory fallback.
func handleSearch(ctx requestContext, k *kb.KB, rec *searchReconciler, misses *searchMissLog, reads *readAccessLog, deps Deps, args json.RawMessage) (ToolResult, error) {
	var params struct {
		Query string `json:"query"`
		Scope string `json:"scope"`
		Limit int    `json:"limit"`
		// RecordMiss false keeps a verification probe out of the miss log
		// (D319); absent means true.
		RecordMiss *bool `json:"record_miss"`
		// IncludeArchived (D322): absent hides archived concepts unless
		// nothing live matches, true returns both, false never falls back.
		IncludeArchived *bool `json:"include_archived"`
		// Related sizes the related block (D781): absent or 0 is off (D1185).
		Related *int `json:"related"`
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
		limit = searchDefaultLimit
	}

	skipArchived := params.IncludeArchived == nil || !*params.IncludeArchived
	// A narrowed principal's visibility is decided concept by concept (a map
	// descriptor and a stat each): the ranking's centrality prior decides it
	// for the whole graph, and the related block reads that answer back.
	if !WholeVisible(ctx, k, false) {
		ctx = withVisibilityMemo(ctx, k)
	}
	hits, mode, expandedTo := expandedKeywordHits(ctx, k, rec, deps, params.Query, params.Scope, limit, skipArchived)
	// Archived fallback (D322): when nothing live matches and the caller did
	// not say include_archived, search again with archived concepts in, so
	// "the KB knows nothing" is never concluded from pages that are only
	// archived. An explicit false disables it (verification searches).
	archivedFallback := false
	if len(hits) == 0 && params.IncludeArchived == nil {
		if ah, _, ex := expandedKeywordHits(ctx, k, rec, deps, params.Query, params.Scope, limit, false); len(ah) > 0 {
			hits, expandedTo, archivedFallback = ah, ex, true
		}
	}
	sections := newSectionResolver(k, rec, deps, mode, params.Query, expandedTo)
	for i := range hits {
		f := k.ConceptFacets(okf.ConceptID(hits[i].ID))
		hits[i].Archived = f.Status == kb.StatusArchived
		hits[i].Bytes = f.Bytes
		sections.annotate(&hits[i], f.Type)
	}
	// A scope no visible concept starts with is a wrong argument, not an empty
	// answer (D779): refused before the miss log, which would record a gap the
	// KB does not have.
	if len(hits) == 0 && params.Scope != "" {
		if msg := unmatchedScope(ctx, k, params.Scope); msg != "" {
			return errorResult(msg), nil
		}
	}
	// A miss is recorded only when every glossary variant came back empty
	// (D276), and only for a principal who sees the whole KB (D247).
	// For a narrowed token zero hits may only mean "hidden from you" — not a
	// gap — and recording it would show that user's queries to every
	// whole-KB reader of kb_status.
	// record_miss: false is a caller's probe, not a gap (D319).
	if len(hits) == 0 && (params.RecordMiss == nil || *params.RecordMiss) && WholeVisible(ctx, k, false) {
		misses.record(params.Query)
	}
	// A hit is surfaced, not read (D778): most hits are never opened, so only
	// concept_read stamps a read.
	if len(hits) > 0 {
		ids := make([]string, len(hits))
		for i, h := range hits {
			ids[i] = h.ID
		}
		reads.recordSurfaced(ids...)
	}
	result := map[string]interface{}{
		"query":   params.Query,
		"mode":    mode,
		"count":   len(hits),
		"results": hits,
	}
	if len(expandedTo) > 0 {
		result["expanded_to"] = expandedTo
	}
	if !archivedFallback {
		if block := relatedBlock(ctx, k, hits, relatedLimit(params.Related)); len(block) > 0 {
			result["related"] = block
		}
	}
	var notes []string
	if archivedFallback {
		result["archived_fallback"] = true
		notes = append(notes, "no live concept matches; these are archived entries")
	}
	for _, h := range hits {
		if h.Partial {
			result["or_fallback"] = true
			notes = append(notes, "some terms matched only partially")
			break
		}
	}
	if len(notes) > 0 {
		result["note"] = strings.Join(notes, " ")
	}
	return jsonResult(result), nil
}

// Thresholds under which naming a section saves nothing (D1207): a body this
// small is cheaper read whole, and so is one the best section nearly fills.
const (
	sectionMinBodyBytes = 2048
	sectionMaxBodyPct   = 80
)

// sectionResolver names, for the hits of one search response, the section that
// matched and whether the page must be read whole. It runs after the ranking
// and only for the ids handleSearch returns, so graph_context's seeding and the
// write-time lookups (which call keywordHits) never pay for it, and a hidden
// concept, already filtered out of the hits, is never looked up (D1207).
type sectionResolver struct {
	k     *kb.KB
	rec   *searchReconciler
	deps  Deps
	fts   bool
	query string // the original query plus the glossary variants, OR-ed
	whole map[string]readWholeSpec
}

// readWholeSpec is one map's answer to "which pages are read whole".
type readWholeSpec struct {
	journal bool
	types   map[string]bool
}

func newSectionResolver(k *kb.KB, rec *searchReconciler, deps Deps, mode, query string, expandedTo []string) *sectionResolver {
	return &sectionResolver{
		k: k, rec: rec, deps: deps,
		fts:   mode == "keyword_fts5" && deps.SQLIndex != nil,
		query: strings.Join(append([]string{query}, expandedTo...), " "),
		whole: map[string]readWholeSpec{},
	}
}

// readsWhole reports whether the concept must be read whole: it lives in a
// journal, or its type is listed in its map's read_whole (D1207). An id with no
// map, or a map whose descriptor cannot be read, answers false. Each map's
// contract is read once per response.
func (r *sectionResolver) readsWhole(id, typ string) bool {
	mapName, _, nested := strings.Cut(id, "/")
	if !nested {
		return false
	}
	spec, ok := r.whole[mapName]
	if !ok {
		if c, err := r.k.ReadMapContract(mapName); err == nil {
			spec.journal = c.Kind == "journal"
			spec.types = map[string]bool{}
			for _, t := range c.ReadWhole {
				spec.types[t] = true
			}
		}
		r.whole[mapName] = spec
	}
	return spec.journal || (typ != "" && spec.types[typ])
}

// annotate sets h.Read, or h.Section and the section's excerpt as h.Snippet.
func (r *sectionResolver) annotate(h *searchHit, typ string) {
	if r.readsWhole(h.ID, typ) {
		h.Read = "whole"
		return
	}
	body := r.rec.live.body(h.ID)
	if len(body) < sectionMinBodyBytes {
		return
	}
	var heading, snippet string
	found := false
	if r.fts {
		var err error
		heading, snippet, found, err = r.deps.SQLIndex.BestSection(h.ID, r.query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cartographer: search section: %v\n", err)
			found = false
		} else if !found {
			return
		}
	}
	if !found {
		sec, ok := search.BestSection(body, r.query)
		if !ok {
			return
		}
		heading, snippet, found = sec.Heading, "", true
	}
	// The heading is the contract with concept_read: it must resolve there.
	text, ok := okf.ExtractSection(body, heading)
	if !ok || len(text)*100 >= sectionMaxBodyPct*len(body) {
		return
	}
	if snippet == "" {
		snippet = extractSnippet(text, r.query, snippetMaxChars)
	}
	h.Section, h.Snippet = heading, snippet
}

// scopePrefixesShown caps the maps and journals an unmatched-scope error
// lists.
const scopePrefixesShown = 30

// unmatchedScope returns the error for a scope that no concept the caller can
// see starts with, and "" when one does. Only visible concepts are compared and
// listed, so the answer says nothing about hidden ones. It costs a walk of the
// concept ids, paid only by a search that already came back empty.
func unmatchedScope(ctx requestContext, k *kb.KB, scope string) string {
	links, err := k.Links()
	if err != nil {
		return ""
	}
	whole := WholeVisible(ctx, k, false)
	ids := make([]string, 0, len(links.Exists))
	for id := range links.Exists {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	prefixes := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if !whole && !Visible(ctx, k, id) {
			continue
		}
		if strings.HasPrefix(id, scope) {
			return ""
		}
		// Trap: listed bare, without the slash. A narrowed token's scope is
		// authorized as a map name (policy.go), so "ops/" would be refused
		// where "ops" is allowed; the bare name is a prefix that works for all.
		top, _, nested := strings.Cut(id, "/")
		if !nested || seen[top] {
			continue
		}
		seen[top] = true
		prefixes = append(prefixes, top)
	}
	list := "none"
	if len(prefixes) > scopePrefixesShown {
		list = strings.Join(prefixes[:scopePrefixesShown], ", ") + ", …"
	} else if len(prefixes) > 0 {
		list = strings.Join(prefixes, ", ")
	}
	return fmt.Sprintf("scope %q matches no concept you can see in this KB: scope is a literal id prefix, usually a map name, not a KB name (the kb argument selects the KB). Maps and journals here: %s. Omit scope to search the whole KB.", scope, list)
}

// glossaryMaxVariants caps the queries one search runs through the glossary
// (D276), the original included: each variant is a full keywordHits pass.
const glossaryMaxVariants = 8

// expandedKeywordHits is keywordHits through the KB's glossary (D276): the
// query, then each variant the glossary derives from it, run with the same
// scope and limit and merged by concept ID keeping the best score. It returns
// the merged hits, the mode of the original query's pass, and the variants
// other than the original (nil when the glossary did not expand the query).
// A missing, unreadable or unparseable glossary expands nothing, so a KB
// without one searches exactly as before.
//
// Trap: on the FTS5 backend a term shorter than three characters matches
// nothing on its own (the trigram tokenizer, sqlindex.ftsTokens), so a query
// that is only a two-letter alias finds pages through its longer variants
// but never pages that use only the two-letter form.
func expandedKeywordHits(ctx requestContext, k *kb.KB, rec *searchReconciler, deps Deps, query, scope string, limit int, skipArchived bool) ([]searchHit, string, []string) {
	var variants []string
	if st, err := k.ReadGlossary(); err == nil {
		variants = st.Glossary.Variants(search.Fold(query), glossaryMaxVariants)
	}
	if len(variants) <= 1 {
		hits, mode := keywordHits(ctx, k, rec, deps, query, scope, limit, skipArchived)
		return hits, mode, nil
	}
	// variants[0] is the folded query: run the query as typed instead, so the
	// original pass is byte-for-byte today's search.
	hits, mode := keywordHits(ctx, k, rec, deps, query, scope, limit, skipArchived)
	best := map[string]searchHit{}
	for _, h := range hits {
		best[h.ID] = h
	}
	for _, v := range variants[1:] {
		vh, _ := keywordHits(ctx, k, rec, deps, v, scope, limit, skipArchived)
		for _, h := range vh {
			if cur, ok := best[h.ID]; !ok || (cur.Partial && !h.Partial) || (cur.Partial == h.Partial && h.Score > cur.Score) {
				best[h.ID] = h
			}
		}
	}
	merged := make([]searchHit, 0, len(best))
	for _, h := range best {
		merged = append(merged, h)
	}
	sort.Slice(merged, func(i, j int) bool { return searchHitLess(merged[i], merged[j]) })
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged, mode, variants[1:]
}

// keywordHits is search's ranking, shared with graph_context's seeding (D242)
// so the two cannot disagree about which concepts match a query: the backend
// choice, FTS5 with its in-memory fallback, visibility filtering, merge and
// sort. It returns the hits and the mode search reports.
//
// It reconciles the indexes with the files first (D245), so every reader
// sees every change however it was made. That is the only freshness
// mechanism: no write path updates an index directly any more.
func keywordHits(ctx requestContext, k *kb.KB, rec *searchReconciler, deps Deps, query, scope string, limit int, skipArchived bool) ([]searchHit, string) {
	if _, err := rec.reconcile(); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: search: %v\n", err)
	}
	return keywordHitsReconciled(ctx, k, rec, deps, query, scope, limit, skipArchived)
}

// keywordHitsReconciled is keywordHits for a caller that has just reconciled
// and runs several queries in a row (the write-time repeated_fact lookups,
// D351): the reconcile walks every file, so it is paid once per call.
func keywordHitsReconciled(ctx requestContext, k *kb.KB, rec *searchReconciler, deps Deps, query, scope string, limit int, skipArchived bool) ([]searchHit, string) {
	live := rec.live
	visible := visiblePredicate(ctx, k)
	// skipArchived (D322) drops archived concepts inside the backend's own
	// filter, before its cut to the window, so the page is still full of live
	// hits (a post-filter would need an over-fetch and still be short). The
	// centrality prior below keeps the visibility-only predicate: an archived
	// concept still links, it just is not offered.
	pick := visible
	if skipArchived {
		pick = func(id string) bool {
			return visible(id) && k.ConceptFacets(okf.ConceptID(id)).Status != kb.StatusArchived
		}
	}

	// Trap: fetch a window larger than the page (D251). The centrality prior
	// re-ranks after the backend's cut, so with only `limit` candidates it
	// could reorder the page but never let a central concept just below it in.
	window := min(3*limit, maxPriorWindow)
	window = max(window, limit)

	var kwHits []searchHit
	mode := "keyword"
	useMem := deps.SQLIndex == nil
	if !useMem {
		// Prefer SQLite FTS5, fall back to the in-memory index when FTS5 fails.
		sqlHits, err := deps.SQLIndex.SearchFTSFiltered(query, scope, window, pick)
		if err != nil {
			useMem = true
		} else {
			mode = "keyword_fts5"
			for _, h := range sqlHits {
				if scope == "" || strings.HasPrefix(h.ID, scope) {
					kwHits = append(kwHits, searchHit{ID: h.ID, Score: h.Score, Snippet: h.Snippet, Partial: h.Partial})
				}
			}
		}
	}
	if useMem {
		for _, h := range live.searchFiltered(query, scope, window, pick) {
			kwHits = append(kwHits, searchHit{ID: h.ID, Score: h.Score, Partial: h.Partial})
		}
	}

	// Centrality prior (D251): final = text × (1 + β·p), p the PageRank
	// percentile on the caller's visible graph, so a hidden hub cannot move
	// the order. A concept nothing links to has p = 0 and keeps its score.
	var include func(string) bool
	if !WholeVisible(ctx, k, false) {
		include = visible
	}
	if pct, err := k.PageRankPercentiles(include); err == nil {
		for i := range kwHits {
			kwHits[i].Score *= 1 + searchCentralityBeta*pct[okf.ConceptID(kwHits[i].ID)]
		}
	} else {
		fmt.Fprintf(os.Stderr, "cartographer: search centrality: %v\n", err)
	}

	// Trap: the partial-last order must be applied after the prior, which
	// multiplies scores and could otherwise lift an OR-only hit over a full one.
	sort.Slice(kwHits, func(i, j int) bool { return searchHitLess(kwHits[i], kwHits[j]) })
	if len(kwHits) > limit {
		kwHits = kwHits[:limit]
	}
	for i := range kwHits {
		kwHits[i].Title = live.title(kwHits[i].ID)
		if kwHits[i].Snippet == "" {
			kwHits[i].Snippet = live.snippet(kwHits[i].ID, query, snippetMaxChars)
		}
	}
	return kwHits, mode
}

// rebuildSQLIndex walks all KB concepts and upserts them into ix's FTS5
// table: `reindex(full: true)`, run by searchReconciler.rebuild. It returns
// the number of concepts upserted.
func rebuildSQLIndex(k *kb.KB, ix *sqlindex.Index) int {
	upserted := 0

	k.WalkConcepts(func(id okf.ConceptID, content string) error {
		conceptID := string(id)
		contentHash := okf.ContentHash(content)

		assets, sig, _ := k.IndexableAssetText(id) // no assets (or not expanded): empty
		if err := ix.Upsert(conceptID, assetContentHash(contentHash, sig), content, assets); err != nil {
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
