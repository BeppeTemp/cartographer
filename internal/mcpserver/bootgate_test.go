package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func gateGet(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestBootGateAnswersLivenessAndReadinessBeforeOpen(t *testing.T) {
	g := NewBootGate("v-test")
	g.SetPhase("cloning")
	h := g.Handler()

	rec := gateGet(h, http.MethodGet, "/health")
	if rec.Code != http.StatusOK {
		t.Fatalf("/health = %d, want 200", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" || body["version"] != "v-test" || body["ready"] != false ||
		body["bootstrapping"] != true || body["phase"] != "cloning" || body["started_at"] == "" {
		t.Errorf("/health body = %v", body)
	}
	if kbs, ok := body["kbs"].([]interface{}); !ok || len(kbs) != 0 {
		t.Errorf("kbs = %v, want []", body["kbs"])
	}

	rec = gateGet(h, http.MethodGet, "/ready")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/ready = %d, want 503", rec.Code)
	}
	body = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["ready"] != false || body["bootstrapping"] != true || body["phase"] != "cloning" || body["kbs"] != float64(0) {
		t.Errorf("/ready body = %v", body)
	}

	for _, p := range []string{"/health", "/ready"} {
		if rec := gateGet(h, http.MethodPost, p); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 405", p, rec.Code)
		}
	}
}

func TestBootGateRefusesEverythingElseWithRetry(t *testing.T) {
	h := NewBootGate("v").Handler()
	for _, p := range []string{"/mcp", "/mcp/kb-a", "/clients", "/ui/", "/api/ui/v1/x", "/.well-known/oauth-protected-resource", "/"} {
		rec := gateGet(h, http.MethodPost, p)
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" {
			t.Errorf("%s = %d retry-after %q", p, rec.Code, rec.Header().Get("Retry-After"))
		}
		if got := rec.Body.String(); got != "kb bootstrapping, retry\n" {
			t.Errorf("%s body = %q", p, got)
		}
	}
}

func TestBootGateDelegatesAfterOpen(t *testing.T) {
	g := NewBootGate("v")
	h := g.Handler()
	g.Open(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok","ready":true}`))
	}))
	rec := gateGet(h, http.MethodGet, "/health")
	if rec.Body.String() != `{"status":"ok","ready":true}` {
		t.Errorf("/health after Open = %q", rec.Body.String())
	}
	// A second Open must not replace the first.
	g.Open(http.NotFoundHandler())
	if rec := gateGet(h, http.MethodGet, "/health"); rec.Code != http.StatusOK {
		t.Errorf("second Open replaced the handler: %d", rec.Code)
	}
}

func TestBootGateOpenIsRaceFree(t *testing.T) {
	g := NewBootGate("v")
	h := g.Handler()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				gateGet(h, http.MethodGet, "/health")
				g.SetPhase("indexing")
			}
		}()
	}
	g.Open(http.NotFoundHandler())
	wg.Wait()
}
