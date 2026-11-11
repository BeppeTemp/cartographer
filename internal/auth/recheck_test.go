package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// serveRecheck runs one authenticated request and returns the Recheck the
// middleware attached to it (nil when it attached none).
func serveRecheck(t *testing.T, ts *TokenStore, token string) Recheck {
	t.Helper()
	var got Recheck
	h := ts.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = RecheckFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestRecheck_AuthOffHasNothingToRevoke(t *testing.T) {
	t.Parallel()
	if serveRecheck(t, NewTokenStore(nil), "") != nil {
		t.Fatal("a Recheck was attached with authentication off")
	}
}

func TestRecheck_FollowsTheCurrentGeneration(t *testing.T) {
	t.Parallel()
	wide := ScopedToken{Token: "tok", Principal: "p", Policy: Policy{Admin: true}}
	ts := NewScopedTokenStore([]ScopedToken{wide}).RequireAuth()
	re := serveRecheck(t, ts, "tok")
	if re == nil {
		t.Fatal("no Recheck on an authenticated request")
	}
	if p, ok := re(); !ok || p.ID != "p" || !p.Policy.Admin {
		t.Fatalf("recheck = %+v, %v", p, ok)
	}

	// A reload that narrows the token: the same Recheck returns the new policy.
	narrow := ScopedToken{Token: "tok", Principal: "p", Policy: Policy{Permissions: []Permission{{KB: "docs", Maps: []string{"ops"}}}}}
	if err := ts.Replace([]ScopedToken{narrow}); err != nil {
		t.Fatal(err)
	}
	if p, ok := re(); !ok || p.Policy.Admin || len(p.Policy.Permissions) != 1 {
		t.Fatalf("after narrowing: %+v, %v", p, ok)
	}

	// A reload that drops the token revokes it.
	other := ScopedToken{Token: "other", Principal: "q", Policy: Policy{Admin: true}}
	if err := ts.Replace([]ScopedToken{other}); err != nil {
		t.Fatal(err)
	}
	if _, ok := re(); ok {
		t.Fatal("a revoked token still passes the recheck")
	}
}

func TestRecheck_ExpiryEndsIt(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	ts := NewScopedTokenStore([]ScopedToken{{Token: "tok", Principal: "p", Policy: Policy{Admin: true}, ExpiresAt: now.Add(time.Minute)}}).RequireAuth()
	ts.now = func() time.Time { return now }
	re := serveRecheck(t, ts, "tok")
	if re == nil {
		t.Fatal("no Recheck")
	}
	if _, ok := re(); !ok {
		t.Fatal("a live token fails the recheck")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := re(); ok {
		t.Fatal("an expired token passes the recheck")
	}
}
