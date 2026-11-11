package hooks

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// ErrBreakerOpen is the failure of a call the breaker did not let out.
var ErrBreakerOpen = errors.New("circuit breaker open")

// Options are what NewRegistry needs from the server.
type Options struct {
	// CredentialsDir is the directory the hooks' credential files are read from.
	CredentialsDir string
	// Approved says whether the plugin is approved to serve the point: a hook is
	// active only when the operator's entry AND an approved plugin exist. Nil
	// approves nothing, so a registry with hooks and no predicate refuses to
	// build (fail closed).
	Approved func(plugin, point string) bool
	// Transport, Now and Logf are test seams; the defaults are the verifying
	// default transport, the wall clock and the standard logger.
	Transport http.RoundTripper
	Now       func() time.Time
	Logf      func(format string, args ...any)
}

type entry struct {
	Hook
	cred     atomic.Pointer[string]
	br       breaker
	calls    atomic.Int64
	failures atomic.Int64
}

// Registry is the set of hooks of a server, built once at start.
type Registry struct {
	entries []*entry
	byName  map[string]*entry
	client  *Client
	now     func() time.Time
	logf    func(string, ...any)
	dir     string
}

// NewRegistry judges the entries, checks each against the approved plugins and
// reads each credential. Any problem refuses the start, naming the hook; none
// prints a credential. No entries yields an empty registry whose every query is
// a no-op: removing the hooks restores today's behaviour exactly.
func NewRegistry(hs []Hook, o Options) (*Registry, error) {
	hs = append([]Hook(nil), hs...)
	if err := NormalizeAll(hs); err != nil {
		return nil, err
	}
	r := &Registry{byName: map[string]*entry{}, dir: o.CredentialsDir, now: o.Now, logf: o.Logf}
	if r.now == nil {
		r.now = time.Now
	}
	if r.logf == nil {
		r.logf = log.Printf
	}
	r.client = NewClient(o.Transport, r.now)
	for i := range hs {
		h := hs[i]
		if o.Approved == nil || !o.Approved(h.Plugin, h.Point) {
			return nil, fmt.Errorf("hook %q: plugin %q is not an approved plugin that serves %s", h.Name, h.Plugin, h.Point)
		}
		e := &entry{Hook: h}
		r.entries = append(r.entries, e)
		r.byName[h.Name] = e
	}
	if err := r.ReloadCredentials(); err != nil {
		return nil, err
	}
	return r, nil
}

// ReloadCredentials rereads every credential file; on any failure it keeps the
// ones in use and returns the error (a missing file at start refuses the start).
func (r *Registry) ReloadCredentials() error {
	fresh := make([]string, len(r.entries))
	for i, e := range r.entries {
		b, err := os.ReadFile(filepath.Join(r.dir, e.Credential))
		if err != nil {
			return fmt.Errorf("hook %q: credential file %q is not readable: %v", e.Name, e.Credential, errors.Unwrap(err))
		}
		fresh[i] = strings.TrimSpace(string(b))
		if fresh[i] == "" {
			return fmt.Errorf("hook %q: credential file %q is empty", e.Name, e.Credential)
		}
	}
	for i, e := range r.entries {
		e.cred.Store(&fresh[i])
	}
	return nil
}

// Len is the number of hooks; zero for a nil registry.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.entries)
}

// Has says whether a hook of that name is configured.
func (r *Registry) Has(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.byName[name]
	return ok
}

// Hook returns a copy of the named hook's configuration.
func (r *Registry) Hook(name string) (Hook, bool) {
	if r == nil {
		return Hook{}, false
	}
	e, ok := r.byName[name]
	if !ok {
		return Hook{}, false
	}
	return e.Hook, true
}

// ForPoint lists the hooks serving a point, in config order.
func (r *Registry) ForPoint(point string) []Hook {
	if r == nil {
		return nil
	}
	var out []Hook
	for _, e := range r.entries {
		if e.Point == point {
			out = append(out, e.Hook)
		}
	}
	return out
}

// Outcome is the result of one Decide: the policy already applied.
type Outcome struct {
	Hook string
	// Decision is Deny only when the caller must refuse the request.
	Decision Decision
	Reason   string
	// Unavailable marks a Deny that comes from a failure under fail_closed (or
	// from a hook that is not configured), not from the hook's answer (the
	// caller codes it hook_unavailable).
	Unavailable bool
	// Verdict is what the audit records: "deny", "dry_run_deny", "error" or "".
	Verdict string
	// DryRun says the hook runs in dry_run mode: the outcome is a record, never
	// an input. Answer is then empty and Decision is NoOpinion, so an identity
	// or resolver answer cannot leak through a hook that is only being observed.
	DryRun bool
	// Answer is the parsed answer when the call succeeded (empty in dry_run).
	Answer Answer
	// Err is the failure, when there was one (also under fail_open and dry_run).
	// ErrCanceled means the caller cancelled: the hook did not fail (the breaker
	// and counters are untouched), and the policy still decides the request.
	Err error
}

// Decide calls the named hook through its breaker and applies the operator's
// policy: fail_closed turns a failure into a deny, fail_open continues with the
// failure recorded, dry_run records what an enforcing hook would have decided
// and never blocks. A nil registry (no hooks) answers an empty outcome; a name
// that is not configured is a wiring error and denies (Unavailable), it never
// silently passes. A call the caller cancelled is neither a failure nor a
// success: the breaker and the counters are untouched, but the policy still
// decides (fail_closed denies, so a cancel never passes a veto). Decide takes
// no lock and starts no goroutine; the caller must not hold the KB lock (the
// veto sits outside it).
func (r *Registry) Decide(ctx context.Context, name string, req Request) Outcome {
	if r == nil {
		return Outcome{Hook: name}
	}
	e, ok := r.byName[name]
	if !ok {
		return Outcome{Hook: name, Decision: Deny, Unavailable: true, Verdict: "error", Err: fmt.Errorf("hook %q is not configured", name)}
	}
	now := r.now()
	allowed, probe := e.br.allow(now)
	if !allowed {
		e.failures.Add(1)
		return r.failed(e, ErrBreakerOpen)
	}
	ans, err := r.client.Call(ctx, &e.Hook, *e.cred.Load(), req)
	if errors.Is(err, ErrCanceled) {
		// The hook did not fail: no breaker input, no counter. But the request
		// has no answer from it, so the operator's policy decides what that
		// means (fail_closed denies, fail_open and dry_run continue): a client
		// must not be able to turn a slow veto into an allow by cancelling.
		e.br.release(probe)
		return r.failed(e, err)
	}
	e.calls.Add(1)
	if err != nil {
		e.failures.Add(1)
		if e.br.failure(r.now(), probe) {
			r.logf("hook %s: circuit breaker open for %s after a failure: %v", e.Name, BreakerOpenFor, err)
		} else {
			r.logf("hook %s: call failed: %v", e.Name, err)
		}
		return r.failed(e, err)
	}
	if e.br.success(probe) {
		r.logf("hook %s: circuit breaker closed", e.Name)
	}
	if e.Mode == ModeDryRun {
		out := Outcome{Hook: e.Name, DryRun: true}
		if ans.Decision == Deny {
			out.Verdict = "dry_run_deny"
			r.logf("hook %s: dry run, would deny: %s", e.Name, ans.Reason)
		}
		return out
	}
	out := Outcome{Hook: e.Name, Answer: ans}
	if ans.Decision == Deny {
		out.Decision, out.Reason, out.Verdict = Deny, ans.Reason, "deny"
	}
	return out
}

// failed applies the failure policy to a call that did not produce an answer.
func (r *Registry) failed(e *entry, err error) Outcome {
	out := Outcome{Hook: e.Name, Err: err, DryRun: e.Mode == ModeDryRun}
	if out.DryRun {
		return out // a failure in dry run is logged only
	}
	out.Verdict = "error"
	if e.FailurePolicy == FailClosed {
		out.Decision, out.Unavailable = Deny, true
	}
	return out
}

// Metrics is the snapshot of one hook for the logs.
type Metrics struct {
	Name     string
	Calls    int64
	Failures int64
	Breaker  string
}

// Metrics snapshots every hook in config order.
func (r *Registry) Metrics() []Metrics {
	if r == nil {
		return nil
	}
	now := r.now()
	out := make([]Metrics, len(r.entries))
	for i, e := range r.entries {
		out[i] = Metrics{Name: e.Name, Calls: e.calls.Load(), Failures: e.failures.Load(), Breaker: e.br.state(now)}
	}
	return out
}
