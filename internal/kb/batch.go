package kb

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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
	// Delete removes the concept instead of writing it (FM, Body and IfMatch
	// are then ignored: the caller checked if_match). An expanded concept is
	// deleted only when the same batch deletes every satellite under it and it
	// owns no asset (D1327).
	Delete bool
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

// RollbackFailedMarker is in the error of a batch whose rollback itself
// failed: the tree may hold part of the batch, and a caller that must tell
// that case from a clean refusal looks for it.
const RollbackFailedMarker = "rollback also failed"

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
// afterFiles, when non-nil, runs after every file and the log entry are
// committed, still before WriteConceptBatch returns success; an error from it
// rolls the files and log back like any other failure. concept_batch uses it
// for curated-index entries (D714); it must undo its own index edits before
// returning an error, since the rollback covers only the files and log written
// here. The search indexes follow the files by validation (D245), so nothing
// else has to be kept in step at this boundary.
//
// Callers must already hold the KB's write lock (gitWrap's WithGitLock):
// this is the single acquisition the whole batch runs under, not a second
// one.
func (kb *KB) WriteConceptBatch(ops []BatchWriteOp, logMessage string, afterFiles func([]BatchWriteResult) error) ([]BatchWriteResult, error) {
	if len(ops) == 0 {
		return nil, fmt.Errorf("%w: empty batch", okf.ErrInvalidConcept)
	}

	plans := make([]*writeConceptPlan, len(ops))
	deleteAbs := make([]string, len(ops)) // absolute path of each delete op, "" for a write
	var expandedDirs []string             // directories of the expanded concepts the batch deletes
	deleting := make(map[okf.ConceptID]bool, len(ops))
	for _, op := range ops {
		if op.Delete {
			deleting[op.ID] = true
		}
	}
	for i, op := range ops {
		if op.Delete {
			abs, expandedDir, err := kb.prepareDeleteConcept(op.ID, ops, deleting)
			if err != nil {
				return nil, fmt.Errorf("batch op %d (%s): %w", i, op.ID, err)
			}
			deleteAbs[i] = abs
			if expandedDir != "" {
				expandedDirs = append(expandedDirs, expandedDir)
			}
			continue
		}
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
	for i, plan := range plans {
		if plan == nil {
			if err := snapshotOnce(deleteAbs[i]); err != nil {
				return nil, fmt.Errorf("batch: %w", err)
			}
			continue
		}
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
			return fmt.Errorf("%w (%s: %s)", cause, RollbackFailedMarker, strings.Join(errs, "; "))
		}
		return cause
	}

	results := make([]BatchWriteResult, len(ops))
	stubbed := make(map[string]bool, len(plans))
	for i, plan := range plans {
		if plan == nil {
			if err := os.Remove(deleteAbs[i]); err != nil {
				return nil, rollback(fmt.Errorf("batch op %d (%s): delete: %w", i, ops[i].ID, err))
			}
			results[i] = BatchWriteResult{ID: string(ops[i].ID)}
			continue
		}
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

	// Pruned last: a rollback above rewrites the deleted index.md into its
	// directory, which must still exist then. Deepest first, best effort: an
	// empty directory left behind is not tracked by git.
	sort.Slice(expandedDirs, func(i, j int) bool { return len(expandedDirs[i]) > len(expandedDirs[j]) })
	for _, dir := range expandedDirs {
		_ = pruneEmptyDirs(dir, root)
	}

	return results, nil
}

// prepareDeleteConcept validates a batch delete and returns the absolute path
// of the file to remove and, for an expanded concept, its directory. A missing
// concept is okf.ErrNotFound; a reserved file is refused. An expanded concept
// is deleted only if it owns no asset and every concept below it is deleted by
// the same batch (deleting is the set of ids the batch deletes), so nothing the
// caller did not list goes with it (D1327); a write or patch of a concept below
// it is refused too, since it would be left without its parent.
func (kb *KB) prepareDeleteConcept(id okf.ConceptID, ops []BatchWriteOp, deleting map[okf.ConceptID]bool) (abs, expandedDir string, err error) {
	relPath, expanded, err := kb.resolveConceptRelPath(id, true)
	if err != nil {
		return "", "", err
	}
	if !expanded && okf.IsReserved(filepath.Base(relPath)) {
		return "", "", fmt.Errorf("%w: %s is a reserved file", okf.ErrInvalidConcept, filepath.Base(relPath))
	}
	abs, err = kb.ResolvePath(relPath, true)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("%w: %s", okf.ErrNotFound, id)
		}
		return "", "", err
	}
	if !expanded {
		return abs, "", nil
	}
	expandedDir = filepath.Dir(abs)
	for _, op := range ops {
		if !op.Delete && strings.HasPrefix(string(op.ID), string(id)+"/") {
			return "", "", fmt.Errorf("%w: %s is an expanded concept deleted by this batch: %s below it cannot be written", okf.ErrInvalidConcept, id, op.ID)
		}
	}
	satellites, assets, err := expandedContents(expandedDir, string(id))
	if err != nil {
		return "", "", err
	}
	if len(assets) > 0 {
		return "", "", fmt.Errorf("%w: expanded concept %s owns %d asset(s): %s: delete it with concept_delete and force=true",
			okf.ErrInvalidConcept, id, len(assets), listCapped(assets))
	}
	var missing []string
	for _, s := range satellites {
		if !deleting[okf.ConceptID(s)] {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		return "", "", fmt.Errorf("%w: %s is an expanded concept with %d concept(s) below it the batch does not delete: %s: delete them in the same batch",
			okf.ErrInvalidConcept, id, len(missing), listCapped(missing))
	}
	return abs, expandedDir, nil
}

// expandedContents lists what lies below an expanded concept's directory: the
// ids of its satellite concepts (the top-level index.md is the concept itself)
// and the paths of every other file, which are assets or reserved files. Hidden
// entries are not content, as for ListAssets.
func expandedContents(dirAbs, id string) (satellites, assets []string, err error) {
	err = filepath.WalkDir(dirAbs, func(abs string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if abs == dirAbs {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%w: %s is not a regular file", okf.ErrInvalidPath, abs)
		}
		rel, err := filepath.Rel(dirAbs, abs)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case rel == "index.md":
		case strings.HasSuffix(rel, "/index.md"):
			satellites = append(satellites, id+"/"+strings.TrimSuffix(rel, "/index.md"))
		case strings.EqualFold(filepath.Ext(rel), ".md") && !okf.IsReserved(filepath.Base(rel)):
			satellites = append(satellites, id+"/"+strings.TrimSuffix(rel, filepath.Ext(rel)))
		default:
			assets = append(assets, rel)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list %s: %w", id, err)
	}
	return satellites, assets, nil
}

// listCapped joins at most ten names, noting the rest.
func listCapped(names []string) string {
	if len(names) <= 10 {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:10], ", ") + fmt.Sprintf(", and %d more", len(names)-10)
}
