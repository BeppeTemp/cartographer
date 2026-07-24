// Package gitx provides a git wrapper via os/exec for the Agentic Wiki.
// No external git library: only CLI invocation.
package gitx

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ErrNothingToCommit signals that there was nothing to commit.
var ErrNothingToCommit = errors.New("nothing to commit")

// IsRepo checks whether the directory is a valid git repository.
func IsRepo(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

// Clone clones remote into dest ("git clone <remote> <dest>"). dest must not
// yet exist (or must be empty); git creates it. env carries extra
// per-KB variables (e.g. GIT_SSH_COMMAND) layered on top of the process
// environment — see runGitEnv.
func Clone(remote, dest string, env ...string) error {
	cmd := exec.Command("git", "clone", remote, dest)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone %s %s: %w: %s", remote, dest, err, out)
	}
	return nil
}

// Init initializes a git repository in the directory, if one does not already exist.
// Configures merge.conflictStyle=zdiff3 locally.
func Init(dir string) error {
	if !IsRepo(dir) {
		if out, err := runGit(dir, "init"); err != nil {
			return fmt.Errorf("git init: %w: %s", err, out)
		}
	}
	// Configure zdiff3 for more readable diffs during merge conflicts.
	if out, err := runGit(dir, "config", "merge.conflictStyle", "zdiff3"); err != nil {
		return fmt.Errorf("git config merge.conflictStyle: %w: %s", err, out)
	}
	return nil
}

// Commit runs "git add -A" followed by a commit with the given message and
// author identity. The committer identity is taken from env (typically
// GIT_COMMITTER_NAME/GIT_COMMITTER_EMAIL, assembled per-KB by the caller);
// if env carries no committer variables, git falls back to its own config/
// process environment. If there is nothing to commit, returns
// ErrNothingToCommit (not a fatal error).
func Commit(dir, message, authorName, authorEmail string, env ...string) error {
	if out, err := runGitEnv(dir, env, "add", "-A"); err != nil {
		return fmt.Errorf("git add -A: %w: %s", err, out)
	}
	return commit(dir, message, authorName, authorEmail, env...)
}

// CommitPaths creates one commit containing changes to paths only. It uses a
// temporary index seeded from HEAD, so pre-existing staged or unstaged work in
// the caller's real index is neither changed nor accidentally committed.
func CommitPaths(dir string, paths []string, message, authorName, authorEmail string, env ...string) error {
	if len(paths) == 0 {
		return ErrNothingToCommit
	}

	index, err := os.CreateTemp("", "cartographer-git-index-*")
	if err != nil {
		return fmt.Errorf("create temporary git index: %w", err)
	}
	indexPath := index.Name()
	if err := index.Close(); err != nil {
		os.Remove(indexPath)
		return fmt.Errorf("close temporary git index: %w", err)
	}
	// Git expects a missing index file, not an empty one.
	if err := os.Remove(indexPath); err != nil {
		return fmt.Errorf("remove temporary git index: %w", err)
	}
	defer os.Remove(indexPath)

	commitEnv := append(append([]string{}, env...), "GIT_INDEX_FILE="+indexPath)
	if out, err := runGitEnv(dir, commitEnv, "read-tree", "HEAD"); err != nil {
		return fmt.Errorf("git read-tree HEAD: %w: %s", err, out)
	}
	args := append([]string{"add", "--"}, paths...)
	if out, err := runGitEnv(dir, commitEnv, args...); err != nil {
		return fmt.Errorf("git add paths: %w: %s", err, out)
	}
	return commit(dir, message, authorName, authorEmail, commitEnv...)
}

func commit(dir, message, authorName, authorEmail string, env ...string) error {

	args := []string{
		"commit",
		"--author", fmt.Sprintf("%s <%s>", authorName, authorEmail),
		"-m", message,
	}
	out, err := runGitEnv(dir, env, args...)
	if err != nil {
		// git commit exits with code 1 when there is nothing to commit.
		if strings.Contains(out, "nothing to commit") ||
			strings.Contains(out, "nothing added to commit") {
			return ErrNothingToCommit
		}
		return fmt.Errorf("git commit: %w: %s", err, out)
	}
	return nil
}

// HeadSHA returns the SHA of the HEAD commit.
func HeadSHA(dir string) (string, error) {
	out, err := runGit(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w: %s", err, out)
	}
	return strings.TrimSpace(out), nil
}

// runGit runs a git command in the specified directory, inheriting the
// process environment unchanged, and returns the combined output.
func runGit(dir string, args ...string) (string, error) {
	return runGitEnv(dir, nil, args...)
}

// runGitEnv runs a git command in the specified directory with extra
// environment variables layered on top of the process environment
// (cmd.Env = append(os.Environ(), env...)): entries in env take precedence
// over the process environment (later entries win on duplicate keys), the
// inverse of setupGitSSH's "process environment wins" rule for the global
// GIT_SSH_COMMAND fallback — see docs/decisions.md D46. A nil/empty env
// behaves exactly like runGit.
func runGitEnv(dir string, env []string, args ...string) (string, error) {
	fullArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", fullArgs...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ErrConflict signals a merge/rebase conflict.
var ErrConflict = errors.New("merge conflict")

// ErrRebaseConflict signals that a pull --rebase hit a conflict and was aborted.
var ErrRebaseConflict = errors.New("gitx: rebase conflict")

// RebaseConflictError carries structured details about a rebase conflict.
// It satisfies errors.Is(err, ErrRebaseConflict) so existing callers are unaffected.
type RebaseConflictError struct {
	Files     []string // git-relative paths of unmerged files (e.g. "data/notes/c.md")
	LocalSHA  string   // HEAD SHA captured before the rebase started
	RemoteSHA string   // SHA of <remote>/<branch> after fetch
	Remote    string   // remote name (e.g. "origin")
	Branch    string   // branch name (e.g. "main")
}

// Error implements the error interface.
func (e *RebaseConflictError) Error() string {
	return fmt.Sprintf("%s: pull --rebase %s %s: %d conflicting file(s): %s",
		ErrRebaseConflict.Error(), e.Remote, e.Branch, len(e.Files), strings.Join(e.Files, ", "))
}

// Is makes errors.Is(err, ErrRebaseConflict) return true for *RebaseConflictError values,
// preserving backward compatibility with all callers that use the sentinel.
func (e *RebaseConflictError) Is(target error) bool {
	return target == ErrRebaseConflict
}

// PullRebaseAutostash runs "git pull --rebase --autostash <remote> <branch>".
// If the operation encounters a rebase conflict, it collects the list of unmerged
// files and the relevant SHAs, runs "git rebase --abort" (best-effort), and returns
// a *RebaseConflictError. errors.Is(err, ErrRebaseConflict) remains true for it.
// Other errors (e.g. remote unreachable) are returned as-is without abort.
// env carries extra per-KB variables (e.g. GIT_SSH_COMMAND) — see runGitEnv.
func PullRebaseAutostash(dir, remote, branch string, env ...string) error {
	if remote == "" {
		remote = "origin"
	}

	// Capture local HEAD before the rebase so callers can record the pre-conflict state.
	localSHA, _ := HeadSHA(dir)

	out, err := runGitEnv(dir, env, "pull", "--rebase", "--autostash", remote, branch)
	if err == nil {
		return nil
	}
	// Detect rebase conflict: output contains "CONFLICT" or "rebase" stall markers.
	if strings.Contains(out, "CONFLICT") ||
		strings.Contains(out, "could not apply") ||
		strings.Contains(out, "error: could not") {

		// Collect unmerged files before aborting.
		var conflictFiles []string
		if filesOut, ferr := runGit(dir, "diff", "--name-only", "--diff-filter=U"); ferr == nil {
			for _, f := range strings.Split(strings.TrimSpace(filesOut), "\n") {
				if f = strings.TrimSpace(f); f != "" {
					conflictFiles = append(conflictFiles, f)
				}
			}
		}

		// Capture remote SHA.
		var remoteSHA string
		if shaOut, serr := runGit(dir, "rev-parse", remote+"/"+branch); serr == nil {
			remoteSHA = strings.TrimSpace(shaOut)
		}

		// Best-effort abort so the working tree is left clean.
		_, _ = runGit(dir, "rebase", "--abort")

		return &RebaseConflictError{
			Files:     conflictFiles,
			LocalSHA:  localSHA,
			RemoteSHA: remoteSHA,
			Remote:    remote,
			Branch:    branch,
		}
	}
	return fmt.Errorf("git pull --rebase --autostash %s %s: %w: %s", remote, branch, err, out)
}

// Branch returns the current branch name, or "" if detached HEAD.
func Branch(dir string) (string, error) {
	out, err := runGit(dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		// detached HEAD — not an error for the caller
		return "", nil
	}
	return strings.TrimSpace(out), nil
}

// CreateBranch creates and checks out a new branch from the current HEAD.
func CreateBranch(dir, name string) error {
	out, err := runGit(dir, "checkout", "-b", name)
	if err != nil {
		return fmt.Errorf("git checkout -b %s: %w: %s", name, err, out)
	}
	return nil
}

// Checkout checks out an existing branch.
func Checkout(dir, branch string) error {
	out, err := runGit(dir, "checkout", branch)
	if err != nil {
		return fmt.Errorf("git checkout %s: %w: %s", branch, err, out)
	}
	return nil
}

// Fetch runs git fetch for a given remote (default "origin"). Returns error on
// failure. env carries extra per-KB variables (e.g. GIT_SSH_COMMAND) — see runGitEnv.
func Fetch(dir, remote string, env ...string) error {
	if remote == "" {
		remote = "origin"
	}
	out, err := runGitEnv(dir, env, "fetch", remote)
	if err != nil {
		return fmt.Errorf("git fetch %s: %w: %s", remote, err, out)
	}
	return nil
}

// Push pushes the current branch to the remote. Never force-pushes.
// Returns error on non-fast-forward (caller should rebase and retry).
// env carries extra per-KB variables (e.g. GIT_SSH_COMMAND) — see runGitEnv.
func Push(dir, remote, branch string, env ...string) error {
	out, err := runGitEnv(dir, env, "push", remote, branch)
	if err != nil {
		return fmt.Errorf("git push %s %s: %w: %s", remote, branch, err, out)
	}
	return nil
}

// Rebase rebases current branch onto target (e.g. "origin/main").
// Returns ErrConflict if there are unresolved conflicts.
func Rebase(dir, onto string) error {
	out, err := runGit(dir, "rebase", onto)
	if err != nil {
		if strings.Contains(out, "CONFLICT") {
			return ErrConflict
		}
		return fmt.Errorf("git rebase %s: %w: %s", onto, err, out)
	}
	return nil
}

// RebaseAbort aborts an in-progress rebase.
func RebaseAbort(dir string) error {
	out, err := runGit(dir, "rebase", "--abort")
	if err != nil {
		return fmt.Errorf("git rebase --abort: %w: %s", err, out)
	}
	return nil
}

// ShowFile returns the contents of path at the given git ref ("git show <ref>:<path>").
// Used to materialise the "ours"/"theirs" sides of a conflict by content, which avoids
// the inverted --ours/--theirs semantics that apply during a rebase.
func ShowFile(dir, ref, path string) (string, error) {
	out, err := runGit(dir, "show", ref+":"+path)
	if err != nil {
		return "", fmt.Errorf("git show %s:%s: %w: %s", ref, path, err, out)
	}
	return out, nil
}

// MergeNoCommitNoFF runs "git merge --no-commit --no-ff <ref>" and reports whether the
// merge produced conflicts. A clean merge (conflicted=false) is left staged but
// uncommitted; a conflicting merge (conflicted=true) leaves the conflicting files
// unmerged for the caller to resolve. Any other failure is returned as err.
func MergeNoCommitNoFF(dir, ref string) (conflicted bool, err error) {
	out, gerr := runGit(dir, "merge", "--no-commit", "--no-ff", ref)
	if gerr == nil {
		return false, nil
	}
	if strings.Contains(out, "CONFLICT") || strings.Contains(out, "Automatic merge failed") {
		return true, nil
	}
	return false, fmt.Errorf("git merge --no-commit --no-ff %s: %w: %s", ref, gerr, out)
}

// MergeAbort aborts an in-progress merge ("git merge --abort").
func MergeAbort(dir string) error {
	out, err := runGit(dir, "merge", "--abort")
	if err != nil {
		return fmt.Errorf("git merge --abort: %w: %s", err, out)
	}
	return nil
}

// UnmergedFiles returns the git-relative paths of files with unresolved conflicts
// (git diff --name-only --diff-filter=U).
func UnmergedFiles(dir string) ([]string, error) {
	out, err := runGit(dir, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, fmt.Errorf("git diff --diff-filter=U: %w: %s", err, out)
	}
	var files []string
	for _, f := range strings.Split(strings.TrimSpace(out), "\n") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// AddPath stages a single path ("git add -- <path>").
func AddPath(dir, path string) error {
	out, err := runGit(dir, "add", "--", path)
	if err != nil {
		return fmt.Errorf("git add %s: %w: %s", path, err, out)
	}
	return nil
}

// FileChange represents a changed file in a diff.
type FileChange struct {
	Status  string // "A", "M", "D", "R"
	Path    string
	OldPath string // set for renames; Path is the destination
}

// CommitChanges contains the files changed by one commit in git log order
// (newest first). Files retain git's name-status order.
type CommitChanges struct {
	SHA     string
	At      time.Time
	Author  string
	Subject string
	Files   []FileChange
}

// LogNameStatus returns commits since the supplied instant with their changed
// files. Renames are normalised to status "R", with OldPath and Path holding
// the source and destination respectively. A directory that is not a git
// repository, or a repository with no commits in range, returns an empty slice.
func LogNameStatus(dir string, since time.Time) ([]CommitChanges, error) {
	if !IsRepo(dir) {
		return []CommitChanges{}, nil
	}
	if _, err := HeadSHA(dir); err != nil {
		// A freshly initialised repository has no HEAD yet. It has no history to
		// report, just like a repository whose commits all predate since.
		return []CommitChanges{}, nil
	}

	// The record separator gives each commit an unambiguous boundary while the
	// NUL-separated header keeps subjects with spaces intact. --name-status is
	// deliberately left line-oriented to match git's ordinary path format.
	format := "%x1e%H%x00%aI%x00%an%x00%s"
	out, err := runGitEnv(dir, nil, "log", "--since="+since.Format(time.RFC3339), "--name-status", "-M", "--pretty=format:"+format)
	if err != nil {
		return nil, fmt.Errorf("git log --name-status: %w: %s", err, out)
	}

	commits := []CommitChanges{}
	for _, record := range strings.Split(out, "\x1e") {
		if record == "" {
			continue
		}
		head := strings.SplitN(record, "\x00", 4)
		if len(head) != 4 {
			continue
		}
		at, err := time.Parse(time.RFC3339, head[1])
		if err != nil {
			return nil, fmt.Errorf("parse git commit time %q: %w", head[1], err)
		}
		subjectAndFiles := strings.SplitN(head[3], "\n", 2)
		commit := CommitChanges{SHA: head[0], At: at, Author: head[2], Subject: subjectAndFiles[0]}
		if len(subjectAndFiles) == 2 {
			for _, line := range strings.Split(subjectAndFiles[1], "\n") {
				if line == "" {
					continue
				}
				parts := strings.Split(line, "\t")
				if len(parts) < 2 {
					continue
				}
				status := string(parts[0][0])
				change := FileChange{Status: status, Path: parts[len(parts)-1]}
				if status == "R" && len(parts) >= 3 {
					change.OldPath = parts[len(parts)-2]
				}
				commit.Files = append(commit.Files, change)
			}
		}
		commits = append(commits, commit)
	}
	return commits, nil
}

// DiffNameStatus returns changed files between two refs as a list of FileChange.
// Example: DiffNameStatus(dir, "HEAD~1", "HEAD") → [{"M", "docs/foo.md"}, {"A", "new.md"}]
func DiffNameStatus(dir, from, to string) ([]FileChange, error) {
	out, err := runGit(dir, "diff", "--name-status", from, to)
	if err != nil {
		return nil, fmt.Errorf("git diff --name-status %s %s: %w: %s", from, to, err, out)
	}
	var changes []FileChange
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		// For renames, format is: R<score>\t<old>\t<new>; take last field as path.
		status := string(parts[0][0])
		path := parts[len(parts)-1]
		change := FileChange{Status: status, Path: path}
		if status == "R" && len(parts) >= 3 {
			change.OldPath = parts[len(parts)-2]
		}
		changes = append(changes, change)
	}
	return changes, nil
}

// Status returns the working tree status (short format).
func Status(dir string) (string, error) {
	out, err := runGit(dir, "status", "--short")
	if err != nil {
		return "", fmt.Errorf("git status --short: %w: %s", err, out)
	}
	return out, nil
}

// RemoteURL returns the URL of the given remote, or error if not configured.
func RemoteURL(dir, remote string) (string, error) {
	out, err := runGit(dir, "remote", "get-url", remote)
	if err != nil {
		return "", fmt.Errorf("git remote get-url %s: %w: %s", remote, err, out)
	}
	return strings.TrimSpace(out), nil
}

// AddRemote adds a remote with the given name and URL.
func AddRemote(dir, name, url string) error {
	out, err := runGit(dir, "remote", "add", name, url)
	if err != nil {
		return fmt.Errorf("git remote add %s: %w: %s", name, err, out)
	}
	return nil
}

// BranchExists checks if a branch exists locally.
func BranchExists(dir, branch string) bool {
	_, err := runGit(dir, "rev-parse", "--verify", "refs/heads/"+branch)
	return err == nil
}

// FastForwardMerge fast-forwards the current branch to include the given branch.
// Returns error if not a fast-forward.
func FastForwardMerge(dir, branch string) error {
	out, err := runGit(dir, "merge", "--ff-only", branch)
	if err != nil {
		return fmt.Errorf("git merge --ff-only %s: %w: %s", branch, err, out)
	}
	return nil
}

// StashPush stashes uncommitted changes.
func StashPush(dir string) error {
	out, err := runGit(dir, "stash", "push")
	if err != nil {
		return fmt.Errorf("git stash push: %w: %s", err, out)
	}
	return nil
}

// StashPop pops the last stash. Returns error if nothing to pop.
func StashPop(dir string) error {
	out, err := runGit(dir, "stash", "pop")
	if err != nil {
		return fmt.Errorf("git stash pop: %w: %s", err, out)
	}
	return nil
}

// StashDrop discards the last stash without applying it ("git stash drop").
func StashDrop(dir string) error {
	out, err := runGit(dir, "stash", "drop")
	if err != nil {
		return fmt.Errorf("git stash drop: %w: %s", err, out)
	}
	return nil
}
