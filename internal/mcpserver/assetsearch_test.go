package mcpserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

// D277: the text of an expanded concept's assets is searchable through its
// owner. Every test runs on both backends.

const assetOwner = "manutenzione/test-runbook"

func expandOwner(t *testing.T, k *kb.KB) {
	t.Helper()
	if err := k.ExpandConcept(okf.ConceptID(assetOwner)); err != nil {
		t.Fatalf("ExpandConcept: %v", err)
	}
}

func TestSearchFindsAssetText(t *testing.T) {
	freshnessBackends(t, func(t *testing.T, k *kb.KB, s *Server) {
		expandOwner(t, k)
		if strings.Contains(searchText(t, s, "aa:bb:cc:dd:ee:ff"), assetOwner) {
			t.Fatal("found before the asset exists")
		}
		// Through the MCP tool.
		callOK(t, s, "asset_write", `{"concept_id":"`+assetOwner+`","path":"inventory/devices.csv","content":"mac,host\naa:bb:cc:dd:ee:ff,gateway-one\n"}`)
		out := searchText(t, s, "aa:bb:cc:dd:ee:ff")
		if !strings.Contains(out, assetOwner) {
			t.Fatalf("asset_write content not found: %s", out)
		}
		if !strings.Contains(out, "gateway-one") {
			t.Fatalf("snippet does not show the asset text: %s", out)
		}
		if strings.Contains(out, "devices.csv\"") && strings.Contains(out, `"asset"`) {
			t.Fatalf("a hit must be the owner, not an asset: %s", out)
		}
		// The path itself is searchable.
		if !strings.Contains(searchText(t, s, "inventory/devices.csv"), assetOwner) {
			t.Fatal("asset path not searchable")
		}
		// Through asset_delete.
		_, entry, err := k.ReadAsset(okf.ConceptID(assetOwner), "inventory/devices.csv")
		if err != nil {
			t.Fatal(err)
		}
		callOK(t, s, "asset_delete", `{"concept_id":"`+assetOwner+`","path":"inventory/devices.csv","if_match":"`+entry.SHA256+`"}`)
		if out := searchText(t, s, "aa:bb:cc:dd:ee:ff"); strings.Contains(out, assetOwner) {
			t.Fatalf("deleted asset still found: %s", out)
		}
	})
}

func TestSearchFollowsAssetEditedOnDisk(t *testing.T) {
	freshnessBackends(t, func(t *testing.T, k *kb.KB, s *Server) {
		expandOwner(t, k)
		abs := filepath.Join(k.DataRoot(), "manutenzione", "test-runbook", "hosts.txt")
		if err := os.WriteFile(abs, []byte("firstvalue"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(searchText(t, s, "firstvalue"), assetOwner) {
			t.Fatal("asset created on disk not seen")
		}
		// Edit in place, no MCP write; the mtime moves past the racy window.
		if err := os.WriteFile(abs, []byte("secondvalue"), 0o644); err != nil {
			t.Fatal(err)
		}
		later := time.Now().Add(-time.Hour) // outside the racy window
		if err := os.Chtimes(abs, later, later); err != nil {
			t.Fatal(err)
		}
		if out := searchText(t, s, "secondvalue"); !strings.Contains(out, assetOwner) {
			t.Fatalf("in-place edit not seen: %s", out)
		}
		if out := searchText(t, s, "firstvalue"); strings.Contains(out, assetOwner) {
			t.Fatalf("old asset text still found: %s", out)
		}
		// A same-size edit inside the racy window is caught on a later reconcile.
		now := time.Now()
		if err := os.WriteFile(abs, []byte("thirdvalue1"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(abs, now, now); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(searchText(t, s, "thirdvalue1"), assetOwner) {
			t.Fatal("edit inside the racy window not seen")
		}
		if err := os.WriteFile(abs, []byte("fourthvalue"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(abs, now, now); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(searchText(t, s, "fourthvalue"), assetOwner) {
			t.Fatal("second edit with an identical mtime not seen (racy rule)")
		}
	})
}

func TestAssetOwnerWithConceptChange(t *testing.T) {
	freshnessBackends(t, func(t *testing.T, k *kb.KB, s *Server) {
		expandOwner(t, k)
		if _, err := k.WriteAsset(okf.ConceptID(assetOwner), "a.txt", []byte("keepme-asset"), "", nil); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(searchText(t, s, "keepme-asset"), assetOwner) {
			t.Fatal("asset not indexed")
		}
		// Editing the concept must not drop the asset text from the index.
		callOK(t, s, "concept_write", `{"id":"`+assetOwner+`","frontmatter":{"type":"Runbook","title":"Test Runbook"},"body":"rewritten body zebrafish"}`)
		if out := searchText(t, s, "keepme-asset"); !strings.Contains(out, assetOwner) {
			t.Fatalf("asset text lost after a concept edit: %s", out)
		}
	})
}

// A KB without assets persists exactly the hashes it persisted before assets
// were indexed, so the upgrade costs no reindex; one with assets persists a
// suffixed hash, so an asset-only change is seen after a restart too.
func TestPersistedHashWithAssets(t *testing.T) {
	k := setupTestKB(t)
	ix, err := sqlindex.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Skipf("sqlindex.Open: %v", err)
	}
	defer ix.Close()
	if _, err := ReconcileIndex(k, nil, ix); err != nil {
		t.Fatal(err)
	}
	c, err := k.ReadConcept(okf.ConceptID(assetOwner))
	if err != nil {
		t.Fatal(err)
	}
	hashes, _ := ix.AllHashes()
	if hashes[assetOwner] != c.ContentHash {
		t.Fatalf("no-assets hash = %q, want the plain content hash %q", hashes[assetOwner], c.ContentHash)
	}
	if st, _ := ReconcileIndex(k, nil, ix); st != (ReconcileStats{}) {
		t.Fatalf("second reconcile changed %+v", st)
	}

	expandOwner(t, k)
	if _, err := k.WriteAsset(okf.ConceptID(assetOwner), "a.txt", []byte("offline-asset"), "", nil); err != nil {
		t.Fatal(err)
	}
	st, err := ReconcileIndex(k, nil, ix)
	if err != nil {
		t.Fatal(err)
	}
	if st.Updated+st.Indexed+st.Removed == 0 {
		t.Fatal("offline reconcile did not see the new asset")
	}
	hashes, _ = ix.AllHashes()
	if !strings.Contains(hashes[assetOwner], "+") {
		t.Fatalf("hash with assets = %q, want a suffixed hash", hashes[assetOwner])
	}
	if hits, err := ix.SearchFTS("offline-asset", "", 10); err != nil || len(hits) != 1 {
		t.Fatalf("asset not in SQLite after ReconcileIndex: %+v, %v", hits, err)
	}
	// Asset-only change after a "restart": only the asset moves.
	abs := filepath.Join(k.DataRoot(), "manutenzione", "test-runbook", "a.txt")
	if err := os.WriteFile(abs, []byte("offline-asset-two"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(-time.Hour) // outside the racy window
	if err := os.Chtimes(abs, later, later); err != nil {
		t.Fatal(err)
	}
	if st, _ := ReconcileIndex(k, nil, ix); st.Updated != 1 {
		t.Fatalf("asset-only change not detected offline: %+v", st)
	}
}

// BenchmarkReconcileNoChangeWithAssets is D277's budget: a reconcile that
// finds nothing to do, over 200 expanded concepts of 10 assets each, may add
// under 5 ms on the CI runner. Compare with the same KB without assets by
// running with -bench and reading both sub-benchmarks.
func BenchmarkReconcileNoChangeWithAssets(b *testing.B) {
	for _, withAssets := range []bool{false, true} {
		b.Run(fmt.Sprintf("assets=%v", withAssets), func(b *testing.B) {
			k, err := kb.Init(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(k.DataRoot(), "dossiers"), 0o755); err != nil {
				b.Fatal(err)
			}
			old := time.Now().Add(-time.Hour)
			for i := 0; i < 200; i++ {
				dir := filepath.Join(k.DataRoot(), "dossiers", fmt.Sprintf("c%03d", i))
				if err := os.MkdirAll(dir, 0o755); err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte("---\ntype: Note\ntitle: C\n---\nbody\n"), 0o644); err != nil {
					b.Fatal(err)
				}
				if !withAssets {
					continue
				}
				for j := 0; j < 10; j++ {
					p := filepath.Join(dir, fmt.Sprintf("a%d.csv", j))
					if err := os.WriteFile(p, []byte("x,y\n1,2\n"), 0o644); err != nil {
						b.Fatal(err)
					}
					os.Chtimes(p, old, old)
				}
			}
			rec := newSearchReconciler(k, nil)
			if _, err := rec.reconcile(); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := rec.reconcile(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
