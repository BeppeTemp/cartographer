package kb

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// BatchWriteOp is one target of an atomic multi-concept write (D125 WP1/WP2):
// the MCP layer materializes a "write" or "patch" request into this
// canonical id/frontmatter/body/if_match form — the same shape
// prepareWriteConcept already validates for a single concept — before
// calling WriteConceptBatch. IfMatch empty means create-only, exactly like
// WriteConcept when the target does not yet exist; the MCP layer is
// responsible for rejecting an empty IfMatch against an already-existing
// target before building the batch (WriteConceptBatch itself stays
// permissive, consistent with WriteConcept).
type BatchWriteOp struct {
	ID      okf.ConceptID
	FM      *okf.Frontmatter
	Body    string
	IfMatch string
}

// BatchWriteResult reports one applied operation's resulting content-hash,
// in the same order as the BatchWriteOp slice passed to WriteConceptBatch.
type BatchWriteResult struct {
	ID          string
	ContentHash string
}

// fileSnapshot is the pre-transaction state of one file touched by a batch
// (an explicit op target or a synthesized expanded-index stub), sufficient
// to restore it byte-for-byte and mode-for-mode on rollback.
type fileSnapshot struct {
	absPath string
	root    string // kb.DataRoot(), the rollback directory-pruning boundary
	existed bool
	data    []byte
	mode    os.FileMode
}

func snapshotFile(absPath, root string) (fileSnapshot, error) {
	info, err := os.Lstat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fileSnapshot{absPath: absPath, root: root}, nil
		}
		return fileSnapshot{}, fmt.Errorf("snapshot %s: %w", absPath, err)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("snapshot %s: %w", absPath, err)
	}
	return fileSnapshot{absPath: absPath, root: root, existed: true, data: data, mode: info.Mode()}, nil
}

// restore reverts absPath to its exact pre-transaction state: removes a file
// the transaction created (pruning any now-empty directory it created along
// the way, down to root), or rewrites and re-chmods one it overwrote back to
// its original bytes and mode — writeFileAtomic's temp-file rename would
// otherwise leave a rewritten file at the temp file's own mode, not the
// original one.
func (s fileSnapshot) restore() error {
	if !s.existed {
		if err := os.Remove(s.absPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return pruneEmptyDirs(filepath.Dir(s.absPath), s.root)
	}
	if err := writeFileAtomic(s.absPath, s.data); err != nil {
		return err
	}
	return os.Chmod(s.absPath, s.mode)
}

// pruneEmptyDirs removes dir and then its now-empty ancestors, stopping at
// the first non-empty directory, a missing one, or root. Mirrors
// DeleteAsset's directory pruning (asset.go), applied here to a rolled-back
// batch's newly created concept/expanded-concept directories.
func pruneEmptyDirs(dir, root string) error {
	for dir != root && dir != filepath.Dir(dir) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if len(entries) > 0 {
			return nil
		}
		if err := os.Remove(dir); err != nil {
			return err
		}
		dir = filepath.Dir(dir)
	}
	return nil
}

// WriteConceptBatch atomically materializes every op's final content plus
// one summary log.md entry as a single logical operation (D125 WP1/WP2):
// every op is prepared — validated and its content built — before any disk
// write, so a preparation failure leaves the tree untouched. If any file
// write, the log append, or afterFiles fails, every file this call already
// wrote — including any implicit expanded-index stub a target's directory
// creation triggered — and log.md are restored to their exact pre-call bytes
// and mode before the error is returned: callers observe either the
// complete batch or the exact pre-call KB state, never an intermediate one.
//
// afterFiles runs after every file and the log entry are committed,
// still before WriteConceptBatch returns success; it exists so the MCP
// layer can keep the keyword/FTS5 search indexes in step with the same
// atomicity boundary without this package depending on internal/search or
// internal/sqlindex. If afterFiles returns an error, it must first reconcile
// any index entries it already changed back to their pre-call state itself
// (it has the original content, read before the batch started) — this
// function then rolls the files and log back to match, so files and both
// indexes stay consistent in every outcome.
//
// Callers must already hold the KB's write lock (gitWrap's WithGitLock):
// this is the single acquisition the whole batch runs under, not a second
// one.
func (kb *KB) WriteConceptBatch(ops []BatchWriteOp, logMessage string, afterFiles func([]BatchWriteResult) error) ([]BatchWriteResult, error) {
	if len(ops) == 0 {
		return nil, fmt.Errorf("%w: empty batch", okf.ErrInvalidConcept)
	}

	plans := make([]*writeConceptPlan, len(ops))
	for i, op := range ops {
		plan, err := kb.prepareWriteConcept(op.ID, op.FM, op.Body, op.IfMatch, false)
		if err != nil {
			return nil, fmt.Errorf("batch op %d (%s): %w", i, op.ID, err)
		}
		plans[i] = plan
	}

	root := kb.DataRoot()
	logAbs := filepath.Join(root, "log.md")

	// Snapshot every distinct target — explicit op targets, any synthesized
	// expanded-index stub, and log.md — before any write. Deduplicated by
	// path: two sibling ops under the same not-yet-existing expanded
	// directory share one stub target.
	snapshots := make(map[string]fileSnapshot, len(plans)*2+1)
	order := make([]string, 0, len(plans)*2+1)
	snapshotOnce := func(path string) error {
		if path == "" {
			return nil
		}
		if _, ok := snapshots[path]; ok {
			return nil
		}
		snap, err := snapshotFile(path, root)
		if err != nil {
			return err
		}
		snapshots[path] = snap
		order = append(order, path)
		return nil
	}
	for _, plan := range plans {
		if err := snapshotOnce(plan.absPath); err != nil {
			return nil, fmt.Errorf("batch: %w", err)
		}
		if err := snapshotOnce(plan.newExpandedDir); err != nil {
			return nil, fmt.Errorf("batch: %w", err)
		}
	}
	if err := snapshotOnce(logAbs); err != nil {
		return nil, fmt.Errorf("batch: %w", err)
	}

	rollback := func(cause error) error {
		var errs []string
		for i := len(order) - 1; i >= 0; i-- {
			if err := snapshots[order[i]].restore(); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", order[i], err))
			}
		}
		if len(errs) > 0 {
			return fmt.Errorf("%w (rollback also failed: %s)", cause, strings.Join(errs, "; "))
		}
		return cause
	}

	results := make([]BatchWriteResult, len(ops))
	stubbed := make(map[string]bool, len(plans))
	for i, plan := range plans {
		if plan.newExpandedDir != "" && stubbed[plan.newExpandedDir] {
			// An earlier op in this same batch already stubbed this
			// brand-new expanded directory (two siblings created together):
			// commit only the concept file itself, mkdir is idempotent.
			if err := os.MkdirAll(filepath.Dir(plan.absPath), 0o755); err != nil {
				return nil, rollback(fmt.Errorf("batch op %d (%s): mkdir: %w", i, ops[i].ID, err))
			}
			if err := writeFileAtomic(plan.absPath, plan.content); err != nil {
				return nil, rollback(fmt.Errorf("batch op %d (%s): %w", i, ops[i].ID, err))
			}
			results[i] = BatchWriteResult{ID: string(ops[i].ID), ContentHash: okf.ContentHash(string(plan.content))}
			continue
		}
		hash, err := kb.commitWriteConceptPlan(plan)
		if err != nil {
			return nil, rollback(fmt.Errorf("batch op %d (%s): %w", i, ops[i].ID, err))
		}
		if plan.newExpandedDir != "" {
			stubbed[plan.newExpandedDir] = true
		}
		results[i] = BatchWriteResult{ID: string(ops[i].ID), ContentHash: hash}
	}

	if err := kb.AppendLog(logMessage, time.Now()); err != nil {
		return nil, rollback(fmt.Errorf("batch: log append: %w", err))
	}

	if afterFiles != nil {
		if err := afterFiles(results); err != nil {
			return nil, rollback(fmt.Errorf("batch: %w", err))
		}
	}

	return results, nil
}
