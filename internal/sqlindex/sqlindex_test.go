package sqlindex

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ix, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := ix.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestUpsertAndSearchFTS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ix, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ix.Close()

	if err := ix.Upsert("archive/container", "hash1", "homelab kubernetes setup"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := ix.Upsert("archive/networking", "hash2", "network config vlan"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := ix.Upsert("services/keycloak", "hash3", "keycloak sso setup"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Search for a trigram substring.
	hits, err := ix.SearchFTS("kuber", "", 10)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit for 'kuber', got %d", len(hits))
	}
	if hits[0].ID != "archive/container" {
		t.Fatalf("expected 'archive/container', got %q", hits[0].ID)
	}

	// Search with scope filter.
	hits, err = ix.SearchFTS("setup", "services/", 10)
	if err != nil {
		t.Fatalf("SearchFTS scoped: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 scoped hit, got %d", len(hits))
	}
	if hits[0].ID != "services/keycloak" {
		t.Fatalf("expected 'services/keycloak', got %q", hits[0].ID)
	}

	// Search with no match.
	hits, err = ix.SearchFTS("zzznotfound", "", 10)
	if err != nil {
		t.Fatalf("SearchFTS no match: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected 0 hits, got %d", len(hits))
	}

	// Count.
	n, err := ix.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected count=3, got %d", n)
	}
}

func TestSearchFTS_MultiTermFallback(t *testing.T) {
	ix, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ix.Close()
	if err := ix.Upsert("both", "h1", "karpenter handles cluster downscaler work"); err != nil {
		t.Fatalf("Upsert both: %v", err)
	}
	if err := ix.Upsert("first", "h2", "karpenter provisions nodes"); err != nil {
		t.Fatalf("Upsert first: %v", err)
	}
	if err := ix.Upsert("second", "h3", "downscaler schedules maintenance"); err != nil {
		t.Fatalf("Upsert second: %v", err)
	}

	hits, err := ix.SearchFTS("karpenter downscaler", "", 10)
	if err != nil {
		t.Fatalf("SearchFTS AND: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "both" {
		t.Fatalf("AND search got %+v, want only both", hits)
	}

	hits, err = ix.SearchFTS("provisions maintenance", "", 10)
	if err != nil {
		t.Fatalf("SearchFTS OR fallback: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("OR fallback got %+v, want 2 hits", hits)
	}
}

func TestSearchFTS_ShortTokensAndSingleTerm(t *testing.T) {
	ix, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ix.Close()
	if err := ix.Upsert("c", "h", "api gateway kubernetes"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	hits, err := ix.SearchFTS("an kubernetes", "", 10)
	if err != nil {
		t.Fatalf("SearchFTS short token: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "c" {
		t.Fatalf("short-token search got %+v, want c", hits)
	}
	hits, err = ix.SearchFTS("api", "", 10)
	if err != nil {
		t.Fatalf("SearchFTS single term: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "c" {
		t.Fatalf("single-term search got %+v, want c", hits)
	}
}

func TestUpsertUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ix, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ix.Close()

	if err := ix.Upsert("concept/a", "hash-v1", "old content"); err != nil {
		t.Fatalf("Upsert v1: %v", err)
	}
	if err := ix.Upsert("concept/a", "hash-v2", "new content rewritten"); err != nil {
		t.Fatalf("Upsert v2: %v", err)
	}

	hits, err := ix.SearchFTS("rewritten", "", 10)
	if err != nil {
		t.Fatalf("SearchFTS after update: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit for 'rewritten', got %d", len(hits))
	}
	if hits[0].ID != "concept/a" {
		t.Fatalf("expected 'concept/a', got %q", hits[0].ID)
	}

	// Old content should not appear.
	hits, err = ix.SearchFTS("old", "", 10)
	if err != nil {
		t.Fatalf("SearchFTS old: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected 0 hits for 'old', got %d", len(hits))
	}
}

func TestAllHashes(t *testing.T) {
	ix, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := ix.Upsert("a", "hash-a", "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := ix.Upsert("b", "hash-b", "beta"); err != nil {
		t.Fatal(err)
	}
	hashes, err := ix.AllHashes()
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) != 2 || hashes["a"] != "hash-a" || hashes["b"] != "hash-b" {
		t.Fatalf("AllHashes = %#v", hashes)
	}
}

func TestEmbeddingRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ix, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ix.Close()

	id := "concept/emb"
	hash := "hash-emb"
	model := "nomic-embed-text"
	vec := []float64{0.1, 0.2, 0.3, -0.5}

	if err := ix.UpsertEmbedding(id, hash, model, vec); err != nil {
		t.Fatalf("UpsertEmbedding: %v", err)
	}

	// EmbeddingFresh should return true.
	fresh, err := ix.EmbeddingFresh(id, hash)
	if err != nil {
		t.Fatalf("EmbeddingFresh: %v", err)
	}
	if !fresh {
		t.Fatal("expected EmbeddingFresh=true")
	}

	// Different hash should return false.
	fresh, err = ix.EmbeddingFresh(id, "different-hash")
	if err != nil {
		t.Fatalf("EmbeddingFresh diff hash: %v", err)
	}
	if fresh {
		t.Fatal("expected EmbeddingFresh=false for different hash")
	}

	// Non-existent id should return false.
	fresh, err = ix.EmbeddingFresh("nonexistent", "any-hash")
	if err != nil {
		t.Fatalf("EmbeddingFresh nonexistent: %v", err)
	}
	if fresh {
		t.Fatal("expected EmbeddingFresh=false for nonexistent")
	}
}

func TestAllEmbeddings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ix, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ix.Close()

	vec1 := []float64{1.0, 2.0, 3.0}
	vec2 := []float64{4.0, 5.0, 6.0}
	model := "test-model"

	if err := ix.UpsertEmbedding("a", "h1", model, vec1); err != nil {
		t.Fatalf("UpsertEmbedding a: %v", err)
	}
	if err := ix.UpsertEmbedding("b", "h2", model, vec2); err != nil {
		t.Fatalf("UpsertEmbedding b: %v", err)
	}

	ids, vecs, m, err := ix.AllEmbeddings()
	if err != nil {
		t.Fatalf("AllEmbeddings: %v", err)
	}
	if m != model {
		t.Fatalf("expected model=%q, got %q", model, m)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %d", len(ids))
	}
	if len(vecs) != 2 {
		t.Fatalf("expected 2 vecs, got %d", len(vecs))
	}

	// Verify vector values round-tripped.
	found := map[string][]float64{}
	for i, id := range ids {
		found[id] = vecs[i]
	}
	for i, v := range found["a"] {
		if math.Abs(v-vec1[i]) > 1e-9 {
			t.Fatalf("vec1 mismatch at %d: %f != %f", i, v, vec1[i])
		}
	}
	for i, v := range found["b"] {
		if math.Abs(v-vec2[i]) > 1e-9 {
			t.Fatalf("vec2 mismatch at %d: %f != %f", i, v, vec2[i])
		}
	}
}

func TestEmptySearch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ix, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ix.Close()

	// Empty query should return nil, no error.
	hits, err := ix.SearchFTS("", "", 10)
	if err != nil {
		t.Fatalf("SearchFTS empty: %v", err)
	}
	if hits != nil {
		t.Fatal("expected nil for empty query")
	}

	hits, err = ix.SearchFTS("   ", "", 10)
	if err != nil {
		t.Fatalf("SearchFTS whitespace: %v", err)
	}
	if hits != nil {
		t.Fatal("expected nil for whitespace query")
	}
}

func TestEncodeDecodeVec(t *testing.T) {
	original := []float64{0.0, -1.5, math.Pi, 1e10, -1e-10}
	encoded := encodeVec(original)
	decoded := decodeVec(encoded)
	if len(decoded) != len(original) {
		t.Fatalf("length mismatch: %d != %d", len(decoded), len(original))
	}
	for i := range original {
		if math.Abs(decoded[i]-original[i]) > 1e-12 {
			t.Fatalf("mismatch at %d: %f != %f", i, decoded[i], original[i])
		}
	}
}

func TestSearchQuotesSanitization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ix, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ix.Close()

	if err := ix.Upsert("c", "h", `text with "quotes" inside`); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Search with quotes in query should be sanitized and still match.
	hits, err := ix.SearchFTS(`"quotes"`, "", 10)
	if err != nil {
		t.Fatalf("SearchFTS with quotes: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
}

// TestSearchFTS_Snippet verifies that SearchFTS (D70) returns a non-empty
// excerpt around the match, produced by FTS5's native snippet() function.
func TestSearchFTS_Snippet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ix, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ix.Close()

	body := "Testo di riempimento prima del termine, poi arriva kubernetes proprio qui in mezzo, e poi ancora altro testo di riempimento dopo per allungare il corpo del concetto oltre i duecento caratteri previsti dal budget dello snippet."
	if err := ix.Upsert("archive/container", "hash1", body); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	hits, err := ix.SearchFTS("kubernetes", "", 10)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if hits[0].Snippet == "" {
		t.Fatal("expected non-empty snippet")
	}
	if !strings.Contains(hits[0].Snippet, "kubernetes") {
		t.Errorf("expected snippet to contain the match, got %q", hits[0].Snippet)
	}
	if len(hits[0].Snippet) >= len(body) {
		t.Errorf("expected snippet shorter than full body (%d chars), got %d: %q", len(body), len(hits[0].Snippet), hits[0].Snippet)
	}
}
