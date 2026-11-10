package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func proxyStore(t *testing.T, tokenPolicy Policy, proxyTok bool) *TokenStore {
	t.Helper()
	trusted, err := ParseTrustedAddresses([]string{"192.0.2.0/24", "2001:db8::1", "127.0.0.1/32"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ProxyConfig{
		Trusted: trusted, UserHeader: "X-User", GroupsHeader: "X-Groups",
		GroupPolicies: map[string]Policy{
			"staff": {Permissions: []Permission{{KB: "kb-a", Write: true}}},
			"other": {Permissions: []Permission{{KB: "kb-b", Write: true}}},
		},
	}
	return NewScopedTokenStore([]ScopedToken{{Token: "proxy-tok", Principal: "sso", Policy: tokenPolicy, Proxy: proxyTok}}).SetProxy(cfg).RequireAuth()
}

func doProxy(store *TokenStore, peer string, hdr map[string]string) (int, Principal) {
	var got Principal
	h := store.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = PrincipalFromContext(r.Context())
	}))
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.RemoteAddr = peer
	req.Header.Set("Authorization", "Bearer proxy-tok")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, got
}

func TestProxyIdentity(t *testing.T) {
	rw := Policy{Permissions: []Permission{{KB: "kb-a", Write: true}}}
	ro := Policy{Permissions: []Permission{{KB: "kb-a"}}}
	long := strings.Repeat("x", 129)
	many := strings.TrimSuffix(strings.Repeat("g,", 65), ",")
	cases := []struct {
		name     string
		policy   Policy
		proxyTok bool
		peer     string
		hdr      map[string]string
		code     int
		id       string // expected principal id
		write    bool
	}{
		{"trusted peer, proxy token", rw, true, "192.0.2.7:1", map[string]string{"X-User": "alice", "X-Groups": "staff"}, 200, "user:alice", true},
		{"ipv6 peer", rw, true, "[2001:db8::1]:1", map[string]string{"X-User": "alice", "X-Groups": "staff"}, 200, "user:alice", true},
		{"ipv4-mapped peer", rw, true, "[::ffff:127.0.0.1]:1", map[string]string{"X-User": "alice", "X-Groups": "staff"}, 200, "user:alice", true},
		{"untrusted peer ignores header", rw, true, "198.51.100.1:1", map[string]string{"X-User": "alice", "X-Groups": "staff"}, 200, "sso", true},
		{"not a proxy token ignores header", rw, false, "192.0.2.7:1", map[string]string{"X-User": "alice", "X-Groups": "staff"}, 200, "sso", true},
		{"no header stays the token", rw, true, "192.0.2.7:1", nil, 200, "sso", true},
		{"forged X-Forwarded-For is no trust", rw, true, "198.51.100.1:1", map[string]string{"X-Forwarded-For": "192.0.2.7", "X-User": "alice", "X-Groups": "staff"}, 200, "sso", true},
		{"no mapped group", rw, true, "192.0.2.7:1", map[string]string{"X-User": "alice", "X-Groups": "nobody"}, 403, "", false},
		{"no groups header value", rw, true, "192.0.2.7:1", map[string]string{"X-User": "alice"}, 403, "", false},
		{"clamp: read-only token", ro, true, "192.0.2.7:1", map[string]string{"X-User": "alice", "X-Groups": "staff"}, 200, "user:alice", false},
		{"clamp: KB outside the ceiling", ro, true, "192.0.2.7:1", map[string]string{"X-User": "alice", "X-Groups": "other"}, 403, "", false},
		{"oversized user", rw, true, "192.0.2.7:1", map[string]string{"X-User": long, "X-Groups": "staff"}, 400, "", false},
		{"angle brackets", rw, true, "192.0.2.7:1", map[string]string{"X-User": "a<b>", "X-Groups": "staff"}, 400, "", false},
		{"control character", rw, true, "192.0.2.7:1", map[string]string{"X-User": "a\x01b", "X-Groups": "staff"}, 400, "", false},
		{"too many groups", rw, true, "192.0.2.7:1", map[string]string{"X-User": "alice", "X-Groups": many}, 400, "", false},
		{"oversized group", rw, true, "192.0.2.7:1", map[string]string{"X-User": "alice", "X-Groups": long}, 400, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, p := doProxy(proxyStore(t, c.policy, c.proxyTok), c.peer, c.hdr)
			if code != c.code {
				t.Fatalf("status %d, want %d", code, c.code)
			}
			if c.code != 200 {
				return
			}
			if p.ID != c.id || p.Policy.HasKBAccess("kb-a", true) != c.write {
				t.Fatalf("principal %+v, want id %q write %v", p, c.id, c.write)
			}
			if c.id != "sso" && (p.Via != "sso" || p.Kind != KindHuman || p.Policy.Admin) {
				t.Errorf("person principal = %+v", p)
			}
		})
	}
}

func TestClampKeepsTheCeilingsRestrictions(t *testing.T) {
	ceiling := Policy{Permissions: []Permission{{KB: "kb-a", Write: true, NoSecrets: true, ReadOnlyArtifacts: true}}}
	got := Clamp(Policy{Permissions: []Permission{{KB: "kb-a", Write: true}, {KB: "kb-z", Write: true}}}, ceiling)
	if len(got.Permissions) != 1 || got.AllowsSecrets("kb-a") || got.AllowsArtifactWrite("kb-a") || !got.HasKBAccess("kb-a", true) {
		t.Fatalf("clamped = %+v", got)
	}
}

func TestParseTrustedAddressesRefusals(t *testing.T) {
	for _, bad := range [][]string{nil, {"0.0.0.0/0"}, {"::/0"}, {"not-an-ip"}, {"192.0.2.0/24", ""}} {
		if _, err := ParseTrustedAddresses(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

// The person never holds an authority the ceiling lacks, secrets included, and
// a ceiling scoped to a map bounds nothing (finding: whole-KB rules only).
func TestClampNeverGrantsWhatTheCeilingLacks(t *testing.T) {
	mapped := Policy{Permissions: []Permission{{KB: "kb-a", Write: true}}}
	cases := map[string]struct {
		ceiling                       Policy
		access, write, secrets, names bool
	}{
		"plain rw":                  {Policy{Permissions: []Permission{{KB: "kb-a", Write: true}}}, true, true, true, true},
		"rw, secrets none":          {Policy{Permissions: []Permission{{KB: "kb-a", Write: true, NoSecrets: true}}}, true, true, false, false},
		"read-only, secrets none":   {Policy{Permissions: []Permission{{KB: "kb-a", NoSecrets: true}}}, true, false, false, false},
		"read-only":                 {Policy{Permissions: []Permission{{KB: "kb-a"}}}, true, false, false, true},
		"map-scoped rw is no bound": {Policy{Permissions: []Permission{{KB: "kb-a", Write: true, Maps: []string{"x"}}}}, false, false, false, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := Clamp(mapped, c.ceiling)
			if got.HasKBAccess("kb-a", false) != c.access || got.HasKBAccess("kb-a", true) != c.write ||
				got.AllowsSecrets("kb-a") != c.secrets || got.SecretsVisible("kb-a") != c.names {
				t.Fatalf("clamped = %+v", got)
			}
		})
	}
}

func TestProxyRepeatedUserHeaderAfterAnEmptyOneIs400(t *testing.T) {
	store := proxyStore(t, Policy{Permissions: []Permission{{KB: "kb-a", Write: true}}}, true)
	h := store.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.RemoteAddr = "192.0.2.7:1"
	req.Header.Set("Authorization", "Bearer proxy-tok")
	req.Header["X-User"] = []string{"", "alice"}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}
