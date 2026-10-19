package kb

import (
	"errors"
	"fmt"
	neturl "net/url"
	"os"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// processLockTimeout bounds how long a server write waits for the advisory KB
// lock before proceeding without it (D155).
var processLockTimeout = 30 * time.Second

var (
	syncOutMaxAttempts    = 5
	syncOutInitialBackoff = 50 * time.Millisecond
	syncOutSleep          = time.Sleep
)

// ErrRebaseStatePresent reports a rebase state directory in the KB clone. Sync
// refuses rather than letting git fail with a message that names no way out: an
// aborted rebase left a .git/rebase-merge containing only an autostash, and from
// then on every MCP write failed with "there is already a rebase-merge
// directory" until it was removed by hand (D155).
type ErrRebaseStatePresent struct {
	Root string
	Kind string
	Dir  string
}

func (e *ErrRebaseStatePresent) Error() string {
	if e.Kind == gitx.RebaseStateOrphanAutostash {
		return fmt.Sprintf("%s exists but holds no rebase (only an autostash): the KB cannot sync until it is cleared. "+
			"Inspect it with `git -C %s stash list` and `git -C %s log -1 --stat refs/stash`, then remove the directory once you are sure nothing in it is needed",
			e.Dir, e.Root, e.Root)
	}
	return fmt.Sprintf("a git rebase is in progress in %s: finish it with `git -C %s rebase --continue` or abort it with `git -C %s rebase --abort`, then retry",
		e.Root, e.Root, e.Root)
}

// ErrBranchDiverged is the sentinel for a KB whose checked-out branch is not its
// canonical branch: the remote's default branch (D264), or the `branch` of the
// settings.yaml committed on it when it sets one (D335, D903). errors.Is matches
// every *BranchDivergedError.
var ErrBranchDiverged = errors.New("KB branch diverges from the remote default branch")

// BranchDivergedError refuses a write that would commit or push on a branch
// other than the KB's canonical one: the remote's default branch.
// Pushing it would create or advance a second branch on the remote and fork
// the KB's history there. Merging two histories is an operator decision, so
// the error names both branches and the way out rather than repairing
// anything (D264).
type BranchDivergedError struct {
	Root string
	// Branch is the checked-out branch.
	Branch string
	// Default is the remote's default branch; "" when the remote has none
	// Cartographer can determine (see RemoteEmpty).
	Default string
	// RemoteEmpty is true when the remote has no refs at all: the only push
	// allowed then is the first one of gitx.DefaultBranch.
	RemoteEmpty bool
	// Configured is true when Default comes from the `branch` setting of the
	// settings.yaml on the remote's default branch (D335, D903) rather than from
	// the remote's HEAD.
	Configured bool
}

func (e *BranchDivergedError) Error() string {
	switch {
	case e.Configured:
		return fmt.Sprintf("%s: %s is checked out on branch %q but the KB's configured branch (`branch` in settings.yaml on the remote's default branch) is %q, so writes are refused to keep the KB from forking on the remote. "+
			"Check out %q in %s (`git -C %s checkout %s`, or `checkout -b %s` if it does not exist locally), or change `branch` on the default branch, then restart the server",
			ErrBranchDiverged, e.Root, e.Branch, e.Default, e.Default, e.Root, e.Root, e.Default, e.Default)
	case e.Default != "":
		return fmt.Sprintf("%s: %s is checked out on branch %q but the remote's default branch is %q, so writes are refused to keep the KB from forking on the remote. "+
			"Merge %q into %q on the remote, or check out %q in %s, then restart the server",
			ErrBranchDiverged, e.Root, e.Branch, e.Default, e.Branch, e.Default, e.Default, e.Root)
	case e.RemoteEmpty:
		return fmt.Sprintf("%s: the remote is empty and %s is checked out on branch %q; the first push of a KB only creates %q. "+
			"Rename the branch with `git -C %s branch -m %s`, then restart the server",
			ErrBranchDiverged, e.Root, e.Branch, gitx.DefaultBranch, e.Root, gitx.DefaultBranch)
	default:
		return fmt.Sprintf("%s: %s is checked out on branch %q, which the remote does not have, and the remote names no default branch; Cartographer never creates a branch on the remote. "+
			"Set the remote's default branch (HEAD) to the KB's branch, or check out that branch in %s, then restart the server",
			ErrBranchDiverged, e.Root, e.Branch, e.Root)
	}
}

// Is makes errors.Is(err, ErrBranchDiverged) hold.
func (e *BranchDivergedError) Is(target error) bool { return target == ErrBranchDiverged }

// canonicalBranch is the remote's default branch, given what it advertises: its
// HEAD; when the remote's HEAD names no
// existing branch (a self-hosted bare repository initialised with "master"
// and pushed "main"), gitx.DefaultBranch if the remote has it; otherwise ""
// (unknown: the check is skipped, and SyncOut still refuses to create a
// branch on the remote).
func canonicalBranch(refs gitx.RemoteRefs) string {
	if refs.Default != "" {
		return refs.Default
	}
	if refs.HasBranch(gitx.DefaultBranch) {
		return gitx.DefaultBranch
	}
	return ""
}

// setCanonical records what a sync resolved: the branch writes go to, and
// whether it comes from the `branch` setting (configured) or is the remote's
// default.
func (k *KB) setCanonical(branch string, configured bool) {
	k.lastSyncInMu.Lock()
	k.remoteDefault = branch
	k.branchConfigured = configured
	k.lastSyncInMu.Unlock()
}

// ConfiguredBranch is the `branch` of the settings.yaml committed on the
// remote's default branch, as the last sync resolved it (D903); "" when the
// setting is absent, or when no sync has run yet.
func (k *KB) ConfiguredBranch() string {
	k.lastSyncInMu.RLock()
	defer k.lastSyncInMu.RUnlock()
	if k.branchConfigured {
		return k.remoteDefault
	}
	return ""
}

// branchSettingAt reads the `branch` key of settings.yaml as committed on ref,
// the remote's default branch. The working copy's file is ignored on purpose:
// `branch` is a decision of the team, the same for every server that mounts the
// KB, so a change made on another branch has no effect until it reaches the
// default one. "" when the file, the key or the ref is absent or unreadable.
// A `branch` that is present but not a valid branch name is an error, not "":
// reading it as absent would send writes to the default branch the team meant
// to keep clear of them.
func (k *KB) branchSettingAt(ref string) (string, error) {
	text, err := gitx.ShowFile(k.Root, ref, SettingsFile)
	if err != nil {
		return "", nil
	}
	s, bad, perr := ParseSettings([]byte(text))
	if perr != nil {
		return "", nil
	}
	for _, m := range bad {
		if m.Key == "branch" {
			return "", fmt.Errorf("%s on %s: `branch` is not usable (%s): writes are refused until it is fixed on the default branch", SettingsFile, ref, m.Reason)
		}
	}
	v, _ := s.String("branch")
	return v, nil
}

// resolveCanonical is the branch writes go to, given what the remote advertises
// (after a fetch): the `branch` setting committed on the default branch, else
// the default branch itself.
func (k *KB) resolveCanonical(remote string, refs gitx.RemoteRefs) (branch string, configured bool, err error) {
	def := canonicalBranch(refs)
	if def == "" || !gitx.RemoteBranchExists(k.Root, remote, def) {
		return def, false, nil
	}
	b, err := k.branchSettingAt(remote + "/" + def)
	if err != nil {
		return def, false, err
	}
	if b != "" {
		return b, true, nil
	}
	return def, false, nil
}

// RemoteDefaultBranch returns the canonical branch last resolved from the
// remote by SyncIn or SyncOut, or "" when not (yet) known.
func (k *KB) RemoteDefaultBranch() string {
	k.lastSyncInMu.RLock()
	defer k.lastSyncInMu.RUnlock()
	return k.remoteDefault
}

// divergedError records a divergence as the KB's sync status and returns it.
func (k *KB) divergedError(err *BranchDivergedError, attempts int) error {
	k.setGitStatus("degraded", err, attempts)
	return err
}

// clearDivergedStatus drops a "degraded" status set by a branch divergence,
// or by a write on the local base after a failed fetch (D311), once a sync
// finds the KB back on its canonical branch with the remote reachable, so
// sync_status does not keep reporting a condition that has passed.
func (k *KB) clearDivergedStatus() {
	k.gitStatusMu.RLock()
	diverged := k.gitStatus.State == "degraded" &&
		(strings.HasPrefix(k.gitStatus.LastError, ErrBranchDiverged.Error()) || strings.HasPrefix(k.gitStatus.LastError, FetchFailedLocalBase))
	k.gitStatusMu.RUnlock()
	if diverged {
		k.setGitStatus("clean", nil, 0)
	}
}

// checkRebaseState refuses to touch git while a rebase state directory exists.
// Deliberately detection plus instruction, never automatic deletion: the
// directory may hold a real operator rebase, or an autostash holding the only
// copy of uncommitted work, and deleting another writer's state is the class of
// action that caused the incident in the first place.
func (k *KB) checkRebaseState() error {
	kind, present := gitx.RebaseInProgress(k.Root)
	if !present {
		return nil
	}
	return &ErrRebaseStatePresent{Root: k.Root, Kind: kind, Dir: gitx.RebaseStateDir(k.Root)}
}

// WithGitLock acquires the per-KB mutex, executes fn, then releases it.
// This serialises git operations so that concurrent tool calls do not interleave
// their working-tree changes and commits.
func (k *KB) WithGitLock(fn func() error) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	// The mutex serialises this process; the advisory lock file serialises
	// against another one — `cartographer import` writes into the same directory
	// the sync loop manages, and the two interleaving corrupted the git index
	// (D155). Best-effort by design: a KB on a filesystem without flock, or a
	// lock held past the window, must not make the server stop writing, so the
	// condition is reported and the write proceeds under the mutex alone.
	// A lock this KB already holds is not waited for: that wait is on ourselves.
	if k.procLockHeld.Load() {
		return fn()
	}
	release, err := k.AcquireProcessLock(processLockTimeout, "cartographer serve")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: proceeding without the KB lock: %v\n", err)
	} else {
		defer release()
	}
	return fn()
}

// hasRemote returns the remote name ("origin") and true if the KB root is a git
// repository and has a non-empty URL configured for "origin"; otherwise "", false.
func (k *KB) hasRemote() (string, bool) {
	url, err := gitx.RemoteURL(k.Root, "origin")
	if err != nil || strings.TrimSpace(url) == "" {
		return "", false
	}
	return "origin", true
}

// SyncInDue reports whether a SyncIn call could run a fetch: only a KB with a
// remote syncs, one without never contacts anything (D903). It is deliberately
// safe to call before acquiring the git lock so read-side callers can avoid the
// lock on the common disabled, no-remote, and fresh-window paths. A concurrent
// SyncIn may make the answer stale before the caller acquires that lock; SyncIn
// repeats the check and safely becomes a no-op in that case.
func (k *KB) SyncInDue() bool {
	if !gitx.IsRepo(k.Root) {
		return false
	}
	if k.syncInWithinWindow() {
		return false
	}
	if _, ok := k.hasRemote(); !ok {
		return false
	}
	branch, _ := gitx.Branch(k.Root)
	return branch != ""
}

// ReadFetchBackoff is how long a read skips SyncIn after a failed fetch. A
// write still fetches every time; when the fetch fails it commits only on a
// base with nothing unpushed (localBaseIsCurrent, D311), never on one that
// may have diverged.
const ReadFetchBackoff = 60 * time.Second

// FetchFailedLocalBase prefixes the sync status a write sets when its fetch
// failed and it proceeded on the local base (D311).
const FetchFailedLocalBase = "fetch failed, writing on local base"

// localBaseIsCurrent reports whether HEAD has no commit missing from the
// remote-tracking ref origin/<branch>: HEAD is at or behind what the last
// successful fetch saw, so committing on it forks from the remote only if
// someone pushed since — the fork the next successful pull rebases. Without a
// tracking ref nothing can be verified and the answer is false.
func (k *KB) localBaseIsCurrent(remote, branch string) bool {
	n, known, err := gitx.AheadCount(k.Root, remote, branch)
	return err == nil && known && n == 0
}

// ReadFetchBackingOff reports whether a fetch failed less than
// ReadFetchBackoff ago, in which case a read serves the local clone as is.
func (k *KB) ReadFetchBackingOff() bool {
	k.lastSyncInMu.RLock()
	last := k.lastFetchFail
	k.lastSyncInMu.RUnlock()
	return !last.IsZero() && time.Since(last) < ReadFetchBackoff
}

func (k *KB) syncInWithinWindow() bool {
	if k.SyncInWindow <= 0 {
		return false
	}
	k.lastSyncInMu.RLock()
	last := k.lastSyncIn
	k.lastSyncInMu.RUnlock()
	return !last.IsZero() && time.Since(last) < k.SyncInWindow
}

func (k *KB) setLastSyncIn(now time.Time) {
	k.lastSyncInMu.Lock()
	k.lastSyncIn = now
	k.lastSyncInMu.Unlock()
}

func (k *KB) lastSyncInAt() time.Time {
	k.lastSyncInMu.RLock()
	defer k.lastSyncInMu.RUnlock()
	return k.lastSyncIn
}

// SyncIn performs a fetch + pull --rebase --autostash from the "origin" remote
// before a git-synchronised operation. It is a no-op when:
//   - the KB root is not a git repository, or
//   - no "origin" remote is configured, or
//   - the freshness window has not elapsed: k.SyncInWindow > 0 and the last
//     successful SyncIn happened less than k.SyncInWindow ago (D76/WP3) —
//     avoids a redundant fetch+pull on every write during a burst.
//
// The returned bool reports whether a fetch was attempted, including a fetch
// that failed. A failed fetch is not an error when HEAD has nothing unpushed
// (D311): the status turns "degraded" and the caller proceeds on the local
// base. A conflict on reserved files only is reconciled in place
// (pullRebase). Returns gitx.ErrRebaseConflict if the pull hits a conflict (the
// rebase is aborted automatically). Other network/git errors are propagated as
// is. lastSyncIn is only updated after a fetch+pull that actually succeeds.
// Callers must hold the git lock.
func (k *KB) SyncIn() (bool, error) {
	if err := k.checkRebaseState(); err != nil {
		return false, err
	}
	if !k.SyncInDue() {
		return false, nil
	}
	remote := "origin"
	branch, _ := gitx.Branch(k.Root)
	headBefore, _ := gitx.HeadSHA(k.Root)
	// Fetch first to update remote refs.
	if err := gitx.Fetch(k.Root, remote, k.GitEnv...); err != nil {
		k.lastSyncInMu.Lock()
		k.lastFetchFail = time.Now()
		k.lastSyncInMu.Unlock()
		// D311 revises D237: with nothing unpushed, the local base is as
		// current as the last successful fetch left it, so the write commits
		// locally and its push is deferred. With unpushed commits, or no
		// tracking ref to compare against, it still fails.
		if k.localBaseIsCurrent(remote, branch) {
			k.setGitStatus("degraded", fmt.Errorf("%s: %w", FetchFailedLocalBase, err), 0)
			return true, nil
		}
		return true, fmt.Errorf("SyncIn fetch: %w", err)
	}
	// The KB's branch is the remote's default branch (D264), or the `branch`
	// the settings.yaml committed on it names (D335, D903).
	refs, err := gitx.LsRemote(k.Root, remote, k.GitEnv...)
	if err != nil {
		return true, fmt.Errorf("SyncIn remote default branch: %w", err)
	}
	canonical, configured, rerr := k.resolveCanonical(remote, refs)
	if rerr != nil {
		return true, rerr
	}
	k.setCanonical(canonical, configured)
	if !refs.HasRefs {
		// An empty remote has nothing to pull (pulling it fails on the missing
		// ref); SyncOut makes the first push of main.
		k.clearDivergedStatus()
		k.setLastSyncIn(time.Now())
		return true, nil
	}
	if canonical != "" && branch != canonical {
		return true, k.divergedError(&BranchDivergedError{Root: k.Root, Branch: branch, Default: canonical, Configured: configured}, 0)
	}
	if configured && !gitx.RemoteBranchExists(k.Root, remote, branch) {
		// Not on the remote yet (the fetch above refreshed the tracking refs):
		// nothing to pull; SyncOut's first push creates it.
		k.clearDivergedStatus()
		k.setLastSyncIn(time.Now())
		return true, nil
	}
	if err := k.pullRebase(remote, branch); err != nil {
		return true, err
	}
	k.clearDivergedStatus()
	k.setLastSyncIn(time.Now())
	k.ensureGitAttributes()
	if headAfter, err := gitx.HeadSHA(k.Root); err == nil && headAfter != headBefore {
		k.RefreshSettings() // D746: a pull may have changed settings.yaml
		if k.OnSyncIn != nil {
			k.OnSyncIn()
		}
	}
	return true, nil
}

// SyncOut pushes the current branch to "origin" AFTER a successful commit.
// It is a no-op when:
//   - the KB root is not a git repository, or
//   - no "origin" remote is configured.
//
// A push the remote refuses because the branch is protected is not retried: it
// comes back as an *ErrProtectedBranch (D903). On a non-fast-forward rejection the loop performs fetch + PullRebaseAutostash
// and retries the push with exponential backoff, capped at 5 attempts total.
// If PullRebaseAutostash returns ErrRebaseConflict, SyncOut returns immediately.
// After 5 failed attempts it returns an error.
func (k *KB) SyncOut() error {
	if err := k.checkRebaseState(); err != nil {
		return err
	}
	if !gitx.IsRepo(k.Root) {
		k.setGitStatus("disabled", nil, 0)
		return nil
	}
	remote, ok := k.hasRemote()
	if !ok {
		k.setGitStatus("no_remote", nil, 0)
		return nil
	}

	maxAttempts := syncOutMaxAttempts
	backoff := syncOutInitialBackoff

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		branch, _ := gitx.Branch(k.Root)
		if branch == "" {
			err := fmt.Errorf("SyncOut: detached HEAD, cannot push")
			k.setGitStatus("failed", err, attempt)
			return err
		}

		pushErr, diverged := k.pushLocal(remote, branch)
		if diverged != nil {
			return k.divergedError(diverged, attempt)
		}
		if pushErr == nil {
			k.recordPushSuccess()
			k.setGitStatus("clean", nil, attempt)
			return nil
		}

		// A protected branch is the remote's decision, not a transient failure:
		// retrying only repeats the refusal. The commit stays safe locally.
		if gitx.IsProtectedBranchRejection(pushErr.Error()) {
			err := &ErrProtectedBranch{Branch: branch, Git: pushErr.Error()}
			k.setGitStatus("failed", err, attempt)
			return err
		}

		// Determine whether the failure is a non-fast-forward rejection.
		// git push prints "[rejected]" and/or "non-fast-forward" in such cases.
		errMsg := pushErr.Error()
		isRejected := strings.Contains(errMsg, "non-fast-forward") ||
			strings.Contains(errMsg, "[rejected]") ||
			strings.Contains(errMsg, "Updates were rejected")

		if !isRejected {
			// Network or authentication error: still count as an attempt,
			// back off and retry rather than returning immediately.
			if attempt == maxAttempts {
				err := fmt.Errorf("SyncOut: push failed after %d attempts: %w", maxAttempts, pushErr)
				k.setGitStatus("failed", err, attempt)
				return err
			}
			syncOutSleep(backoff)
			backoff *= 2
			continue
		}

		// Non-fast-forward: fetch + pull --rebase, then retry push.
		if fetchErr := gitx.Fetch(k.Root, remote, k.GitEnv...); fetchErr != nil {
			if attempt == maxAttempts {
				err := fmt.Errorf("SyncOut: push failed after %d attempts: %w", maxAttempts, pushErr)
				k.setGitStatus("failed", err, attempt)
				return err
			}
			syncOutSleep(backoff)
			backoff *= 2
			continue
		}

		if rebaseErr := k.pullRebase(remote, branch); rebaseErr != nil {
			if errors.Is(rebaseErr, gitx.ErrRebaseConflict) {
				k.setGitStatus("failed", rebaseErr, attempt)
				return rebaseErr
			}
			if attempt == maxAttempts {
				err := fmt.Errorf("SyncOut: push failed after %d attempts: %w", maxAttempts, pushErr)
				k.setGitStatus("failed", err, attempt)
				return err
			}
			syncOutSleep(backoff)
			backoff *= 2
			continue
		}

		syncOutSleep(backoff)
		backoff *= 2
	}

	err := fmt.Errorf("SyncOut: push failed after %d attempts", maxAttempts)
	k.setGitStatus("failed", err, maxAttempts)
	return err
}

// pushLocal is one push attempt that never creates a branch on the remote
// except the configured one (D264), the defence behind SyncIn's check for
// anything that bypassed it: a freshness window that skipped SyncIn, a checkout
// changed between SyncIn and SyncOut. It pushes branch when it is the canonical
// branch last resolved and the remote has it; the one exception is the first
// push of a KB, gitx.DefaultBranch to an empty remote, which also sets the
// upstream. A refusal comes back as diverged, with nothing pushed; the local
// commit stays. A failure to ask the remote is returned as pushErr, retried like
// a failed push.
//
// With a `branch` setting on the remote's default branch (D335, D903) the
// configured branch is the only one pushed, and the first push creates it on the
// remote when it is missing.
func (k *KB) pushLocal(remote, branch string) (pushErr error, diverged *BranchDivergedError) {
	if configured := k.ConfiguredBranch(); configured != "" {
		return k.pushConfigured(remote, branch, configured)
	}
	canonical := k.RemoteDefaultBranch()
	if canonical != "" && branch != canonical {
		return nil, &BranchDivergedError{Root: k.Root, Branch: branch, Default: canonical}
	}
	// A tracking ref alone is no proof the branch is the right one: while the
	// canonical branch is unresolved (right after a start, before the first
	// sync) the remote is asked below.
	if canonical != "" && gitx.RemoteBranchExists(k.Root, remote, branch) {
		return gitx.Push(k.Root, remote, branch, k.GitEnv...), nil
	}
	// No remote-tracking ref, or nothing resolved yet: ask the remote itself,
	// since the ref may just be stale (SyncIn skipped by the freshness window,
	// or never run).
	refs, err := gitx.LsRemote(k.Root, remote, k.GitEnv...)
	if err != nil {
		return err, nil
	}
	canonical, configured, rerr := k.resolveCanonical(remote, refs)
	if rerr != nil {
		return rerr, nil
	}
	k.setCanonical(canonical, configured)
	if configured {
		return k.pushConfigured(remote, branch, canonical)
	}
	switch {
	case !refs.HasRefs && branch == gitx.DefaultBranch:
		return gitx.PushSetUpstream(k.Root, remote, branch, k.GitEnv...), nil
	case !refs.HasRefs:
		return nil, &BranchDivergedError{Root: k.Root, Branch: branch, RemoteEmpty: true}
	case canonical != "" && branch != canonical:
		return nil, &BranchDivergedError{Root: k.Root, Branch: branch, Default: canonical}
	case refs.HasBranch(branch):
		return gitx.Push(k.Root, remote, branch, k.GitEnv...), nil
	default:
		return nil, &BranchDivergedError{Root: k.Root, Branch: branch}
	}
}

// pushConfigured is pushLocal for a KB whose settings.yaml names its branch.
func (k *KB) pushConfigured(remote, branch, configured string) (pushErr error, diverged *BranchDivergedError) {
	if branch != configured {
		return nil, &BranchDivergedError{Root: k.Root, Branch: branch, Default: configured, Configured: true}
	}
	if gitx.RemoteBranchExists(k.Root, remote, branch) {
		return gitx.Push(k.Root, remote, branch, k.GitEnv...), nil
	}
	// No remote-tracking ref: it may just be stale, so ask the remote.
	refs, err := gitx.LsRemote(k.Root, remote, k.GitEnv...)
	if err != nil {
		return err, nil
	}
	if refs.HasBranch(branch) {
		return gitx.Push(k.Root, remote, branch, k.GitEnv...), nil
	}
	return gitx.PushSetUpstream(k.Root, remote, branch, k.GitEnv...), nil
}

// ErrProtectedBranch is the error of a push the remote refused because the
// branch is protected, or a server-side hook declined it (D903). Cartographer
// pushes to the branch itself and never opens a pull request, so the way out is
// on the remote or in `branch`, not in a retry.
type ErrProtectedBranch struct {
	Branch string
	// Git is what git printed, kept for the operator.
	Git string
}

func (e *ErrProtectedBranch) Error() string {
	return fmt.Sprintf("the remote refused the push to branch %q: it is protected, or a server-side hook declined it. "+
		"Cartographer pushes to the branch directly and never opens a pull request: unprotect the branch, or set `branch` in settings.yaml "+
		"(committed on the remote's default branch) to write to another one. The change is committed and safe in the local clone. Git said: %s",
		e.Branch, firstLines(e.Git, 3))
}

// firstLines keeps the first n non-empty lines of git's output, on one line.
func firstLines(out string, n int) string {
	var kept []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			kept = append(kept, l)
			if len(kept) == n {
				break
			}
		}
	}
	return strings.Join(kept, " | ")
}

// HasRemote reports whether the KB has a configured origin remote.
func (k *KB) HasRemote() (string, bool) { return k.hasRemote() }

// RemoteInfo returns the origin remote URL with any embedded credentials
// redacted, plus whether a remote is configured at all. It is the read-side
// answer to "does this KB replicate anywhere", exposed by kb_status (D145):
// the URL reaches an LLM context and the audit output, so it must never carry
// token material.
func (k *KB) RemoteInfo() (url string, ok bool) {
	raw, err := gitx.RemoteURL(k.Root, "origin")
	if err != nil || strings.TrimSpace(raw) == "" {
		return "", false
	}
	return RedactRemoteURL(strings.TrimSpace(raw)), true
}

// RedactRemoteURL strips credential material from a remote URL. For http(s)
// the whole userinfo is dropped: a token can sit in either half
// (https://token@host/... as well as https://user:token@host/...). For other
// schemes only the password is dropped — an ssh username is not a secret and
// identifies the remote. Values git cannot parse as a URL (scp-style
// git@host:owner/repo.git, local paths) carry no userinfo and are returned
// unchanged.
func RedactRemoteURL(raw string) string {
	if !strings.Contains(raw, "://") {
		return raw
	}
	u, err := neturl.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		u.User = nil
	default:
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = neturl.User(u.User.Username())
		}
	}
	return u.String()
}

// CommitOp creates a git commit if the KB root is a git repository and the
// working tree is dirty (every write is a commit, whatever the configuration,
// D903). It is a no-op in every other case.
// If git commit itself fails, the error is returned to the caller; the wrapper in
// mcpserver treats commit errors as non-fatal (logs to stderr, does not surface to
// the MCP client).
//
// On a successful commit, sha is the new commit's SHA, resolved via
// gitx.HeadSHA immediately after the commit — while the caller still holds
// the per-KB git lock (WithGitLock in mcpserver.gitWrap), so this is never a
// race with a concurrent commit/push on the same KB (D119: the audit log
// records this SHA on the write's completion event instead of a later,
// racier HEAD query). sha is empty when no commit was made (not a repo, clean
// tree, or "nothing to commit").
func (k *KB) CommitOp(message string) (sha string, err error) {
	return k.CommitOpAs(message, "", "")
}

// CommitOpAs is CommitOp with an explicit author: a non-empty authorName and
// authorEmail author this one commit. Empty values mean the server identity
// (ServerIdentity): the commit is not a write, or the writer did not declare
// who it is. The committer is always the server's (D904).
func (k *KB) CommitOpAs(message, authorName, authorEmail string) (sha string, err error) {
	if !gitx.IsRepo(k.Root) {
		return "", nil
	}
	status, err := gitx.Status(k.Root)
	if err != nil || strings.TrimSpace(status) == "" {
		// Either status check failed or working tree is clean — nothing to commit.
		return "", nil
	}
	if authorName == "" || authorEmail == "" {
		authorName, authorEmail = "", ""
	}
	if err := gitx.Commit(k.Root, message, authorName, authorEmail, k.GitCommandEnv()...); err != nil {
		if errors.Is(err, gitx.ErrNothingToCommit) {
			return "", nil
		}
		return "", err
	}
	sha, shaErr := gitx.HeadSHA(k.Root)
	if shaErr != nil {
		// The commit itself succeeded; a failure to resolve its SHA afterwards
		// is not a commit failure and must not be reported as one.
		return "", nil
	}
	return sha, nil
}

// CommitPaths creates one commit containing changes to paths only. The
// per-KB lock serialises its staging and commit with other git operations;
// gitx uses a temporary index so unrelated dirty work remains untouched.
func (k *KB) CommitPaths(paths []string, message string) error {
	return k.WithGitLock(func() error { return k.commitPathsLocked(paths, message) })
}

// commitPathsLocked is CommitPaths for a caller that already holds the git lock.
func (k *KB) commitPathsLocked(paths []string, message string) error {
	if !gitx.IsRepo(k.Root) {
		return fmt.Errorf("CommitPaths: KB root is not a git repository")
	}
	if err := gitx.CommitPaths(k.Root, paths, message, "", "", k.GitCommandEnv()...); err != nil {
		if errors.Is(err, gitx.ErrNothingToCommit) {
			return nil
		}
		return err
	}
	// The commit went through a temporary index: the real one still holds the
	// old content of these paths and would show them as staged changes against
	// the new HEAD. Best-effort, the commit itself is made.
	_ = gitx.ResetPaths(k.Root, paths...)
	return nil
}
