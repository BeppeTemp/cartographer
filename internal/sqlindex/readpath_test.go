package sqlindex

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func openAt(t *testing.T, path string) *Index {
	t.Helper()
	ix, err := Open(path)
	if err != nil {
		t.Skipf("Open: %v", err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix
}

// WP5: excerpts are computed for the final hits only, so hidden hits and the
// pages that skip them pay no snippet() (#1507).
func TestSearchFTSSnippetsOnlyForFinalHits(t *testing.T) {
	ix := openAt(t, filepath.Join(t.TempDir(), "s.db"))
	for i := 0; i < 3*ftsSearchBatch; i++ {
		mustUpsert(t, ix, fmt.Sprintf("hidden/%03d", i), "---\ntype: Note\n---\nthe needle sits here, "+strings.Repeat("filler ", i%5)+"\n")
	}
	mustUpsert(t, ix, "shown/a", "---\ntype: Note\n---\nanother needle here\n")

	snippetRowsEvaluated.Store(0)
	hits, err := ix.SearchFTSFiltered("needle", "", 5, func(string) bool { return false })
	if err != nil || len(hits) != 0 {
		t.Fatalf("all hidden: %+v %v", hits, err)
	}
	if n := snippetRowsEvaluated.Load(); n != 0 {
		t.Fatalf("snippet rows for hidden hits = %d, want 0", n)
	}

	hits, err = ix.SearchFTSFiltered("needle", "", 1, func(id string) bool { return strings.HasPrefix(id, "shown/") })
	if err != nil || len(hits) != 1 || hits[0].ID != "shown/a" {
		t.Fatalf("hits = %+v %v", hits, err)
	}
	if n := snippetRowsEvaluated.Load(); n != 1 {
		t.Fatalf("snippet rows = %d, want 1 (the final hit)", n)
	}
	if !strings.Contains(hits[0].Snippet, "needle") {
		t.Fatalf("snippet = %q", hits[0].Snippet)
	}
}

// The two-phase search returns the same excerpt the single query did.
func TestSearchFTSSnippetEqualsInlineSnippet(t *testing.T) {
	ix := openAt(t, filepath.Join(t.TempDir(), "e.db"))
	for i := 0; i < 20; i++ {
		mustUpsert(t, ix, fmt.Sprintf("n/%02d", i), fmt.Sprintf("---\ntype: Note\ntitle: T%d\n---\nlead %d. %s the needle appears.\n", i, i, strings.Repeat("lorem ipsum ", 30+i)))
	}
	hits, err := ix.SearchFTS("needle", "", 20)
	if err != nil || len(hits) != 20 {
		t.Fatalf("hits = %d %v", len(hits), err)
	}
	for _, h := range hits {
		var body, assets string
		if err := ix.db.QueryRow(`SELECT `+ftsSnippet+`, `+ftsAssetSnippet+` FROM concepts_fts WHERE concepts_fts MATCH ? AND id = ?`,
			ftsSnippetTokens, ftsSnippetTokens, "needle", h.ID).Scan(&body, &assets); err != nil {
			t.Fatal(err)
		}
		if want := pickSnippet(body, assets); h.Snippet != want || want == "" {
			t.Errorf("%s: snippet %q, want %q", h.ID, h.Snippet, want)
		}
	}
}

// WP9: an update or delete addresses the FTS rows by rowid, never by the
// UNINDEXED id column, which FTS5 can only scan.
func TestFTSDeleteIsByRowid(t *testing.T) {
	ix := openAt(t, filepath.Join(t.TempDir(), "d.db"))
	mustUpsert(t, ix, "a/one", sectionsDoc)
	var plan strings.Builder
	rows, err := ix.db.Query(`EXPLAIN QUERY PLAN `+ftsDeleteByRowid(ftsKindConcept), 1)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a, b, c int
		var d string
		if err := rows.Scan(&a, &b, &c, &d); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(d + "\n")
	}
	rows.Close()
	scan, err := ix.db.Query(`EXPLAIN QUERY PLAN DELETE FROM concepts_fts WHERE id = ?`, "x")
	if err != nil {
		t.Fatal(err)
	}
	var byID strings.Builder
	for scan.Next() {
		var a, b, c int
		var d string
		if err := scan.Scan(&a, &b, &c, &d); err != nil {
			t.Fatal(err)
		}
		byID.WriteString(d + "\n")
	}
	scan.Close()
	if plan.String() == byID.String() {
		t.Fatalf("rowid delete plans like the id scan: %q", plan.String())
	}
	if !strings.Contains(plan.String(), "INDEX 0:=") || strings.Contains(byID.String(), "INDEX 0:=") {
		t.Fatalf("plans: rowid %q, id %q", plan.String(), byID.String())
	}
}

func ftsCounts(t *testing.T, ix *Index) (concepts, sections, rows int) {
	t.Helper()
	for q, dst := range map[string]*int{
		`SELECT COUNT(*) FROM concepts_fts`: &concepts,
		`SELECT COUNT(*) FROM sections_fts`: &sections,
		`SELECT COUNT(*) FROM fts_rows`:     &rows,
	} {
		if err := ix.db.QueryRow(q).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	return
}

// Update and delete leave no stale FTS row and no stale rowid mapping.
func TestFTSUpdateAndDeleteLeaveNoStaleRows(t *testing.T) {
	ix := openAt(t, filepath.Join(t.TempDir(), "u.db"))
	mustUpsert(t, ix, "ops/gw", sectionsDoc)
	mustUpsert(t, ix, "ops/other", "---\ntype: Note\n---\nsolo\n")
	c, s, r := ftsCounts(t, ix)
	if c != 2 || s == 0 || r != c+s {
		t.Fatalf("after insert: concepts %d sections %d rows %d", c, s, r)
	}
	for i := 0; i < 3; i++ {
		if err := ix.Upsert("ops/gw", fmt.Sprintf("v%d", i), sectionsDoc, ""); err != nil {
			t.Fatal(err)
		}
	}
	if c2, s2, r2 := ftsCounts(t, ix); c2 != c || s2 != s || r2 != r {
		t.Fatalf("after updates: %d %d %d, want %d %d %d", c2, s2, r2, c, s, r)
	}
	if err := ix.Delete("ops/gw"); err != nil {
		t.Fatal(err)
	}
	if c3, s3, r3 := ftsCounts(t, ix); c3 != 1 || s3 != 0 || r3 != 1 {
		t.Fatalf("after delete: %d %d %d, want 1 0 1", c3, s3, r3)
	}
	if hits, _ := ix.SearchFTS("solo", "", 5); len(hits) != 1 {
		t.Fatalf("hits = %+v", hits)
	}
}

// A database at version 4 (no fts_rows) is rebuilt, not read with a missing map.
func TestMigrateFromVersion4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v4.db")
	ix, err := Open(path)
	if err != nil {
		t.Skipf("Open: %v", err)
	}
	mustUpsert(t, ix, "ops/gw", sectionsDoc)
	for _, q := range []string{`DROP TABLE fts_rows`, `PRAGMA user_version = 4`} {
		if _, err := ix.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	ix.Close()
	ix = openAt(t, path)
	if n, _ := ix.Count(); n != 0 {
		t.Fatalf("Count = %d, want 0 after migration", n)
	}
	mustUpsert(t, ix, "ops/gw", sectionsDoc)
	mustUpsert(t, ix, "ops/gw", sectionsDoc)
	if c, _, r := ftsCounts(t, ix); c != 1 || r == 0 {
		t.Fatalf("after reindex: concepts %d rows %d", c, r)
	}
}

// BenchmarkReindexAll reports the cost of replacing every concept once at two
// sizes; it asserts nothing (#1507).
func BenchmarkReindexAll(b *testing.B) {
	for _, n := range []int{200, 1600} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			ix, err := Open(filepath.Join(b.TempDir(), "r.db"))
			if err != nil {
				b.Skip(err)
			}
			defer ix.Close()
			for i := 0; i < n; i++ {
				if err := ix.Upsert(fmt.Sprintf("c/%d", i), "h", rebuildBody(i), ""); err != nil {
					b.Fatal(err)
				}
			}
			for b.Loop() {
				for i := 0; i < n; i++ {
					if err := ix.Upsert(fmt.Sprintf("c/%d", i), "h2", rebuildBody(i), ""); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
