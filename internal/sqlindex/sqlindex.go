// Package sqlindex implements a persistent SQLite-backed keyword and embedding
// index with per-content-hash embedding caching.
//
// The schema:
//
//	concepts(id TEXT PRIMARY KEY, content_hash TEXT NOT NULL, body TEXT NOT NULL)
//	concepts_fts — FTS5 virtual table with trigram tokenizer
//	embeddings(id TEXT PRIMARY KEY, content_hash TEXT NOT NULL, model TEXT NOT NULL, vec BLOB NOT NULL)
package sqlindex

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
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

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS embeddings (
			id TEXT PRIMARY KEY,
			content_hash TEXT NOT NULL,
			model TEXT NOT NULL,
			vec BLOB NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("sqlindex: create embeddings: %w", err)
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
// FTS5 index (used by concept_delete). It does not touch the embeddings
// table: a stale cached embedding for a removed id is harmless, since it is
// only ever looked up by content-hash match on a live concept.
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

// SearchFTS performs a keyword search via FTS5 trigram tokenizer.
// If scope is non-empty, only concepts whose id starts with scope are returned.
// Results are sorted by BM25 relevance (higher score = better match). Each hit
// carries a Snippet excerpt produced by FTS5's native snippet() function.
func (ix *Index) SearchFTS(query, scope string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 20
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}

	hits, err := ix.searchFTS(sanitizeFTSQuery(q), scope, limit)
	if err != nil {
		return nil, err
	}

	tokens := ftsTokens(strings.ReplaceAll(q, "\"", ""))
	if len(hits) == 0 && len(tokens) >= 2 {
		return ix.searchFTS(`"`+strings.Join(tokens, `" OR "`)+`"`, scope, limit)
	}
	return hits, nil
}

func (ix *Index) searchFTS(query, scope string, limit int) ([]Hit, error) {
	var rows *sql.Rows
	var err error
	if scope != "" {
		rows, err = ix.db.Query(
			`SELECT c.id, -1.0 * bm25(concepts_fts) AS score,
			        snippet(concepts_fts, 1, '', '', '…', ?) AS snip
			 FROM concepts_fts
			 JOIN concepts c ON c.id = concepts_fts.id
			 WHERE concepts_fts MATCH ? AND c.id LIKE ?
			 ORDER BY score DESC
			 LIMIT ?`,
			ftsSnippetTokens, query, scope+"%", limit,
		)
	} else {
		rows, err = ix.db.Query(
			`SELECT c.id, -1.0 * bm25(concepts_fts) AS score,
			        snippet(concepts_fts, 1, '', '', '…', ?) AS snip
			 FROM concepts_fts
			 JOIN concepts c ON c.id = concepts_fts.id
			 WHERE concepts_fts MATCH ?
			 ORDER BY score DESC
			 LIMIT ?`,
			ftsSnippetTokens, query, limit,
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

// EmbeddingFresh returns true if an embedding exists for the given id with a
// matching content hash (i.e. the cached embedding is still valid).
func (ix *Index) EmbeddingFresh(id, contentHash string) (bool, error) {
	var storedHash string
	err := ix.db.QueryRow(
		`SELECT content_hash FROM embeddings WHERE id = ?`, id,
	).Scan(&storedHash)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sqlindex: check embedding: %w", err)
	}
	return storedHash == contentHash, nil
}

// UpsertEmbedding stores (or replaces) an embedding vector for a concept.
// vec is serialized as a blob of float64 values in little-endian order.
func (ix *Index) UpsertEmbedding(id, contentHash, model string, vec []float64) error {
	blob := encodeVec(vec)
	_, err := ix.db.Exec(
		`INSERT INTO embeddings(id, content_hash, model, vec) VALUES(?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET content_hash=excluded.content_hash, model=excluded.model, vec=excluded.vec`,
		id, contentHash, model, blob,
	)
	if err != nil {
		return fmt.Errorf("sqlindex: upsert embedding: %w", err)
	}
	return nil
}

// AllEmbeddings reads all stored embedding vectors.
// Returns parallel slices of ids and vectors, plus the model identifier.
// If no embeddings are stored, returns nil slices and empty model.
func (ix *Index) AllEmbeddings() (ids []string, vecs [][]float64, model string, err error) {
	rows, err := ix.db.Query(`SELECT id, model, vec FROM embeddings`)
	if err != nil {
		return nil, nil, "", fmt.Errorf("sqlindex: all embeddings: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, m string
		var blob []byte
		if err := rows.Scan(&id, &m, &blob); err != nil {
			return nil, nil, "", fmt.Errorf("sqlindex: scan embedding: %w", err)
		}
		ids = append(ids, id)
		model = m // last model wins; caller should ensure consistency
		vecs = append(vecs, decodeVec(blob))
	}
	if err := rows.Err(); err != nil {
		return nil, nil, "", fmt.Errorf("sqlindex: rows iteration: %w", err)
	}
	return ids, vecs, model, nil
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

// encodeVec serializes a float64 slice to a little-endian byte blob.
func encodeVec(v []float64) []byte {
	buf := make([]byte, len(v)*8)
	for i, f := range v {
		binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(f))
	}
	return buf
}

// decodeVec deserializes a byte blob back to a float64 slice.
func decodeVec(b []byte) []float64 {
	n := len(b) / 8
	v := make([]float64, n)
	for i := 0; i < n; i++ {
		v[i] = math.Float64frombits(binary.LittleEndian.Uint64(b[i*8:]))
	}
	return v
}
