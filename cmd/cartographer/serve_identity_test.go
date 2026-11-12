package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/hooks"
)

func identityFixture(t *testing.T, policy string, handler http.HandlerFunc) (*identityCache, *atomic.Int32, *time.Time) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "acme.token"), []byte("secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := hooks.NewRegistry([]hooks.Hook{{
		Name: "acme/who", Point: hooks.PointIdentity, Plugin: "acme", URL: srv.URL, Credential: "acme.token",
		FailurePolicy: policy, Headers: []string{"X-Session"}, CacheTTL: time.Minute,
	}}, hooks.Options{CredentialsDir: dir, Approved: func(string, string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	if identityHookFrom(reg) == nil {
		t.Fatal("no hook")
	}
	ic := &identityCache{reg: reg, hook: reg.ForPoint(hooks.PointIdentity)[0], now: func() time.Time { return now }, m: map[[32]byte]identityEntry{}}
	return ic, &calls, &now
}

func identityReq(session string) *http.Request {
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.RemoteAddr = "192.0.2.7:1"
	r.Header.Set("X-Session", session)
	r.Header.Set("Authorization", "Bearer must-not-leak")
	return r
}

func answer(w http.ResponseWriter, v map[string]any) {
	v["version"] = hooks.Version
	_ = json.NewEncoder(w).Encode(v)
}

func TestIdentityHookCachesPerUserAndExpires(t *testing.T) {
	ic, calls, now := identityFixture(t, hooks.FailClosed, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if h, _ := body["headers"].(map[string]any); h["X-Session"] == nil {
			t.Errorf("allow-listed header missing: %v", body)
		}
		if _, leaked := body["headers"].(map[string]any)["Authorization"]; leaked {
			t.Error("Authorization reached the hook")
		}
		answer(w, map[string]any{"user": "bob", "groups": []string{"staff"}})
	})
	via := auth.Principal{ID: "sso"}
	hdr := auth.Identity{User: "alice"}
	for i := 0; i < 3; i++ {
		id, ok, err := ic.resolve(identityReq("s1"), via, hdr)
		if err != nil || !ok || id.User != "bob" || len(id.Groups) != 1 {
			t.Fatalf("%+v %v %v", id, ok, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("%d calls for one user in one TTL", calls.Load())
	}
	ic.resolve(identityReq("s2"), via, hdr) // another session value: its own entry
	if calls.Load() != 2 {
		t.Fatalf("%d calls", calls.Load())
	}
	*now = now.Add(2 * time.Minute)
	ic.resolve(identityReq("s1"), via, hdr)
	if calls.Load() != 3 {
		t.Fatalf("%d calls after the TTL", calls.Load())
	}
}

func TestIdentityHookFailurePolicy(t *testing.T) {
	down := func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", 500) }
	via, hdr := auth.Principal{ID: "sso"}, auth.Identity{User: "alice"}

	ic, _, _ := identityFixture(t, hooks.FailOpen, down)
	if _, ok, err := ic.resolve(identityReq("s"), via, hdr); ok || err != nil {
		t.Fatalf("fail_open: supplied %v err %v, want the header identity to stay", ok, err)
	}
	ic, _, _ = identityFixture(t, hooks.FailClosed, down)
	if _, _, err := ic.resolve(identityReq("s"), via, hdr); err != auth.ErrIdentityUnavailable {
		t.Fatalf("fail_closed: %v", err)
	}
	// A malformed answer follows the same policy.
	bad := func(w http.ResponseWriter, _ *http.Request) { answer(w, map[string]any{"user": 7}) }
	ic, _, _ = identityFixture(t, hooks.FailClosed, bad)
	if _, _, err := ic.resolve(identityReq("s"), via, hdr); err != auth.ErrIdentityUnavailable {
		t.Fatalf("malformed, fail_closed: %v", err)
	}
	ic, _, _ = identityFixture(t, hooks.FailOpen, bad)
	if _, ok, err := ic.resolve(identityReq("s"), via, hdr); ok || err != nil {
		t.Fatalf("malformed, fail_open: %v %v", ok, err)
	}
}

func TestIdentityHookBreakerOpenFallsBackOrRefuses(t *testing.T) {
	down := func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", 500) }
	via, hdr := auth.Principal{ID: "sso"}, auth.Identity{User: "alice"}
	ic, calls, _ := identityFixture(t, hooks.FailOpen, down)
	for i := 0; i < 20; i++ {
		if _, ok, err := ic.resolve(identityReq("s"), via, hdr); ok || err != nil {
			t.Fatal("fail_open must fall back")
		}
	}
	if calls.Load() >= 20 {
		t.Fatalf("breaker never opened: %d calls", calls.Load())
	}
}

func TestIdentityHookNoOpinionAndDeny(t *testing.T) {
	via, hdr := auth.Principal{ID: "sso"}, auth.Identity{User: "alice"}
	ic, _, _ := identityFixture(t, hooks.FailClosed, func(w http.ResponseWriter, _ *http.Request) { answer(w, map[string]any{"decision": "no_opinion"}) })
	if _, ok, err := ic.resolve(identityReq("s"), via, hdr); ok || err != nil {
		t.Fatalf("no_opinion: %v %v", ok, err)
	}
	ic, _, _ = identityFixture(t, hooks.FailClosed, func(w http.ResponseWriter, _ *http.Request) {
		answer(w, map[string]any{"decision": "deny", "reason": "disabled account"})
	})
	if _, _, err := ic.resolve(identityReq("s"), via, hdr); err != auth.ErrIdentityDenied {
		t.Fatalf("deny: %v", err)
	}
}
