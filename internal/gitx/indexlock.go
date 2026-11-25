package gitx

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// indexLockRe is git's refusal to take the index lock (LC_ALL=C, see gitEnv): the
// text names the lock file whatever the platform's path separators.
var indexLockRe = regexp.MustCompile(`Unable to create '([^']*index\.lock)': File exists`)

// indexLockHint turns git's raw "Unable to create '.../index.lock': File exists"
// into a sentence for the operator, to append to the error: it names the file and
// says when it is safe to remove. "" when out is not that refusal and the repository
// holds no index.lock: git 2.43 does not always print the refusal (a failed stash or
// merge says only "fatal: stash failed", or nothing), so when the text is absent the
// repository's own lock file is looked up after the failure. The lock may
// belong to a git the operator is running in that clone, so the server never removes
// it on a write (RemoveStaleIndexLock is the one removal, at start-up).
func indexLockHint(dir, out string) string {
	lock := ""
	if m := indexLockRe.FindStringSubmatch(out); m != nil {
		lock = m[1]
	} else {
		lock = HasIndexLock(dir)
	}
	if lock == "" {
		return ""
	}
	return fmt.Sprintf("\ncartographer: %s is a lock left by a git that did not finish: if no git process runs in %s, remove that file", lock, dir)
}

// shellQuote makes s one word of a command the operator pastes: untouched when it
// holds only safe characters, otherwise single-quoted (POSIX) or double-quoted
// (Windows, where single quotes are not quoting).
func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-:+=@%\\", r))
	}) < 0 {
		return s
	}
	if runtime.GOOS == "windows" {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// abortRebaseIfStarted aborts the rebase a refused pull or rebase may have begun,
// best effort. When git could not take the index lock it usually stopped before
// creating any rebase state ("could not detach HEAD"): then there is nothing to
// abort, and `rebase --abort` would only fail on the same lock. Only a state
// directory actually present is aborted, so a refusal never runs it blindly.
// When git replayed commits and then hit the lock, the abort fails on the lock
// too: the returned text, to append to the lock hint, says the rebase is still in
// progress and how to end it once the lock is gone ("" when none remains).
func abortRebaseIfStarted(dir string) string {
	if _, ok := RebaseInProgress(dir); !ok {
		return ""
	}
	_, _ = runGit(dir, "rebase", "--abort")
	if _, ok := RebaseInProgress(dir); !ok {
		return ""
	}
	return fmt.Sprintf("\ncartographer: a rebase is also left in progress in %s: after removing the lock, run `git -C %s rebase --abort`", dir, shellQuote(dir))
}

// RemoveStaleIndexLock removes the index.lock a crash, a power loss or a kill left
// in the repository at dir, and returns its path and age (path "" when there was
// none). The caller must hold the KB's exclusive process lock and run it before
// serving: only then no git of this server runs in the clone, so a lock found is
// a leftover. dir must be the root of its own repository or linked worktree (a
// `.git` directory or file in it), never a folder inside a larger one, whose lock
// is not ours to clear: a bogus or empty `.git` there makes git walk up to the
// enclosing repository, so the top level git reports must be dir itself. The lock
// path comes from git itself (rev-parse --git-path), so a worktree or a `.git`
// file pointing elsewhere resolves to the right git dir.
// Only a regular file is removed.
func RemoveStaleIndexLock(dir string) (path string, age time.Duration, err error) {
	lock, fi := findIndexLock(dir)
	if lock == "" {
		return "", 0, nil
	}
	age = time.Since(fi.ModTime())
	if err := os.Remove(lock); err != nil {
		return lock, age, fmt.Errorf("remove %s: %w", lock, err)
	}
	return lock, age, nil
}

// HasIndexLock returns the path of the index.lock file present in the repository
// at dir, "" when there is none. It removes nothing and applies the guards of
// RemoveStaleIndexLock (dir is the root of its own repository).
func HasIndexLock(dir string) string {
	lock, _ := findIndexLock(dir)
	return lock
}

// findIndexLock is the regular index.lock file of the repository rooted at dir,
// with its stat; "" when dir is not such a root or there is no such file.
func findIndexLock(dir string) (string, os.FileInfo) {
	if _, statErr := os.Lstat(filepath.Join(dir, ".git")); statErr != nil {
		return "", nil
	}
	if !isOwnToplevel(dir) {
		return "", nil
	}
	lock := indexLockPath(dir, nil)
	if lock == "" {
		return "", nil
	}
	fi, statErr := os.Lstat(lock)
	if statErr != nil || !fi.Mode().IsRegular() {
		return "", nil
	}
	return lock, fi
}

// isOwnToplevel reports whether the repository git finds at dir has dir as its
// top level, symlinks resolved on both sides (Windows and macOS report the same
// folder under different spellings, and Windows paths compare case-insensitively).
func isOwnToplevel(dir string) bool {
	out, err := runGit(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	top, err := filepath.EvalSymlinks(filepath.FromSlash(strings.TrimSpace(out)))
	if err != nil {
		return false
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	top, want = filepath.Clean(top), filepath.Clean(want)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(top, want)
	}
	return top == want
}

// RestoreWithoutIndex puts the working tree back to HEAD while a foreign
// index.lock refuses every index write: untracked files are removed, a modified
// or deleted tracked file is rewritten from HEAD's blob (git cat-file and status
// take no lock). It handles only the plain states a write leaves (modified,
// deleted, untracked) and refuses the rest, which a reset must settle.
func RestoreWithoutIndex(dir string) error {
	out, err := runGit(dir, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	type step struct {
		path      string
		untracked bool
	}
	var steps []step
	for _, e := range strings.Split(out, "\x00") {
		if e == "" {
			continue
		}
		if len(e) < 4 {
			return fmt.Errorf("unexpected status entry %q", e)
		}
		xy, path := e[:2], e[3:]
		switch {
		case xy == "??":
			steps = append(steps, step{path, true})
		case xy == " M" || xy == " D" || xy == " T":
			steps = append(steps, step{path, false})
		default:
			return fmt.Errorf("status %q of %s needs a reset", xy, path)
		}
	}
	for _, st := range steps {
		abs := filepath.Join(dir, filepath.FromSlash(st.path))
		if fi, err := os.Lstat(abs); err == nil && fi.Mode()&os.ModeSymlink != 0 && !st.untracked {
			return fmt.Errorf("%s is a symlink", st.path)
		}
		if st.untracked {
			if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
				return err
			}
			for d := filepath.Dir(abs); d != dir && strings.HasPrefix(d, dir); d = filepath.Dir(d) {
				if os.Remove(d) != nil { // not empty: stop
					break
				}
			}
			continue
		}
		blob, err := runGit(dir, "cat-file", "blob", "HEAD:"+st.path)
		if err != nil {
			return fmt.Errorf("git cat-file %s: %w", st.path, err)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if fi, err := os.Stat(abs); err == nil {
			mode = fi.Mode().Perm()
		}
		if err := os.WriteFile(abs, []byte(blob), mode); err != nil {
			return err
		}
	}
	return nil
}

// AfterStage runs in Commit once the changes are staged and before git commit
// takes the index lock: a seam for the tests that need a foreign lock to appear
// in that window (#2241). Nil in production.
var AfterStage func(dir string)

// WhileWaitingForIndexLock runs on each pause of the restore that waits for a
// foreign index.lock to clear: a seam for the tests that clear the lock by an
// event of the wait itself instead of a timer (#2361). Nil in production.
var WhileWaitingForIndexLock func()

// IndexLockWait is how long a restore after a failed commit waits for a foreign
// index.lock to clear before giving up (#2241); IndexLockPoll is the pause
// between two attempts. Variables so a test shortens them.
var (
	IndexLockWait = 3 * time.Second
	IndexLockPoll = 50 * time.Millisecond
)

// DirtyPaths lists the paths git status reports in dir (staged, modified,
// deleted or untracked, renames split in two), slash-separated.
func DirtyPaths(dir string) ([]string, error) {
	out, err := runGit(dir, "status", "--porcelain", "-z", "--untracked-files=all", "--no-renames")
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}
	var paths []string
	for _, e := range strings.Split(out, "\x00") {
		if len(e) >= 4 {
			paths = append(paths, e[3:])
		}
	}
	return paths, nil
}

// RestorePaths puts exactly paths back to HEAD: unstaged, then rewritten from
// HEAD when HEAD holds them, removed when it does not. It needs the index lock
// free (the unstaging writes the index) and fails otherwise, touching nothing.
func RestorePaths(dir string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if lock := HasIndexLock(dir); lock != "" {
		return fmt.Errorf("%s is held", lock)
	}
	args := append([]string{"--literal-pathspecs", "reset", "-q", "HEAD", "--"}, paths...)
	if out, err := runGit(dir, args...); err != nil {
		return fmt.Errorf("git reset: %w: %s", err, out)
	}
	for _, p := range paths {
		abs := filepath.Join(dir, filepath.FromSlash(p))
		if fi, err := os.Lstat(abs); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", p)
		}
		if _, err := runGit(dir, "cat-file", "-e", "HEAD:"+p); err == nil {
			if out, err := runGit(dir, append(noEOL, "--literal-pathspecs", "checkout", "HEAD", "--", p)...); err != nil {
				return fmt.Errorf("git checkout %s: %w: %s", p, err, out)
			}
			continue
		}
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return err
		}
		for d := filepath.Dir(abs); d != dir && strings.HasPrefix(d, dir); d = filepath.Dir(d) {
			if os.Remove(d) != nil {
				break
			}
		}
	}
	return nil
}
