package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/webui"
)

// newMountedUIHandler mounts one KB with the full web surface: the JSON API
// and the embedded static bundle, exactly as `serve` assembles it.
func newMountedUIHandler(t *testing.T) http.Handler {
	t.Helper()
	static, err := webui.Handler()
	if err != nil {
		t.Fatal(err)
	}
	multi := NewMultiKBServer("test")
	k := uiFixtureKB(t, "docs")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(static)
	return auth.NewTokenStore(nil).Middleware(multi.Handler())
}

// TestWebMountDoesNotShadowAnyExistingRoute is the trap of the whole work
// package. The UI serves its shell for *any* unknown path below /ui/, which is
// what client-side routing needs; the failure mode is a fallback that reaches
// wider than that and starts answering for /mcp, /health or /api. Every
// endpoint that existed before the UI is asserted to still resolve to its own
// handler with the UI mounted.
func TestWebMountDoesNotShadowAnyExistingRoute(t *testing.T) {
	handler := newMountedUIHandler(t)

	for _, tc := range []struct{ path, marker string }{
		{"/health", `"status"`},
		{"/ready", `"ready"`},
		{"/clients", `"clients"`},
		{auth.WellKnownProtectedResourcePath, `"resource"`},
		{UIAPIPrefix + "/kbs", `"kbs"`},
		{UIAPIPrefix + "/kbs/docs/overview", `"collections"`},
	} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rr.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200", tc.path, rr.Code)
			continue
		}
		body := rr.Body.String()
		if !strings.Contains(body, tc.marker) {
			t.Errorf("%s did not reach its own handler: %s", tc.path, truncate(body))
		}
		if strings.Contains(body, `<div id="root">`) {
			t.Errorf("%s was answered with the UI shell", tc.path)
		}
	}

	// The MCP endpoints keep working, prefixed and bare.
	for _, path := range []string{"/mcp", "/mcp/docs", "/mcp?kb=docs"} {
		req := httptest.NewRequest(http.MethodPost, path,
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200: %s", path, rr.Code, truncate(rr.Body.String()))
		}
		if strings.Contains(rr.Body.String(), `<div id="root">`) {
			t.Errorf("%s was answered with the UI shell", path)
		}
	}

	// An unknown path *outside* /ui/ stays a 404: the SPA fallback is scoped
	// to its own mount, not to the whole server.
	for _, path := range []string{"/nope", "/assets/index.js", "/api/other"} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404 (the SPA fallback reached outside /ui/)", path, rr.Code)
		}
	}
}

func TestWebMountServesTheShellAndRedirects(t *testing.T) {
	handler := newMountedUIHandler(t)

	for _, path := range []string{"/ui/", "/ui/atlas", "/ui/deep/route"} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `<div id="root">`) {
			t.Errorf("%s: status %d, body is not the shell", path, rr.Code)
		}
		if csp := rr.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
			t.Errorf("%s: shell served without a CSP: %q", path, csp)
		}
	}

	for _, path := range []string{"/ui"} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusMovedPermanently {
			t.Errorf("%s: status %d, want 301", path, rr.Code)
		}
		if got := rr.Header().Get("Location"); got != webui.MountPath {
			t.Errorf("%s: Location %q, want %q", path, got, webui.MountPath)
		}
	}
}

// Static assets carry no KB data, so they are reachable without a token even
// when the server requires one. The API is not.
func TestWebMountStaticIsReachableButTheAPIIsNot(t *testing.T) {
	static, err := webui.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "tok", Scopes: []auth.KBScope{{KB: "docs"}}},
	})
	multi := NewMultiKBServer("test")
	k := uiFixtureKB(t, "docs")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(static)
	handler := ts.Middleware(multi.Handler())

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, UIAPIPrefix+"/kbs", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("the UI API without a token: status %d, want 401", rr.Code)
	}

	// The shell must load without one, or the page that asks for the token is
	// itself behind the token.
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, webui.MountPath, nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `<div id="root">`) {
		t.Errorf("the UI shell without a token: status %d, body is not the shell", rr.Code)
	}
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
