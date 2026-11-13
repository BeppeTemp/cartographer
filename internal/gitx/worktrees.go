package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/procattr"
)

// worktreeListTimeout bounds one `git worktree list`: it reads local metadata
// only, so a stuck call means a broken repository, not a slow network.
const worktreeListTimeout = 10 * time.Second

// LinkedWorktrees returns the canonical absolute paths of the linked working
// trees of the repository containing dir, sorted: the main working tree, a
// bare record, a prunable one (its directory is gone), dir itself and anything
// at or below another entry's `.git` are left out, and only existing
// directories are kept. A directory that is not in a repository, or a missing
// git, is not an error: the result is nil, nil. It is not memoised (memo.go
// caches plain repositories only): a worktree added or removed is seen on the
// next call.
func LinkedWorktrees(dir string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), worktreeListTimeout)
	defer cancel()
	cmd := procattr.Hide(exec.CommandContext(ctx, "git", "-C", dir, "worktree", "list", "--porcelain"))
	cmd.Env = Env() // -C names the repository: a hook's GIT_DIR must not
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}
	self := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		self = r
	}
	return filterLinkedWorktrees(parseWorktreeList(string(out)), self), nil
}

// parseWorktreeList returns the paths of the linked records of `git worktree
// list --porcelain` output, in order: the first record is the main working
// tree and is dropped, as are bare and prunable ones.
func parseWorktreeList(out string) []string {
	var paths []string
	for i, rec := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n\n") {
		var path string
		skip := i == 0
		for _, line := range strings.Split(rec, "\n") {
			key, val, _ := strings.Cut(line, " ")
			switch key {
			case "worktree":
				path = val
			case "bare", "prunable":
				skip = true
			}
		}
		if !skip && path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

// filterLinkedWorktrees keeps the absolute paths that exist as directories,
// canonicalised, other than self and than anything at or below another entry's
// `.git`; the result is sorted and deduplicated.
func filterLinkedWorktrees(paths []string, self string) []string {
	var kept []string
	seen := map[string]bool{}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			continue
		}
		info, err := os.Stat(p)
		if err != nil || !info.IsDir() {
			continue
		}
		c, err := filepath.EvalSymlinks(p)
		if err != nil || c == self || seen[c] {
			continue
		}
		seen[c] = true
		kept = append(kept, c)
	}
	out := kept[:0:0]
	for _, p := range kept {
		inside := false
		for _, o := range kept {
			g := filepath.Join(o, ".git")
			if p == g || strings.HasPrefix(p, g+string(filepath.Separator)) {
				inside = true
				break
			}
		}
		if !inside {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
