package hooks

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) logf(f string, a ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(f, a...))
	l.mu.Unlock()
}

// reg builds a registry of one hook against url, with the credential on disk.
func reg(t *testing.T, mut func(*Hook), o Options) (*Registry, *clock, *logs) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "acme.token"), []byte(secretCredential+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := Hook{Name: "acme/review", Point: PointWriteVeto, Plugin: "acme", URL: "http://127.0.0.1:1/x", Credential: "acme.token", FailurePolicy: FailClosed}
	if mut != nil {
		mut(&h)
	}
	ck := &clock{t: time.Unix(1_700_000_000, 0)}
	lg := &logs{}
	o.CredentialsDir, o.Now, o.Logf = dir, ck.now, lg.logf
	if o.Approved == nil {
		o.Approved = func(string, string) bool { return true }
	}
	r, err := NewRegistry([]Hook{h}, o)
	if err != nil {
		t.Fatal(err)
	}
	return r, ck, lg
}

func atURL(u string) func(*Hook) { return func(h *Hook) { h.URL = u } }

func TestFailurePolicy(t *testing.T) {
	down := func(h *Hook) { h.URL = "http://127.0.0.1:1/x" }
	r, _, _ := reg(t, down, Options{})
	if out := r.Decide(context.Background(), "acme/review", Request{}); out.Decision != Deny || !out.Unavailable || out.Verdict != "error" {
		t.Fatalf("fail_closed: %+v", out)
	}
	r, _, _ = reg(t, func(h *Hook) { down(h); h.FailurePolicy = FailOpen }, Options{})
	if out := r.Decide(context.Background(), "acme/review", Request{}); out.Decision != NoOpinion || out.Err == nil || out.Verdict != "error" {
		t.Fatalf("fail_open: %+v", out)
	}
}

func TestDryRunNeverBlocks(t *testing.T) {
	s, _ := serve(t, answer(`{"version":1,"decision":"deny","reason":"nope"}`))
	r, _, _ := reg(t, func(h *Hook) { h.URL, h.Mode = s.URL, ModeDryRun }, Options{})
	out := r.Decide(context.Background(), "acme/review", Request{})
	if out.Decision != NoOpinion || out.Verdict != "dry_run_deny" || !out.DryRun || out.Reason != "" {
		t.Fatalf("%+v", out)
	}
	r, _, _ = reg(t, func(h *Hook) { h.Mode = ModeDryRun }, Options{})
	out = r.Decide(context.Background(), "acme/review", Request{})
	if out.Decision != NoOpinion || out.Unavailable || out.Err == nil || out.Verdict != "" {
		t.Fatalf("a failure in dry run is logged only: %+v", out)
	}
}

func TestBreakerOpensProbesAndCloses(t *testing.T) {
	var healthy atomic.Bool
	s, n := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`{"version":1,"decision":"no_opinion"}`))
	})
	r, ck, lg := reg(t, atURL(s.URL), Options{})
	ctx := context.Background()
	for i := 0; i < BreakerThreshold; i++ {
		r.Decide(ctx, "acme/review", Request{})
	}
	if st := r.Metrics()[0].Breaker; st != StateOpen {
		t.Fatalf("state = %s", st)
	}
	hits := n.Load()
	out := r.Decide(ctx, "acme/review", Request{})
	if n.Load() != hits || out.Decision != Deny || out.Err != ErrBreakerOpen {
		t.Fatalf("open breaker must apply the policy without calling: %+v", out)
	}
	ck.add(BreakerOpenFor + time.Second)
	if st := r.Metrics()[0].Breaker; st != StateHalfOpen {
		t.Fatalf("state = %s", st)
	}
	r.Decide(ctx, "acme/review", Request{}) // the probe fails: open again
	if n.Load() != hits+1 || r.Metrics()[0].Breaker != StateOpen {
		t.Fatalf("a failed probe reopens: hits %d", n.Load())
	}
	ck.add(BreakerOpenFor + time.Second)
	healthy.Store(true)
	if out := r.Decide(ctx, "acme/review", Request{}); out.Err != nil {
		t.Fatal(out.Err)
	}
	if st := r.Metrics()[0].Breaker; st != StateClosed {
		t.Fatalf("state = %s", st)
	}
	m := r.Metrics()[0]
	if m.Calls != int64(BreakerThreshold)+2 || m.Failures != int64(BreakerThreshold)+2 {
		t.Fatalf("metrics: %+v", m)
	}
	if strings.Contains(strings.Join(lg.lines, "\n"), secretCredential) {
		t.Fatal("a log line carries the credential")
	}
}

func TestRegistryStartRefusals(t *testing.T) {
	dir := t.TempDir()
	h := Hook{Name: "acme/review", Point: PointWriteVeto, Plugin: "acme", URL: "https://hooks.example.com/v", Credential: "missing.token", FailurePolicy: FailClosed}
	yes := func(string, string) bool { return true }
	for name, tc := range map[string]struct {
		o    Options
		want string
	}{
		"no predicate":       {Options{CredentialsDir: dir}, "not an approved plugin"},
		"unapproved":         {Options{CredentialsDir: dir, Approved: func(string, string) bool { return false }}, "not an approved plugin"},
		"missing credential": {Options{CredentialsDir: dir, Approved: yes}, "not readable"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewRegistry([]Hook{h}, tc.o)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v", err)
			}
		})
	}
	if err := os.WriteFile(filepath.Join(dir, "missing.token"), []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry([]Hook{h}, Options{CredentialsDir: dir, Approved: yes}); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("an empty credential: %v", err)
	}
}

func TestReloadCredentials(t *testing.T) {
	var seen atomic.Value
	s, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"version":1}`))
	})
	r, _, _ := reg(t, atURL(s.URL), Options{})
	if err := os.WriteFile(filepath.Join(r.dir, "acme.token"), []byte("rotated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.ReloadCredentials(); err != nil {
		t.Fatal(err)
	}
	r.Decide(context.Background(), "acme/review", Request{})
	if seen.Load() != "Bearer rotated" {
		t.Fatalf("got %v", seen.Load())
	}
	_ = os.Remove(filepath.Join(r.dir, "acme.token"))
	if err := r.ReloadCredentials(); err == nil {
		t.Fatal("a vanished credential is reported")
	}
	r.Decide(context.Background(), "acme/review", Request{})
	if seen.Load() != "Bearer rotated" {
		t.Fatal("the credential in use is kept")
	}
}

func TestEmptyRegistryIsANoOp(t *testing.T) {
	var r *Registry
	if r.Len() != 0 || r.Has("a/b") || r.ForPoint(PointWriteVeto) != nil || r.Metrics() != nil {
		t.Fatal("a nil registry answers nothing")
	}
	e, err := NewRegistry(nil, Options{})
	if err != nil || e.Len() != 0 {
		t.Fatalf("%v", err)
	}
}

func TestFailedCallLeaksNeitherCredentialNorURL(t *testing.T) {
	r, _, lg := reg(t, func(h *Hook) { h.URL = "http://127.0.0.1:1/path?token=queryvalue" }, Options{})
	out := r.Decide(context.Background(), "acme/review", Request{})
	all := out.Err.Error() + strings.Join(lg.lines, "\n")
	if strings.Contains(all, secretCredential) || strings.Contains(all, "queryvalue") {
		t.Fatalf("leak: %s", all)
	}
}

func TestWrongVersionIsAFailure(t *testing.T) {
	s, _ := serve(t, answer(`{"version":2,"decision":"deny"}`))
	r, _, _ := reg(t, atURL(s.URL), Options{})
	if out := r.Decide(context.Background(), "acme/review", Request{}); out.Err == nil || !out.Unavailable {
		t.Fatalf("%+v", out)
	}
}

func TestADenyAnswerDenies(t *testing.T) {
	s, _ := serve(t, answer(`{"version":1,"decision":"deny","reason":"no"}`))
	r, _, _ := reg(t, atURL(s.URL), Options{})
	if out := r.Decide(context.Background(), "acme/review", Request{}); out.Decision != Deny || out.Verdict != "deny" || out.Unavailable {
		t.Fatalf("%+v", out)
	}
}

// A hook observed in dry_run must not hand its answer to the point: an identity
// or resolver answer would otherwise take effect while "only recording".
func TestDryRunHandsBackNoAnswer(t *testing.T) {
	s, _ := serve(t, answer(`{"version":1,"decision":"no_opinion","user":"u1","groups":["g"],"value":"hunter2"}`))
	r, _, _ := reg(t, func(h *Hook) { h.URL, h.Mode = s.URL, ModeDryRun }, Options{})
	out := r.Decide(context.Background(), "acme/review", Request{})
	if !out.DryRun || out.Answer.Raw != nil || out.Decision != NoOpinion || out.Err != nil {
		t.Fatalf("%+v", out)
	}
}

func TestCancelledCallsNeverOpenTheBreaker(t *testing.T) {
	for policy, wantDeny := range map[string]bool{FailClosed: true, FailOpen: false} {
		t.Run(policy, func(t *testing.T) {
			s, _ := serve(t, func(w http.ResponseWriter, r *http.Request) { waitGone(r) })
			r, _, _ := reg(t, func(h *Hook) { h.URL, h.Timeout, h.FailurePolicy = s.URL, 5*time.Second, policy }, Options{})
			for i := 0; i < BreakerThreshold+3; i++ {
				ctx, cancel := context.WithCancel(context.Background())
				time.AfterFunc(20*time.Millisecond, cancel)
				out := r.Decide(ctx, "acme/review", Request{})
				// A cancelled veto is not an allow under fail_closed.
				if out.Err != ErrCanceled || (out.Decision == Deny) != wantDeny || out.Unavailable != wantDeny {
					t.Fatalf("a cancelled call follows the policy: %+v", out)
				}
			}
			m := r.Metrics()[0]
			if m.Breaker != StateClosed || m.Failures != 0 || m.Calls != 0 {
				t.Fatalf("a cancel is no failure and no call: %+v", m)
			}
		})
	}
}

func TestCancelledDryRunContinues(t *testing.T) {
	s, _ := serve(t, func(w http.ResponseWriter, r *http.Request) { waitGone(r) })
	r, _, _ := reg(t, func(h *Hook) { h.URL, h.Timeout, h.Mode = s.URL, 5*time.Second, ModeDryRun }, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	if out := r.Decide(ctx, "acme/review", Request{}); out.Err != ErrCanceled || out.Decision != NoOpinion || out.Unavailable || !out.DryRun {
		t.Fatalf("%+v", out)
	}
}

func TestCancelledProbeFreesTheProbeSlot(t *testing.T) {
	var cancelNext atomic.Bool
	s, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if cancelNext.Load() {
			waitGone(r)
			return
		}
		w.WriteHeader(503)
	})
	r, ck, _ := reg(t, func(h *Hook) { h.URL, h.Timeout = s.URL, 5*time.Second }, Options{})
	for i := 0; i < BreakerThreshold; i++ {
		r.Decide(context.Background(), "acme/review", Request{})
	}
	ck.add(BreakerOpenFor + time.Second)
	cancelNext.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	if out := r.Decide(ctx, "acme/review", Request{}); out.Err != ErrCanceled {
		t.Fatalf("%+v", out)
	}
	cancelNext.Store(false)
	// The slot is free: the next call probes (and fails the 503), instead of
	// being refused by a wedged breaker.
	if out := r.Decide(context.Background(), "acme/review", Request{}); out.Err == ErrBreakerOpen {
		t.Fatal("the cancelled probe wedged the breaker")
	}
}

func TestUnknownHookDeniesAndNilRegistryIsEmpty(t *testing.T) {
	r, _, _ := reg(t, nil, Options{})
	out := r.Decide(context.Background(), "acme/typo", Request{})
	if out.Decision != Deny || !out.Unavailable || out.Err == nil {
		t.Fatalf("an unknown hook is a wiring error that denies: %+v", out)
	}
	var none *Registry
	if out := none.Decide(context.Background(), "acme/review", Request{}); out.Hook != "acme/review" || out.Decision != NoOpinion || out.Err != nil || out.Unavailable {
		t.Fatalf("a nil registry answers an empty outcome: %+v", out)
	}
}
