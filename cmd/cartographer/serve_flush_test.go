package main

import (
	"sync"
	"testing"
	"time"
)

// barrierFlusher returns only once every flusher of the set is in flight, so a
// sequential loop deadlocks (the test then fails by timeout, no timing assert).
type barrierFlusher struct {
	wg      *sync.WaitGroup
	timeout chan time.Duration
}

func (b barrierFlusher) FlushPush(timeout time.Duration) error {
	b.timeout <- timeout
	b.wg.Done()
	b.wg.Wait()
	return nil
}

func TestFlushPushes_ParallelUnderOneBudget(t *testing.T) {
	const n = 4
	budget := 10 * time.Second
	var wg sync.WaitGroup
	wg.Add(n)
	timeouts := make(chan time.Duration, n)
	flushers := make([]pushFlusher, n)
	for i := range flushers {
		flushers[i] = barrierFlusher{wg: &wg, timeout: timeouts}
	}
	done := make(chan struct{})
	go func() { flushPushes(flushers, budget); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("flushPushes did not return: the flushers ran one after the other")
	}
	close(timeouts)
	for got := range timeouts {
		if got > budget {
			t.Errorf("a flusher received %s, more than the overall budget %s", got, budget)
		}
	}
}
