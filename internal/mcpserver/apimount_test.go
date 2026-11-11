package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// newAPIHandler mounts one KB with the public API on and the Atlas per web.
func newAPIHandler(t *testing.T, ts *auth.TokenStore, web, api bool) http.Handler {
	t.Helper()
	multi := NewMultiKBServer("test")
	k := uiFixtureKB(t, "docs")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	if web {
		multi.EnableWeb(nil)
	}
	if api {
		multi.EnableAPI(APIConfig{MaxStreamsPerToken: 4, MaxStreams: 64})
	}
	return ts.Middleware(multi.Handler())
}

func TestAPIMount_AbsentUnlessEnabledAndIndependentOfWeb(t *testing.T) {
	t.Parallel()
	open := auth.NewTokenStore(nil)
	if rr := getUI(t, newAPIHandler(t, open, true, false), APIPrefix+"/kbs", ""); rr.Code != http.StatusNotFound {
		t.Errorf("api off: %d, want 404", rr.Code)
	}
	h := newAPIHandler(t, open, false, true)
	if rr := getUI(t, h, APIPrefix+"/kbs", ""); rr.Code != http.StatusOK {
		t.Errorf("web off, api on: %d, want 200", rr.Code)
	}
	if rr := getUI(t, h, UIAPIPrefix+"/kbs", ""); rr.Code != http.StatusNotFound {
		t.Errorf("web off: Atlas mount %d, want 404", rr.Code)
	}
	both := newAPIHandler(t, open, true, true)
	if rr := getUI(t, both, UIAPIPrefix+"/kbs", ""); rr.Code != http.StatusOK {
		t.Errorf("Atlas mount unaffected: %d", rr.Code)
	}
}

func TestAPIMount_ReadOnly(t *testing.T) {
	t.Parallel()
	h := newAPIHandler(t, auth.NewTokenStore(nil), false, true)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(method, APIPrefix+"/kbs", nil))
		if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s: %d Allow %q", method, rr.Code, rr.Header().Get("Allow"))
		}
	}
}

// The public mount serves an explicit mask: every internal route is a 404
// (not a 403), and the mask names no tier but the two.
func TestAPIMount_InternalRoutesAre404(t *testing.T) {
	t.Parallel()
	if apiPublicTiers != tierStable|tierPreview {
		t.Fatalf("apiPublicTiers = %b, want exactly stable|preview", apiPublicTiers)
	}
	h := newAPIHandler(t, auth.NewTokenStore(nil), true, true)
	n := 0
	for _, rt := range apiRoutes {
		path := strings.ReplaceAll("/"+strings.Join(rt.segments, "/"), "{kb}", "docs")
		if rt.Events != nil {
			continue
		}
		api := getUI(t, h, APIPrefix+path, "")
		if rt.Tier == tierInternal {
			n++
			if api.Code != http.StatusNotFound {
				t.Errorf("internal %s on /api/v1: %d, want 404", rt.Pattern, api.Code)
			}
		}
	}
	if n == 0 {
		t.Fatal("no internal route in the table")
	}
}

func TestAPIMount_SameBodyAsAtlas(t *testing.T) {
	t.Parallel()
	h := newAPIHandler(t, auth.NewTokenStore(nil), true, true)
	for _, p := range []string{"/kbs", "/kbs/docs/overview", "/kbs/docs/concept?id=visible/alpha", "/kbs/docs/maintenance/review"} {
		a := getUI(t, h, UIAPIPrefix+p, "")
		b := getUI(t, h, APIPrefix+p, "")
		if a.Code != http.StatusOK || a.Code != b.Code || a.Body.String() != b.Body.String() {
			t.Errorf("%s: atlas %d / api %d differ", p, a.Code, b.Code)
		}
	}
}

func TestAPIMount_NarrowedTokenAndAuth(t *testing.T) {
	t.Parallel()
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}}},
	})
	h := newAPIHandler(t, ts, false, true)
	if rr := getUI(t, h, APIPrefix+"/kbs", ""); rr.Code != http.StatusUnauthorized || rr.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("no token: %d %q", rr.Code, rr.Header().Get("WWW-Authenticate"))
	}
	if rr := getUI(t, h, APIPrefix+"/kbs/docs/concept?id=hidden/secret", "narrow"); rr.Code != http.StatusNotFound {
		t.Errorf("hidden concept: %d, want 404", rr.Code)
	}
	graph := getUI(t, h, APIPrefix+"/kbs/docs/graph", "narrow")
	if graph.Code != http.StatusOK || strings.Contains(graph.Body.String(), "hidden/secret") {
		t.Errorf("graph leaks hidden concept: %d %s", graph.Code, graph.Body.String())
	}
	if rr := getUI(t, h, APIPrefix+"/kbs/docs/search?q=secret", "narrow"); strings.Contains(rr.Body.String(), `"id":"hidden/secret"`) {
		t.Errorf("search leaks hidden concept: %s", rr.Body.String())
	}
}
