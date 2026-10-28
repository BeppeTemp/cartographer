package provisioning

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// kbTracked is the set of files git tracks in a KB clone, as slash paths
// relative to the KB root. The server builds skill, agent, hook and mcp
// artifacts only from these: an untracked file or a git-ignored one (a
// __pycache__ left by running a skill's script) is never distributed (#1325).
// The zero value (nil set) is a KB that is not a git clone: nothing is filtered.
type kbTracked struct{ set map[string]bool }

// kbTrackedFilter lists what git tracks in kbRoot. A directory without a .git
// entry (a plain folder, a test fixture) is not filtered; when git cannot
// answer, the build fails rather than distributing the working tree.
func kbTrackedFilter(kbRoot string) (kbTracked, error) {
	if _, err := os.Lstat(filepath.Join(kbRoot, ".git")); err != nil {
		return kbTracked{}, nil
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
	return kbTracked{set: set}, nil
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
