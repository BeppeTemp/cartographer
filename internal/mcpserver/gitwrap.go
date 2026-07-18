package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// pushFlushTimeout bounds how long a sync-sensitive tool handler (D76/WP4:
// sync_check/sync_apply/sync_pull, git_conflict_resolve) waits for a pending
// async push to complete via k.FlushPush before proceeding anyway — a flush
// failure/timeout is logged and non-fatal, consistent with push errors
// elsewhere in this file.
const pushFlushTimeout = 5 * time.Second

// gitWrap wraps a write Tool so that a successful execution is followed by a git
// commit via k.CommitOp. The entire call (original handler + commit) runs under
// k.WithGitLock to serialise concurrent write operations on the same KB.
//
// Semantics:
//   - If the original handler returns a Go error or an application error
//     (res.IsError), no commit is attempted.
//   - If the commit itself fails, the error is logged to stderr but is NOT
//     propagated to the MCP client: a commit failure never turns a successful
//     operation into an error.
//   - If k.AutoCommit is false (the zero-value default), CommitOp is a no-op,
//     so existing tests that do not set AutoCommit are unaffected.
//
// Step 3 — agentic conflict handling:
//   - If SyncIn returns a *gitx.RebaseConflictError, each conflicting file is
//     registered in the KB conflict registry and marked as degraded; the write
//     is aborted with an informative errorResult.
//   - If SyncOut returns a *gitx.RebaseConflictError, conflicts are registered
//     and logged to stderr; the write result is not changed (already reported
//     as success).
func gitWrap(k *kb.KB, t Tool) Tool {
	orig := t
	t.Handler = func(args json.RawMessage) (ToolResult, error) {
		var res ToolResult
		var handlerErr error
		var syncInDur, handlerDur, commitDur, pushDur time.Duration
		var pushAsync bool
		start := time.Now()
		k.WithGitLock(func() error { //nolint:errcheck // inner func always returns nil
			// Step 2: fetch + pull --rebase before every write operation.
			// If the KB has no remote or GitSync is false, SyncIn is a no-op.
			syncInStart := time.Now()
			syncErr := k.SyncIn()
			syncInDur = time.Since(syncInStart)
			if syncErr != nil {
				var rce *gitx.RebaseConflictError
				if errors.As(syncErr, &rce) {
					// Step 3: rebase conflict — register conflicts, mark concepts degraded.
					n := handleConflictError(k, rce)
					res = errorResult(fmt.Sprintf(
						"git conflict detected and registered on %d concept(s); "+
							"use the conflicts_list tool and kb-conflict-resolve skill to resolve",
						n,
					))
				} else {
					res = errorResult("git sync (fetch/pull) failed: " + syncErr.Error())
				}
				return nil
			}
			handlerStart := time.Now()
			res, handlerErr = orig.Handler(args)
			handlerDur = time.Since(handlerStart)
			if handlerErr == nil && !res.IsError {
				msg := commitMessage(orig.Name, args)
				commitStart := time.Now()
				commitErr := k.CommitOp(msg)
				commitDur = time.Since(commitStart)
				if commitErr != nil {
					fmt.Fprintf(os.Stderr, "cartographer: git commit failed (%s): %v\n", orig.Name, commitErr)
				}
				// Step 2 / D76-WP4: push after the commit. If SyncOutDebounce
				// is set, take the push off the critical path entirely by
				// scheduling it on the per-KB async worker instead of
				// calling SyncOut inline; the worker runs under the same
				// WithGitLock (see pushworker.go) and surfaces conflicts via
				// k.OnPushConflict (wired in RegisterKBTools). Push failure —
				// sync or async — is non-fatal; Step 3 surfaces rebase
				// conflicts as degraded concepts.
				if k.SyncOutDebounce > 0 {
					k.SchedulePush()
					pushAsync = true
				} else {
					pushStart := time.Now()
					syncErr := k.SyncOut()
					pushDur = time.Since(pushStart)
					if syncErr != nil {
						var rce *gitx.RebaseConflictError
						if errors.As(syncErr, &rce) {
							n := handleConflictError(k, rce)
							fmt.Fprintf(os.Stderr,
								"cartographer: git conflict during push (%s): registered %d concept(s) as degraded\n",
								orig.Name, n)
						} else {
							fmt.Fprintf(os.Stderr, "cartographer: git push failed (%s): %v\n", orig.Name, syncErr)
						}
					}
				}
			}
			return nil
		})
		total := time.Since(start)
		fmt.Fprintln(os.Stderr, formatTiming(commitMessage(orig.Name, args), syncInDur, handlerDur, commitDur, pushDur, pushAsync, total))
		return res, handlerErr
	}
	return t
}

// formatTiming renders a single greppable timing line for a write operation.
// Durations are rounded to whole milliseconds; phases that were skipped are
// passed in as 0. When pushAsync is true (D76/WP4, SyncOutDebounce > 0), the
// push field reads "async" instead of a duration: the push was scheduled on
// the per-KB worker rather than awaited inline, so there is no meaningful
// elapsed time to report here.
func formatTiming(op string, syncIn, handler, commit, push time.Duration, pushAsync bool, total time.Duration) string {
	pushField := fmt.Sprintf("%dms", push.Milliseconds())
	if pushAsync {
		pushField = "async"
	}
	return fmt.Sprintf(
		"cartographer: timing op=%q sync_in=%dms handler=%dms commit=%dms push=%s total=%dms",
		op, syncIn.Milliseconds(), handler.Milliseconds(), commit.Milliseconds(), pushField, total.Milliseconds(),
	)
}

// handleConflictError registers each conflicting concept in the KB conflict registry
// and marks it as degraded. Best-effort: errors are logged to stderr.
// Returns the number of concept IDs successfully identified in the conflict.
func handleConflictError(k *kb.KB, rce *gitx.RebaseConflictError) int {
	now := time.Now().UTC().Format(time.RFC3339)
	n := 0
	for _, file := range rce.Files {
		conceptID, ok := kb.GitPathToConceptID(file)
		if !ok {
			continue
		}
		c := kb.Conflict{
			ConceptID:  conceptID,
			Path:       file,
			LocalSHA:   rce.LocalSHA,
			RemoteSHA:  rce.RemoteSHA,
			Branch:     rce.Branch,
			Files:      rce.Files,
			DetectedAt: now,
		}
		if err := k.RegisterConflict(c); err != nil {
			fmt.Fprintf(os.Stderr, "cartographer: register conflict %q: %v\n", conceptID, err)
		}
		if err := k.MarkDegraded(conceptID); err != nil {
			fmt.Fprintf(os.Stderr, "cartographer: mark degraded %q: %v\n", conceptID, err)
		}
		n++
	}
	return n
}

// commitMessage builds a human-readable commit message from the tool name and
// the first recognisable identifier found in the args map.
// Priority: "id" > "name" > "source_id" > "contradiction_id".
// Falls back to the tool name alone if none are present.
func commitMessage(toolName string, args json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return toolName
	}
	for _, key := range []string{"id", "name", "source_id", "contradiction_id"} {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return fmt.Sprintf("%s: %s", toolName, s)
			}
		}
	}
	return toolName
}
