package sqlindex

import (
	"fmt"
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

func TestSearchFTSFilteredFillsLimitAcrossHiddenPages(t *testing.T) {
	ix, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	for i := 0; i < ftsSearchBatch+1; i++ {
		if err := ix.Upsert(fmt.Sprintf("hidden/%03d", i), fmt.Sprintf("h%d", i), "needle needle"); err != nil {
			t.Fatal(err)
		}
	}
	if err := ix.Upsert("visible", "visible", "needle"); err != nil {
		t.Fatal(err)
	}
	hits, err := ix.SearchFTSFiltered("needle", "", 1, func(id string) bool { return !strings.HasPrefix(id, "hidden/") })
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "visible" {
		t.Fatalf("filtered FTS hits = %+v, want visible", hits)
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

// D135 invariant 3: a database created before the removal still carries an
// embeddings table. Opening it must not be an error, and ordinary keyword
// indexing and search must keep working against it — no operator migration.
func TestOpenDatabaseWithLegacyEmbeddingsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	ix, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Recreate the pre-D135 table with a row in it, as an old server left it.
	if _, err := ix.db.Exec(`CREATE TABLE IF NOT EXISTS embeddings (
		id TEXT PRIMARY KEY, content_hash TEXT NOT NULL, model TEXT NOT NULL, vec BLOB NOT NULL)`); err != nil {
		t.Fatalf("create legacy embeddings table: %v", err)
	}
	if _, err := ix.db.Exec(`INSERT INTO embeddings(id, content_hash, model, vec) VALUES(?,?,?,?)`,
		"notes/legacy", "hash-legacy", "nomic-embed-text", []byte{1, 2, 3, 4, 5, 6, 7, 8}); err != nil {
		t.Fatalf("insert legacy embedding: %v", err)
	}
	if err := ix.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen a database with an embeddings table: %v", err)
	}
	defer reopened.Close()

	if err := reopened.Upsert("notes/legacy", "hash-legacy", "the legacy concept body"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	hits, err := reopened.SearchFTS("legacy", "", 10)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "notes/legacy" {
		t.Fatalf("SearchFTS = %+v, want the single legacy concept", hits)
	}
	if err := reopened.Delete("notes/legacy"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

// TestSearchFTSScopeTreatsLikeMetacharactersLiterally pins the SQL backend to
// the same prefix semantics the in-memory index documents ("only concepts
// whose id starts with scope"). Concept IDs are file paths, so "_" is ordinary
// and "%" is legal; before likePrefixPattern both were LIKE wildcards, so a
// scope like "maps_a/" also matched "mapsXa/" and those stray rows ate the
// caller's LIMIT.
func TestSearchFTSScopeTreatsLikeMetacharactersLiterally(t *testing.T) {
	cases := []struct {
		name    string
		scope   string
		inScope string
		outside string
	}{
		{"underscore", "maps_a/", "maps_a/one", "mapsXa/two"},
		{"percent", "maps%b/", "maps%b/one", "mapsZZb/two"},
		{"backslash", `maps\c/`, `maps\c/one`, "mapsQc/two"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ix, err := Open(filepath.Join(t.TempDir(), "index.db"))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer ix.Close()
			if err := ix.Upsert(tc.inScope, "h1", "alpha bravo charlie"); err != nil {
				t.Fatalf("Upsert: %v", err)
			}
			if err := ix.Upsert(tc.outside, "h2", "alpha bravo charlie"); err != nil {
				t.Fatalf("Upsert: %v", err)
			}

			hits, err := ix.SearchFTS("alpha", tc.scope, 10)
			if err != nil {
				t.Fatalf("SearchFTS: %v", err)
			}
			if len(hits) != 1 || hits[0].ID != tc.inScope {
				t.Fatalf("scope %q: want exactly [%s], got %v", tc.scope, tc.inScope, hits)
			}
		})
	}
}
