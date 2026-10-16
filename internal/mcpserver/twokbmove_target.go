package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// missingFromCommit lists the planned files the commit at sha does not hold.
// The commit listing is the ground truth: a file `git add -A` skipped (an
// ignore rule of the target, including a .gitignore the move itself brought, an
// info/exclude or core.excludesFile entry, a nested repository that turns a
// directory into a gitlink) is absent from it, however the copy looked on disk.
func missingFromCommit(dir, sha string, planned []string) ([]string, error) {
	held, err := gitx.TreePaths(dir, sha)
	if err != nil {
		return nil, err
	}
	have := make(map[string]bool, len(held))
	for _, p := range held {
		have[p] = true
	}
	var missing []string
	for _, p := range planned {
		if !have[p] {
			missing = append(missing, p)
		}
	}
	return missing, nil
}

// undoIncompleteTarget is the target-step failure for a commit that landed
// without some planned file. The source has not been touched, so it still holds
// the only complete copy; the commit is undone by a revert, never a reset.
func (m *twoKBMove) undoIncompleteTarget(ctx context.Context, dstSHA string, planned, newDirs, missing []string, verifyErr error) ToolResult {
	dst, tool := m.Dst, m.Tool
	var why string
	if verifyErr != nil {
		why = fmt.Sprintf("the target commit %.7s could not be checked for the moved files (%v)", dstSHA, verifyErr)
	} else {
		why = fmt.Sprintf("the target commit %.7s lacks %s: an ignore rule of %s matches them (.gitignore, .git/info/exclude or core.excludesFile), so git does not store them", dstSHA, strings.Join(kb.CapList(missing, 10), ", "), m.DstName)
	}
	// Before the revert commit: it runs `git add -A`, and once the revert
	// deletes an ignore file that travelled with the move, a copy it had hidden
	// stops being ignored and would be committed as a stray file.
	if cErr := removeUntrackedPlanned(dst, planned); cErr != nil {
		why += fmt.Sprintf("; removing the files it left in the working tree failed too: %v", cErr)
	}
	if rErr := revertTransferTarget(ctx, tool, dst, dstSHA, m.SrcName, "the commit lacks files the target ignores"); rErr != nil {
		return errorResult(fmt.Sprintf("%s: target step failed, nothing was changed in the source: %s; undoing the target commit %.7s failed too (%v): %s now holds part of the %s, remove them from %s by hand", tool, why, dstSHA, rErr, m.DstName, m.Things, m.DstName))
	}
	pruneDirs(dst.Root, newDirs)
	return errorResult(fmt.Sprintf("%s: target step failed, nothing was changed in the source: %s; the target commit %.7s was undone by a revert commit", tool, why, dstSHA))
}

// removeUntrackedPlanned removes the planned files that HEAD does not track and
// that are still in the target's working tree. DiscardChanges runs `git clean
// -fd`, which keeps ignored files: a copy an ignore rule hid would stay behind,
// and the next attempt would fail on "already exists".
//
// Safe only because Plan refuses an occupied destination (artifact_move: any
// entry at the unit's path; concept_transfer: targetOccupied on the file and the
// directory), so a planned path never held anything of the target's own before
// the copy. Two guards keep that from being the only defence: HEAD's tree is
// asked and a tracked path is never touched, and only a regular file below the
// real target root is removed.
func removeUntrackedPlanned(dst *kb.KB, planned []string) error {
	tracked := map[string]bool{}
	if !gitx.HeadUnborn(dst.Root) {
		paths, err := gitx.TreePaths(dst.Root, "HEAD")
		if err != nil {
			return err
		}
		for _, p := range paths {
			tracked[p] = true
		}
	}
	realRoot, err := filepath.EvalSymlinks(dst.Root)
	if err != nil {
		return err
	}
	insideRoot := map[string]bool{}
	var firstErr error
	for _, rel := range planned {
		local := filepath.FromSlash(rel)
		if tracked[rel] || !filepath.IsLocal(local) {
			continue
		}
		abs := filepath.Join(dst.Root, local)
		info, err := os.Lstat(abs)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		parent := filepath.Dir(abs)
		inside, seen := insideRoot[parent]
		if !seen {
			if resolved, rerr := filepath.EvalSymlinks(parent); rerr == nil {
				r, relErr := filepath.Rel(realRoot, resolved)
				inside = relErr == nil && filepath.IsLocal(r)
			}
			insideRoot[parent] = inside
		}
		if !inside {
			continue
		}
		if err := os.Remove(abs); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// missingParentDirs lists the directories below root that planned files will
// need and that do not exist yet, deepest first: the ones a failed move created
// and so may remove again, as opposed to the shared skills/ or data/ parents a
// KB already had.
func missingParentDirs(root string, planned []string) []string {
	seen := map[string]bool{}
	var dirs []string
	for _, rel := range planned {
		for d := pathDir(rel); d != "" && !seen[d]; d = pathDir(d) {
			seen[d] = true
			if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(d))); os.IsNotExist(err) {
				dirs = append(dirs, d)
			}
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	return dirs
}

// pathDir is the slash-path parent of rel, "" at the top.
func pathDir(rel string) string {
	i := strings.LastIndexByte(rel, '/')
	if i < 0 {
		return ""
	}
	return rel[:i]
}

// pruneDirs removes the given directories (deepest first, as missingParentDirs
// orders them) when they are empty. A non-empty one stays: os.Remove refuses it.
func pruneDirs(root string, dirs []string) {
	for _, d := range dirs {
		abs := filepath.Join(root, filepath.FromSlash(d))
		if info, err := os.Lstat(abs); err == nil && info.IsDir() {
			_ = os.Remove(abs)
		}
	}
}
