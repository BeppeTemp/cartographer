package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newMultiKBTestHandler mounts the given KB names on a fresh MultiKBServer
// and returns its Handler() unwrapped (no auth middleware), for routing and
// readiness tests that don't care about scopes.
func newMultiKBTestHandler(t *testing.T, names ...string) *MultiKBServer {
	t.Helper()
	multi := NewMultiKBServer("test")
	for _, name := range names {
		k := setupTestKB(t)
		multi.MountKB(name, func(s *Server) {
			RegisterKBTools(s, k, Deps{})
		})
	}
	return multi
}

func TestMultiKB_PathRouting_KnownName(t *testing.T) {
	multi := newMultiKBTestHandler(t, "kbx", "kby")
	handler := multi.Handler()

	rr := doMCP(handler, "kby", "", toolsListBody) // sanity: ?kb= still works
	if rr.Code != http.StatusOK {
		t.Fatalf("?kb=kby: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp/kbx", strings.NewReader(toolsListBody))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/mcp/kbx: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestMultiKB_PathRouting_UnknownName(t *testing.T) {
	multi := newMultiKBTestHandler(t, "kbx")
	handler := multi.Handler()

	req := httptest.NewRequest(http.MethodPost, "/mcp/does-not-exist", strings.NewReader(toolsListBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("/mcp/does-not-exist: status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestMultiKB_PathRouting_ConflictingKBSelection(t *testing.T) {
	multi := newMultiKBTestHandler(t, "kbx", "kby")
	handler := multi.Handler()

	req := httptest.NewRequest(http.MethodPost, "/mcp/kbx?kb=kby", strings.NewReader(toolsListBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("/mcp/kbx?kb=kby: status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestMultiKB_PathRouting_AgreeingKBSelection(t *testing.T) {
	multi := newMultiKBTestHandler(t, "kbx", "kby")
	handler := multi.Handler()

	req := httptest.NewRequest(http.MethodPost, "/mcp/kbx?kb=kbx", strings.NewReader(toolsListBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/mcp/kbx?kb=kbx: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestMultiKB_Ready_ZeroKBs(t *testing.T) {
	multi := NewMultiKBServer("test")
	handler := multi.Handler()

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("/ready with 0 KBs: status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if ready, _ := body["ready"].(bool); ready {
		t.Fatalf("ready = %v, want false", body["ready"])
	}
	if kbs, _ := body["kbs"].(float64); kbs != 0 {
		t.Fatalf("kbs = %v, want 0", body["kbs"])
	}
}

func TestMultiKB_Ready_AtLeastOneKB(t *testing.T) {
	multi := newMultiKBTestHandler(t, "kbx")
	handler := multi.Handler()

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/ready with 1 KB: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if ready, _ := body["ready"].(bool); !ready {
		t.Fatalf("ready = %v, want true", body["ready"])
	}
}

func TestMultiKB_Health_IncludesReady(t *testing.T) {
	// 0 KBs: status stays "ok" (liveness invariant), ready is false.
	multi := NewMultiKBServer("test")
	handler := multi.Handler()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/health: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status = %v, want \"ok\" (liveness must not break)", body["status"])
	}
	if ready, _ := body["ready"].(bool); ready {
		t.Fatalf("ready = %v, want false with 0 KBs", body["ready"])
	}

	// 1 KB: ready flips to true.
	multi = newMultiKBTestHandler(t, "kbx")
	handler = multi.Handler()
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status = %v, want \"ok\"", body["status"])
	}
	if ready, _ := body["ready"].(bool); !ready {
		t.Fatalf("ready = %v, want true with 1 KB", body["ready"])
	}
}

func TestServer_Health_IncludesReady(t *testing.T) {
	s := New("test")
	handler := s.HTTPHandler()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/health: status = %d, want 200", rr.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status = %v, want \"ok\"", body["status"])
	}
	if ready, _ := body["ready"].(bool); !ready {
		t.Fatalf("ready = %v, want true (single-KB server always ready)", body["ready"])
	}
}

func TestServer_Ready(t *testing.T) {
	s := New("test")
	handler := s.HTTPHandler()

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/ready: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if ready, _ := body["ready"].(bool); !ready {
		t.Fatalf("ready = %v, want true", body["ready"])
	}
}
