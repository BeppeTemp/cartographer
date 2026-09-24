package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func healthBody(t *testing.T, h http.Handler) map[string]interface{} {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid /health JSON: %v", err)
	}
	return body
}

// D254: latest_version is additive on both /health shapes — present only when
// a newer release is known, absent otherwise, so a probe sees the same keys.
func TestHealth_LatestVersionOnlyWhenKnown(t *testing.T) {
	latest := ""
	source := func() string { return latest }

	single := New("v0.16.1")
	multi := newMultiKBTestHandler(t, "kbx")
	for name, h := range map[string]http.Handler{"single": single.HTTPHandler(), "multi": multi.Handler()} {
		if _, ok := healthBody(t, h)["latest_version"]; ok {
			t.Errorf("%s: latest_version present with no source", name)
		}
	}
	single.SetLatestVersionSource(source)
	multi.SetLatestVersionSource(source)
	for name, h := range map[string]http.Handler{"single": single.HTTPHandler(), "multi": multi.Handler()} {
		if _, ok := healthBody(t, h)["latest_version"]; ok {
			t.Errorf("%s: latest_version present while nothing newer is known", name)
		}
	}
	latest = "v0.17.0"
	for name, h := range map[string]http.Handler{"single": single.HTTPHandler(), "multi": multi.Handler()} {
		body := healthBody(t, h)
		if body["latest_version"] != "v0.17.0" || body["status"] != "ok" {
			t.Errorf("%s: /health = %v", name, body)
		}
	}
}

func TestKBStatus_ServerAndLatestVersion(t *testing.T) {
	k := setupTestKB(t)
	s := New("v0.16.1")
	RegisterKBTools(s, k, Deps{})
	latest := ""
	s.SetLatestVersionSource(func() string { return latest })

	call := func() map[string]interface{} {
		msgs := []string{
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"kb_status","arguments":{}}}`,
		}
		tr := decodeToolResult(t, runMCPSequence(t, s, msgs)[1])
		var out map[string]interface{}
		if err := json.Unmarshal([]byte(tr.Content[0].Text), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := call()
	if out["server_version"] != "v0.16.1" {
		t.Errorf("server_version = %v", out["server_version"])
	}
	if _, ok := out["latest_version"]; ok {
		t.Error("latest_version present while nothing newer is known")
	}
	latest = "v0.17.0"
	if out := call(); out["latest_version"] != "v0.17.0" {
		t.Errorf("latest_version = %v", out["latest_version"])
	}
}
