package hooks

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBreakerStateMachine(t *testing.T) {
	var b breaker
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < BreakerThreshold-1; i++ {
		if b.failure(now, false) {
			t.Fatal("opened before the threshold")
		}
	}
	if !b.failure(now, false) || b.state(now) != StateOpen {
		t.Fatal("the threshold opens it")
	}
	if ok, _ := b.allow(now); ok {
		t.Fatal("open admits nothing")
	}
	later := now.Add(BreakerOpenFor + time.Second)
	if b.state(later) != StateHalfOpen {
		t.Fatalf("state = %s", b.state(later))
	}
	ok, probe := b.allow(later)
	if !ok || !probe {
		t.Fatal("half-open admits one probe")
	}
	if !b.failure(later, true) || b.state(later) != StateOpen {
		t.Fatal("a failed probe reopens")
	}
	later = later.Add(BreakerOpenFor + time.Second)
	if ok, probe := b.allow(later); !ok || !probe {
		t.Fatal("a second probe after the new window")
	}
	if !b.success(true) || b.state(later) != StateClosed {
		t.Fatal("a successful probe closes")
	}
	if b.success(false) {
		t.Fatal("closing a closed breaker is no transition")
	}
}

func TestOnlyOneProbeWhenHalfOpen(t *testing.T) {
	var b breaker
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < BreakerThreshold; i++ {
		b.failure(now, false)
	}
	later := now.Add(BreakerOpenFor + time.Second)
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := b.allow(later); ok {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatalf("%d probes admitted", admitted.Load())
	}
}

func TestLateSuccessDoesNotCloseAnOpenBreaker(t *testing.T) {
	var b breaker
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < BreakerThreshold; i++ {
		b.failure(now, false)
	}
	// A call admitted before the breaker opened answers late.
	if b.success(false) || b.state(now) != StateOpen {
		t.Fatal("a late success must not close an open breaker")
	}
	later := now.Add(BreakerOpenFor + time.Second)
	if ok, probe := b.allow(later); !ok || !probe {
		t.Fatal("first probe")
	}
	if b.success(false) || b.state(later) != StateHalfOpen {
		t.Fatal("a late success must not close it while the probe is out")
	}
	if ok, _ := b.allow(later); ok {
		t.Fatal("a second probe must not be admitted while one is out")
	}
}

func TestReleasedProbeFreesTheSlot(t *testing.T) {
	var b breaker
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < BreakerThreshold; i++ {
		b.failure(now, false)
	}
	later := now.Add(BreakerOpenFor + time.Second)
	if ok, probe := b.allow(later); !ok || !probe {
		t.Fatal("first probe")
	}
	b.release(true)
	if b.state(later) != StateHalfOpen {
		t.Fatal("a released probe changes no state")
	}
	if ok, probe := b.allow(later); !ok || !probe {
		t.Fatal("the slot is free again after release")
	}
}

func TestSuccessWhileClosedResetsTheFailureRun(t *testing.T) {
	var b breaker
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < BreakerThreshold-1; i++ {
		b.failure(now, false)
	}
	if b.success(false) {
		t.Fatal("a success while closed is no transition")
	}
	for i := 0; i < BreakerThreshold-1; i++ {
		if b.failure(now, false) {
			t.Fatal("the run restarted after the success")
		}
	}
	if !b.failure(now, false) || b.state(now) != StateOpen {
		t.Fatal("the fifth consecutive failure opens it")
	}
	// A stale success after the opening (its call was admitted before) leaves
	// openUntil alone: only a probe closes the breaker.
	if b.success(false); b.state(now) != StateOpen {
		t.Fatal("a stale success must never clear openUntil")
	}
}
