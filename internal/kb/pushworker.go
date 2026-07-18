package kb

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// This file implements the debounced async push worker (D76/WP4): gitWrap
// schedules a push via SchedulePush instead of calling SyncOut inline on the
// critical path of a write. A single goroutine per KB debounces bursts of
// SchedulePush signals into one SyncOut call, executed under the same
// WithGitLock used by writes (no separate git lock).
//
// All worker bookkeeping (pending/running/force/waiters) lives in plain
// fields guarded by pushMu; pushWake is only ever used to nudge the worker
// goroutine to re-read that state promptly (it carries no information of its
// own). This matters: an earlier revision tried to make pushWake/pushFlush
// channel sends themselves the source of truth, which raced with a Go
// `select` picking either of two simultaneously-ready channels — e.g. a
// SchedulePush() immediately followed by a FlushPush() could have the
// worker observe the flush request before the push signal and no-op. Making
// pushMu-guarded state authoritative and pushWake a pure "recheck now" poke
// avoids that class of race entirely.
//
// The worker is started lazily on first use (ensurePushWorker) and, once
// started, runs for the lifetime of the process — parked on a channel
// receive between signals. This is a deliberate, bounded "leak": one
// goroutine per KB that is mounted with SyncOutDebounce > 0, acceptable
// because the number of KBs a server mounts is small and fixed. It is never
// started at all when SyncOutDebounce == 0 (gitWrap keeps calling SyncOut
// synchronously in that case — see gitwrap.go), and FlushPush is a no-op
// without starting it either, so the "0 = no worker" rollback flag holds.

// ensurePushWorker lazily starts the async push worker goroutine on first
// use. Safe to call concurrently; the goroutine is started at most once per
// KB (guarded by pushMu/pushStarted rather than sync.Once so that FlushPush
// can cheaply check "was the worker ever started" without starting it as a
// side effect).
func (k *KB) ensurePushWorker() {
	k.pushMu.Lock()
	defer k.pushMu.Unlock()
	if k.pushStarted {
		return
	}
	k.pushWake = make(chan struct{}, 1)
	k.pushStarted = true
	go k.pushWorkerLoop()
}

// wakePushWorker nudges the worker goroutine to re-read its state promptly
// (e.g. because pending/lastSignal/force just changed) instead of waiting
// out a timer or blocking on the next signal. Non-blocking: if a wake is
// already buffered, this is a no-op (the worker hasn't consumed it yet, so
// it will re-check state anyway).
func (k *KB) wakePushWorker() {
	select {
	case k.pushWake <- struct{}{}:
	default:
	}
}

// SchedulePush signals that a push is pending for this KB. It starts the
// worker on first use. Multiple signals received before the worker actually
// pushes are coalesced into a single SyncOut call: every call extends the
// debounce window (pushLastSignal), so a burst of writes close together
// results in exactly one push, issued SyncOutDebounce after the last one.
func (k *KB) SchedulePush() {
	k.ensurePushWorker()
	k.pushMu.Lock()
	k.pushPending = true
	k.pushLastSignal = time.Now()
	k.pushMu.Unlock()
	k.wakePushWorker()
}

// FlushPush forces immediate execution of any pending or in-flight push and
// waits for it to finish (or for timeout to elapse). It is a no-op — and
// does not start the worker — if the worker was never started (i.e.
// SchedulePush was never called on this KB, which is always the case when
// SyncOutDebounce == 0) or if there is currently nothing pending/running.
func (k *KB) FlushPush(timeout time.Duration) error {
	k.pushMu.Lock()
	if !k.pushStarted || (!k.pushPending && !k.pushRunning) {
		k.pushMu.Unlock()
		return nil
	}
	k.pushForce = true
	done := make(chan struct{})
	k.pushWaiters = append(k.pushWaiters, done)
	k.pushMu.Unlock()
	k.wakePushWorker()

	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("FlushPush: timed out after %s waiting for a pending push", timeout)
	}
}

// pushWorkerLoop is the body of the per-KB async push worker goroutine
// started by ensurePushWorker. It never returns.
func (k *KB) pushWorkerLoop() {
	for {
		k.pushMu.Lock()
		if !k.pushPending {
			k.pushMu.Unlock()
			<-k.pushWake
			continue
		}
		force := k.pushForce
		wait := k.SyncOutDebounce - time.Since(k.pushLastSignal)
		k.pushMu.Unlock()

		if !force && wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-k.pushWake:
				if !timer.Stop() {
					<-timer.C
				}
			}
			continue // re-read state from the top: lastSignal/force may have changed
		}

		k.pushMu.Lock()
		k.pushPending = false
		k.pushForce = false
		k.pushRunning = true
		k.pushMu.Unlock()

		k.doAsyncPush()

		k.pushMu.Lock()
		k.pushRunning = false
		waiters := k.pushWaiters
		k.pushWaiters = nil
		k.pushMu.Unlock()

		for _, w := range waiters {
			close(w)
		}
	}
}

// doAsyncPush performs the actual SyncOut call under k.WithGitLock (the same
// lock gitWrap holds for SyncIn/handler/CommitOp, guaranteeing serialization
// with writes without any new lock on the git path). Errors are non-fatal:
// a *gitx.RebaseConflictError is routed to k.OnPushConflict if set (mcpserver
// wires this to the same conflict-registry/degraded handling used for
// synchronous pushes — see tools.go); any other error, and a conflict with no
// OnPushConflict set, is just logged to stderr.
func (k *KB) doAsyncPush() {
	var syncErr error
	_ = k.WithGitLock(func() error { //nolint:errcheck // inner func always returns nil
		syncErr = k.SyncOut()
		return nil
	})
	if syncErr == nil {
		return
	}

	var rce *gitx.RebaseConflictError
	if errors.As(syncErr, &rce) {
		if k.OnPushConflict != nil {
			k.OnPushConflict(rce)
			return
		}
		fmt.Fprintf(os.Stderr, "cartographer: git conflict during async push: %v\n", syncErr)
		return
	}
	fmt.Fprintf(os.Stderr, "cartographer: async git push failed: %v\n", syncErr)
}
