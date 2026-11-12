package auth

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/hooks"
)

func hookStore(t *testing.T, hook IdentityHook) *TokenStore {
	t.Helper()
	trusted, _ := ParseTrustedAddresses([]string{"192.0.2.0/24"})
	cfg := &ProxyConfig{
		Trusted: trusted, UserHeader: "X-User", GroupsHeader: "X-Groups", Hook: hook,
		GroupPolicies: map[string]Policy{
			"staff": {Permissions: []Permission{{KB: "kb-a", Write: true}}},
			"wide":  {Permissions: []Permission{{KB: "kb-a", Write: true}, {KB: "kb-b", Write: true}}},
		},
	}
	// The proxy token reaches kb-a read-only: the ceiling.
	tok := ScopedToken{Token: "proxy-tok", Principal: "sso", Proxy: true, Policy: Policy{Permissions: []Permission{{KB: "kb-a"}}}}
	return NewScopedTokenStore([]ScopedToken{tok}).SetProxy(cfg).RequireAuth()
}

func doHook(store *TokenStore, peer string, hdr map[string]string) (*httptest.ResponseRecorder, Principal) {
	var got Principal
	h := store.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = PrincipalFromContext(r.Context()) }))
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.RemoteAddr = peer
	req.Header.Set("Authorization", "Bearer proxy-tok")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, got
}

func TestIdentityHookReplacesTheHeaderIdentityAndIsClamped(t *testing.T) {
	var seen Identity
	store := hookStore(t, func(_ *http.Request, via Principal, header Identity) (Identity, bool, error) {
		seen = header
		return Identity{User: "bob", Groups: []string{"wide"}}, true, nil
	})
	rec, p := doHook(store, "192.0.2.7:1", map[string]string{"X-User": "alice", "X-Groups": "staff"})
	if rec.Code != 200 || p.ID != "user:bob" || p.Via != "sso" {
		t.Fatalf("code %d principal %+v", rec.Code, p)
	}
	if seen.User != "alice" || len(seen.Groups) != 1 {
		t.Fatalf("the hook is told the header identity: %+v", seen)
	}
	// The group maps to kb-a and kb-b write; the token allows kb-a read only.
	for _, perm := range p.Policy.Permissions {
		if perm.KB != "kb-a" || perm.Write {
			t.Fatalf("not clamped to the proxy token: %+v", p.Policy)
		}
	}
	if len(p.Policy.Permissions) == 0 {
		t.Fatal("the clamp left nothing")
	}
}

func TestIdentityHookNoOpinionKeepsTheHeader(t *testing.T) {
	store := hookStore(t, func(*http.Request, Principal, Identity) (Identity, bool, error) { return Identity{}, false, nil })
	rec, p := doHook(store, "192.0.2.7:1", map[string]string{"X-User": "alice", "X-Groups": "staff"})
	if rec.Code != 200 || p.ID != "user:alice" {
		t.Fatalf("code %d principal %+v", rec.Code, p)
	}
}

func TestIdentityHookFailures(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{ErrIdentityUnavailable, 503},
		{ErrIdentityDenied, 403},
		{http.ErrAbortHandler, 503}, // an unknown failure never passes as the header identity
	}
	for _, c := range cases {
		store := hookStore(t, func(*http.Request, Principal, Identity) (Identity, bool, error) { return Identity{}, false, c.err })
		rec, _ := doHook(store, "192.0.2.7:1", map[string]string{"X-User": "alice", "X-Groups": "staff"})
		if rec.Code != c.code {
			t.Errorf("%v: code %d, want %d", c.err, rec.Code, c.code)
		}
		// Tied to the breaker window: the client retries when a probe can pass.
		if want := strconv.Itoa(hooks.RetryAfterSeconds); c.code == 503 && rec.Header().Get("Retry-After") != want {
			t.Errorf("%v: Retry-After %q, want %q", c.err, rec.Header().Get("Retry-After"), want)
		}
		if hooks.RetryAfterSeconds != int(hooks.BreakerOpenFor.Seconds()) {
			t.Errorf("Retry-After %ds is not the breaker window %s", hooks.RetryAfterSeconds, hooks.BreakerOpenFor)
		}
	}
}

func TestIdentityHookIsNeverCalledForAnUntrustedPeerOrWithoutIdentity(t *testing.T) {
	calls := 0
	store := hookStore(t, func(*http.Request, Principal, Identity) (Identity, bool, error) {
		calls++
		return Identity{}, false, nil
	})
	doHook(store, "198.51.100.1:1", map[string]string{"X-User": "alice", "X-Groups": "staff"})
	doHook(store, "192.0.2.7:1", nil)
	if calls != 0 {
		t.Fatalf("the hook ran %d times", calls)
	}
}

func TestIdentityHookAnswerIsCheckedLikeAHeader(t *testing.T) {
	store := hookStore(t, func(*http.Request, Principal, Identity) (Identity, bool, error) {
		return Identity{User: "<script>", Groups: []string{"staff"}}, true, nil
	})
	if rec, _ := doHook(store, "192.0.2.7:1", map[string]string{"X-User": "alice", "X-Groups": "staff"}); rec.Code != 400 {
		t.Fatalf("code %d, want 400", rec.Code)
	}
}

func TestIdentityHookNeverSeesOverLimitProxyInput(t *testing.T) {
	calls := 0
	store := hookStore(t, func(*http.Request, Principal, Identity) (Identity, bool, error) {
		calls++
		return Identity{}, false, nil
	})
	long := strings.Repeat("x", 129)
	if rec, _ := doHook(store, "192.0.2.7:1", map[string]string{"X-User": long, "X-Groups": "staff"}); rec.Code != 400 {
		t.Fatalf("code %d, want 400", rec.Code)
	}
	if calls != 0 {
		t.Fatalf("the hook saw over-limit input (%d calls)", calls)
	}
}
