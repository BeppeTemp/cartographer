package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/BeppeTemp/cartographer/internal/procattr"
)

// worktreeListTimeout bounds one `git worktree list`: it reads local metadata
// only, so a stuck call means a broken repository, not a slow network.
const worktreeListTimeout = 10 * time.Second

// LinkedWorktrees returns the canonical absolute paths of the other working
// trees of the repository containing dir, sorted: the main working tree when
// dir is a linked one, and the linked ones otherwise. A bare record, a prunable
// one (its directory is gone), dir itself, a path with a control character and
// anything at or below another entry's `.git` are left out, and only existing
// directories are kept. A directory that is not in a repository, or a missing
// git, is not an error: the result is nil, nil. Any other failure of the
// listing (a timeout, a lock, a refusal over ownership, a git too old for
// `-z`) is returned: a caller must not read it as "no worktrees", because that
// would make every projection it knows look orphaned. It is not memoised
// (memo.go caches plain repositories only): a worktree added or removed is seen
// on the next call.
func LinkedWorktrees(dir string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), worktreeListTimeout)
	defer cancel()
	cmd := procattr.Hide(exec.CommandContext(ctx, "git", "-C", dir, "worktree", "list", "--porcelain", "-z"))
	// -C names the repository: a hook's GIT_DIR must not. LC_ALL=C keeps the
	// "not a git repository" message below recognisable.
	cmd.Env = append(Env(), "LC_ALL=C")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || strings.Contains(stderr.String(), "not a git repository") {
			return nil, nil
		}
		msg := strings.TrimSpace(stderr.String())
		if ctx.Err() != nil {
			msg = "timed out"
		}
		if msg == "" {
			return nil, fmt.Errorf("git worktree list: %w", err)
		}
		return nil, fmt.Errorf("git worktree list: %w: %s", err, msg)
	}
	self := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		self = r
	}
	return filterLinkedWorktrees(parseWorktreeList(string(out)), self), nil
}

// parseWorktreeList returns the paths of the records of `git worktree list
// --porcelain -z` output, in order, except bare and prunable ones and paths
// holding a control character. With -z every field ends in NUL and a record
// in an empty field, so a newline in a path cannot forge a record. The first
// record (the main working tree) is kept: filterLinkedWorktrees drops the
// bound folder itself, which is the main tree only when it is bound.
func parseWorktreeList(out string) []string {
	var paths []string
	var path string
	var skip bool
	flush := func() {
		if !skip && path != "" && !hasControl(path) {
			paths = append(paths, path)
		}
		path, skip = "", false
	}
	for _, field := range strings.Split(out, "\x00") {
		if field == "" {
			flush()
			continue
		}
		key, val, _ := strings.Cut(field, " ")
		switch key {
		case "worktree":
			path = val
		case "bare", "prunable":
			skip = true
		}
	}
	flush()
	return paths
}

// hasControl reports whether s holds a control character (newline, escape...).
func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
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
