package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SinkEvent is one audit entry as the sink delivers it: its chain hash (the
// cursor) and the entry itself, encoded as the log stores it.
type SinkEvent struct {
	Hash  string
	Entry json.RawMessage
}

// SinkSource reads the chain after a cursor (audit.Log.EventsAfter, adapted:
// this package imports no other of the module).
type SinkSource func(cursor string, limit int) (events []SinkEvent, gap bool, err error)

const (
	sinkTick       = 2 * time.Second
	sinkBackoffMin = time.Second
	sinkBackoffMax = 60 * time.Second
	// SinkStopFlush bounds the last delivery attempt at shutdown.
	SinkStopFlush = 3 * time.Second
)

// Sink delivers the audit chain to one audit_sink hook, in chain order and at
// least once (a receiver deduplicates by the entry's id). The cursor, the hash
// of the last entry the hook acknowledged, is persisted next to the log, so a
// restart resumes where it was; the local chain stays the source of truth, so
// a failing hook loses nothing and never slows an append.
type Sink struct {
	reg        *Registry
	e          *entry
	src        SinkSource
	wake       <-chan struct{}
	cursorPath string

	turn   chan struct{} // one delivery at a time (the tailer, Await): a lock a deadline can give up on
	cursor string
	acked  ackRing // the hashes recently acknowledged, so Await knows its event is out
}

// ackRing remembers the last ackRingSize acknowledged hashes.
const ackRingSize = 4096

type ackRing struct {
	mu   sync.Mutex
	set  map[string]bool
	ring []string
	next int
}

func (a *ackRing) add(hash string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.set == nil {
		a.set, a.ring = map[string]bool{}, make([]string, ackRingSize)
	}
	delete(a.set, a.ring[a.next])
	a.ring[a.next], a.set[hash] = hash, true
	a.next = (a.next + 1) % ackRingSize
}

func (a *ackRing) has(hash string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.set[hash]
}

// NewSink builds the sink of the named audit_sink hook. wake is signalled after
// each append; dir is where the cursor file lives (the audit log's directory).
func (r *Registry) NewSink(name string, src SinkSource, wake <-chan struct{}, dir string) (*Sink, error) {
	e, ok := r.byName[name]
	if !ok || e.Point != PointAuditSink {
		return nil, fmt.Errorf("hook %q is not an audit_sink", name)
	}
	s := &Sink{reg: r, e: e, src: src, wake: wake, turn: make(chan struct{}, 1),
		cursorPath: filepath.Join(dir, "sink-"+strings.ReplaceAll(name, "/", ".")+".cursor")}
	b, err := os.ReadFile(s.cursorPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("hook %q: read the sink cursor: %w", name, err)
	}
	s.cursor = strings.TrimSpace(string(b))
	return s, nil
}

// Hook is the sink's hook configuration.
func (s *Sink) Hook() Hook { return s.e.Hook }

// Run delivers until ctx ends: at each wake-up and every 2 s, as many batches
// as the chain has; a failure backs off from 1 s to 60 s, during which a
// wake-up is ignored (an append must not turn the backoff into a retry per
// append). When ctx ends it makes one last attempt bounded by SinkStopFlush.
func (s *Sink) Run(ctx context.Context) {
	backoff := time.Duration(0)
	for {
		wait, wake := sinkTick, s.wake
		if backoff > 0 {
			wait, wake = backoff, nil
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			fctx, cancel := context.WithTimeout(context.Background(), SinkStopFlush)
			_, _ = s.drain(fctx, "")
			cancel()
			return
		case <-wake:
		case <-t.C:
		}
		t.Stop()
		if _, err := s.drain(ctx, ""); err != nil && ctx.Err() == nil {
			backoff = min(max(2*backoff, sinkBackoffMin), sinkBackoffMax)
			continue
		}
		backoff = 0
	}
}

// Await delivers the chain up to and including hash, bounded by the hook's
// timeout, waiting for a delivery already running included: the blocking
// mode, called after the attempt event is appended and outside the KB lock.
// An error means the hook did not acknowledge it in time.
func (s *Sink) Await(ctx context.Context, hash string) error {
	ctx, cancel := context.WithTimeout(ctx, s.e.Timeout)
	defer cancel()
	_, err := s.drain(ctx, hash)
	return err
}

// drain delivers batches until the chain is exhausted or, when until is set,
// until until is acknowledged (by this drain or another). The delivery turn is
// taken per batch, so a long backlog delivered by the tailer delays an Await by
// one batch at most, and never past its deadline.
func (s *Sink) drain(ctx context.Context, until string) (bool, error) {
	for {
		if until != "" && s.acked.has(until) {
			return true, nil
		}
		select {
		case s.turn <- struct{}{}:
		case <-ctx.Done():
			return false, ctx.Err()
		}
		done, err := s.batch(ctx)
		<-s.turn
		if err != nil || done {
			return done, err
		}
	}
}

// batch delivers the next batch after the cursor; done when there was none.
func (s *Sink) batch(ctx context.Context) (bool, error) {
	events, gap, err := s.src(s.cursor, s.e.BatchSize)
	if err != nil {
		s.reg.logf("hook %s: read the audit chain: %v", s.e.Name, err)
		return false, err
	}
	if len(events) == 0 {
		return true, nil // nothing left: an until was already delivered
	}
	if gap {
		s.reg.logf("hook %s: audit entries after the cursor were removed by retention; delivery resumes at the oldest retained entry", s.e.Name)
	}
	return false, s.deliver(ctx, events, gap)
}

// deliver posts one batch and, once acknowledged, moves the cursor.
func (s *Sink) deliver(ctx context.Context, events []SinkEvent, gap bool) error {
	entries := make([]json.RawMessage, len(events))
	for i, ev := range events {
		entries[i] = ev.Entry
	}
	first, last := events[0].Hash, events[len(events)-1].Hash
	ans, err := s.reg.call(ctx, s.e, Request{Fields: map[string]any{
		"events": entries, "first_hash": first, "last_hash": last, "gap": gap,
	}})
	if err != nil {
		return err
	}
	var ack string
	if json.Unmarshal(ans.Raw["ack"], &ack) != nil || ack != last {
		s.reg.logf("hook %s: the answer does not acknowledge the batch", s.e.Name)
		return errors.New("batch not acknowledged")
	}
	if err := writeCursor(s.cursorPath, last); err != nil {
		// The batch is delivered; the next start may deliver it again (at
		// least once), so the in-memory cursor still moves.
		s.reg.logf("hook %s: persist the sink cursor: %v", s.e.Name, err)
	}
	s.cursor = last
	for _, ev := range events {
		s.acked.add(ev.Hash)
	}
	return nil
}

// writeCursor replaces the cursor file atomically: a temporary file beside
// it, fsync, rename.
func writeCursor(path, hash string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sink-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once renamed
	if _, err := tmp.WriteString(hash + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
