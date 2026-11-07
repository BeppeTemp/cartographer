package star

import (
	"context"
	"fmt"
	"runtime/metrics"
	"sync"
	"sync/atomic"
	"time"

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
	stepCap atomic.Uint64 // the cap of the thread that ran out of steps
	memHit  atomic.Bool
	base    uint64 // live heap at the start of the call: one baseline for the whole call
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
	limit := min(left, MaxLoadSteps)
	t.OnMaxSteps = func(t *starlark.Thread) {
		l.stepCap.Store(limit)
		l.stepHit.Store(true)
		t.Cancel(stepsCancel)
	}
	return limit
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
	if l.memHit.Load() {
		return &CallError{Kind: LimitExceeded, Msg: memoryMsg()}
	}
	if l.stepHit.Load() {
		return &CallError{Kind: LimitExceeded, Msg: fmt.Sprintf("the script ran more than %d steps", l.stepCap.Load())}
	}
	return nil
}

func memoryMsg() string {
	return fmt.Sprintf("the script's memory use grew past %d MiB", MaxHeapGrowth>>20)
}

func liveHeap() uint64 {
	s := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return s[0].Value.Uint64()
}

// heapWatch is the memory watchdog of one run.
type heapWatch struct {
	hit  atomic.Bool
	once sync.Once
	done chan struct{}
	end  chan struct{}
}

// watchHeap starts the watchdog: a goroutine that samples the live heap every
// HeapSampleEvery and, once it has grown more than MaxHeapGrowth past base (the
// live heap at the start of the run, see liveHeap), sets hit and calls cancel with the reason (once). The caller must call
// stop when the run ends, however it ends: it stops the goroutine and waits.
func watchHeap(base uint64, cancel func(reason string)) *heapWatch {
	w := &heapWatch{done: make(chan struct{}), end: make(chan struct{})}
	limit, every := MaxHeapGrowth, HeapSampleEvery
	go func() {
		defer close(w.end)
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-w.done:
				return
			case <-tick.C:
				if cur := liveHeap(); cur > base && cur-base > limit {
					w.hit.Store(true)
					cancel(memoryMsg())
					return
				}
			}
		}
	}()
	return w
}

func (w *heapWatch) stop() {
	w.once.Do(func() { close(w.done) })
	<-w.end
}
