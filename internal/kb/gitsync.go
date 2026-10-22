package kb

import (
	"errors"
	"fmt"
	"log"
	neturl "net/url"
	"os"
	"path/filepath"
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
// canonical branch: the server's `kbs[].branch` override (D1093), else the
// `branch` of the settings.yaml committed on the remote's default branch (D335,
// D903), else the remote's default branch (D264). errors.Is matches every
// *BranchDivergedError.
var ErrBranchDiverged = errors.New("KB branch diverges from the remote default branch")

// Where the canonical branch comes from (GitStatus.BranchSource).
const (
	BranchSourceRemote   = "remote"   // the remote's default branch (D264)
	BranchSourceSettings = "settings" // `branch` of settings.yaml on the default branch (D335, D903)
	BranchSourceServer   = "server"   // `kbs[].branch` of the server file (D1093)
)

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
	// Default is the canonical branch; "" when the remote has none
	// Cartographer can determine (see RemoteEmpty).
	Default string
	// RemoteEmpty is true when the remote has no refs at all: the only push
	// allowed then is the first one, which creates gitx.DefaultBranch.
	RemoteEmpty bool
	// Source is where Default comes from (BranchSource*): the server's
	// override, the `branch` setting of the settings.yaml on the remote's
	// default branch (D335, D903) or the remote's HEAD. "" reads as the remote.
	Source string
	// Reason is why the clone was not switched to Default by itself (D1093):
	// what stands in the way. "" when no switch was tried.
	Reason string
}

func (e *BranchDivergedError) Error() string {
	var msg string
	switch {
	case e.Source == BranchSourceServer:
		msg = fmt.Sprintf("%s: %s is checked out on branch %q but `kbs[].branch` of the server file is %q, so writes are refused to keep the KB from forking on the remote. "+
			"Check out %q in %s (`git -C %s checkout %s`, or `checkout -b %s` if it does not exist locally), or change or remove `kbs[].branch` in the server file, then restart the server",
			ErrBranchDiverged, e.Root, e.Branch, e.Default, e.Default, e.Root, e.Root, e.Default, e.Default)
	case e.Source == BranchSourceSettings:
		msg = fmt.Sprintf("%s: %s is checked out on branch %q but the KB's configured branch (`branch` in settings.yaml on the remote's default branch) is %q, so writes are refused to keep the KB from forking on the remote. "+
			"Check out %q in %s (`git -C %s checkout %s`, or `checkout -b %s` if it does not exist locally), or change `branch` on the default branch, then restart the server",
			ErrBranchDiverged, e.Root, e.Branch, e.Default, e.Default, e.Root, e.Root, e.Default, e.Default)
	case e.Default != "":
		msg = fmt.Sprintf("%s: %s is checked out on branch %q but the remote's default branch is %q, so writes are refused to keep the KB from forking on the remote. "+
			"Merge %q into %q on the remote, or check out %q in %s, then restart the server",
			ErrBranchDiverged, e.Root, e.Branch, e.Default, e.Branch, e.Default, e.Default, e.Root)
	case e.RemoteEmpty:
		msg = fmt.Sprintf("%s: the remote is empty and %s is checked out on branch %q; the first push of a KB only creates %q, or `kbs[].branch` of the server file when it is set. "+
			"Rename the branch with `git -C %s branch -m %s`, then restart the server",
			ErrBranchDiverged, e.Root, e.Branch, gitx.DefaultBranch, e.Root, gitx.DefaultBranch)
	default:
		msg = fmt.Sprintf("%s: %s is checked out on branch %q, which the remote does not have, and the remote names no default branch; Cartographer never creates a branch on the remote. "+
			"Set the remote's default branch (HEAD) to the KB's branch, or check out that branch in %s, then restart the server",
			ErrBranchDiverged, e.Root, e.Branch, e.Root)
	}
	if e.Reason != "" {
		msg += ". The clone was not switched by itself: " + e.Reason
	}
	return msg
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

// setCanonical records what a sync resolved: the branch writes go to, and where
// it comes from (BranchSource*).
func (k *KB) setCanonical(branch, source string) {
	k.lastSyncInMu.Lock()
	k.remoteDefault = branch
	k.branchSource = source
	k.lastSyncInMu.Unlock()
}

// SetBranchOverride sets `kbs[].branch` of the server file for this KB (D1093):
// the branch this server alone commits and pushes to. It wins over the KB's own
// `branch`, which is then not read; "" removes it. The server calls it once,
// when it mounts the KB, before any sync, so the branch is known before the
// first one resolves anything.
func (k *KB) SetBranchOverride(branch string) {
	k.lastSyncInMu.Lock()
	k.branchOverride = branch
	k.lastSyncInMu.Unlock()
}

// ConfiguredBranch is the branch the operator chose for this KB, ok or not on
// the remote yet: the server's override from the moment it is set, else the
// `branch` of the settings.yaml committed on the remote's default branch as the
// last sync resolved it (D903). "" when neither is set, or when no sync has
// run yet and there is no override.
func (k *KB) ConfiguredBranch() string {
	k.lastSyncInMu.RLock()
	defer k.lastSyncInMu.RUnlock()
	if k.branchOverride != "" {
		return k.branchOverride
	}
	if k.branchSource == BranchSourceSettings {
		return k.remoteDefault
	}
	return ""
}

// BranchSource says where the canonical branch comes from (BranchSource*).
func (k *KB) BranchSource() string {
	k.lastSyncInMu.RLock()
	defer k.lastSyncInMu.RUnlock()
	if k.branchOverride != "" {
		return BranchSourceServer
	}
	if k.branchSource == BranchSourceSettings {
		return BranchSourceSettings
	}
	return BranchSourceRemote
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
// (after a fetch): the server's override when it sets one (D1093, resolved
// before anything is read, so it also holds on an empty remote and a malformed
// KB `branch` cannot refuse it), else the `branch` setting committed on the
// default branch, else the default branch itself. source is BranchSource*.
func (k *KB) resolveCanonical(remote string, refs gitx.RemoteRefs) (branch, source string, err error) {
	k.lastSyncInMu.RLock()
	override := k.branchOverride
	k.lastSyncInMu.RUnlock()
	if override != "" {
		return override, BranchSourceServer, nil
	}
	def := canonicalBranch(refs)
	if def == "" || !gitx.RemoteBranchExists(k.Root, remote, def) {
		return def, BranchSourceRemote, nil
	}
	b, err := k.branchSettingAt(remote + "/" + def)
	if err != nil {
		return def, BranchSourceRemote, err
	}
	if b != "" {
		return b, BranchSourceSettings, nil
	}
	return def, BranchSourceRemote, nil
}

// switchToCanonical puts a clone that sits on another branch onto target, the
// branch writes go to, when that loses nothing (D1093): HEAD is on a named
// branch, the tree is clean, nothing is unpushed on it, and target can be built
// without merging, resetting or deleting anything. It returns the reason when
// it cannot, for BranchDivergedError.Reason. remoteDefault is the remote's
// default branch ("" when unknown): a target the remote does not have yet is
// created from it, never from the branch the clone happens to be on.
func (k *KB) switchToCanonical(remote, current, target, remoteDefault string) (switched bool, reason string) {
	if current == "" {
		return false, "HEAD is detached"
	}
	status, err := gitx.Status(k.Root)
	if err != nil {
		return false, err.Error()
	}
	if strings.TrimSpace(status) != "" {
		return false, fmt.Sprintf("uncommitted changes in %s", k.Root)
	}
	ahead, _, known, err := gitx.AheadBehind(k.Root, remote, current)
	switch {
	case err != nil:
		return false, err.Error()
	case !known:
		return false, fmt.Sprintf("branch %q was never pushed, so its commits exist only in %s", current, k.Root)
	case ahead > 0:
		return false, fmt.Sprintf("%d commits unpushed on %q", ahead, current)
	}
	switch {
	case gitx.BranchExists(k.Root, target):
		err = gitx.Checkout(k.Root, target)
	case gitx.RemoteBranchExists(k.Root, remote, target):
		err = gitx.CheckoutNewBranch(k.Root, target, remote+"/"+target, k.GitEnv...)
	case remoteDefault != "" && gitx.RemoteBranchExists(k.Root, remote, remoteDefault):
		err = gitx.CheckoutNewBranch(k.Root, target, remote+"/"+remoteDefault, k.GitEnv...)
	default:
		return false, fmt.Sprintf("%q exists neither here nor on the remote, and the remote has no default branch to create it from", target)
	}
	if err != nil {
		return false, err.Error()
	}
	log.Printf("KB %s: switched branch %s -> %s", filepath.Base(k.Root), current, target)
	return true, ""
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
	canonical, source, rerr := k.resolveCanonical(remote, refs)
	if rerr != nil {
		return true, rerr
	}
	k.setCanonical(canonical, source)
	if !refs.HasRefs {
		// An empty remote has nothing to pull (pulling it fails on the missing
		// ref); SyncOut makes the first push of main.
		k.clearDivergedStatus()
		k.setLastSyncIn(time.Now())
		return true, nil
	}
	if canonical != "" && branch != canonical {
		// A clone that can be moved without losing anything follows the
		// effective branch (D1093); otherwise it stays refused, with the reason.
		switched, reason := k.switchToCanonical(remote, branch, canonical, canonicalBranch(refs))
		if !switched {
			return true, k.divergedError(&BranchDivergedError{Root: k.Root, Branch: branch, Default: canonical, Source: source, Reason: reason}, 0)
		}
		branch = canonical
	}
	if source != BranchSourceRemote && !gitx.RemoteBranchExists(k.Root, remote, branch) {
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
		// A commit made before its writer was known (a 0.25.0 commit by the
		// server, D950) is re-committed by its writer before it leaves.
		if attempt == 1 && gitx.RemoteBranchExists(k.Root, remote, branch) {
			k.realignCommitters(remote, branch)
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
		//
		// TRAP: the identity test runs first. A membership rule and a branch
		// protection both end with "pre-receive hook declined", so testing the
		// protected wording first sent the operator to unprotect a branch that
		// was never the problem (D1063).
		if gitx.IsIdentityRejection(pushErr.Error()) {
			err := k.identityRejection(remote, branch, pushErr.Error())
			k.setGitStatus("failed", err, attempt)
			return err
		}
		if gitx.IsProtectedBranchRejection(pushErr.Error()) {
			// A bare "hook declined" is still an identity problem when the local
			// evidence is conclusive: fallback identities ahead, none configured.
			l := strings.ToLower(pushErr.Error())
			explicit := strings.Contains(l, "gh006") || strings.Contains(l, "protected branch")
			if idents, fallback := k.localIdentities(remote, branch); !explicit && fallback && !k.hasConfiguredIdentity() {
				err := &ErrIdentityRejected{Branch: branch, Git: pushErr.Error(), Identities: idents}
				k.setGitStatus("failed", err, attempt)
				return err
			}
			err := &ErrProtectedBranch{Branch: branch, Git: pushErr.Error()}
			k.setGitStatus("failed", err, attempt)
			return err
		}

		// A push that hit PushTimeout is not retried: five attempts would hold
		// the KB's git lock for minutes against a remote that is not answering.
		// The commit stays safe locally; the next write pushes it.
		var rte *gitx.RemoteTimeoutError
		if errors.As(pushErr, &rte) {
			k.setGitStatus("failed", pushErr, attempt)
			return pushErr
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
	canonical, source, rerr := k.resolveCanonical(remote, refs)
	if rerr != nil {
		return rerr, nil
	}
	k.setCanonical(canonical, source)
	if source != BranchSourceRemote {
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

// pushConfigured is pushLocal for a KB whose branch the operator chose: the
// server's override or the settings.yaml's `branch`.
func (k *KB) pushConfigured(remote, branch, configured string) (pushErr error, diverged *BranchDivergedError) {
	if branch != configured {
		return nil, &BranchDivergedError{Root: k.Root, Branch: branch, Default: configured, Source: k.BranchSource()}
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

// ErrIdentityRejected is the error of a push the remote refused because of who
// authored or committed a commit (a membership push rule, a private-email
// rule), not because the branch is protected (D1063). The way out is the
// identity of the local commits, not the branch.
type ErrIdentityRejected struct {
	Branch string
	// Git is what git printed, kept for the operator.
	Git string
	// Identities are the distinct "Name <email>" of author and committer over
	// the commits ahead of the upstream, at most three, those that differ from
	// the configured identity first; empty when the range cannot be read.
	Identities []string
	// Configured says whether git.identity (or git.identities) applies to the KB.
	Configured bool
}

func (e *ErrIdentityRejected) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "the remote refused the push to branch %q because of who authored or committed its commits, not because the branch is protected.", e.Branch)
	if len(e.Identities) > 0 {
		fmt.Fprintf(&b, " The unpushed commits are by: %s.", strings.Join(e.Identities, ", "))
	}
	if !e.Configured {
		b.WriteString(" No git identity is configured: set `git.identity` (or `git.identities`) in the server file to a user the remote accepts, and restart.")
	}
	b.WriteString(" Commits made by the server identity are re-authored by the next sync; commits authored by a token need `cartographer kb reauthor <kb>` run on the server host (a preview first, then `--apply`).")
	fmt.Fprintf(&b, " The changes are committed and safe in the local clone. Git said: %s", firstLines(e.Git, 3))
	return b.String()
}

func (k *KB) hasConfiguredIdentity() bool {
	_, _, ok := k.ConfiguredIdentity()
	return ok
}

// identityRejection builds the *ErrIdentityRejected for a push the remote
// refused on identity grounds.
func (k *KB) identityRejection(remote, branch, git string) *ErrIdentityRejected {
	idents, _ := k.localIdentities(remote, branch)
	return &ErrIdentityRejected{Branch: branch, Git: git, Identities: idents, Configured: k.hasConfiguredIdentity()}
}

// localIdentities lists the distinct "Name <email>" of author and committer
// over the commits ahead of the upstream (at most three, the ones that differ
// from the configured identity first), and whether any of them is a fallback
// identity: the server's or a token's, both at cartographer@<host>. Empty and
// false when the range cannot be read.
func (k *KB) localIdentities(remote, branch string) (idents []string, fallback bool) {
	if remote == "" {
		remote = "origin"
	}
	if !gitx.RemoteBranchExists(k.Root, remote, branch) {
		return nil, false
	}
	commits, err := gitx.LocalCommits(k.Root, remote+"/"+branch)
	if err != nil {
		return nil, false
	}
	_, serverEmail := ServerIdentity()
	local, _, _ := strings.Cut(serverEmail, "@")
	cfgName, cfgEmail, _ := k.ConfiguredIdentity()
	seen := map[string]bool{}
	var differing, same []string
	add := func(name, email string) {
		id := name + " <" + email + ">"
		if seen[id] {
			return
		}
		seen[id] = true
		if strings.HasPrefix(email, local+"@") {
			fallback = true
		}
		if name == cfgName && email == cfgEmail {
			same = append(same, id)
		} else {
			differing = append(differing, id)
		}
	}
	for _, c := range commits {
		add(c.Author, c.AuthorEmail)
		add(c.Committer, c.CommitterEmail)
	}
	idents = append(differing, same...)
	if len(idents) > 3 {
		idents = idents[:3]
	}
	return idents, fallback
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

// CommitOpAs is CommitOp with an explicit writer: a non-empty authorName and
// authorEmail are the author and the committer of this one commit (D950).
// Empty values mean the KB's default identity (DefaultIdentity): the commit is
// not a write, or the writer did not declare who it is.
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
	if err := gitx.Commit(k.Root, message, authorName, authorEmail, k.gitCommandEnvAs(authorName, authorEmail)...); err != nil {
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

// CommitPaths creates one commit containing changes to paths only, by
// authorName/authorEmail as author and committer, or by the KB's default
// identity when either is empty (D950). The per-KB lock serialises its
// staging and commit with other git operations; gitx uses a temporary index so
// unrelated dirty work remains untouched.
func (k *KB) CommitPaths(paths []string, message, authorName, authorEmail string) error {
	return k.WithGitLock(func() error { return k.commitPathsLocked(paths, message, authorName, authorEmail) })
}

// commitPathsLocked is CommitPaths for a caller that already holds the git lock.
func (k *KB) commitPathsLocked(paths []string, message, authorName, authorEmail string) error {
	if !gitx.IsRepo(k.Root) {
		return fmt.Errorf("CommitPaths: KB root is not a git repository")
	}
	if err := gitx.CommitPaths(k.Root, paths, message, authorName, authorEmail, k.gitCommandEnvAs(authorName, authorEmail)...); err != nil {
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
