package kb

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// WithGitLock acquires the per-KB mutex, executes fn, then releases it.
// This serialises git operations so that concurrent tool calls do not interleave
// their working-tree changes and commits.
func (k *KB) WithGitLock(fn func() error) error {
	k.mu.Lock()
	defer k.mu.Unlock()
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

// SyncIn performs a fetch + pull --rebase --autostash from the "origin" remote
// BEFORE a write operation. It is a no-op when:
//   - k.GitSync is false, or
//   - the KB root is not a git repository, or
//   - no "origin" remote is configured, or
//   - the freshness window has not elapsed: k.SyncInWindow > 0 and the last
//     successful SyncIn happened less than k.SyncInWindow ago (D76/WP3) —
//     avoids a redundant fetch+pull on every write during a burst.
//
// Returns gitx.ErrRebaseConflict if the pull hits a conflict (the rebase is
// aborted automatically). Other network/git errors are propagated as-is.
// lastSyncIn is only updated after a fetch+pull that actually succeeds.
func (k *KB) SyncIn() error {
	if !k.GitSync || !gitx.IsRepo(k.Root) {
		return nil
	}
	if k.SyncInWindow > 0 && !k.lastSyncIn.IsZero() && time.Since(k.lastSyncIn) < k.SyncInWindow {
		return nil
	}
	remote, ok := k.hasRemote()
	if !ok {
		return nil
	}
	branch, _ := gitx.Branch(k.Root)
	if branch == "" {
		return nil // detached HEAD — skip sync
	}
	// Fetch first to update remote refs.
	if err := gitx.Fetch(k.Root, remote, k.GitEnv...); err != nil {
		return fmt.Errorf("SyncIn fetch: %w", err)
	}
	if err := gitx.PullRebaseAutostash(k.Root, remote, branch, k.GitEnv...); err != nil {
		return err
	}
	k.lastSyncIn = time.Now()
	return nil
}

// SyncOut pushes the current branch to "origin" AFTER a successful commit.
// It is a no-op when:
//   - k.GitSync is false, or
//   - the KB root is not a git repository, or
//   - no "origin" remote is configured.
//
// On a non-fast-forward rejection the loop performs fetch + PullRebaseAutostash
// and retries the push with exponential backoff, capped at 5 attempts total.
// If PullRebaseAutostash returns ErrRebaseConflict, SyncOut returns immediately.
// After 5 failed attempts it returns an error.
func (k *KB) SyncOut() error {
	if !k.GitSync || !gitx.IsRepo(k.Root) {
		return nil
	}
	remote, ok := k.hasRemote()
	if !ok {
		return nil
	}

	const maxAttempts = 5
	backoff := 50 * time.Millisecond

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		branch, _ := gitx.Branch(k.Root)
		if branch == "" {
			return fmt.Errorf("SyncOut: detached HEAD, cannot push")
		}

		pushErr := gitx.Push(k.Root, remote, branch, k.GitEnv...)
		if pushErr == nil {
			return nil
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
				return fmt.Errorf("SyncOut: push failed after %d attempts: %w", maxAttempts, pushErr)
			}
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		// Non-fast-forward: fetch + pull --rebase, then retry push.
		if fetchErr := gitx.Fetch(k.Root, remote, k.GitEnv...); fetchErr != nil {
			if attempt == maxAttempts {
				return fmt.Errorf("SyncOut: push failed after %d attempts: %w", maxAttempts, pushErr)
			}
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		if rebaseErr := gitx.PullRebaseAutostash(k.Root, remote, branch, k.GitEnv...); rebaseErr != nil {
			if errors.Is(rebaseErr, gitx.ErrRebaseConflict) {
				return rebaseErr
			}
			if attempt == maxAttempts {
				return fmt.Errorf("SyncOut: push failed after %d attempts: %w", maxAttempts, pushErr)
			}
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		time.Sleep(backoff)
		backoff *= 2
	}

	return fmt.Errorf("SyncOut: push failed after %d attempts", maxAttempts)
}

// CommitOp creates a git commit if AutoCommit is enabled, the KB root is a git
// repository, and the working tree is dirty. It is a no-op in every other case.
// If git commit itself fails, the error is returned to the caller; the wrapper in
// mcpserver treats commit errors as non-fatal (logs to stderr, does not surface to
// the MCP client).
func (k *KB) CommitOp(message string) error {
	if !k.AutoCommit || !gitx.IsRepo(k.Root) {
		return nil
	}
	status, err := gitx.Status(k.Root)
	if err != nil || strings.TrimSpace(status) == "" {
		// Either status check failed or working tree is clean — nothing to commit.
		return nil
	}
	authorName, authorEmail := k.gitAuthor()
	if err := gitx.Commit(k.Root, message, authorName, authorEmail, k.GitEnv...); err != nil {
		if errors.Is(err, gitx.ErrNothingToCommit) {
			return nil
		}
		return err
	}
	return nil
}
