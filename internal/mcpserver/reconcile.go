package mcpserver

import (
	"fmt"
	"os"
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

	var stats ReconcileStats
	err = k.WalkConcepts(func(id okf.ConceptID, content string) error {
		conceptID := string(id)
		hash := okf.ContentHash(content)
		oldHash, exists := persisted[conceptID]
		delete(persisted, conceptID)
		if exists && oldHash == hash {
			return nil
		}
		if err := sqlIdx.Upsert(conceptID, hash, content); err != nil {
			return err
		}
		if live != nil {
			live.add(conceptID, content)
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
	var stats ReconcileStats
	if len(changes) == 0 {
		return stats, nil
	}
	var persisted map[string]string
	if r.sql != nil && applySQL {
		if persisted, err = r.sql.AllHashes(); err != nil {
			fmt.Fprintf(os.Stderr, "cartographer: search index: read SQLite hashes: %v\n", err)
			persisted = nil
		}
	}
	for _, c := range changes {
		id := string(c.ID)
		switch {
		case c.Removed:
			r.live.remove(id)
			stats.Removed++
		case c.Known:
			r.live.add(id, c.Content)
			stats.Updated++
		default:
			r.live.add(id, c.Content)
			stats.Indexed++
		}
		if r.sql == nil || !applySQL {
			continue
		}
		if c.Removed {
			if err := r.sql.Delete(id); err != nil {
				fmt.Fprintf(os.Stderr, "cartographer: search index: SQLite delete %s: %v\n", id, err)
			}
			continue
		}
		if h, ok := persisted[id]; ok && h == c.Hash {
			continue
		}
		if err := r.sql.Upsert(id, c.Hash, c.Content); err != nil {
			fmt.Fprintf(os.Stderr, "cartographer: search index: SQLite upsert %s: %v\n", id, err)
		}
	}
	return stats, nil
}
