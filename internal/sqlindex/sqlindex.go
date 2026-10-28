// Package sqlindex implements a persistent SQLite-backed keyword index.
//
// The schema:
//
//	concepts(id TEXT PRIMARY KEY, content_hash TEXT NOT NULL, body TEXT NOT NULL)
//	concepts_fts(id UNINDEXED, title, meta, body) — FTS5, trigram tokenizer
//	with diacritics removed, ranked by bm25 weights 5 / 2 / 1 (D246)
//	sections_fts(id UNINDEXED, heading UNINDEXED, title, body) — FTS5, same
//	tokenizer: one row per "##" section of a concept's body, bm25 weights 3 / 1
//	(D1207). It never ranks pages: it only answers which section of an already
//	ranked page matched best (BestSection).
//
// The schema version lives in PRAGMA user_version (schemaVersion). Opening an
// older database recreates concepts_fts and sections_fts and empties concepts: the index is
// disposable, and the next reconciliation reindexes every concept.
//
// A database created before D135 may still carry an embeddings table: it is
// never read nor written, and opening such a file is not an error.
package sqlindex

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/search"

	_ "modernc.org/sqlite" // register "sqlite" driver (pure-Go, no CGo)
)

// Hit is a single search result.
type Hit struct {
	ID      string
	Score   float64
	Snippet string // excerpt around the match, from FTS5's native snippet()
	// Partial marks an OR-pass hit that matched only some terms (D343); it is
	// always listed after every full match.
	Partial bool
}

// Index is a persistent SQLite-backed search and embedding index.
type Index struct {
	db   *sql.DB
	path string
}

// Open opens a SQLite index at dbPath. It creates the schema if it does not
// exist and enables WAL mode. If FTS5 support is missing, Open returns an
// error — the caller should fall back to the in-memory path.
func Open(dbPath string) (*Index, error) {
	// Ensure the parent directory exists: the SQLite driver cannot create the
	// database (or its WAL sidecar) inside a missing directory. The KB's
	// .cartographer/ dir is created lazily elsewhere, so do not rely on it here.
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("sqlindex: mkdir %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("sqlindex: open %s: %w", dbPath, err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlindex: wal: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := createSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Index{db: db, path: dbPath}, nil
}

// schemaVersion is the concepts_fts layout this package writes. Version 2
// (D246) split the single body column into title/meta/body and folded
// diacritics; version 3 (D277) added the assets column; version 4 (D1207)
// added sections_fts; version 1 is every database created before it
// (user_version 0).
const schemaVersion = 4

// migrate brings an older database to schemaVersion by dropping what cannot
// be converted in place. concepts is emptied with concepts_fts so the hashes
// no longer claim rows that are gone: the next reconciliation sees every
// concept as new and reindexes it.
func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("sqlindex: read user_version: %w", err)
	}
	if version >= schemaVersion {
		return nil
	}
	// Trap: every FTS table must be dropped here AND created in createSchema,
	// or a database migrated from an older version keeps a stale layout (or
	// none) and concepts, emptied below, no longer matches it.
	for _, table := range []string{"concepts_fts", "sections_fts"} {
		if _, err := db.Exec(`DROP TABLE IF EXISTS ` + table); err != nil {
			return fmt.Errorf("sqlindex: drop v%d %s: %w", version, table, err)
		}
	}
	var hasConcepts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='concepts'`).Scan(&hasConcepts); err != nil {
		return fmt.Errorf("sqlindex: inspect schema: %w", err)
	}
	if hasConcepts > 0 {
		if _, err := db.Exec(`DELETE FROM concepts`); err != nil {
			return fmt.Errorf("sqlindex: clear v%d concepts: %w", version, err)
		}
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return fmt.Errorf("sqlindex: set user_version: %w", err)
	}
	return nil
}

func createSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS concepts (
			id TEXT PRIMARY KEY,
			content_hash TEXT NOT NULL,
			body TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("sqlindex: create concepts: %w", err)
	}

	_, err = db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS concepts_fts USING fts5(
			id UNINDEXED,
			title,
			meta,
			body,
			assets,
			tokenize='trigram remove_diacritics 1'
		)
	`)
	if err != nil {
		return fmt.Errorf("sqlindex: fts5 not available (fallback to in-memory): %w", err)
	}

	// Trap: a new FTS table is also dropped in migrate and cleared in Upsert
	// and Delete.
	_, err = db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS sections_fts USING fts5(
			id UNINDEXED,
			heading UNINDEXED,
			title,
			body,
			tokenize='trigram remove_diacritics 1'
		)
	`)
	if err != nil {
		return fmt.Errorf("sqlindex: create sections_fts: %w", err)
	}

	return nil
}

// Close closes the underlying database connection.
func (ix *Index) Close() error {
	return ix.db.Close()
}

// Upsert inserts or updates a concept's content in both the concepts table and
// the FTS5 index. body is the raw file content; it is split into the title,
// meta and body columns here, the same way the in-memory index weights it.
// assetsText is the searchable text of the concept's assets (D277), empty for
// a concept that has none; contentHash should cover it (see the reconciler).
func (ix *Index) Upsert(id, contentHash, body, assetsText string) error {
	_, err := ix.db.Exec(
		`INSERT INTO concepts(id, content_hash, body) VALUES(?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET content_hash=excluded.content_hash, body=excluded.body`,
		id, contentHash, body,
	)
	if err != nil {
		return fmt.Errorf("sqlindex: upsert concepts: %w", err)
	}

	if _, err := ix.db.Exec(`DELETE FROM concepts_fts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlindex: delete fts: %w", err)
	}
	f := search.SplitFields(body)
	if _, err := ix.db.Exec(`INSERT INTO concepts_fts(id, title, meta, body, assets) VALUES(?, ?, ?, ?, ?)`, id, f.Title, f.Meta, f.Body, assetsText); err != nil {
		return fmt.Errorf("sqlindex: insert fts: %w", err)
	}

	if _, err := ix.db.Exec(`DELETE FROM sections_fts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlindex: delete sections: %w", err)
	}
	for _, sec := range okf.Level2Sections(f.Body) {
		if _, err := ix.db.Exec(`INSERT INTO sections_fts(id, heading, title, body) VALUES(?, ?, ?, ?)`, id, sec.Heading, sec.Title, sec.Text); err != nil {
			return fmt.Errorf("sqlindex: insert section: %w", err)
		}
	}

	return nil
}

// Delete removes a concept's content from both the concepts table and the
// FTS5 index (used by concept_delete).
func (ix *Index) Delete(id string) error {
	if _, err := ix.db.Exec(`DELETE FROM concepts_fts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlindex: delete fts: %w", err)
	}
	if _, err := ix.db.Exec(`DELETE FROM sections_fts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlindex: delete sections: %w", err)
	}
	if _, err := ix.db.Exec(`DELETE FROM concepts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlindex: delete concepts: %w", err)
	}
	return nil
}

// AllHashes returns the persisted content hash for every indexed concept.
// Callers use it to reconcile the derived index with the KB files on disk.
func (ix *Index) AllHashes() (map[string]string, error) {
	rows, err := ix.db.Query(`SELECT id, content_hash FROM concepts`)
	if err != nil {
		return nil, fmt.Errorf("sqlindex: all hashes: %w", err)
	}
	defer rows.Close()

	hashes := make(map[string]string)
	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			return nil, fmt.Errorf("sqlindex: scan hash: %w", err)
		}
		hashes[id] = hash
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlindex: iterate hashes: %w", err)
	}
	return hashes, nil
}

// sanitizeFTSQuery wraps the query string for safe FTS5 trigram MATCH.
// The trigram tokenizer with a quoted string performs a substring search.
// Any double quotes in the input are stripped to avoid syntax errors.
func sanitizeFTSQuery(q string) string {
	clean := strings.ReplaceAll(q, "\"", "")
	tokens := ftsTokens(clean)
	if len(tokens) == 0 {
		return `"` + clean + `"`
	}
	return `"` + strings.Join(tokens, `" AND "`) + `"`
}

// likePrefixPattern builds the LIKE pattern matching exactly the IDs that
// start with scope. SQL LIKE treats "%" and "_" as wildcards, and concept IDs
// are file paths, where "_" is ordinary and common ("maps/cert_rotation"): an
// unescaped scope of "maps_a/" also matches "mapsXa/", so the SQL backend
// disagreed with the in-memory index, which uses strings.HasPrefix. The
// mismatch does not leak data — the search tool re-filters by prefix — but the
// stray rows are counted against the caller's LIMIT before that filter runs,
// silently shortening the result page. The pattern is paired with an explicit
// ESCAPE clause at every call site.
func likePrefixPattern(scope string) string {
	r := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	return r.Replace(scope) + "%"
}

// ftsTokens returns query terms accepted by FTS5's trigram tokenizer.
func ftsTokens(q string) []string {
	var tokens []string
	for _, token := range strings.Fields(q) {
		if utf8.RuneCountInString(token) >= 3 {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

// ftsSnippetTokens bounds the width of the excerpt returned by FTS5's
// snippet() (3rd arg): with the trigram tokenizer each "token" advances by
// roughly one character, so this approximates the ~200 char budget shared
// with the in-memory snippet extraction (D70).
const ftsSnippetTokens = 200

// ftsSearchBatch is deliberately bounded: SearchFTSFiltered reads additional
// ranked pages only when hidden candidates leave the requested result page
// short.
const ftsSearchBatch = 64

// SearchFTS performs a keyword search via FTS5 trigram tokenizer.
// If scope is non-empty, only concepts whose id starts with scope are returned.
// Results are sorted by BM25 relevance (higher score = better match). Each hit
// carries a Snippet excerpt produced by FTS5's native snippet() function.
func (ix *Index) SearchFTS(query, scope string, limit int) ([]Hit, error) {
	return ix.SearchFTSFiltered(query, scope, limit, nil)
}

// SearchFTSFiltered scans ranked FTS rows in bounded pages until it collects
// limit allowed hits or reaches EOF. The predicate is applied before a hit is
// returned, preventing IDs, snippets, and result counts of hidden concepts
// from escaping into caller-visible pagination.
func (ix *Index) SearchFTSFiltered(query, scope string, limit int, allow func(id string) bool) ([]Hit, error) {
	return ix.searchFTS(query, scope, limit, allow, true)
}

// SearchFTSIDs is SearchFTSFiltered for a caller that needs only which
// concepts match and in what order: the hits carry no Snippet. snippet() is
// quadratic in the number of matches inside one body under the trigram
// tokenizer, so a 76 KB body took 8 s to excerpt (#1276); the write-time
// repeated_fact lookup (D351) never reads an excerpt and must not pay it.
func (ix *Index) SearchFTSIDs(query, scope string, limit int, allow func(id string) bool) ([]Hit, error) {
	return ix.searchFTS(query, scope, limit, allow, false)
}

func (ix *Index) searchFTS(query, scope string, limit int, allow func(id string) bool, snippets bool) ([]Hit, error) {
	if limit <= 0 {
		limit = 20
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}

	// The AND pass is asked for at least the floor so that a small limit
	// cannot make full matches beyond the page look like OR-only ones.
	hits, _, err := ix.searchFTSFiltered(sanitizeFTSQuery(q), scope, max(limit, search.OrFallbackFloor), allow, snippets)
	if err != nil {
		return nil, err
	}

	tokens := ftsTokens(strings.ReplaceAll(q, "\"", ""))
	if len(hits) < search.OrFallbackFloor && len(tokens) >= 2 {
		seen := make(map[string]bool, len(hits))
		for _, h := range hits {
			seen[h.ID] = true
		}
		// The OR query also returns the full matches: ask for enough extra
		// rows that `limit` OR-only hits survive the de-duplication.
		fallback, _, err := ix.searchFTSFiltered(`"`+strings.Join(tokens, `" OR "`)+`"`, scope, limit+len(hits), allow, snippets)
		if err != nil {
			return nil, err
		}
		for _, h := range fallback {
			if !seen[h.ID] {
				h.Partial = true
				hits = append(hits, h)
			}
		}
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func (ix *Index) searchFTSFiltered(query, scope string, limit int, allow func(id string) bool, snippets bool) ([]Hit, bool, error) {
	var hits []Hit
	found := false
	for offset := 0; ; offset += ftsSearchBatch {
		batch, err := ix.searchFTSPage(query, scope, ftsSearchBatch, offset, snippets)
		if err != nil {
			return nil, false, err
		}
		if len(batch) == 0 {
			return hits, found, nil
		}
		found = true
		for _, hit := range batch {
			if allow != nil && !allow(hit.ID) {
				continue
			}
			hits = append(hits, hit)
			if len(hits) == limit {
				return hits, found, nil
			}
		}
		if len(batch) < ftsSearchBatch {
			return hits, found, nil
		}
	}
}

// ftsRank and the two snippet expressions are the ranking and excerpt of every
// search query. bm25's weights are positional over ALL columns, the
// unindexed id included: the leading 0 is id's, then title 5, meta 2, body 1,
// assets 1 (D277). Dropping the 0 silently shifts every weight one column to
// the left.
// snippet() reads column 3, the body: a match only in title or meta yields
// the start of the body, as the in-memory excerpt does with no term found,
// and an empty body yields "", for which the caller uses its own excerpt. A
// match only in the assets (column 4) would yield that same start of the
// body, so both columns are excerpted with match marks and pickSnippet takes
// the assets excerpt when only it carries a mark.
const (
	ftsRank         = `-1.0 * bm25(concepts_fts, 0.0, 5.0, 2.0, 1.0, 1.0)`
	ftsSnippet      = `snippet(concepts_fts, 3, char(1), char(2), '…', ?)`
	ftsAssetSnippet = `snippet(concepts_fts, 4, char(1), char(2), '…', ?)`
	snipMarks       = "\x01\x02"
)

// pickSnippet chooses between the body and assets excerpts and strips the
// match marks.
func pickSnippet(body, assets string) string {
	pick := body
	if !strings.Contains(body, "\x01") && strings.Contains(assets, "\x01") {
		pick = assets
	}
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(snipMarks, r) {
			return -1
		}
		return r
	}, pick)
}

func (ix *Index) searchFTSPage(query, scope string, limit, offset int, snippets bool) ([]Hit, error) {
	var rows *sql.Rows
	var err error
	snip, asnip := ftsSnippet, ftsAssetSnippet
	var snipArgs []any
	if snippets {
		snipArgs = []any{ftsSnippetTokens, ftsSnippetTokens}
	} else {
		snip, asnip = `''`, `''`
	}
	if scope != "" {
		rows, err = ix.db.Query(
			`SELECT c.id, `+ftsRank+` AS score,
			        `+snip+` AS snip,
			        `+asnip+` AS asnip
			 FROM concepts_fts
			 JOIN concepts c ON c.id = concepts_fts.id
			 WHERE concepts_fts MATCH ? AND c.id LIKE ? ESCAPE '\'
			 ORDER BY score DESC, c.id
			 LIMIT ? OFFSET ?`,
			append(snipArgs, query, likePrefixPattern(scope), limit, offset)...,
		)
	} else {
		rows, err = ix.db.Query(
			`SELECT c.id, `+ftsRank+` AS score,
			        `+snip+` AS snip,
			        `+asnip+` AS asnip
			 FROM concepts_fts
			 JOIN concepts c ON c.id = concepts_fts.id
			 WHERE concepts_fts MATCH ?
			 ORDER BY score DESC, c.id
			 LIMIT ? OFFSET ?`,
			append(snipArgs, query, limit, offset)...,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("sqlindex: search fts: %w", err)
	}
	defer rows.Close()

	var hits []Hit
	for rows.Next() {
		var h Hit
		var bodySnip, assetSnip string
		if err := rows.Scan(&h.ID, &h.Score, &bodySnip, &assetSnip); err != nil {
			return nil, fmt.Errorf("sqlindex: scan hit: %w", err)
		}
		h.Snippet = pickSnippet(bodySnip, assetSnip)
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlindex: rows iteration: %w", err)
	}
	return hits, nil
}

// Count returns the number of indexed concepts.
func (ix *Index) Count() (int, error) {
	var n int
	err := ix.db.QueryRow(`SELECT COUNT(*) FROM concepts`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sqlindex: count: %w", err)
	}
	return n, nil
}

// sectionRank is the ranking of BestSection: bm25's weights are positional
// over all columns of sections_fts, the two unindexed ones (id, heading)
// included, then the heading text 3 and the section body 1 (D1207).
const (
	sectionRank    = `-1.0 * bm25(sections_fts, 0.0, 0.0, 3.0, 1.0)`
	sectionSnippet = `snippet(sections_fts, 3, char(1), char(2), '…', ?)`
)

// BestSection returns the "##" section of concept id that best matches query:
// its heading as okf.ExtractSection accepts it and an excerpt of its text. The
// query's tokens (ftsTokens: terms under three characters match nothing, as for
// pages) are OR-ed, so a section holding any of them qualifies and bm25 picks
// the best. No matching section, or none indexed, is ok == false, not an error.
func (ix *Index) BestSection(id, query string) (heading, snippet string, ok bool, err error) {
	tokens := ftsTokens(strings.ReplaceAll(query, "\"", ""))
	if len(tokens) == 0 {
		return "", "", false, nil
	}
	match := `"` + strings.Join(tokens, `" OR "`) + `"`
	var snip string
	err = ix.db.QueryRow(
		`SELECT heading, `+sectionSnippet+`
		 FROM sections_fts
		 WHERE sections_fts MATCH ? AND id = ?
		 ORDER BY `+sectionRank+` DESC, rowid
		 LIMIT 1`,
		ftsSnippetTokens, match, id,
	).Scan(&heading, &snip)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("sqlindex: best section: %w", err)
	}
	return heading, pickSnippet(snip, ""), true, nil
}
