package worker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const canary = "sk-canary-0123456789"

func TestRetryWait(t *testing.T) {
	tr := &transport{retryDelay: time.Second}
	for _, c := range []struct {
		n    int
		hdr  string
		want time.Duration
	}{
		{0, "", time.Second}, {1, "", 2 * time.Second},
		{0, "5", 5 * time.Second}, {0, "999", 30 * time.Second},
		{0, "Wed, 21 Oct 2026 07:28:00 GMT", time.Second}, // date form ignored
	} {
		if got := tr.retryWait(c.n, c.hdr); got != c.want {
			t.Errorf("retryWait(%d,%q) = %v, want %v", c.n, c.hdr, got, c.want)
		}
	}
}

func newTestTransport(t *testing.T, url string, opts ...Option) *transport {
	t.Helper()
	tr, err := newTransport(url, canary, "m", append(opts, WithRetryDelay(time.Millisecond)))
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestTransport_RetryThenSuccess(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(503 - calls%2*74) // 502 then 503/429 mix
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	out, err := newTestTransport(t, srv.URL).post(context.Background(), "/x", nil, []byte("{}"))
	if err != nil || string(out) != "ok" || calls != 3 {
		t.Fatalf("out=%q err=%v calls=%d", out, err, calls)
	}
}

func TestTransport_ExhaustedFinalAndAuth(t *testing.T) {
	for _, c := range []struct {
		status, calls int
		auth          bool
	}{{500, 3, false}, {400, 1, false}, {401, 1, true}, {403, 1, true}} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(strings.Repeat("x", 5000) + canary))
		}))
		_, err := newTestTransport(t, srv.URL).post(context.Background(), "/x", nil, []byte("{}"))
		srv.Close()
		if err == nil || calls != c.calls || errors.Is(err, ErrModelAuth) != c.auth {
			t.Errorf("status %d: err=%v calls=%d", c.status, err, calls)
		}
		if err != nil && (strings.Contains(err.Error(), canary) || len(err.Error()) > 700) {
			t.Errorf("status %d: error not capped/scrubbed (len %d)", c.status, len(err.Error()))
		}
	}
}

func TestTransport_CancelMidBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	tr, _ := newTransport(srv.URL, canary, "m", []Option{WithRetryDelay(time.Minute)})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	if _, err := tr.post(ctx, "/x", nil, nil); err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
}

func TestTransport_URLGateAndRedirect(t *testing.T) {
	if _, err := newTransport("http://example.com/v1", "k", "m", nil); err == nil {
		t.Error("plain http to a non-loopback host accepted")
	}
	if _, err := newTransport("http://example.com/v1", "k", "m", []Option{WithAllowInsecureHTTP(true)}); err != nil {
		t.Errorf("opt-in refused: %v", err)
	}
	var leaked string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("x-api-key") + r.Header.Get("Authorization")
		_, _ = w.Write([]byte("ok"))
	}))
	defer other.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer redir.Close()
	h := map[string]string{"x-api-key": canary, "Authorization": "Bearer " + canary}
	if _, err := newTestTransport(t, redir.URL).post(context.Background(), "/x", h, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if leaked != "" {
		t.Errorf("credential followed the redirect: %q", leaked)
	}
}
