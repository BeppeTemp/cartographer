package mcpserver

import (
	"fmt"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
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
// SQLite hashes and applies only the delta. When live is supplied, the same
// incremental add/remove path used by MCP writes keeps the in-memory index and
// result metadata aligned too. A nil live index is used by the offline CLI,
// where no server process owns an in-memory index.
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
