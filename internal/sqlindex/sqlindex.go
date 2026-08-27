// Package sqlindex implements a persistent SQLite-backed keyword index.
//
// The schema:
//
//	concepts(id TEXT PRIMARY KEY, content_hash TEXT NOT NULL, body TEXT NOT NULL)
//	concepts_fts — FTS5 virtual table with trigram tokenizer
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

	_ "modernc.org/sqlite" // register "sqlite" driver (pure-Go, no CGo)
)

// Hit is a single search result.
type Hit struct {
	ID      string
	Score   float64
	Snippet string // excerpt around the match, from FTS5's native snippet()
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

	if err := createSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Index{db: db, path: dbPath}, nil
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
			body,
			tokenize='trigram'
		)
	`)
	if err != nil {
		return fmt.Errorf("sqlindex: fts5 not available (fallback to in-memory): %w", err)
	}

	return nil
}

// Close closes the underlying database connection.
func (ix *Index) Close() error {
	return ix.db.Close()
}

// Path returns the filesystem path of the SQLite database.
func (ix *Index) Path() string {
	return ix.path
}

// Upsert inserts or updates a concept's content in both the concepts table and
// the FTS5 index.
func (ix *Index) Upsert(id, contentHash, body string) error {
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
	if _, err := ix.db.Exec(`INSERT INTO concepts_fts(id, body) VALUES(?, ?)`, id, body); err != nil {
		return fmt.Errorf("sqlindex: insert fts: %w", err)
	}

	return nil
}

// Delete removes a concept's content from both the concepts table and the
// FTS5 index (used by concept_delete).
func (ix *Index) Delete(id string) error {
	if _, err := ix.db.Exec(`DELETE FROM concepts_fts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlindex: delete fts: %w", err)
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
	if limit <= 0 {
		limit = 20
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}

	hits, found, err := ix.searchFTSFiltered(sanitizeFTSQuery(q), scope, limit, allow)
	if err != nil {
		return nil, err
	}

	tokens := ftsTokens(strings.ReplaceAll(q, "\"", ""))
	if !found && len(tokens) >= 2 {
		fallback, _, err := ix.searchFTSFiltered(`"`+strings.Join(tokens, `" OR "`)+`"`, scope, limit, allow)
		return fallback, err
	}
	return hits, nil
}

func (ix *Index) searchFTSFiltered(query, scope string, limit int, allow func(id string) bool) ([]Hit, bool, error) {
	var hits []Hit
	found := false
	for offset := 0; ; offset += ftsSearchBatch {
		batch, err := ix.searchFTSPage(query, scope, ftsSearchBatch, offset)
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

func (ix *Index) searchFTSPage(query, scope string, limit, offset int) ([]Hit, error) {
	var rows *sql.Rows
	var err error
	if scope != "" {
		rows, err = ix.db.Query(
			`SELECT c.id, -1.0 * bm25(concepts_fts) AS score,
			        snippet(concepts_fts, 1, '', '', '…', ?) AS snip
			 FROM concepts_fts
			 JOIN concepts c ON c.id = concepts_fts.id
			 WHERE concepts_fts MATCH ? AND c.id LIKE ?
			 ORDER BY score DESC, c.id
			 LIMIT ? OFFSET ?`,
			ftsSnippetTokens, query, scope+"%", limit, offset,
		)
	} else {
		rows, err = ix.db.Query(
			`SELECT c.id, -1.0 * bm25(concepts_fts) AS score,
			        snippet(concepts_fts, 1, '', '', '…', ?) AS snip
			 FROM concepts_fts
			 JOIN concepts c ON c.id = concepts_fts.id
			 WHERE concepts_fts MATCH ?
			 ORDER BY score DESC, c.id
			 LIMIT ? OFFSET ?`,
			ftsSnippetTokens, query, limit, offset,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("sqlindex: search fts: %w", err)
	}
	defer rows.Close()

	var hits []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ID, &h.Score, &h.Snippet); err != nil {
			return nil, fmt.Errorf("sqlindex: scan hit: %w", err)
		}
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
