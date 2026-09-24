// Package gitx provides a git wrapper via os/exec for the Agentic Wiki.
// No external git library: only CLI invocation.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

// ErrCloneTimeout reports that a clone was stopped by its context deadline
// rather than failing on its own. The caller owns the remedy, since only it
// knows the flag that changes the bound.
var ErrCloneTimeout = errors.New("git clone timed out")

// Clone clones remote into dest ("git clone <remote> <dest>"), bounded by ctx.
// dest must not yet exist (or must be empty); git creates it. env carries
// extra per-KB variables (e.g. GIT_SSH_COMMAND) layered on top of the process
// environment — see runGitEnv.
//
// The child never prompts: GIT_TERMINAL_PROMPT=0, plus an SSH batch mode and
// connect timeout for ssh remotes. Without those, git blocks indefinitely on a
// credential or host-key prompt when stdin is not a usable terminal, and the
// caller shows nothing while it does. Host-key POLICY is left alone:
// StrictHostKeyChecking=accept-new would trade a hang for a silent
// trust-on-first-use decision on an operator's machine (D173).
//
// Progress goes to stderr as it happens — a clone with no output is
// indistinguishable from a hang — while a copy is kept to translate a failure
// into something actionable.
func Clone(ctx context.Context, remote, dest string, env ...string) error {
	cmd := exec.CommandContext(ctx, "git", "clone", "--progress", remote, dest)
	cmd.Env = cloneEnv(remote, env)
	var stderr strings.Builder
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	out, err := cmd.Output()
	if err != nil {
		return cloneError(ctx, remote, dest, err, stderr.String()+string(out))
	}
	return nil
}

// cloneEnv layers the non-interactive defaults on top of the process
// environment and the caller's own variables.
//
// GIT_SSH_COMMAND is set only for an ssh remote and only when nobody else
// provided one: an operator who configured a proxy command, an identity file
// or a jump host has said how to reach their forge, and overwriting that
// breaks a working setup to prevent a hypothetical one.
func cloneEnv(remote string, extra []string) []string {
	env := append(os.Environ(), extra...)
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	if !isSSHRemote(remote) || hasEnv(env, "GIT_SSH_COMMAND") {
		return env
	}
	return append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=10")
}

// hasEnv reports whether key is assigned anywhere in env.
func hasEnv(env []string, key string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			return true
		}
	}
	return false
}

// isSSHRemote covers both remote spellings git accepts: URL-style
// (ssh://host/path) and scp-style (git@host:path). A local path or an
// https URL is neither.
func isSSHRemote(remote string) bool {
	if strings.HasPrefix(remote, "ssh://") {
		return true
	}
	if strings.Contains(remote, "://") {
		return false
	}
	host, _, found := strings.Cut(remote, ":")
	return found && !strings.Contains(host, "/") && !filepath.IsAbs(remote)
}

// cloneError turns a failed clone into a message an operator can act on. The
// raw git output is kept in every case: the recognised phrases below are a
// convenience, not a filter, and an unrecognised failure must not be reduced
// to "git clone failed".
func cloneError(ctx context.Context, remote, dest string, err error, output string) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%w while cloning %s: %v", ErrCloneTimeout, remote, ctx.Err())
	}
	if remedy := remoteRemedy(output); remedy != "" {
		return fmt.Errorf("git clone %s: %s\n%s", remote, remedy, strings.TrimSpace(output))
	}
	return fmt.Errorf("git clone %s %s: %w: %s", remote, dest, err, strings.TrimSpace(output))
}

// remoteRemedy maps git's output for a failed network operation to the one
// line an operator can act on, or "" when none of the known phrases appears.
func remoteRemedy(output string) string {
	remedies := []struct{ match, remedy string }{
		{"could not resolve host", "the host name does not resolve: check the remote URL and this machine's DNS"},
		{"permission denied (publickey", "the forge rejected this machine's SSH key: check the key loaded in your agent has access to the repository"},
		{"host key verification failed", "the forge's host key is not in known_hosts: connect once with ssh to review and accept it, then retry"},
		{"repository not found", "the remote repository does not exist or this account cannot see it"},
		{"not found in the known hosts", "the forge's host key is not in known_hosts: connect once with ssh to review and accept it, then retry"},
		{"authentication failed", "the credentials were rejected: check the token or credential helper for this forge"},
		{"terminal prompts disabled", "git needed credentials and could not ask: configure a credential helper or use an ssh remote"},
	}
	lower := strings.ToLower(output)
	for _, r := range remedies {
		if strings.Contains(lower, r.match) {
			return r.remedy
		}
	}
	return ""
}

// ErrProbeTimeout reports that ProbeRemote was stopped by its context
// deadline: the remote did not answer in time.
var ErrProbeTimeout = errors.New("git ls-remote timed out")

// ProbeRemote asks remote which refs it has ("git ls-remote"), under the same
// non-interactive environment as Clone, and reports whether it has any. It is
// the read-only question `cartographer setup` asks before changing anything:
// it proves the remote is reachable with this machine's credentials, and an
// empty answer is what distinguishes a repository to create a KB in from one
// that already holds something to clone.
func ProbeRemote(ctx context.Context, remote string) (hasRefs bool, err error) {
	cmd := exec.CommandContext(ctx, "git", "ls-remote", remote)
	cmd.Env = cloneEnv(remote, nil)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return false, fmt.Errorf("%w for %s: %v", ErrProbeTimeout, remote, ctx.Err())
		}
		output := strings.TrimSpace(stderr.String())
		if remedy := remoteRemedy(output); remedy != "" {
			return false, fmt.Errorf("git ls-remote %s: %s\n%s", remote, remedy, output)
		}
		return false, fmt.Errorf("git ls-remote %s: %w: %s", remote, err, output)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// DefaultBranch is the branch a freshly initialized KB is created on. The
// server Git profile compares the checked-out branch against a configured base
// branch, so inheriting the host's init.defaultBranch would make a KB unusable
// on any machine configured for "master".
const DefaultBranch = "main"

// Init initializes a git repository in the directory, if one does not already exist.
// Pins the initial branch to DefaultBranch and configures merge.conflictStyle=zdiff3
// locally. An existing repository whose HEAD is unborn (no commit yet) is
// pinned too: that is what `git clone` of an empty remote leaves, on a branch
// named by the host's git version and init.defaultBranch (often "master"), and
// a KB bootstrapped from it must land where `kb create` does (D264).
// Repositories with commits keep whatever branch they are on.
func Init(dir string) error {
	if !IsRepo(dir) {
		if out, err := runGit(dir, "init"); err != nil {
			return fmt.Errorf("git init: %w: %s", err, out)
		}
	}
	if HeadUnborn(dir) {
		// The repository has no commits yet, so moving HEAD is safe and does
		// not depend on a git version that supports "init -b".
		if out, err := runGit(dir, "symbolic-ref", "HEAD", "refs/heads/"+DefaultBranch); err != nil {
			return fmt.Errorf("git symbolic-ref HEAD: %w: %s", err, out)
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
// process environment, and when git cannot resolve one either the author
// becomes the committer (withCommitterFallback). If there is nothing to commit, returns
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
	args := []string{"commit"}
	if authorName != "" && authorEmail != "" {
		args = append(args, "--author", fmt.Sprintf("%s <%s>", authorName, authorEmail))
	}
	args = append(args, "-m", message)
	env = withCommitterFallback(dir, env, authorName, authorEmail)
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

// withCommitterFallback returns env extended with GIT_COMMITTER_NAME/EMAIL set
// to the author when an author is supplied and git cannot resolve a committer
// on its own. --author sets only the author: on a machine with no user.email
// (a fresh Git for Windows install) git still exits 128 with "Committer
// identity unknown", so every commit Cartographer makes would fail (D265).
//
// The fallback applies only when `git var GIT_COMMITTER_IDENT` fails, rather
// than always making the committer the author: a committer the operator did
// configure (per-KB env, GIT_COMMITTER_* in the process, or user.* in git
// config) is recorded unchanged, and the extra probe is skipped when env
// already names both committer variables.
func withCommitterFallback(dir string, env []string, authorName, authorEmail string) []string {
	if authorName == "" || authorEmail == "" {
		return env
	}
	if hasEnv(env, "GIT_COMMITTER_NAME") && hasEnv(env, "GIT_COMMITTER_EMAIL") {
		return env
	}
	if _, err := runGitEnv(dir, env, "var", "GIT_COMMITTER_IDENT"); err == nil {
		return env
	}
	return append(append([]string{}, env...),
		"GIT_COMMITTER_NAME="+authorName, "GIT_COMMITTER_EMAIL="+authorEmail)
}

// AuthorIdent returns the identity resolved by Git itself (including local,
// global and ambient GIT_AUTHOR_* configuration). Empty values mean Git could
// not resolve a usable identity.
func AuthorIdent(dir string, env ...string) (name, email string, err error) {
	out, err := runGitEnv(dir, env, "var", "GIT_AUTHOR_IDENT")
	if err != nil {
		return "", "", fmt.Errorf("git var GIT_AUTHOR_IDENT: %w: %s", err, out)
	}
	ident := strings.TrimSpace(out)
	start, end := strings.LastIndex(ident, " <"), strings.LastIndex(ident, ">")
	if start < 1 || end <= start+2 {
		return "", "", fmt.Errorf("git var GIT_AUTHOR_IDENT: malformed identity %q", ident)
	}
	return strings.TrimSpace(ident[:start]), ident[start+2 : end], nil
}

// AheadCount returns commits reachable from HEAD but not origin/<branch>.
// known is false when the remote-tracking ref is absent, so callers never
// mistake an unavailable comparison for zero unpushed commits.
func AheadCount(dir, remote, branch string) (count int, known bool, err error) {
	if branch == "" {
		return 0, false, nil
	}
	ref := remote + "/" + branch
	if _, err := runGit(dir, "rev-parse", "--verify", "refs/remotes/"+ref); err != nil {
		return 0, false, nil
	}
	out, err := runGit(dir, "rev-list", "--count", ref+"..HEAD")
	if err != nil {
		return 0, false, fmt.Errorf("git rev-list %s..HEAD: %w: %s", ref, err, out)
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &n); err != nil {
		return 0, false, fmt.Errorf("git rev-list count %q: %w", out, err)
	}
	return n, true, nil
}

// HeadUnborn reports whether dir is a git repository whose HEAD names a branch
// with no commit yet: a fresh `git init`, or a clone of an empty remote.
func HeadUnborn(dir string) bool {
	if !IsRepo(dir) {
		return false
	}
	if _, err := runGit(dir, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		return false
	}
	// A detached or corrupt HEAD fails rev-parse too; only a symbolic HEAD
	// pointing at a missing branch is "unborn".
	_, err := runGit(dir, "symbolic-ref", "--quiet", "HEAD")
	return err == nil
}

// RemoteRefs is what a remote advertises, as far as the canonical-branch
// check needs it (D264).
type RemoteRefs struct {
	// HasRefs is false for an empty remote: no branch, no tag.
	HasRefs bool
	// Default is the branch the remote's HEAD points at, or "" when the
	// remote does not advertise one (empty, or HEAD naming a branch that does
	// not exist — common on a self-hosted `git init --bare` whose HEAD is
	// "master" while only "main" was pushed).
	Default string
	// Branches are the remote's branch names.
	Branches []string
}

// HasBranch reports whether the remote has branch.
func (r RemoteRefs) HasBranch(branch string) bool {
	for _, b := range r.Branches {
		if b == branch {
			return true
		}
	}
	return false
}

// LsRemote asks remote for its branches and default branch
// ("git ls-remote --symref"), bounded by FetchTimeout like Fetch. It asks the
// remote every time rather than reading refs/remotes/<remote>/HEAD: that ref
// is written once by clone and never refreshed by fetch, so it goes stale when
// the forge's default branch changes (and is absent in a repository that was
// init'ed and then given a remote). On success, and when the default branch
// has a remote-tracking ref, <remote>/HEAD is re-pointed at it (local only,
// best-effort) so an operator's `git log origin/HEAD` agrees with Cartographer.
// env carries extra per-KB variables (e.g. GIT_SSH_COMMAND) — see runGitEnv.
func LsRemote(dir, remote string, env ...string) (RemoteRefs, error) {
	if remote == "" {
		remote = "origin"
	}
	ctx, cancel := context.WithTimeout(context.Background(), FetchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "ls-remote", "--symref", remote)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	killGroupOnCancel(cmd)
	cmd.WaitDelay = time.Second
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return RemoteRefs{}, fmt.Errorf("git ls-remote %s: %w", remote, &RemoteTimeoutError{Remote: remote, After: FetchTimeout})
	}
	if err != nil {
		return RemoteRefs{}, fmt.Errorf("git ls-remote %s: %w: %s", remote, err, strings.TrimSpace(stderr.String()))
	}
	refs := parseLsRemoteSymref(string(out))
	if refs.Default != "" && RemoteBranchExists(dir, remote, refs.Default) {
		_, _ = runGit(dir, "symbolic-ref", "refs/remotes/"+remote+"/HEAD", "refs/remotes/"+remote+"/"+refs.Default)
	}
	return refs, nil
}

// parseLsRemoteSymref parses `git ls-remote --symref` output:
//
//	ref: refs/heads/main	HEAD
//	<sha>	HEAD
//	<sha>	refs/heads/main
func parseLsRemoteSymref(out string) RemoteRefs {
	var r RemoteRefs
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		r.HasRefs = true
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "ref:" && fields[2] == "HEAD" {
			r.Default = strings.TrimPrefix(fields[1], "refs/heads/")
			continue
		}
		if len(fields) == 2 && strings.HasPrefix(fields[1], "refs/heads/") {
			r.Branches = append(r.Branches, strings.TrimPrefix(fields[1], "refs/heads/"))
		}
	}
	// A HEAD symref naming a branch the remote does not have is no default.
	if r.Default != "" && !r.HasBranch(r.Default) {
		r.Default = ""
	}
	return r
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
// GIT_SSH_COMMAND fallback — see D46. A nil/empty env
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
	ctx, cancel := context.WithTimeout(context.Background(), FetchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "fetch", remote)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	killGroupOnCancel(cmd)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("git fetch %s: %w", remote, &RemoteTimeoutError{Remote: remote, After: FetchTimeout})
	}
	if err != nil {
		return fmt.Errorf("git fetch %s: %w: %s", remote, err, string(out))
	}
	return nil
}

// FetchTimeout bounds one git fetch. Without it a remote whose host drops
// packets (a VPN that is down) holds the fetch for the OS TCP connect timeout
// -- about 75s on macOS -- and every tool call waiting on the KB's git lock
// with it, past the client's 30s HTTP timeout (#348). The fetch is the
// operation that meets an unreachable remote first; pull and push run after a
// fetch has just succeeded. A variable so tests can shorten it.
var FetchTimeout = 15 * time.Second

// RemoteTimeoutError reports a git remote that did not answer in time. It is
// a distinct type so a caller can say "the remote is unreachable" rather than
// pass on a bare exit status.
type RemoteTimeoutError struct {
	Remote string
	After  time.Duration
}

func (e *RemoteTimeoutError) Error() string {
	return fmt.Sprintf("git remote %q did not answer within %s (unreachable or blocked)", e.Remote, e.After)
}

// RemoteBranchExists reports whether refs/remotes/<remote>/<branch> exists.
// Call Fetch first when the answer must reflect the forge's current state.
func RemoteBranchExists(dir, remote, branch string) bool {
	if remote == "" {
		remote = "origin"
	}
	_, err := runGit(dir, "rev-parse", "--verify", "refs/remotes/"+remote+"/"+branch)
	return err == nil
}

// CheckoutNewBranch checks out branch at start. It never resets an existing
// branch; callers that see an existing branch must make an explicit, safe
// decision instead of discarding history.
func CheckoutNewBranch(dir, branch, start string, env ...string) error {
	out, err := runGitEnv(dir, env, "checkout", "-b", branch, start)
	if err != nil {
		return fmt.Errorf("git checkout -b %s %s: %w: %s", branch, start, err, out)
	}
	return nil
}

// IsAncestor reports whether ancestor is reachable from descendant.
func IsAncestor(dir, ancestor, descendant string) (bool, error) {
	out, err := runGit(dir, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	if strings.TrimSpace(out) == "" {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w: %s", ancestor, descendant, err, out)
}

// RebaseOntoAutostash rebases the checked-out branch onto remote/branch. It
// preserves a dirty tree with Git's autostash and returns the same structured
// conflict error used by PullRebaseAutostash.
func RebaseOntoAutostash(dir, remote, branch string, env ...string) error {
	if remote == "" {
		remote = "origin"
	}
	localSHA, _ := HeadSHA(dir)
	target := remote + "/" + branch
	out, err := runGitEnv(dir, env, "rebase", "--autostash", target)
	if err == nil {
		return nil
	}
	if strings.Contains(out, "CONFLICT") || strings.Contains(out, "could not apply") || strings.Contains(out, "error: could not") {
		files, _ := UnmergedFiles(dir)
		remoteSHA, _ := HeadSHAAt(dir, target)
		_, _ = runGit(dir, "rebase", "--abort")
		return &RebaseConflictError{Files: files, LocalSHA: localSHA, RemoteSHA: remoteSHA, Remote: remote, Branch: branch}
	}
	return fmt.Errorf("git rebase --autostash %s: %w: %s", target, err, out)
}

// HeadSHAAt returns the commit SHA for ref.
func HeadSHAAt(dir, ref string) (string, error) {
	out, err := runGit(dir, "rev-parse", ref)
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s: %w: %s", ref, err, out)
	}
	return strings.TrimSpace(out), nil
}

// PushForceWithLease updates branch only if the remote still points at
// expectedSHA. It is intended exclusively for an unprotected working branch.
func PushForceWithLease(dir, remote, branch, expectedSHA string, env ...string) error {
	lease := "--force-with-lease=refs/heads/" + branch + ":" + expectedSHA
	out, err := runGitEnv(dir, env, "push", lease, remote, "HEAD:refs/heads/"+branch)
	if err != nil {
		return fmt.Errorf("git push %s %s: %w: %s", lease, branch, err, out)
	}
	return nil
}

// ResetHardTo resets a clean working tree to ref. It is deliberately narrow:
// server-profile callers check cleanliness before using it for post-merge
// reconciliation.
func ResetHardTo(dir, ref string, env ...string) error {
	out, err := runGitEnv(dir, env, "reset", "--hard", ref)
	if err != nil {
		return fmt.Errorf("git reset --hard %s: %w: %s", ref, err, out)
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

// PushSetUpstream pushes branch to remote and sets it as the branch's
// upstream ("git push -u"). Used once per KB: when its origin is first
// attached (kb create --remote), or by the local-profile SyncOut for the very
// first push of DefaultBranch to an empty remote (D264). Every other sync path
// in internal/kb uses Push, which must not touch tracking configuration.
// env carries extra per-KB variables (e.g. GIT_SSH_COMMAND) — see runGitEnv.
func PushSetUpstream(dir, remote, branch string, env ...string) error {
	out, err := runGitEnv(dir, env, "push", "-u", remote, branch)
	if err != nil {
		return fmt.Errorf("git push -u %s %s: %w: %s", remote, branch, err, out)
	}
	return nil
}

// Rebase state kinds returned by RebaseInProgress.
const (
	// RebaseStateNormal: a real rebase is in progress (a head-name or todo is
	// present), so someone is mid-operation and git owns the worktree.
	RebaseStateNormal = "rebase"
	// RebaseStateOrphanAutostash: the rebase directory exists but holds no
	// rebase — only an autostash. Observed after a process was killed mid
	// `pull --rebase --autostash`: from then on every write failed with "there is
	// already a rebase-merge directory", and the KB was frozen until the
	// directory was removed by hand (D155).
	RebaseStateOrphanAutostash = "orphan-autostash"
)

// RebaseInProgress reports whether dir has a rebase state directory and what kind
// it is. A caller must not run git in the worktree while this returns true: the
// point is to refuse with an actionable message instead of letting git fail with
// one that names no way out.
func RebaseInProgress(dir string) (kind string, ok bool) {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		d := filepath.Join(dir, ".git", name)
		if _, err := os.Stat(d); err != nil {
			continue
		}
		for _, marker := range []string{"head-name", "git-rebase-todo", "onto", "next"} {
			if _, err := os.Stat(filepath.Join(d, marker)); err == nil {
				return RebaseStateNormal, true
			}
		}
		return RebaseStateOrphanAutostash, true
	}
	return "", false
}

// RebaseStateDir returns the path of the rebase state directory in dir, or "".
// Named in the operator-facing message so the remedy points at a real path.
func RebaseStateDir(dir string) string {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		d := filepath.Join(dir, ".git", name)
		if _, err := os.Stat(d); err == nil {
			return d
		}
	}
	return ""
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
// Example: DiffNameStatus(dir, "HEAD~1", "HEAD") → [{"M", "a/foo.md"}, {"A", "new.md"}]
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
