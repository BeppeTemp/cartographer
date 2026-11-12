package provisioning

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// trackedPathsFn is indirected for tests: it asks git which of a few paths
// (taken literally, under any capitalisation: a case-insensitive filesystem
// writes CLAUDE.md into a tracked claude.md) a workspace tracks, and a test makes
// it fail.
var trackedPathsFn = gitx.TrackedPathsFold

// checkRepoFn tells a workspace in no repository from a git that refused to
// answer; indirected for tests.
var checkRepoFn = gitx.CheckRepo

// InstructionsDestTracked reports whether the project-scope instructions block
// of opts is written into a file git tracks (#1897), or into a place the client
// cannot show is not shared: the destination itself (honouring
// opts.InstructionsRel) or the file it resolves to through any symlink, in the
// leaf or in a directory above it. The block of a tracked file is shared with
// everyone who clones the repository, so the client sets
// ApplyOptions.WithholdLocalPaths from this answer: the "Local paths" table and
// the paths the KB's own instruction text cites are absolute paths of this
// machine and have no place there.
//
// It is decided once per projection, by the caller, and handed to Apply and to
// the status diff alike, so the recorded PathsSectionHash agrees with the one
// status recomputes. It asks git about the one or two paths concerned, never
// for the whole index. Any other scope, an empty destination and a workspace
// that is in no repository answer false: the block keeps its paths, as before.
// A repository git cannot read (dubious ownership, a corrupt index, git
// missing, a lock) answers true: git was asked and did not say, and the
// unsafe side is writing this machine's paths into a file that may be shared. A destination that resolves outside the
// workspace answers true without running git: the workspace's tracked set says
// nothing about the file it lands in (a subdirectory's `CLAUDE.md ->
// ../AGENTS.md`), so the paths are withheld. So is one that cannot be
// resolved (a dangling or looping link). So does one that lands inside a
// nested repository or a submodule (a `.git` between the workspace and the
// file): the workspace's git does not list those files, so it cannot say. The
// question is case-folded: a repository tracking `claude.md` shares the file the
// client writes as `CLAUDE.md` on a case-insensitive filesystem.
func InstructionsDestTracked(opts ApplyOptions) bool {
	if opts.Scope != contract.ScopeProject || opts.BaseDir == "" {
		return false
	}
	rel := instructionsDest(opts)
	if rel == "" {
		return false
	}
	rel = filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	resolved, outside, ok := resolveInWorkspace(opts.BaseDir, rel)
	if !ok {
		return false
	}
	if outside || inNestedRepo(opts.BaseDir, resolved) {
		return true
	}
	paths := []string{rel}
	if resolved != rel {
		paths = append(paths, resolved)
	}
	tracked, err := trackedPathsFn(opts.BaseDir, paths...)
	if err != nil {
		// Only a workspace in no repository keeps the table; any other
		// failure of git withholds it.
		inRepo, cerr := checkRepoFn(opts.BaseDir)
		return cerr != nil || inRepo
	}
	for _, p := range paths {
		if tracked[p] {
			return true
		}
	}
	return false
}

// inNestedRepo reports whether the slash path rel, relative to base, goes
// through a directory below base that holds its own `.git` (a directory, or the
// file of a submodule or worktree): another repository, whose files the
// repository of base does not list. base's own `.git` is the repository being
// asked and does not count. A base that cannot be resolved answers false: the
// caller has already failed on it.
func inNestedRepo(base, rel string) bool {
	root, err := filepath.EvalSymlinks(base)
	if err != nil {
		return false
	}
	dir := root
	parts := strings.Split(rel, "/")
	for _, part := range parts[:len(parts)-1] {
		dir = filepath.Join(dir, part)
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
	}
	return false
}

// resolveInWorkspace follows every symlink of rel (slash path under base),
// whatever component it is in, and returns where it lands as a slash path
// relative to base. outside is true when it lands outside base or cannot be
// followed to an end, and resolved is then empty. ok is false only when base
// itself cannot be resolved. A part of rel that does not exist yet is kept as
// written under the deepest component that does.
func resolveInWorkspace(base, rel string) (resolved string, outside, ok bool) {
	root, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", false, false
	}
	landed, err := evalExisting(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", true, true
	}
	out, err := filepath.Rel(root, landed)
	if err != nil || out == ".." || strings.HasPrefix(out, ".."+string(filepath.Separator)) {
		return "", true, true
	}
	return filepath.ToSlash(out), false, true
}

// evalExisting is filepath.EvalSymlinks for a path whose tail may not exist:
// the existing prefix is resolved and the rest is appended as written. A link
// whose target is missing is an error, not a tail, because where a write
// through it lands is not known.
func evalExisting(p string) (string, error) {
	var tail []string
	for {
		resolved, err := filepath.EvalSymlinks(p)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if _, lerr := os.Lstat(p); lerr == nil {
			return "", err // a dangling link
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", err
		}
		tail = append(tail, filepath.Base(p))
		p = parent
	}
}
