package star

import (
	"context"
	"sync"
	"sync/atomic"

	"go.starlark.net/starlark"
)

// runLimits bounds a run of plugin.star made for a call: it is cancelled with
// the call's context and every thread it starts is charged to the call's step
// budget. A first Load has none (nil) and is bounded by MaxLoadSteps alone.
type runLimits struct {
	ctx    context.Context
	budget uint64 // steps the whole call may use
	used   uint64 // steps the run has used

	mu      sync.Mutex
	threads []*starlark.Thread
	stepHit atomic.Bool
}

// start registers a thread of the run; it returns the cap to give it, 0 if the
// budget is gone.
func (l *runLimits) start(t *starlark.Thread) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.threads = append(l.threads, t)
	if l.ctx.Err() != nil {
		t.Cancel("context done")
	}
	left := l.budget - min(l.budget, l.used)
	t.OnMaxSteps = func(t *starlark.Thread) {
		l.stepHit.Store(true)
		t.Cancel("too many steps")
	}
	return min(left, MaxLoadSteps)
}

// charge adds the steps a finished thread used.
func (l *runLimits) charge(t *starlark.Thread) {
	l.mu.Lock()
	l.used += t.Steps
	l.mu.Unlock()
}

// watch cancels the run's threads when the context ends; the returned func stops it.
func (l *runLimits) watch() func() {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		select {
		case <-l.ctx.Done():
			l.mu.Lock()
			for _, t := range l.threads {
				t.Cancel("context done")
			}
			l.mu.Unlock()
		case <-done:
		}
	}()
	return func() { close(done); <-finished }
}

// failure is the typed error of a run that was cut short, nil if it was not.
func (l *runLimits) failure() *CallError {
	if err := l.ctx.Err(); err != nil {
		return ctxError(err)
	}
	if l.stepHit.Load() {
		return &CallError{Kind: LimitExceeded, Msg: "the script used up its step budget"}
	}
	return nil
}
