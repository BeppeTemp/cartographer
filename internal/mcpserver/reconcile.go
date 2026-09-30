package mcpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/search"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

// ReconcileStats reports the changes made while bringing an index in line with
// the KB files on disk. Indexed counts new concepts; Updated counts existing
// concepts whose content hash changed; Removed counts vanished concepts.
type ReconcileStats struct {
	Indexed int
	Updated int
	Removed int
}

// ReconcileIndex compares every concept's content hash with the persisted
// SQLite hashes and applies only the delta. It is the offline path — the CLI
// `reindex` and the startup freshness check, before any server owns an
// in-memory index; a running server reconciles through searchReconciler
// (D245). When live is supplied it is kept aligned too.
func ReconcileIndex(k *kb.KB, live *liveIndex, sqlIdx *sqlindex.Index) (ReconcileStats, error) {
	if sqlIdx == nil {
		return ReconcileStats{}, fmt.Errorf("reconcile index: SQLite index is not available")
	}
	persisted, err := sqlIdx.AllHashes()
	if err != nil {
		return ReconcileStats{}, err
	}

	sigs, err := k.AssetSignatures()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: search index: asset signatures: %v\n", err)
		sigs = nil
	}

	var stats ReconcileStats
	err = k.WalkConcepts(func(id okf.ConceptID, content string) error {
		conceptID := string(id)
		hash := assetContentHash(okf.ContentHash(content), sigs[id])
		oldHash, exists := persisted[conceptID]
		delete(persisted, conceptID)
		racy := strings.HasSuffix(sigs[id], "~") // see searchReconciler.reconcileLocked
		if exists && oldHash == hash && !racy {
			return nil
		}
		var assets string
		if _, has := sigs[id]; has {
			var sig string
			var readErr error
			if assets, sig, readErr = k.IndexableAssetText(id); readErr == nil {
				hash = assetContentHash(okf.ContentHash(content), sig)
			}
		}
		if err := sqlIdx.Upsert(conceptID, hash, content, assets); err != nil {
			return err
		}
		if live != nil {
			live.addWithAssets(conceptID, content, assets)
		}
		if exists {
			stats.Updated++
		} else {
			stats.Indexed++
		}
		return nil
	})
	if err != nil {
		return stats, fmt.Errorf("reconcile index: walk concepts: %w", err)
	}

	for id := range persisted {
		if err := sqlIdx.Delete(id); err != nil {
			return stats, err
		}
		if live != nil {
			live.remove(id)
		}
		stats.Removed++
	}
	return stats, nil
}

// searchReconciler keeps a KB's two derived search indexes — the in-memory
// live index and the optional SQLite FTS5 index — in line with the concept
// files (D245). It is the only code that updates them incrementally: no write
// path may touch an index directly any more (enforced by
// TestIndexUpdatesOnlyInReconciler). The indexes follow the files by
// validation, like the graph (D241): before a search, after a git pull and on
// reindex, the D241 cache reports every concept that changed, appeared or
// vanished since the last reconciliation, by any means — a handler, a move,
// a conflict resolution, a pull, an editor — and this applies it.
type searchReconciler struct {
	k    *kb.KB
	live *liveIndex
	sql  *sqlindex.Index // nil → in-memory only
	// mu serialises reconciliations: a second concurrent caller waits and
	// then finds an empty delta.
	mu sync.Mutex
	// assetSigs is, per owner concept, the asset signature the indexes were
	// last built from (D277); nil until the first reconciliation.
	assetSigs map[okf.ConceptID]string
}

// assetContentHash is the hash persisted for a concept: its content hash when
// it has no indexable assets, so a KB without assets keeps the hashes it had
// before assets were indexed, else the content hash joined with a digest of
// the asset signature, so an asset-only change is seen after a restart too.
func assetContentHash(contentHash, sig string) string {
	if sig == "" {
		return contentHash
	}
	sum := sha256.Sum256([]byte(sig))
	return contentHash + "+" + hex.EncodeToString(sum[:])[:16]
}

// reconcile applies the delta since the previous call to both indexes. SQLite
// errors are logged and skipped: that index is disposable, and the delta is
// already consumed, so a failed row is repaired by `reindex(full: true)` or
// the startup reconciliation.
func (r *searchReconciler) reconcile() (ReconcileStats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reconcileLocked(true)
}

// rebuild is `reindex(full: true)` for the live index: it forgets what was
// reported and re-reads every concept into an empty index. It returns the
// number of concepts indexed and, with SQLite, the number of rows
// rebuildSQLIndex upserted under the same lock.
func (r *searchReconciler) rebuild() (indexed, sqlUpserted int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.k.SeedReportedHashes(nil)
	r.live.swap(search.New(), map[string]conceptMeta{})
	r.assetSigs = nil
	stats, err := r.reconcileLocked(false)
	if err == nil && r.sql != nil {
		sqlUpserted = rebuildSQLIndex(r.k, r.sql)
	}
	return stats.Indexed, sqlUpserted, err
}

func newSearchReconciler(k *kb.KB, sql *sqlindex.Index) *searchReconciler {
	return &searchReconciler{k: k, live: newLiveIndex(search.New(), nil), sql: sql}
}

func (r *searchReconciler) reconcileLocked(applySQL bool) (ReconcileStats, error) {
	changes, err := r.k.ConceptChanges()
	if err != nil {
		return ReconcileStats{}, fmt.Errorf("reconcile index: %w", err)
	}
	// Asset changes are invisible to ConceptChanges (Markdown only): they are
	// found by comparing stat signatures, without reading any content (D277).
	sigs, sigErr := r.k.AssetSignatures()
	if sigErr != nil {
		fmt.Fprintf(os.Stderr, "cartographer: search index: asset signatures: %v\n", sigErr)
	}
	var stats ReconcileStats
	var persisted map[string]string
	if r.sql != nil && applySQL && (len(changes) > 0 || sigErr == nil) {
		if persisted, err = r.sql.AllHashes(); err != nil {
			fmt.Fprintf(os.Stderr, "cartographer: search index: read SQLite hashes: %v\n", err)
			persisted = nil
		}
	}
	handled := make(map[okf.ConceptID]bool, len(changes))
	// put indexes one concept into both backends.
	put := func(id okf.ConceptID, content, contentHash string) {
		var assets, sig string
		if _, has := sigs[id]; has {
			assets, sig, _ = r.k.IndexableAssetText(id)
		}
		r.live.addWithAssets(string(id), content, assets)
		if r.sql == nil || !applySQL {
			return
		}
		hash := assetContentHash(contentHash, sig)
		// A racy signature proves nothing: the same stats may front new bytes,
		// so the persisted hash cannot vouch for the row.
		if h, ok := persisted[string(id)]; ok && h == hash && !strings.HasSuffix(sig, "~") {
			return
		}
		if err := r.sql.Upsert(string(id), hash, content, assets); err != nil {
			fmt.Fprintf(os.Stderr, "cartographer: search index: SQLite upsert %s: %v\n", id, err)
		}
	}
	for _, c := range changes {
		id := string(c.ID)
		handled[c.ID] = true
		if c.Removed {
			r.live.remove(id)
			stats.Removed++
			if r.sql != nil && applySQL {
				if err := r.sql.Delete(id); err != nil {
					fmt.Fprintf(os.Stderr, "cartographer: search index: SQLite delete %s: %v\n", id, err)
				}
			}
			continue
		}
		if c.Known {
			stats.Updated++
		} else {
			stats.Indexed++
		}
		put(c.ID, c.Content, c.Hash)
	}
	if sigErr != nil {
		return stats, nil // keep the cached signatures: nothing is known about the assets
	}
	// An owner whose assets appeared, changed or vanished is re-added with its
	// unchanged concept file. A signature marked racy ("~") is never equal to
	// itself, so it is redone once more after its window has passed.
	for id := range unionAssetOwners(sigs, r.assetSigs) {
		if handled[id] {
			continue
		}
		if old, now := r.assetSigs[id], sigs[id]; old == now && !strings.HasSuffix(now, "~") {
			continue
		}
		rel, _ := r.k.ConceptRelPath(id)
		content, err := r.k.ReadRaw(rel)
		if err != nil {
			continue // the concept is gone: ConceptChanges reports that
		}
		stats.Updated++
		put(id, content, okf.ContentHash(content))
	}
	r.assetSigs = sigs
	return stats, nil
}

func unionAssetOwners(a, b map[okf.ConceptID]string) map[okf.ConceptID]struct{} {
	out := make(map[okf.ConceptID]struct{}, len(a)+len(b))
	for id := range a {
		out[id] = struct{}{}
	}
	for id := range b {
		out[id] = struct{}{}
	}
	return out
}
