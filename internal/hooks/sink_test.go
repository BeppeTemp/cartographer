package hooks

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/audit"
)

// sinkReceiver records the entry ids it was handed and acknowledges each
// batch, unless fail says otherwise.
type sinkReceiver struct {
	mu   sync.Mutex
	ids  []string
	gaps int
	fail bool
}

func (rc *sinkReceiver) handler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
		LastHash string `json:"last_hash"`
		Gap      bool   `json:"gap"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.fail {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	for _, e := range body.Events {
		rc.ids = append(rc.ids, e.ID)
	}
	if body.Gap {
		rc.gaps++
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"version": 1, "ack": body.LastHash})
}

func (rc *sinkReceiver) got() []string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return append([]string(nil), rc.ids...)
}

func auditSource(l *audit.Log) SinkSource {
	return func(cursor string, limit int) ([]SinkEvent, bool, error) {
		es, gap, err := l.EventsAfter(cursor, limit)
		out := make([]SinkEvent, len(es))
		for i, e := range es {
			b, _ := json.Marshal(e)
			out[i] = SinkEvent{Hash: e.Hash, Entry: b}
		}
		return out, gap, err
	}
}

func appendN(t *testing.T, l *audit.Log, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := l.AppendEvent(audit.Entry{RequestID: "r", Phase: audit.PhaseAttempt, Tool: "concept_read"}); err != nil {
			t.Fatal(err)
		}
	}
}

func sinkReg(t *testing.T, url string, mut func(*Hook)) *Registry {
	r, _ := sinkRegClock(t, url, mut)
	return r
}

func sinkRegClock(t *testing.T, url string, mut func(*Hook)) (*Registry, *clock) {
	t.Helper()
	r, ck, _ := reg(t, func(h *Hook) {
		h.Name, h.Point, h.URL, h.BatchSize = "acme/siem", PointAuditSink, url, 3
		if mut != nil {
			mut(h)
		}
	}, Options{})
	return r, ck
}

// Delivery resumes from the persisted cursor: no duplicate and no loss across
// a restart of the sink.
func TestSink_ReplayFromTheCursor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	l, err := audit.OpenWithOptions(filepath.Join(dir, "audit.jsonl"), audit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	rc := &sinkReceiver{}
	s, _ := serve(t, rc.handler)
	r := sinkReg(t, s.URL, nil)
	appendN(t, l, 7)
	sk, err := r.NewSink("acme/siem", auditSource(l), nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sk.drain(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	appendN(t, l, 4)
	// A new sink (a restart) reads the cursor file.
	sk2, err := r.NewSink("acme/siem", auditSource(l), nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sk2.drain(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	ids := rc.got()
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate %s", id)
		}
		seen[id] = true
	}
	if len(ids) != 11 {
		t.Fatalf("delivered %d, want 11", len(ids))
	}
}

// A sink that always fails never slows an append, and the tailer delivers what
// it missed once the hook is back.
func TestSink_FailingHookNeverSlowsAnAppend(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	l, err := audit.OpenWithOptions(filepath.Join(dir, "audit.jsonl"), audit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	rc := &sinkReceiver{fail: true}
	s, _ := serve(t, rc.handler)
	r, ck := sinkRegClock(t, s.URL, nil)
	sk, err := r.NewSink("acme/siem", auditSource(l), l.Wake(), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sk.Run(ctx); close(done) }()
	start := time.Now()
	appendN(t, l, 200)
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("200 appends took %s with a failing sink", d)
	}
	rc.mu.Lock()
	rc.fail = false
	rc.mu.Unlock()
	ck.add(BreakerOpenFor + time.Second) // the failures opened the breaker
	if _, err := sk.drain(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	if n := len(rc.got()); n != 200 {
		t.Fatalf("delivered %d, want 200", n)
	}
}

// Await delivers through the given hash; a batch the hook does not acknowledge
// is a failure that leaves the cursor where it was.
func TestSink_AwaitAndAck(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	l, err := audit.OpenWithOptions(filepath.Join(dir, "audit.jsonl"), audit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	rc := &sinkReceiver{}
	s, _ := serve(t, rc.handler)
	r := sinkReg(t, s.URL, func(h *Hook) { h.Blocking = true })
	appendN(t, l, 5)
	last, _ := l.TailFunc(1, nil)
	sk, err := r.NewSink("acme/siem", auditSource(l), nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := sk.Await(context.Background(), last[0].Hash); err != nil || len(rc.got()) != 5 {
		t.Fatalf("await: %v, delivered %d", err, len(rc.got()))
	}
	// Already delivered: nothing to send, no error.
	if err := sk.Await(context.Background(), last[0].Hash); err != nil {
		t.Fatal(err)
	}
	bad, _ := serve(t, answer(`{"version":1,"ack":"something-else"}`))
	r2 := sinkReg(t, bad.URL, nil)
	sk2, err := r2.NewSink("acme/siem", auditSource(l), nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := sk2.Await(context.Background(), last[0].Hash); err == nil || sk2.cursor != "" {
		t.Fatalf("unacknowledged: %v, cursor %q", err, sk2.cursor)
	}
	if _, err := r.NewSink("acme/other", auditSource(l), nil, dir); err == nil {
		t.Fatal("a sink for an unknown hook")
	}
}

// A gap (entries removed by retention after the cursor) is reported to the hook.
func TestSink_ReportsAGap(t *testing.T) {
	t.Parallel()
	rc := &sinkReceiver{}
	s, _ := serve(t, rc.handler)
	r := sinkReg(t, s.URL, nil)
	src := func(cursor string, limit int) ([]SinkEvent, bool, error) {
		if cursor == "h2" {
			return nil, false, nil
		}
		return []SinkEvent{{Hash: "h2", Entry: json.RawMessage(`{"id":"e2"}`)}}, true, nil
	}
	sk, err := r.NewSink("acme/siem", src, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sk.drain(context.Background(), ""); err != nil || rc.gaps != 1 {
		t.Fatalf("gap: %v, %d", err, rc.gaps)
	}
}

// Await is bounded by the hook's timeout even while the tailer delivers a
// long backlog through a slow hook.
func TestSink_AwaitIsBoundedWhileTheTailerDelivers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	l, err := audit.OpenWithOptions(filepath.Join(dir, "audit.jsonl"), audit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	rc := &sinkReceiver{}
	s, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		rc.handler(w, r)
	})
	r := sinkReg(t, s.URL, func(h *Hook) { h.BatchSize, h.Timeout = 1, time.Second })
	appendN(t, l, 30)
	last, _ := l.TailFunc(1, nil)
	sk, err := r.NewSink("acme/siem", auditSource(l), nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, _ = sk.drain(ctx, "") }() // the tailer, with 6 s of backlog
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	err = sk.Await(context.Background(), last[0].Hash)
	if d := time.Since(start); err == nil || d > 2*time.Second {
		t.Fatalf("await: %v after %s, want a timeout within about 1 s", err, d)
	}
}

// During a backoff an append does not trigger a delivery.
func TestSink_BackoffIgnoresWakeUps(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	l, err := audit.OpenWithOptions(filepath.Join(dir, "audit.jsonl"), audit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	s, hits := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	r := sinkReg(t, s.URL, nil)
	sk, err := r.NewSink("acme/siem", auditSource(l), l.Wake(), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sk.Run(ctx); close(done) }()
	appendN(t, l, 1) // the first wake-up: one failed delivery, then a 1 s backoff
	time.Sleep(100 * time.Millisecond)
	before := hits.Load()
	for i := 0; i < 20; i++ {
		appendN(t, l, 1)
		time.Sleep(10 * time.Millisecond)
	}
	if got := hits.Load(); got != before {
		t.Fatalf("%d deliveries during the backoff", got-before)
	}
	cancel()
	<-done
}
