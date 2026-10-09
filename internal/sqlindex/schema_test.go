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
	if err := ix.Upsert(id, "h-"+id, content, ""); err != nil {
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

// D277: asset text lives in its own column; a match only there returns the
// owner, with a snippet taken from the asset rather than the start of the body.
func TestSearchFTS_AssetText(t *testing.T) {
	ix := openTest(t)
	body := "---\ntype: Note\ntitle: Inventory\n---\nThe devices of the lab.\n"
	if err := ix.Upsert("lab/inventory", "h1", body, "devices.csv\nmac,host\naa:bb:cc:dd:ee:ff,gateway-one\n"); err != nil {
		t.Fatal(err)
	}
	mustUpsert(t, ix, "lab/other", "---\ntype: Note\ntitle: Other\n---\nnothing\n")
	hits, err := ix.SearchFTS("gateway-one", "", 10)
	if err != nil || len(hits) != 1 || hits[0].ID != "lab/inventory" {
		t.Fatalf("asset-only match = %+v, %v", hits, err)
	}
	if !strings.Contains(hits[0].Snippet, "gateway-one") || strings.ContainsAny(hits[0].Snippet, "\x01\x02") {
		t.Fatalf("snippet = %q, want the asset excerpt without marks", hits[0].Snippet)
	}
	// A body match keeps the body excerpt.
	hits, err = ix.SearchFTS("devices of the lab", "", 10)
	if err != nil || len(hits) == 0 || hits[0].ID != "lab/inventory" || !strings.Contains(hits[0].Snippet, "devices of the lab") {
		t.Fatalf("body match = %+v, %v", hits, err)
	}
	// Re-upserting without assets drops the asset text.
	if err := ix.Upsert("lab/inventory", "h2", body, ""); err != nil {
		t.Fatal(err)
	}
	if hits, _ := ix.SearchFTS("gateway-one", "", 10); len(hits) != 0 {
		t.Fatalf("stale asset text: %+v", hits)
	}
}

// A v2 database (no assets column) is rebuilt on open: fts dropped, concepts
// emptied so the next reconciliation reindexes everything with its assets.
func TestOpen_MigratesV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE concepts (id TEXT PRIMARY KEY, content_hash TEXT NOT NULL, body TEXT NOT NULL)`,
		`CREATE VIRTUAL TABLE concepts_fts USING fts5(id UNINDEXED, title, meta, body, tokenize='trigram remove_diacritics 1')`,
		`INSERT INTO concepts VALUES ('old/one', 'h1', 'old body')`,
		`INSERT INTO concepts_fts(id, title, meta, body) VALUES ('old/one', '', '', 'old body')`,
		`PRAGMA user_version = 2`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Skipf("build v2 fixture (%s): %v", stmt, err)
		}
	}
	db.Close()

	ix, err := Open(path)
	if err != nil {
		t.Fatalf("Open v2: %v", err)
	}
	defer ix.Close()
	if v := userVersion(t, ix.db); v != schemaVersion {
		t.Fatalf("user_version = %d, want %d", v, schemaVersion)
	}
	if n, _ := ix.Count(); n != 0 {
		t.Fatalf("concepts not emptied: %d rows", n)
	}
	if err := ix.Upsert("new/one", "h", "---\ntitle: Fresh\n---\nbody\n", "a.txt\nneedle-in-asset\n"); err != nil {
		t.Fatal(err)
	}
	if hits, err := ix.SearchFTS("needle-in-asset", "", 10); err != nil || len(hits) != 1 {
		t.Fatalf("v3 asset search after migration = %+v, %v", hits, err)
	}
}
