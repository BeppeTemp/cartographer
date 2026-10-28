package provisioning

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// kbTracked is the set of files git tracks in a KB clone, as slash paths
// relative to the KB root. The server builds skill, agent, hook and mcp
// artifacts only from these: an untracked file or a git-ignored one (a
// __pycache__ left by running a skill's script) is never distributed (#1325).
// The zero value (nil set) is a KB that is not a git clone: nothing is filtered.
type kbTracked struct{ set map[string]bool }

// kbTrackedFilter lists what git tracks in kbRoot. A directory without a .git
// entry (a plain folder, a test fixture) is not filtered; when git cannot
// answer, it returns an error and the caller withholds that KB's artifacts
// rather than distributing the working tree (D1325). The listing is cached per
// KB root and revalidated against the index file's mtime and size, so a build
// that reads many artifacts runs git once and manifest and served files agree.
func kbTrackedFilter(kbRoot string) (kbTracked, error) {
	gitPath := filepath.Join(kbRoot, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil {
		return kbTracked{}, nil
	}
	key := trackedIndexKey(gitPath, info)
	if key != "" {
		trackedCache.Lock()
		e, ok := trackedCache.m[kbRoot]
		trackedCache.Unlock()
		if ok && e.key == key {
			return kbTracked{set: e.set}, nil
		}
	}
	out, err := exec.Command("git", "-C", kbRoot, "ls-files", "-z", "--", "skills", "agents", "hooks", "mcp").Output()
	if err != nil {
		return kbTracked{}, fmt.Errorf("provisioning: list tracked files in %s: %w", kbRoot, err)
	}
	set := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			set[filepath.ToSlash(p)] = true
		}
	}
	if key != "" && trackedIndexKey(gitPath, info) == key {
		trackedCache.Lock()
		trackedCache.m[kbRoot] = trackedEntry{key: key, set: set}
		trackedCache.Unlock()
	}
	return kbTracked{set: set}, nil
}

type trackedEntry struct {
	key string
	set map[string]bool
}

var trackedCache = struct {
	sync.Mutex
	m map[string]trackedEntry
}{m: map[string]trackedEntry{}}

// trackedIndexKey identifies the state of the git index of the clone whose
// ".git" entry is gitPath (a directory, or a file pointing at the git dir of a
// worktree). Empty when it cannot be told: the caller then does not cache.
func trackedIndexKey(gitPath string, info os.FileInfo) string {
	dir := gitPath
	if !info.IsDir() {
		raw, err := os.ReadFile(gitPath)
		if err != nil {
			return ""
		}
		line := strings.TrimSpace(string(raw))
		if !strings.HasPrefix(line, "gitdir:") {
			return ""
		}
		dir = strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(filepath.Dir(gitPath), dir)
		}
	}
	st, err := os.Stat(filepath.Join(dir, "index"))
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d/%d", st.ModTime().UnixNano(), st.Size())
}

// has reports whether rel (relative to the KB root) is tracked.
func (t kbTracked) has(rel string) bool { return t.set == nil || t.set[rel] }

// hasAny reports whether any tracked file lies under the prefix (ends in "/").
func (t kbTracked) hasAny(prefix string) bool {
	if t.set == nil {
		return true
	}
	for p := range t.set {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// under returns the include filter for files relative to the directory dir
// (relative to the KB root), or nil when nothing is filtered.
func (t kbTracked) under(dir string) func(rel string) bool {
	if t.set == nil {
		return nil
	}
	return func(rel string) bool { return t.set[dir+"/"+rel] }
}
