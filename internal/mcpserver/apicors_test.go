package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

const corsDash = "https://dash.example.com"

// newCORSHandler is the production chain: OriginGuard, APICORS, the token
// middleware, the mounts. Auth is on, so a preflight reaching the middleware
// would be a 401.
func newCORSHandler(t *testing.T, origins []string) http.Handler {
	t.Helper()
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{{Token: "tok", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}}})
	multi := NewMultiKBServer("test")
	k := uiFixtureKB(t, "docs")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	multi.EnableAPI(APIConfig{MaxStreamsPerToken: 4, MaxStreams: 64, CORSOrigins: origins})
	return OriginGuard(APICORS(origins, ts.Middleware(multi.Handler())))
}

func corsDo(h http.Handler, method, path, origin, acrm, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if acrm != "" {
		req.Header.Set("Access-Control-Request-Method", acrm)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestAPICORS_ListedOriginGetsHeaders(t *testing.T) {
	t.Parallel()
	h := newCORSHandler(t, []string{corsDash})
	rr := corsDo(h, http.MethodGet, APIPrefix+"/kbs", corsDash, "", "tok")
	if rr.Code != http.StatusOK || rr.Header().Get("Access-Control-Allow-Origin") != corsDash {
		t.Fatalf("GET: %d ACAO %q", rr.Code, rr.Header().Get("Access-Control-Allow-Origin"))
	}
	if rr.Header().Get("Vary") != "Origin" || rr.Header().Get("Access-Control-Expose-Headers") != "Retry-After, Content-Type" ||
		rr.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("headers: %v", rr.Header())
	}
}

func TestAPICORS_PreflightWithoutTokenOnlyForListedOrigin(t *testing.T) {
	t.Parallel()
	h := newCORSHandler(t, []string{corsDash})
	rr := corsDo(h, http.MethodOptions, APIPrefix+"/kbs/docs/events", corsDash, "GET", "")
	if rr.Code != http.StatusNoContent || rr.Header().Get("Access-Control-Allow-Origin") != corsDash ||
		rr.Header().Get("Access-Control-Allow-Methods") != "GET, HEAD, OPTIONS" ||
		rr.Header().Get("Access-Control-Allow-Headers") != "Authorization, Last-Event-ID, Content-Type" ||
		rr.Header().Get("Access-Control-Max-Age") != "600" || rr.Body.Len() != 0 {
		t.Fatalf("preflight: %d %v %q", rr.Code, rr.Header(), rr.Body.String())
	}
	if rr.Header().Get("Access-Control-Allow-Credentials") != "" || rr.Header().Get("Access-Control-Allow-Origin") == "*" {
		t.Error("credentials or wildcard")
	}
	// An unlisted origin: no header, 403 envelope, never 401 and never 204.
	rr = corsDo(h, http.MethodOptions, APIPrefix+"/kbs", "https://evil.example.org", "GET", "")
	if rr.Code != http.StatusForbidden || rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("unlisted preflight: %d %v", rr.Code, rr.Header())
	}
	// A preflight asking for POST is refused even from a listed origin.
	rr = corsDo(h, http.MethodOptions, APIPrefix+"/kbs", corsDash, "POST", "")
	if rr.Code != http.StatusForbidden || rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("POST preflight: %d %v", rr.Code, rr.Header())
	}
	// An OPTIONS that is not a preflight (no request-method) is no exemption.
	rr = corsDo(h, http.MethodOptions, APIPrefix+"/kbs", corsDash, "", "")
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("plain OPTIONS without token: %d, want 401", rr.Code)
	}
}

func TestAPICORS_UnlistedOriginGetsNoHeader(t *testing.T) {
	t.Parallel()
	for name, origins := range map[string][]string{"empty list": nil, "other origin": {"https://other.example.com"}} {
		h := newCORSHandler(t, origins)
		rr := corsDo(h, http.MethodGet, APIPrefix+"/kbs", corsDash, "", "tok")
		for k := range rr.Header() {
			if len(k) > 14 && k[:14] == "Access-Control" {
				t.Errorf("%s: unexpected %s", name, k)
			}
		}
	}
}

// The allow-list never reaches /mcp or the Atlas API, whatever the config: the
// D906 invariant.
func TestAPICORS_OtherPathsUntouched(t *testing.T) {
	t.Parallel()
	h := newCORSHandler(t, []string{corsDash})
	for _, p := range []string{"/mcp", UIAPIPrefix + "/kbs", "/api/component/v1/heartbeat"} {
		rr := corsDo(h, http.MethodGet, p, corsDash, "", "tok")
		for k := range rr.Header() {
			if strings.HasPrefix(k, "Access-Control") {
				t.Errorf("%s: allow-list header %s leaked", p, k)
			}
		}
	}
	if rr := corsDo(h, http.MethodOptions, "/mcp", corsDash, "GET", ""); rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Errorf("/mcp preflight: %d", rr.Code)
	}
	if rr := corsDo(h, http.MethodOptions, UIAPIPrefix+"/kbs", corsDash, "GET", ""); rr.Code == http.StatusNoContent {
		t.Errorf("Atlas API preflight answered 204")
	}
}

func TestCanonicalOrigin(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"https://Dash.Example.com", "https://dash.example.com", true},
		{"HTTP://X.example.com:80", "http://x.example.com", true},
		{"http://x.example.com:80", "http://x.example.com", true},
		{"https://x.example.com:443", "https://x.example.com", true},
		{"https://x.example.com:80", "https://x.example.com:80", true},
		{"http://localhost:5173", "http://localhost:5173", true},
		{"http://[::1]:8080", "http://[::1]:8080", true},
		{"*", "", false},
		{"https://*.example.com", "", false},
		{"https://x.example.com/", "", false},
		{"https://x.example.com/app", "", false},
		{"https://user@x.example.com", "", false},
		{"https://x.example.com?a=1", "", false},
		{"ftp://x.example.com", "", false},
		{"x.example.com", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := canonicalOrigin(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("canonicalOrigin(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
	// Equivalent spellings in config and in the request match.
	h := newCORSHandler(t, []string{"http://Dash.example.com:80"})
	rr := corsDo(h, http.MethodGet, APIPrefix+"/kbs", "http://dash.example.com", "", "tok")
	if rr.Header().Get("Access-Control-Allow-Origin") != "http://dash.example.com" {
		t.Errorf("normalised match failed: %v", rr.Header())
	}
}

// Every /api/v1 answer varies on Origin, with or without one, so a shared
// cache keeps the listed origin's copy apart; the preflight also varies on its
// request headers.
func TestAPICORS_VaryOnEveryAPIAnswer(t *testing.T) {
	t.Parallel()
	h := newCORSHandler(t, []string{corsDash})
	for _, origin := range []string{"", "http://example.com", corsDash} {
		rr := corsDo(h, http.MethodGet, APIPrefix+"/kbs", origin, "", "tok")
		if rr.Header().Get("Vary") != "Origin" {
			t.Errorf("origin %q: Vary %q", origin, rr.Header().Values("Vary"))
		}
	}
	rr := corsDo(h, http.MethodOptions, APIPrefix+"/kbs", corsDash, "GET", "")
	if v := strings.Join(rr.Header().Values("Vary"), ","); v != "Origin,Access-Control-Request-Method,Access-Control-Request-Headers" {
		t.Errorf("preflight Vary %q", v)
	}
}
