package hooks

import (
	"sync/atomic"
	"time"
)

// A breaker opens after BreakerThreshold consecutive failures and stays open
// BreakerOpenFor; then it lets exactly one probe call through (half-open). All
// state is atomic: no mutex, so a dead hook never serialises the callers.
const (
	BreakerThreshold = 5
	BreakerOpenFor   = 30 * time.Second
	// RetryAfterSeconds is the Retry-After a request refused because the
	// identity hook is down carries: the window after which the breaker lets a
	// probe through.
	RetryAfterSeconds = int(BreakerOpenFor / time.Second)
)

// Breaker states, as Metrics reports them.
const (
	StateClosed   = "closed"
	StateOpen     = "open"
	StateHalfOpen = "half_open"
)

type breaker struct {
	fails     atomic.Int32
	openUntil atomic.Int64 // unix nanoseconds; 0 when closed
	probing   atomic.Bool
}

// allow reports whether a call may go out. probe is true for the single call
// admitted while half-open. Contract: every admitted call is settled exactly
// once by the caller, with success(probe), failure(now, probe) or, when the
// call ended without saying anything about the hook (the caller cancelled it),
// release(probe). A probe that is never settled would hold the probe slot and
// wedge the breaker half-open.
func (b *breaker) allow(now time.Time) (ok, probe bool) {
	until := b.openUntil.Load()
	if until == 0 {
		return true, false
	}
	if now.UnixNano() < until {
		return false, false
	}
	if b.probing.CompareAndSwap(false, true) {
		return true, true
	}
	return false, false
}

// success settles a call that worked. A call admitted before the breaker opened
// and finishing after is not evidence that the hook recovered: only the probe
// clears openUntil. A non-probe success never touches openUntil (not even when
// it races with the failure that opens the breaker: it would otherwise close it
// right after), it only resets the failure run while the breaker is closed.
// changed says the breaker was not closed.
func (b *breaker) success(probe bool) (changed bool) {
	if !probe {
		if b.openUntil.Load() == 0 {
			b.fails.Store(0)
		}
		return false
	}
	b.fails.Store(0)
	was := b.openUntil.Swap(0) != 0
	b.probing.Store(false)
	return was
}

// release settles a call that says nothing about the hook: it frees the probe
// slot so the next call may probe, and changes no count.
func (b *breaker) release(probe bool) {
	if probe {
		b.probing.Store(false)
	}
}

// failure settles a call that failed; opened says this call opened (or
// reopened) the breaker.
func (b *breaker) failure(now time.Time, probe bool) (opened bool) {
	if probe {
		b.openUntil.Store(now.Add(BreakerOpenFor).UnixNano())
		b.probing.Store(false)
		return true
	}
	if int(b.fails.Add(1)) >= BreakerThreshold && b.openUntil.Load() == 0 {
		b.openUntil.Store(now.Add(BreakerOpenFor).UnixNano())
		return true
	}
	return false
}

func (b *breaker) state(now time.Time) string {
	until := b.openUntil.Load()
	switch {
	case until == 0:
		return StateClosed
	case now.UnixNano() < until:
		return StateOpen
	}
	return StateHalfOpen
}
