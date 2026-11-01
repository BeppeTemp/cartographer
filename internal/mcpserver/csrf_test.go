package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// A server with authentication off on loopback: the case a foreign page can
// reach with a no-cors POST.
func openUsageHandler(t *testing.T) (http.Handler, *kb.KB) {
	t.Helper()
	k := uiFixtureKB(t, "docs")
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	return LoopbackHostGuard(OriginGuard(auth.NewTokenStore(nil).Middleware(multi.Handler()))), k
}

func rawPost(h http.Handler, path, contentType, origin, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Host = "127.0.0.1:39273"
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

const csrfSettings = `{"machine":"m1","entries":[],"yaml":""}`

func TestStateChangingRoutes_CrossOriginRefused(t *testing.T) {
	t.Parallel()
	h, k := openUsageHandler(t)
	usage := `[{"name":"a","kind":"skill","provider":"p","last_used":"` + time.Now().UTC().Format(time.RFC3339) + `","count":1}]`
	for _, tc := range []struct{ path, body string }{
		{UsagePath + "?kb=docs", usage},
		{ClientSettingsPath, csrfSettings},
		{UIAPIPrefix + "/anything", "{}"},
		{"/clients", "{}"},
	} {
		for _, ct := range []string{"text/plain", "application/json"} {
			rr := rawPost(h, tc.path, ct, "https://evil.example.com", tc.body)
			if rr.Code != http.StatusForbidden {
				t.Errorf("%s (%s) cross-origin: status %d, want 403", tc.path, ct, rr.Code)
			}
		}
	}
	if got, _ := k.LoadUsage(); len(got) != 0 {
		t.Fatalf("a cross-origin report was stored: %+v", got)
	}
}

func TestStateChangingRoutes_SameOriginAndNoOriginAccepted(t *testing.T) {
	t.Parallel()
	h, _ := openUsageHandler(t)
	for _, origin := range []string{"", "http://127.0.0.1:39273"} {
		if rr := rawPost(h, ClientSettingsPath, "application/json", origin, csrfSettings); rr.Code != http.StatusOK {
			t.Errorf("origin %q: status %d, want 200: %s", origin, rr.Code, rr.Body)
		}
		if rr := rawPost(h, ClientSettingsPath, "application/json; charset=utf-8", origin, csrfSettings); rr.Code != http.StatusOK {
			t.Errorf("origin %q with charset: status %d, want 200", origin, rr.Code)
		}
	}
}

func TestJSONEndpoints_RequireJSONContentType(t *testing.T) {
	t.Parallel()
	h, k := openUsageHandler(t)
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data", ""} {
		for _, p := range []string{UsagePath + "?kb=docs", ClientSettingsPath} {
			body := csrfSettings
			if strings.HasPrefix(p, UsagePath) {
				body = "[]"
			}
			if rr := rawPost(h, p, ct, "", body); rr.Code != http.StatusUnsupportedMediaType {
				t.Errorf("%s with %q: status %d, want 415", p, ct, rr.Code)
			}
		}
	}
	if got, _ := k.LoadUsage(); len(got) != 0 {
		t.Fatalf("store = %+v", got)
	}
}

func TestJSONEndpoints_OversizedBodyRefused(t *testing.T) {
	t.Parallel()
	h, _ := openUsageHandler(t)
	big := `["` + strings.Repeat("a", maxUsageBody+10) + `"]`
	if rr := rawPost(h, UsagePath+"?kb=docs", "application/json", "", big); rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("usage: status %d, want 413", rr.Code)
	}
	bigSettings := `{"machine":"m","yaml":"` + strings.Repeat("a", maxClientSettingsBody+10) + `"}`
	if rr := rawPost(h, ClientSettingsPath, "application/json", "", bigSettings); rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("client settings: status %d, want 413", rr.Code)
	}
}

func TestGuard_SafeMethodsOnOtherPathsUnaffected(t *testing.T) {
	t.Parallel()
	h, _ := openUsageHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Host = "127.0.0.1:39273"
	req.Header.Set("Origin", "https://other.example.com")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code == http.StatusForbidden {
		t.Fatalf("a read of /health with a foreign Origin was refused")
	}
}
