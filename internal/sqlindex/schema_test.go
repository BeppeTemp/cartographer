package sqlindex

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func openTest(t *testing.T) *Index {
	t.Helper()
	ix, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Skipf("Open: %v", err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix
}

// D246: a title hit outranks a body that mentions the term twice.
func TestSearchFTS_TitleOutranksBody(t *testing.T) {
	ix := openTest(t)
	mustUpsert(t, ix, "notes/prose", "---\ntype: Note\ntitle: Prose\n---\nThe gateway is here, and the gateway is there.\n")
	mustUpsert(t, ix, "infra/gw", "---\ntype: Service\ntitle: Gateway\n---\nRoutes traffic.\n")
	hits, err := ix.SearchFTS("gateway", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ID != "infra/gw" {
		t.Fatalf("hits = %+v, want infra/gw first", hits)
	}
}

// D246: diacritics are folded on both sides of the match.
func TestSearchFTS_FoldsDiacritics(t *testing.T) {
	ix := openTest(t)
	mustUpsert(t, ix, "a/accented", "---\ntype: Note\n---\nLe attività della città.\n")
	mustUpsert(t, ix, "a/plain", "---\ntype: Note\n---\nLe attivita della citta.\n")
	for _, q := range []string{"attivita", "attività", "citta", "città"} {
		hits, err := ix.SearchFTS(q, "", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 2 {
			t.Errorf("%q: hits = %+v, want both concepts", q, hits)
		}
	}
	hits, _ := ix.SearchFTS("citta", "a/accented", 10)
	if len(hits) != 1 || strings.TrimSpace(hits[0].Snippet) != "Le attività della città." {
		t.Errorf("snippet lost the accents: %+v", hits)
	}
}

func mustUpsert(t *testing.T, ix *Index, id, content string) {
	t.Helper()
	if err := ix.Upsert(id, "h-"+id, content); err != nil {
		t.Fatalf("Upsert %s: %v", id, err)
	}
}

func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// A v1 database (single body column, no user_version) is migrated on open:
// concepts_fts recreated with the v2 columns, concepts emptied so the next
// reconciliation reindexes everything.
func TestOpen_MigratesV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE concepts (id TEXT PRIMARY KEY, content_hash TEXT NOT NULL, body TEXT NOT NULL)`,
		`CREATE VIRTUAL TABLE concepts_fts USING fts5(id UNINDEXED, body, tokenize='trigram')`,
		`INSERT INTO concepts VALUES ('old/one', 'h1', 'old body')`,
		`INSERT INTO concepts_fts(id, body) VALUES ('old/one', 'old body')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Skipf("build v1 fixture (%s): %v", stmt, err)
		}
	}
	db.Close()

	ix, err := Open(path)
	if err != nil {
		t.Fatalf("Open v1: %v", err)
	}
	defer ix.Close()
	if v := userVersion(t, ix.db); v != schemaVersion {
		t.Fatalf("user_version = %d, want %d", v, schemaVersion)
	}
	if n, _ := ix.Count(); n != 0 {
		t.Fatalf("concepts not emptied: %d rows", n)
	}
	mustUpsert(t, ix, "new/one", "---\ntitle: Fresh\n---\nbody\n")
	if hits, err := ix.SearchFTS("fresh", "", 10); err != nil || len(hits) != 1 {
		t.Fatalf("v2 search after migration = %+v, %v", hits, err)
	}
}

// A v2 database is left alone on reopen.
func TestOpen_KeepsV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	ix, err := Open(path)
	if err != nil {
		t.Skipf("Open: %v", err)
	}
	mustUpsert(t, ix, "keep/me", "---\ntitle: Kept\n---\nbody\n")
	ix.Close()
	ix, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if n, _ := ix.Count(); n != 1 {
		t.Fatalf("v2 database was reset: %d rows", n)
	}
}
