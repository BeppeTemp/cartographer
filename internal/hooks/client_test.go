package hooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const secretCredential = "s3cret-bearer-value"

// serve answers every request through fn and counts them.
func serve(t *testing.T, fn http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.Add(1); fn(w, r) }))
	t.Cleanup(s.Close)
	return s, &n
}

func answer(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }
}

// waitGone blocks until the client has gone. The request body is drained first:
// the server only notices a closed connection once it has read the body, so a
// handler that waits without reading would hang (and so would Server.Close).
func waitGone(r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	<-r.Context().Done()
}

// hookAt is a normalized veto hook posting to url.
func hookAt(t *testing.T, url string, timeout time.Duration) *Hook {
	t.Helper()
	h := Hook{Name: "acme/review", Point: PointWriteVeto, Plugin: "acme", URL: url, Credential: "acme.token", FailurePolicy: FailClosed, Timeout: timeout}
	if err := h.Normalize(); err != nil {
		t.Fatal(err)
	}
	return &h
}

func call(t *testing.T, url string, req Request) (Answer, error) {
	t.Helper()
	return NewClient(nil, nil).Call(context.Background(), hookAt(t, url, 0), secretCredential, req)
}

func TestCallDecisionParsing(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want Decision
		fail bool
	}{
		"no opinion":    {`{"version":1,"decision":"no_opinion"}`, NoOpinion, false},
		"allow is none": {`{"version":1,"decision":"allow"}`, NoOpinion, false},
		"unknown":       {`{"version":1,"decision":"maybe"}`, NoOpinion, false},
		"missing":       {`{"version":1}`, NoOpinion, false},
		"deny":          {`{"version":1,"decision":"deny","reason":"no","extra":{"x":1}}`, Deny, false},
		"wrong version": {`{"version":2,"decision":"deny"}`, NoOpinion, true},
		"no version":    {`{"decision":"deny"}`, NoOpinion, true},
		"not json":      {`<html>`, NoOpinion, true},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := serve(t, answer(tc.body))
			a, err := call(t, s.URL, Request{KB: "kb-a"})
			if (err != nil) != tc.fail {
				t.Fatalf("err = %v", err)
			}
			if !tc.fail && a.Decision != tc.want {
				t.Fatalf("decision = %v", a.Decision)
			}
		})
	}
}

func TestCallKeepsPointFields(t *testing.T) {
	s, _ := serve(t, answer(`{"version":1,"decision":"no_opinion","user":"u1","unknown_later":true}`))
	a, err := call(t, s.URL, Request{})
	if err != nil || string(a.Raw["user"]) != `"u1"` {
		t.Fatalf("%v %v", a.Raw, err)
	}
}

func TestEnvelopeAndHeaders(t *testing.T) {
	var got map[string]any
	var hdr http.Header
	s, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Clone()
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"version":1,"decision":"no_opinion"}`))
	})
	if _, err := call(t, s.URL, Request{KB: "kb-a", Fields: map[string]any{"tool": "concept_write", "version": 99, "kb": "forged"}}); err != nil {
		t.Fatal(err)
	}
	if hdr.Get("Cartographer-Hook") != PointWriteVeto || hdr.Get("Cartographer-Hook-Version") != "1" || hdr.Get("Cartographer-Request-Id") == "" || hdr.Get("Authorization") != "Bearer "+secretCredential {
		t.Fatalf("headers: %v", hdr)
	}
	if got["version"] != float64(1) || got["hook"] != "acme/review" || got["kb"] != "kb-a" || got["tool"] != "concept_write" || got["request_id"] != hdr.Get("Cartographer-Request-Id") || got["time"] == nil {
		t.Fatalf("envelope: %v", got)
	}
}

func TestDenyReasonIsBounded(t *testing.T) {
	long := strings.Repeat("a", 500)
	s, _ := serve(t, answer(`{"version":1,"decision":"deny","reason":"bad\u0000\n`+long+`"}`))
	a, err := call(t, s.URL, Request{})
	if err != nil || a.Decision != Deny || len(a.Reason) != MaxReasonLen || strings.ContainsAny(a.Reason, "\x00\n") {
		t.Fatalf("reason %q (%d) %v", a.Reason, len(a.Reason), err)
	}
}

func TestTimeout(t *testing.T) {
	release := make(chan struct{})
	s, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	start := time.Now()
	_, err := NewClient(nil, nil).Call(context.Background(), hookAt(t, s.URL, 50*time.Millisecond), secretCredential, Request{})
	if err == nil || !strings.Contains(err.Error(), "timeout") || time.Since(start) > 3*time.Second {
		t.Fatalf("got %v after %s", err, time.Since(start))
	}
}

func TestRedirectIsNeverFollowed(t *testing.T) {
	target, n := serve(t, answer(`{"version":1,"decision":"deny"}`))
	s, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	if _, err := call(t, s.URL, Request{}); err == nil || n.Load() != 0 {
		t.Fatalf("a redirect is a failure and is not followed: err=%v hits=%d", err, n.Load())
	}
}

func TestOversizedBody(t *testing.T) {
	s, _ := serve(t, answer(`{"version":1,"decision":"no_opinion","pad":"`+strings.Repeat("x", MaxBody)+`"}`))
	if _, err := call(t, s.URL, Request{}); err == nil {
		t.Fatal("a body over 64 KiB is a failure")
	}
}

func TestNon2xx(t *testing.T) {
	s, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"version":1,"decision":"no_opinion"}`))
	})
	if _, err := call(t, s.URL, Request{}); err == nil {
		t.Fatal("a 500 is a failure whatever the body")
	}
}

func TestTLSVerificationIsOn(t *testing.T) {
	var n atomic.Int32
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		_, _ = w.Write([]byte(`{"version":1,"decision":"no_opinion"}`))
	}))
	defer s.Close()
	if _, err := call(t, s.URL, Request{}); err == nil || n.Load() != 0 {
		t.Fatalf("an unknown certificate authority must fail the call: %v", err)
	}
	if _, err := NewClient(s.Client().Transport, nil).Call(context.Background(), hookAt(t, s.URL, 0), secretCredential, Request{}); err != nil {
		t.Fatalf("trusted: %v", err)
	}
}

func TestErrorsAreAFixedClassWithoutHostOrCredential(t *testing.T) {
	_, err := call(t, "http://127.0.0.1:1/path?token=queryvalue", Request{})
	if err == nil || err.Error() != "connection refused" {
		t.Fatalf("want the class only, got %v", err)
	}
	// A TLS failure names no address either.
	s := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer s.Close()
	_, err = call(t, s.URL, Request{})
	if err == nil || err.Error() != "tls failure" || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("got %v", err)
	}
}

func TestCallerCancelIsNotAHookFailure(t *testing.T) {
	s, _ := serve(t, func(w http.ResponseWriter, r *http.Request) { waitGone(r) })
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := NewClient(nil, nil).Call(ctx, hookAt(t, s.URL, 5*time.Second), secretCredential, Request{})
	if err != ErrCanceled {
		t.Fatalf("got %v", err)
	}
}
