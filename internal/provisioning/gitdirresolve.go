package provisioning

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// gitdirresolve.go — where a workspace's `info/exclude` is, when `.git` is a file.
//
// A linked worktree and a submodule keep `.git` as a file holding `gitdir: <path>`.
// That file is part of the repository the user cloned, so it is untrusted input
// to a tool that writes: a `.git` pointing at `/home/user/.config` or at `../..`
// would turn the exclude write into a write anywhere. A target is accepted only
// when it is a real git directory that belongs to this workspace, by the proof
// git itself leaves on disk (worktree: the back-link; submodule: the modules
// directory of an ancestor repository). Everything else is ErrGitDirUnresolvable.

// ErrGitDirUnresolvable is the refusal for a `.git` file that does not lead to
// the git directory of its own workspace. The exclude block is a convenience, so
// the callers warn and go on. The message names the pointer file and never what
// is written in it.
var ErrGitDirUnresolvable = errors.New("provisioning: the .git file does not lead to this workspace's git directory")

// ExcludeWarning reports whether err from EnsureWorkspaceExcluded or
// RemoveWorkspaceExclusions is one the sync survives with a warning: the exclude
// file sits behind a link (D148) or cannot be resolved.
func ExcludeWarning(err error) bool {
	return errors.Is(err, ErrSymlinkDestination) || errors.Is(err, ErrGitDirUnresolvable)
}

// excludeTarget is where the exclude block of one workspace lives.
type excludeTarget struct {
	// baseDir anchors the no-follow walk of the write: the workspace for a `.git`
	// directory (so a linked `.git` is refused), the git directory for a file.
	baseDir string
	// path is the exclude file.
	path string
	// key names the block inside it: the worktree's name for a linked worktree,
	// which shares the file with the main checkout and the other worktrees, empty
	// when the file is the workspace's own.
	key string

	// gitDir and commonDir are the validated directories, for the ancestor search.
	gitDir, commonDir string
}

// excludeFile resolves the exclude file of the repository at workspaceDir the way
// git does, without running it. isRepo is false for a directory that has no
// `.git`: there is nothing to keep clean.
func excludeFile(workspaceDir string) (t excludeTarget, isRepo bool, err error) {
	dotGit := filepath.Join(workspaceDir, ".git")
	info, lerr := os.Lstat(dotGit)
	if lerr != nil {
		return t, false, nil
	}
	plain := excludeTarget{baseDir: workspaceDir, path: filepath.Join(dotGit, "info", "exclude"), commonDir: dotGit}
	switch {
	case info.Mode().IsDir():
		return plain, true, nil
	case info.Mode().IsRegular():
		t, err = resolveGitFile(workspaceDir)
		return t, err == nil, err
	}
	// A link or something else. One that leads to a directory stays the writers'
	// decision, so a refusal only comes when a write would happen (D148); a
	// broken one is no repository; one that leads to a file can never hold
	// info/exclude, and is refused like the link it is rather than failing the
	// sync with a not-a-directory error.
	st, serr := os.Stat(dotGit)
	if serr != nil {
		return t, false, nil
	}
	if st.IsDir() {
		return plain, true, nil
	}
	return t, false, safewrite.Refusal(dotGit)
}

// resolveGitFile validates the `.git` file of workspaceDir.
func resolveGitFile(workspaceDir string) (excludeTarget, error) {
	var zero excludeTarget
	abs, err := filepath.Abs(workspaceDir)
	if err != nil {
		return zero, err
	}
	dotGit := filepath.Join(abs, ".git")
	bad := func(why string) (excludeTarget, error) {
		return zero, fmt.Errorf("%w: %s (%s)", ErrGitDirUnresolvable, dotGit, why)
	}

	content, err := readPointer(dotGit)
	if err != nil {
		return bad("not a readable pointer file")
	}
	target, ok := gitdirLine(content)
	if !ok {
		return bad("no gitdir line")
	}
	if isNetworkPath(target, runtime.GOOS == "windows") {
		return bad("network path")
	}
	gitDir := resolveFrom(abs, target)
	if !isGitDir(gitDir) {
		return bad("target is not a git directory")
	}

	hasCommon, err := pointerExists(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return bad("unreadable commondir")
	}
	if hasCommon {
		return resolveWorktree(abs, gitDir, bad)
	}
	// No commondir: a submodule, whose git directory is its own. It must be one of
	// the directories git keeps under the modules directory of a repository that
	// contains the workspace, and say so itself: its core.worktree is this
	// workspace. Both are checked on the resolved path, so a link under modules/
	// cannot stand in for another git directory and a sibling submodule's
	// directory is not this workspace's.
	resolved, err := filepath.EvalSymlinks(gitDir)
	if err != nil || !isGitDir(resolved) {
		return bad("target is not a git directory")
	}
	if !underAncestorModules(abs, resolved) {
		return bad("target is not a submodule git directory of an enclosing repository")
	}
	if wt, ok := coreWorktree(resolved); !ok || !samePath(resolveFrom(resolved, wt), abs) {
		return bad("submodule git directory belongs to another workspace")
	}
	return excludeTarget{
		baseDir: resolved, path: filepath.Join(resolved, "info", "exclude"),
		gitDir: resolved, commonDir: resolved,
	}, nil
}

// resolveWorktree validates a linked worktree's git directory <common>/worktrees/<name>.
func resolveWorktree(abs, gitDir string, bad func(string) (excludeTarget, error)) (excludeTarget, error) {
	// The back-link git writes in the worktree's git directory names this
	// workspace's .git: a directory some other repository merely has is not it.
	back, err := readPointer(filepath.Join(gitDir, "gitdir"))
	if err != nil {
		return bad("worktree has no readable back-link")
	}
	backPath := resolveFrom(gitDir, strings.TrimSpace(firstLine(back)))
	if !samePath(backPath, filepath.Join(abs, ".git")) {
		return bad("worktree back-link names another workspace")
	}
	rel, err := readPointer(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return bad("unreadable commondir")
	}
	relLine := strings.TrimSpace(firstLine(rel))
	if relLine == "" || isNetworkPath(relLine, runtime.GOOS == "windows") {
		return bad("commondir is empty or a network path")
	}
	common := resolveFrom(gitDir, relLine)
	if !isGitDir(common) {
		return bad("commondir is not a git directory")
	}
	if !samePath(filepath.Dir(gitDir), filepath.Join(common, "worktrees")) {
		return bad("worktree git directory is not under commondir/worktrees")
	}
	key := filepath.Base(gitDir)
	if key == "" || key == "." || strings.ContainsAny(key, "\r\n") {
		return bad("worktree name cannot key a block")
	}
	return excludeTarget{
		baseDir: common, path: filepath.Join(common, "info", "exclude"), key: key,
		gitDir: gitDir, commonDir: common,
	}, nil
}

// underAncestorModules reports whether gitDir, already resolved, lies under the
// resolved modules directory of the git directory of a repository that contains
// abs, which is where git puts a submodule's git directory.
func underAncestorModules(abs, gitDir string) bool {
	for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
		if parents, ok := ancestorGitDirs(dir); ok {
			for _, p := range parents {
				if modules, err := filepath.EvalSymlinks(filepath.Join(p, "modules")); err == nil && within(modules, gitDir) {
					return true
				}
			}
		}
		if filepath.Dir(dir) == dir {
			return false
		}
	}
}

// ancestorGitDirs returns the git directories (its own and the shared one) of the
// repository rooted at dir, if there is one.
func ancestorGitDirs(dir string) ([]string, bool) {
	dotGit := filepath.Join(dir, ".git")
	info, err := os.Lstat(dotGit)
	if err != nil {
		return nil, false
	}
	switch {
	case info.Mode().IsDir():
		return []string{dotGit}, isGitDir(dotGit)
	case info.Mode().IsRegular():
		t, err := resolveGitFile(dir)
		if err != nil {
			return nil, false
		}
		return []string{t.gitDir, t.commonDir}, true
	}
	return nil, false
}

// gitdirLine returns the path of the `gitdir:` line of a `.git` file.
func gitdirLine(content string) (string, bool) {
	target, ok := strings.CutPrefix(strings.TrimSpace(firstLine(content)), "gitdir:")
	target = strings.TrimSpace(target)
	return target, ok && target != ""
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// isNetworkPath reports whether p, as git wrote it, names a UNC or other
// volume-less network location on Windows (`\\host\share`, `//host/share`): a
// repository's `.git` must not send a write to another machine.
func isNetworkPath(p string, windows bool) bool {
	if !windows || len(p) < 2 {
		return false
	}
	sep := func(c byte) bool { return c == '\\' || c == '/' }
	return sep(p[0]) && sep(p[1])
}

// resolveFrom joins a path git wrote (slashes, absolute or relative to base).
func resolveFrom(base, p string) string {
	p = filepath.FromSlash(p)
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// isGitDir reports whether dir is a git directory: a directory holding a regular
// file HEAD.
func isGitDir(dir string) bool {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return false
	}
	st, err := os.Lstat(filepath.Join(dir, "HEAD"))
	return err == nil && st.Mode().IsRegular()
}

// samePath compares two paths as written, then as resolved on disk (a temporary
// directory behind a symlink is spelled both ways by git and by the caller).
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if pathEq(a, b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && pathEq(ra, rb)
}

func pathEq(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// within reports whether child lies strictly below parent, both resolved.
func within(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// coreWorktree returns the core.worktree a git directory's config declares.
func coreWorktree(gitDir string) (string, bool) {
	content, err := readCapped(filepath.Join(gitDir, "config"), maxGitConfigSize)
	if err != nil {
		return "", false
	}
	var value string
	inCore := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || line[0] == '#' || line[0] == ';':
		case line[0] == '[':
			inCore = strings.EqualFold(strings.TrimSpace(strings.Trim(line, "[]")), "core")
		case inCore:
			k, v, ok := strings.Cut(line, "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), "worktree") {
				value = strings.Trim(strings.TrimSpace(v), `"`)
			}
		}
	}
	return value, value != ""
}

// maxGitPointerSize bounds the pointer files read (`.git`, `commondir`, the
// worktree back-link): a real one is a line, so a larger file is not one.
// maxGitConfigSize bounds a submodule's config, read for core.worktree.
const (
	maxGitPointerSize = 64 * 1024
	maxGitConfigSize  = 1024 * 1024
)

// pointerExists reports whether path exists at all (a link counts: readPointer
// then refuses it).
func pointerExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	}
	return false, err
}

// readPointer reads a pointer file: readCapped at the pointer size.
func readPointer(path string) (string, error) { return readCapped(path, maxGitPointerSize) }

// readCapped reads a small regular file without following a link. Its errors
// carry no content.
func readCapped(path string, limit int64) (string, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Size() > limit {
		return "", errors.New("not a small regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit))
	return string(b), err
}
