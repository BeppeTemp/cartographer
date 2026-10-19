package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTokenStoreValidate(t *testing.T) {
	tok, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	ts := NewTokenStore([]string{tok})

	agentID, ok := ts.Validate(tok)
	if !ok {
		t.Fatal("expected valid token to pass")
	}
	if agentID == "" || agentID == tok[:8] {
		t.Fatalf("agentID = %q must be a non-secret derived principal ID", agentID)
	}

	_, ok = ts.Validate("invalid-token")
	if ok {
		t.Fatal("expected invalid token to fail")
	}
}

func TestTokenStoreDisabled(t *testing.T) {
	ts := NewTokenStore(nil)
	if ts.IsEnabled() {
		t.Fatal("empty store should be disabled")
	}

	// all tokens pass when disabled
	_, ok := ts.Validate("anything")
	// Validate on a disabled store: hash not in empty map → ok=false
	// but Middleware must pass through; Validate semantics are separate
	_ = ok

	// Middleware must pass through when disabled
	called := false
	handler := ts.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if !called {
		t.Fatal("middleware with disabled store should pass through")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestMiddleware401(t *testing.T) {
	tok, _ := GenerateToken()
	ts := NewTokenStore([]string{tok})

	handler := ts.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// no Authorization header
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if rr.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("expected WWW-Authenticate header")
	}

	// wrong token
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Authorization", "Bearer wrongtoken")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong token, got %d", rr2.Code)
	}
}

func TestMiddlewareValid(t *testing.T) {
	tok, _ := GenerateToken()
	ts := NewTokenStore([]string{tok})

	called := false
	handler := ts.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if !called {
		t.Fatal("handler not called with valid token")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestGenerateToken(t *testing.T) {
	tok, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	// 32 bytes hex-encoded = 64 chars
	if len(tok) != 64 {
		t.Fatalf("expected 64 chars, got %d", len(tok))
	}

	tok2, _ := GenerateToken()
	if tok == tok2 {
		t.Fatal("two generated tokens should be unique")
	}
}

func TestParseScopes(t *testing.T) {
	scopes := ParseScopes("kb:docs:rw kb:notes:r kb:bad")
	if len(scopes) != 2 {
		t.Fatalf("expected 2 valid scopes, got %d", len(scopes))
	}

	if scopes[0].KB != "docs" || !scopes[0].Write {
		t.Errorf("scope[0] = %+v, want {docs, true}", scopes[0])
	}
	if scopes[1].KB != "notes" || scopes[1].Write {
		t.Errorf("scope[1] = %+v, want {notes, false}", scopes[1])
	}

	if !HasAccess(scopes, "docs", false) {
		t.Error("docs read should be accessible")
	}
	if !HasAccess(scopes, "docs", true) {
		t.Error("docs write should be accessible")
	}
	if !HasAccess(scopes, "notes", false) {
		t.Error("notes read should be accessible")
	}
	if HasAccess(scopes, "notes", true) {
		t.Error("notes write should NOT be accessible")
	}
	if HasAccess(scopes, "unknown", false) {
		t.Error("unknown KB should not be accessible")
	}
}

func TestPolicyRoleUnionAndSelectors(t *testing.T) {
	p := Policy{Permissions: []Permission{
		{KB: "docs", Maps: []string{"runbooks"}, Types: []string{"Runbook"}},
		{KB: "docs", Journals: []string{"journal"}, Write: true},
	}}
	if !p.Allows("docs", "runbooks", "", "Runbook", false) || p.Allows("docs", "runbooks", "", "Secret", false) {
		t.Fatal("map/type intersection failed")
	}
	if !p.Allows("docs", "", "journal", "Entry", true) || p.Allows("docs", "", "journal", "Entry", false) == false {
		t.Fatal("journal rw rule failed")
	}
	if p.AllowsWholeKB("docs", false) {
		t.Fatal("selected role must not become whole-KB")
	}
}

func TestDerivedPrincipalNeverContainsToken(t *testing.T) {
	secret := "highly-sensitive-token-value"
	ts := NewScopedTokenStore([]ScopedToken{{Token: secret}})
	id, ok := ts.Validate(secret)
	if !ok || strings.Contains(id, secret) || strings.Contains(id, secret[:8]) {
		t.Fatalf("principal ID leaked token material: %q", id)
	}
}

func TestParseScopesSemicolonSeparated(t *testing.T) {
	// ";"-separated form (used by the CARTOGRAPHER_TOKENS env, where scopes
	// cannot be whitespace-separated because whitespace splits token entries).
	scopes := ParseScopes("kb:docs:rw;kb:notes:r")
	if len(scopes) != 2 {
		t.Fatalf("expected 2 scopes, got %d", len(scopes))
	}
	if scopes[0].KB != "docs" || !scopes[0].Write {
		t.Errorf("scope[0] = %+v, want {docs, true}", scopes[0])
	}
	if scopes[1].KB != "notes" || scopes[1].Write {
		t.Errorf("scope[1] = %+v, want {notes, false}", scopes[1])
	}
	// mixed separators must also work.
	if len(ParseScopes("kb:a:r; kb:b:rw")) != 2 {
		t.Error("mixed ';'+space separators should yield 2 scopes")
	}
}

func TestProtectedResourceMetadata(t *testing.T) {
	data := ProtectedResourceMetadata("https://auth.example.com", "https://api.example.com")
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["resource"] != "https://api.example.com" {
		t.Errorf("resource = %v", m["resource"])
	}
	if _, ok := m["bearer_methods_supported"]; !ok {
		t.Error("missing bearer_methods_supported")
	}
	if _, ok := m["authorization_servers"]; !ok {
		t.Error("missing authorization_servers")
	}
}

func TestNewScopedTokenStore(t *testing.T) {
	ts := NewScopedTokenStore([]ScopedToken{
		{Token: "admin-tok"}, // no scopes = full access
		{Token: "scoped-tok", Scopes: []KBScope{{KB: "wiki", Write: true}, {KB: "notes"}}},
	})

	if !ts.IsEnabled() {
		t.Fatal("expected enabled store")
	}

	scopes, ok := ts.ScopesOf("admin-tok")
	if !ok {
		t.Fatal("expected admin-tok to be valid")
	}
	if len(scopes) != 0 {
		t.Fatalf("expected no scopes (full access) for admin-tok, got %+v", scopes)
	}

	scopes, ok = ts.ScopesOf("scoped-tok")
	if !ok {
		t.Fatal("expected scoped-tok to be valid")
	}
	if len(scopes) != 2 {
		t.Fatalf("expected 2 scopes, got %+v", scopes)
	}
	if !HasAccess(scopes, "wiki", true) {
		t.Error("expected write access to wiki")
	}
	if HasAccess(scopes, "notes", true) {
		t.Error("expected no write access to notes")
	}
	if !HasAccess(scopes, "notes", false) {
		t.Error("expected read access to notes")
	}

	if _, ok := ts.ScopesOf("unknown-tok"); ok {
		t.Error("unknown token should be invalid")
	}
}

func TestNewTokenStoreBackwardCompat(t *testing.T) {
	// NewTokenStore([]string) must still work and grant full access (nil scopes).
	ts := NewTokenStore([]string{"tok-a", "tok-b"})
	agentID, ok := ts.Validate("tok-a")
	if !ok || agentID == "tok-a" || agentID == "" {
		t.Fatalf("Validate(tok-a) = %q, %v", agentID, ok)
	}
	scopes, ok := ts.ScopesOf("tok-a")
	if !ok {
		t.Fatal("expected tok-a to be valid")
	}
	if len(scopes) != 0 {
		t.Fatalf("expected full access (no scopes), got %+v", scopes)
	}
}

func TestMiddlewareInjectsScopesFromStore(t *testing.T) {
	ts := NewScopedTokenStore([]ScopedToken{
		{Token: "scoped-tok", Scopes: []KBScope{{KB: "wiki", Write: false}}},
	})

	var gotScopes []KBScope
	var gotOK bool
	handler := ts.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotScopes = ScopesFromContext(r.Context())
		gotOK = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer scoped-tok")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !gotOK {
		t.Fatal("handler not called")
	}
	if len(gotScopes) != 1 || gotScopes[0].KB != "wiki" || gotScopes[0].Write {
		t.Fatalf("scopes injected into context = %+v, want [{wiki false}]", gotScopes)
	}
}

func TestMiddlewarePublicPaths(t *testing.T) {
	ts := NewTokenStore([]string{"secret-token"})
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := ts.Middleware(ok)

	cases := []struct {
		path       string
		auth       string
		wantStatus int
	}{
		{"/health", "", http.StatusOK},                               // probe: no token, must pass
		{"/.well-known/oauth-protected-resource", "", http.StatusOK}, // RFC 9728: public
		{"/mcp", "", http.StatusUnauthorized},                        // protected: no token → 401
		{"/mcp", "Bearer secret-token", http.StatusOK},               // protected: valid token
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", c.path, nil)
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.wantStatus {
			t.Errorf("%s (auth=%q): status %d, want %d", c.path, c.auth, rec.Code, c.wantStatus)
		}
	}
}

func TestPolicyAndPrincipalCopiesCannotBeMutated(t *testing.T) {
	input := ScopedToken{Token: "token", Principal: "reader", Policy: Policy{Permissions: []Permission{{KB: "docs", Maps: []string{"visible"}, Types: []string{"Runbook"}}}}}
	ts := NewScopedTokenStore([]ScopedToken{input})
	// Mutating the configuration after construction must not alter the store.
	input.Policy.Permissions[0].Maps[0] = "hidden"
	p, ok := ts.PrincipalOf("token")
	if !ok || !p.Policy.Allows("docs", "visible", "", "Runbook", false) {
		t.Fatal("store retained caller-owned policy slices")
	}
	// Nor may a returned principal mutate the stored policy or a context copy.
	p.Policy.Permissions[0].Types[0] = "Secret"
	again, _ := ts.PrincipalOf("token")
	if !again.Policy.Allows("docs", "visible", "", "Runbook", false) {
		t.Fatal("PrincipalOf returned mutable store state")
	}
	ctx := ContextWithPrincipal(context.Background(), again)
	fromCtx := PrincipalFromContext(ctx)
	fromCtx.Policy.Permissions[0].Maps[0] = "other"
	if !PrincipalFromContext(ctx).Policy.Allows("docs", "visible", "", "Runbook", false) {
		t.Fatal("PrincipalFromContext returned mutable context state")
	}
}

// --- D179: invalid auth configuration must not widen access ---

func TestParseScopesStrictRejectsWhatParseScopesDiscards(t *testing.T) {
	cases := []struct {
		name  string
		scope string
		ok    bool
	}{
		{"read", "kb:finance:r", true},
		{"write", "kb:finance:rw", true},
		{"several", "kb:a:r kb:b:rw", true},
		{"missing access segment", "kb:finance", false},
		{"unknown access value", "kb:finance:write", false},
		{"empty access value", "kb:finance:", false},
		{"empty kb name", "kb::r", false},
		{"wrong prefix", "db:finance:r", false},
		{"empty string", "", false},
		{"one valid one not", "kb:a:r kb:b", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseScopesStrict(tc.scope)
			if (err == nil) != tc.ok {
				t.Errorf("ParseScopesStrict(%q) err = %v, want ok=%v", tc.scope, err, tc.ok)
			}
		})
	}
}

// TestParseScopesUnknownAccessIsNotRead: "kb:x:write" used to be read access.
// Granting something nobody wrote is the same class of bug as granting
// everything.
func TestParseScopesUnknownAccessIsNotRead(t *testing.T) {
	if got := ParseScopes("kb:finance:write"); len(got) != 0 {
		t.Errorf("ParseScopes = %+v, want nothing for an unknown access value", got)
	}
}

// TestEmptyPolicyIsNotAdmin is the core regression: a token whose declared
// restrictions produced no permission grants NOTHING. Before D179 it was
// promoted to a legacy admin, so `scopes: ["kb:finance"]` — a typo — meant full
// access to every KB.
func TestEmptyPolicyIsNotAdmin(t *testing.T) {
	ts := NewScopedTokenStore([]ScopedToken{{
		Token:  "secret-token",
		Scopes: ParseScopes("kb:finance"), // malformed: yields nothing
	}})
	p, ok := ts.PrincipalOf("secret-token")
	if !ok {
		t.Fatal("token not found")
	}
	if p.Policy.Admin {
		t.Fatal("a token whose scopes all failed to parse was promoted to admin")
	}
	if p.Policy.Allows("finance", "", "", "", false) {
		t.Error("the empty policy granted read access")
	}
}

// TestDeclaredAdminStillWorks: the legacy pre-scopes token keeps full access —
// it is now declared by the caller rather than inferred from emptiness.
func TestDeclaredAdminStillWorks(t *testing.T) {
	ts := NewTokenStore([]string{"legacy-token"})
	p, ok := ts.PrincipalOf("legacy-token")
	if !ok {
		t.Fatal("token not found")
	}
	if !p.Policy.Admin {
		t.Error("a bare token string must remain an unrestricted admin token")
	}
}

// TestRequireAuthFailsClosedOnEmptyStore: a deployment that asked for
// authentication and ended up with no usable token must reject requests, not
// serve them as a local admin.
func TestRequireAuthFailsClosedOnEmptyStore(t *testing.T) {
	ts := NewScopedTokenStore(nil).RequireAuth()
	if !ts.IsEnabled() {
		t.Fatal("a required store must report itself enabled even when empty")
	}

	var reached bool
	h := ts.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if reached {
		t.Error("an unauthenticated request reached the handler")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}

	// Liveness must stay reachable: a probe is not a bypass.
	reached = false
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if !reached {
		t.Error("/health must stay public so probes keep working")
	}
}

// TestUnrequiredEmptyStoreStillDisablesAuth: the auth-off path is unchanged —
// enforcement is only forced when a caller explicitly required it.
func TestUnrequiredEmptyStoreStillDisablesAuth(t *testing.T) {
	ts := NewTokenStore(nil)
	if ts.IsEnabled() {
		t.Error("an empty store with no requirement must leave auth disabled")
	}
}
